# Soundtrack capture runbook

Identifying the camera control that plays Nanit's built-in sounds (white noise,
nature sounds), so soundtrack control can be finished.

Everything except one value is already implemented. That value is the protobuf
field number on the `Control` message which selects a soundtrack. This document
explains how to find it against your camera and where to put it.

> **Safety.** These steps send experimental bytes to a live baby monitor.
> Do them while the camera is **not** in active use, and **turn the volume
> down first** — an unknown field could start playback at whatever level the
> camera was last set to. The experiment endpoints are refused unless you opt
> in explicitly, and they should be turned off again when you are done.

---

## 1. What is already known

Established from the generated schema (`pkg/client/websocket.pb.go`) and its
source (`pkg/client/websocket.proto`):

| Fact | Value |
|---|---|
| Request types | `GET_SETTINGS=4`, `PUT_SETTINGS=5`, `GET_CONTROL=6`, `PUT_CONTROL=7`, `GET_SOUNDTRACKS=21` |
| `PUT_SOUNDTRACKS` | Does not exist |
| `Control` mapped tags | `nightLight=3`, `sensorDataTransfer=4`, `forceConnectToServer=5`, `nightLightTimeout=6` |
| `Control` free tags | **1, 2, 7 and above** — the soundtrack selector is most likely one of these |
| `Settings.volume` | Tag 9, `optional int32`, set via `PUT_SETTINGS` — fully implemented already |
| `Response` soundtracks field | **None.** The schema maps `status=5`, `settings=6`, `sensorData=9`, `control=13` only |

Because `Response` has no soundtracks field, a `GET_SOUNDTRACKS` reply arrives
as *unknown fields* — bytes protobuf preserves but cannot name. The code decodes
those and prints them, so you can read the real structure instead of guessing
at it. That decoder is `client.DescribeUnknownFields`.

### Findings from your camera

*Fill this in as you work through the steps below. This is the record that turns
the scaffolding into a finished feature.*

- `GET_SOUNDTRACKS` response shape: _(paste the `unknown_fields` dump)_
- Soundtrack identifiers and names: _(id → name)_
- Soundtrack `Control` field tag: _(the number to put in the code)_
- Value encoding: _(e.g. varint; 0 = off, 1..N select a sound)_

---

## 2. Enable tracing and the experiment endpoints

Two environment variables. Both are off by default.

```bash
NANIT_LOG_LEVEL=trace      # dump every websocket frame, including unmapped fields
NANIT_DEBUG_CONTROL=true   # expose /api/debug/* — off in normal operation
```

With Docker:

```bash
docker run -d --name=nanit \
  -e NANIT_LOG_LEVEL=trace \
  -e NANIT_DEBUG_CONTROL=true \
  ... your usual flags ...
  deltathreed/nanit-web
```

On startup you should see:

```
WRN NANIT_DEBUG_CONTROL is enabled: experimental /api/debug endpoints are exposed. Do not leave this on.
```

Trace logging is verbose — it prints every frame from the camera. Follow the log
in a second terminal:

```bash
docker logs -f nanit
```

---

## 3. Read the soundtrack catalog

`GET_SOUNDTRACKS` is sent automatically at startup, and its reply is logged in
full. Look for:

```
INF GET_SOUNDTRACKS response baby_uid=... status_code=200 unknown_fields=[...] raw="..."
```

You can also request it on demand:

```bash
curl -s http://localhost:8080/api/debug/soundtracks | jq
```

The `unknown_fields` array is the useful part. Each entry reports the field
`tag`, its `wire_type`, the decoded `value`, and `nested` when the payload is
itself a message. For example, a catalog of embedded id/name pairs looks like:

```json
[
  {"path":"Response","tag":14,"wire_type":"bytes","value":"<21 bytes>",
   "nested":[{"tag":1,"wire_type":"varint","value":1},
             {"tag":2,"wire_type":"bytes","value":"\"White Noise\""}]}
]
```

The code already recognises that shape, plus a plain list of strings, and will
log `Discovered camera soundtrack catalog` when it can infer the list. If it
logs `Unable to infer a soundtrack catalog`, read the dump and map it by hand —
**record it in the Findings section above**.

The ids you find here are the values the play command will reference.

---

## 4. Find the control field by watching the app (preferred)

This is the low-risk route: **the camera does the writing, you only read.**

Nanit propagates state changes to every connected session, so when you change
something in the phone app, the camera broadcasts it to this bridge too.

1. Make sure trace logging is on and you are following the log.
2. Turn the camera volume down.
3. In the Nanit app, start a soundtrack.
4. Watch for a `Websocket frame` line with `unknown_fields` populated, on a
   frame whose `request_type` is `PUT_CONTROL`:

```
TRC Websocket frame baby_uid=... direction=camera->us request_type=PUT_CONTROL \
    unknown_fields=[{"path":"Message.request.control","tag":7,"wire_type":"varint","value":2}] raw="..."
```

That `tag` is the answer, and `value` tells you how sounds are numbered.

5. Repeat for each of the four sounds, and again with playback stopped, to learn
   the full value mapping (the "off" value is almost certainly 0).

If toggling a sound produces no frame at all, the camera may only report it to
the session that made the change. In that case use the sweep below.

---

## 5. Sweep candidate field tags (fallback)

Only if step 4 produced nothing. This **writes** to the camera, so keep the
volume low and the camera out of use.

`/api/debug/control` sends a single `PUT_CONTROL` carrying one arbitrary field:

```bash
curl -s -X POST http://localhost:8080/api/debug/control \
  -H 'Content-Type: application/json' \
  -d '{"tag":7,"value":1}' | jq
```

The response includes the camera's `status_code` and `status_message`. A
rejected field usually comes back non-200, which is itself informative.

Sweep the unmapped tags, listening for sound after each:

```bash
for tag in 1 2 7 8 9 10 11 12; do
  echo "--- tag $tag"
  curl -s -X POST http://localhost:8080/api/debug/control \
    -H 'Content-Type: application/json' \
    -d "{\"tag\":$tag,\"value\":1}" | jq -c '{tag:.tag,status:.status_code,msg:.status_message}'
  sleep 3
done
```

Notes:

- `{"tag":N,"value":V}` sends a varint. For a length-delimited candidate, send
  `{"tag":N,"text":"..."}` instead.
- Use a soundtrack id from step 3 as `value` if you have one; otherwise `1`.
- When a tag starts playback, **stop it** with `{"tag":N,"value":0}`.
- With multiple cameras, add `"baby_uid":"..."`.

Skip tags 3–6: those are mapped already, and writing to them changes the night
light rather than the sound.

---

## 6. Put the discovered value into the code

One constant, in `pkg/app/websocket_handlers.go`:

```go
// soundtrackControlFieldTag - protobuf field number on Control that selects the
// active soundtrack. 0 means "not yet identified".
//
// TODO: set this to the tag discovered via SOUNDTRACK_CAPTURE.md.
const soundtrackControlFieldTag int32 = 0
```

Change the `0` to the tag from step 4 or 5 and rebuild. That single edit
activates the whole path: sending, state tracking, MQTT and the Home Assistant
entities all key off it, and the guards that keep the feature inert stop firing.

If "off" is not 0 on your camera, adjust `SoundtrackOff` in `pkg/baby/state.go`
to match.

### Making it part of the schema

Once the tag is confirmed, fold it into the protobuf definition properly rather
than leaving it as an unknown field. Edit `pkg/client/websocket.proto`:

```protobuf
message Control {
  ...
  optional int32 soundtrack = 7;   // use the discovered tag
}
```

Then regenerate. The toolchain is not in the repo; install it once:

```bash
# protoc 29.3 and protoc-gen-go v1.36.5 are the versions the checked-in file was built with
go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.5
protoc --go_out=. --go_opt=module=github.com/indiefan/home_assistant_nanit pkg/client/websocket.proto
```

Regenerating with those versions reproduces the committed
`websocket.pb.go` byte for byte, so any diff you see is genuinely your change.

After that, `sendSoundCommand` and `processSoundtrack` can use the generated
`Soundtrack` accessors instead of the unknown-field helpers.

---

## 7. Verify

```bash
# Play a sound (id from step 3)
curl -s -X POST http://localhost:8080/api/control/soundtrack \
  -H 'Content-Type: application/json' \
  -d '{"baby_uid":"YOUR_UID","action":"set","value":1}'

# Stop it
curl -s -X POST http://localhost:8080/api/control/soundtrack \
  -H 'Content-Type: application/json' \
  -d '{"baby_uid":"YOUR_UID","action":"set","value":0}'
```

Over MQTT:

```bash
mosquitto_pub -t 'nanit/babies/YOUR_UID/soundtrack/select' -m 'White Noise'
mosquitto_pub -t 'nanit/babies/YOUR_UID/soundtrack/switch' -m 'false'
mosquitto_sub -t 'nanit/babies/YOUR_UID/soundtrack_name' -v
```

In Home Assistant, the camera device should carry **Soundtrack** (switch),
**Soundtrack Selection** (select) and **Volume** (number) entities. Changing a
sound from the phone app should move the select, confirming the receive path.

---

## 8. Turn the harness off

```bash
NANIT_LOG_LEVEL=info
NANIT_DEBUG_CONTROL=false   # or drop the variable
```

The `/api/debug/*` routes are not registered at all unless `NANIT_DEBUG_CONTROL`
is `true`, so clearing it removes them entirely.

---

## Volume, for reference

Volume needed no reverse engineering — `Settings.volume` is tag 9 and is already
implemented end to end. One thing worth confirming while you are here: the
schema does not document the value range. The code assumes **0–100** and clamps
to it.

Check what your camera actually reports:

```bash
NANIT_LOG_LEVEL=debug docker logs nanit 2>&1 | grep -i 'device info from settings'
```

If the reported volume uses a different scale (0–10, say), update `MinVolume` /
`MaxVolume` in `pkg/app/websocket_handlers.go` and the `min`/`max` of the volume
number entity in `pkg/mqtt/discovery.go`.
