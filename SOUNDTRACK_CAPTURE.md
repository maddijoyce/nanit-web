# Soundtrack capture runbook

Notes on Nanit's soundtrack (white noise) protocol, and the procedure for
confirming the one field that is still unverified.

> **Safety.** The steps below send experimental bytes to a live baby monitor.
> Do them while the camera is **not** in active use, and **turn the volume
> down first**. The experiment endpoints are refused unless you opt in
> explicitly, and should be turned off again afterwards.

---

## 1. Findings

Captured from a Nanit Pro on 20 Aug 2026. This section is the record; the code
follows it.

### Playback is PUT_PLAYBACK, not PUT_CONTROL

The initial hypothesis — an unmapped field on `Control`, sent via `PUT_CONTROL`
— was **wrong**. Playback is driven by `PUT_PLAYBACK` (request type 20) carrying
the `Playback` message, which was already in the schema.

Stopping a sound, observed as a broadcast from the camera:

```
request:{id:2640 type:PUT_PLAYBACK playback:{status:STOPPED}}
response:{requestId:83 requestType:PUT_PLAYBACK statusCode:200 statusMessage:"OK"}
```

Starting produced the same `200 OK` response shape. `Playback.status` is a
required enum, `STARTED = 0` / `STOPPED = 1`.

### The catalog is Response.soundtracks, field 12

`GET_SOUNDTRACKS` (21) returns four entries on field 12, each an embedded
message of `{1: varint, 2: string}`:

```
12:"\x08\x00\x12\x0fWhite Noise.wav"
12:"\x08\x00\x12\tBirds.wav"
12:"\x08\x00\x12\tWaves.wav"
12:"\x08\x00\x12\x08Wind.wav"
```

So the built-in sounds are **White Noise, Birds, Waves, Wind**.

Two things worth noting:

- Field 1 was `0` on **every** entry, so it is *not* an id. It is recorded as
  `type` in the schema with its purpose unknown.
- **The filename is the identifier.** There are no numeric soundtrack ids, which
  is why the implementation keys everything on the name and derives a display
  name by dropping the `.wav` extension.

This is now mapped properly in `websocket.proto`:

```protobuf
message Soundtrack {
  optional int32 type = 1;
  optional string name = 2;
}

message Response {
  ...
  repeated Soundtrack soundtracks = 12;
}
```

A unit test decodes the exact captured bytes and asserts nothing is left
unmapped.

### Not yet solved: how to choose which track plays

`PUT_PLAYBACK` with `status:STARTED` plays **the track the camera already has
selected**. It does not select one. Confirmed by playing "Wind" from the phone
app, stopping it, then asking this bridge for `Birds.wav` — playback started,
on Wind.

A sweep of candidate fields on `Playback`, sending the filename as a
length-delimited value, gave a clean split:

| Tags | Result | Reading |
|---|---|---|
| 2, 5, 6, 7, 8 | Played, track unchanged | Ignored — protobuf discards fields the receiver does not know |
| 3, 4 | **Request timed out** | The camera *does* map these tags, and rejected the message |

That timeout is the most useful result in the whole exercise. An unknown field
is silently dropped; a *known* field sent with the wrong wire type makes the
parse fail, so the camera never replies. **`Playback` has real fields at 3 and
4** that this schema does not know about, and neither is a string.

The obvious candidate: a **varint** — most likely an index into the catalog, in
the order `GET_SOUNDTRACKS` returned it:

| Index | Sound |
|---|---|
| 0 | White Noise |
| 1 | Birds |
| 2 | Waves |
| 3 | Wind |

---

## 2. Next experiment: varints on tags 3 and 4

### Enable the harness

```bash
NANIT_LOG_LEVEL=trace
NANIT_DEBUG_CONTROL=true
```

You should see `WRN NANIT_DEBUG_CONTROL is enabled...` on startup. Follow the
log in a second terminal (`docker logs -f nanit`).

### Try an index

Volume down, camera not in use. Ask for **Birds** (index 1) while something
other than Birds is the camera's current selection, so a change is unmistakable:

```bash
curl -s -X POST http://localhost:8080/api/debug/playback \
  -H 'Content-Type: application/json' \
  -d '{"tag":3,"varint":true,"value":1}' | jq
```

Stop between attempts:

```bash
curl -s -X POST http://localhost:8080/api/debug/playback \
  -H 'Content-Type: application/json' -d '{"stop":true}'
```

Interpreting it:

| Outcome | Meaning |
|---|---|
| **Birds** plays | Tag 3 is the selector, as a catalog index |
| Something plays, but the same track as before | Tag 3 exists but is not the selector — try tag 4 |
| Timeout again | Wrong wire type for this tag too; try `fixed32`-shaped values or a nested message |
| Non-200 status | The camera parsed it and refused — read `status_message` |

Sweep both tags and a few indices:

```bash
for tag in 3 4; do
  for value in 0 1 2 3; do
    echo "--- tag $tag value $value"
    curl -s -X POST http://localhost:8080/api/debug/playback \
      -H 'Content-Type: application/json' \
      -d "{\"tag\":$tag,\"varint\":true,\"value\":$value}" \
      | jq -c '{tag,value,status:.status_code,msg:.status_message}'
    sleep 4
    curl -s -X POST http://localhost:8080/api/debug/playback \
      -H 'Content-Type: application/json' -d '{"stop":true}' > /dev/null
    sleep 1
  done
done
```

Note which `(tag, value)` plays which sound. If the mapping is an index, you
should be able to hit all four.

### If that fails: find where the selection is stored

Selection may not live on `Playback` at all — the camera clearly *remembers* it
between sessions, which means it is readable somewhere. The generic probe issues
any GET request and dumps everything, including fields the schema cannot name:

```bash
# Ask for every Control item, including tags the schema has no name for.
# GetControl is a filtered request: the startup code asks only for nightLight,
# so anything else the camera could report has never been visible.
curl -s -X POST http://localhost:8080/api/debug/get \
  -H 'Content-Type: application/json' \
  -d '{"type":"GET_CONTROL","flags":[1,2,3,4,5,6,7,8]}' | jq

# Settings are the other plausible home for a remembered selection
curl -s -X POST http://localhost:8080/api/debug/get \
  -H 'Content-Type: application/json' \
  -d '{"type":"GET_SETTINGS"}' | jq
```

**The decisive move is a diff.** The camera does not broadcast track changes, but
it must store the selection:

1. In the Nanit app, select **Wind**.
2. Run both probes above, save the output.
3. In the app, switch to **Birds**.
4. Run both probes again.
5. Diff.

Whatever field changed between the two is the selection. If it is a string you
will see `"Birds.wav"`; if it is an index you will see a small integer change.
Paste the diff back and it can be mapped.

Worth probing too, since each may carry state the schema misses:

```bash
for t in GET_STATUS GET_SETTINGS GET_CONTROL GET_PLAYBACK; do
  echo "=== $t"
  curl -s -X POST http://localhost:8080/api/debug/get \
    -H 'Content-Type: application/json' -d "{\"type\":\"$t\"}" \
    | jq -c '{type,status:.status_code,unknown:.unknown_fields}'
done
```

`GET_PLAYBACK` (19) is especially interesting — it is in the enum but this
project has never called it, and a getter for playback state would be the
natural place to find the selected track.

### Record the answer

Two constants in `pkg/client/soundtrack.go`:

```go
const (
	SoundtrackNameFieldTag int32 = 2      // <- the confirmed tag
	SoundtrackSelectionVerified = false   // <- flip to true once it works
)
```

Flipping `SoundtrackSelectionVerified` to `true` is what publishes the Home
Assistant **select** entity. It is deliberately withheld while selection does
not work, because a dropdown that silently plays a different sound is worse on a
baby monitor than no dropdown at all. If the selector turns out to be a varint
index rather than a name, `sendSoundCommand` in
`pkg/app/websocket_handlers.go` needs the catalog index instead of the filename
— the catalog is already ordered as the camera returned it.

Once confirmed, fold it into `pkg/client/websocket.proto`:

```protobuf
message Playback {
  required Status status = 1;
  optional int32 soundtrack = 3;   // the confirmed tag and type
}
```

Regenerate with protoc **29.3** and protoc-gen-go **v1.36.5**, which reproduce
the committed `websocket.pb.go` byte for byte:

```bash
go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.5
protoc --go_out=. --go_opt=module=github.com/indiefan/home_assistant_nanit pkg/client/websocket.proto
```

---

## 3. Verify

```bash
# Play a specific sound
curl -s -X POST http://localhost:8080/api/control/soundtrack \
  -H 'Content-Type: application/json' \
  -d '{"baby_uid":"YOUR_UID","action":"set","name":"Waves.wav"}'

# Stop
curl -s -X POST http://localhost:8080/api/control/soundtrack \
  -H 'Content-Type: application/json' \
  -d '{"baby_uid":"YOUR_UID","action":"set","name":"Off"}'
```

Over MQTT — note the select takes **display names** (no `.wav`):

```bash
mosquitto_pub -t 'nanit/babies/YOUR_UID/soundtrack/select' -m 'White Noise'
mosquitto_pub -t 'nanit/babies/YOUR_UID/soundtrack/switch' -m 'false'
mosquitto_sub -t 'nanit/babies/YOUR_UID/soundtrack_name' -v
```

In Home Assistant the camera device carries a **Soundtrack** switch (start/stop,
working). The **Soundtrack Selection** select appears only once
`SoundtrackSelectionVerified` is true.

### Known limitations

**The camera broadcasts stops, but not starts and not track changes.** So
stopping from the phone app is reflected here; starting or switching tracks
there is not. State is updated optimistically when a command is sent from this
side, which is what keeps the Home Assistant switch from springing back to off.

**Start/stop is all that works today.** Until the selector is found, starting
playback plays whatever the camera last had selected — set that from the Nanit
app.

## 4. Turn the harness off

```bash
NANIT_LOG_LEVEL=info
NANIT_DEBUG_CONTROL=false   # or drop the variable
```

The `/api/debug/*` routes are not registered at all unless `NANIT_DEBUG_CONTROL`
is `true`.

---

## Volume, for reference

Volume needed no reverse engineering — `Settings.volume` is tag 9 via
`PUT_SETTINGS`, and it is confirmed working.

One caveat: the schema does not document the value range. The code assumes
**0–100** and clamps to it. Check what your camera reports:

```bash
NANIT_LOG_LEVEL=debug docker logs nanit 2>&1 | grep -i 'device info from settings'
```

If the scale differs, update `MinVolume` / `MaxVolume` in
`pkg/app/websocket_handlers.go` and the `min`/`max` of the volume number entity
in `pkg/mqtt/discovery.go`.

---

## Appendix: the unknown-field decoder

`client.DescribeUnknownFields` walks a message and every nested message,
decoding anything the schema does not map — tag, wire type, value, and embedded
messages. It is what made the `GET_SOUNDTRACKS` structure readable rather than
guessable, and it stays useful for the next unmapped field.

Every inbound frame is dumped through it at trace level:

```
TRC Websocket frame baby_uid=... request_type=PUT_PLAYBACK unknown_fields=[...] raw="..."
```
