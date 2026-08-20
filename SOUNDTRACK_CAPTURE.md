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

### GET_PLAYBACK reports playback state (Response field 11)

`GET_PLAYBACK` (19) had never been called by this project. It answers with an
embedded message on field 11:

```
unknown_fields=[{"path":"Response","tag":11,"wire_type":"bytes",
                 "nested":[{"tag":1,"wire_type":"varint","value":1}]}]
```

Field 1 = 1 is `Playback.Status.STOPPED`, so field 11 is a `Playback`. Now
mapped as `Response.playback = 11`, and requested at startup.

**This is the most promising place left to look.** It was read while nothing was
playing, so it reported only a status. If the camera tracks *which* track is
selected, it should appear here once something is playing.

### Not yet solved: how to choose which track plays

`PUT_PLAYBACK` with `status:STARTED` plays **the track the camera already has
selected**. Confirmed by playing "Wind" from the phone app, stopping it, then
asking this bridge for `Birds.wav` — playback started, on Wind.

Two sweeps of candidate fields on `Playback`, taken together:

| Tags | As bytes | As varint | Reading |
|---|---|---|---|
| 2, 5, 6, 7, 8 | Played, no change | — | Unknown to the camera; silently discarded |
| 3, 4 | **Timeout** | 200 OK, no change | Real fields, varint-typed, but not the selector |

The bytes/varint split is what pins this down. An unknown field is dropped
whatever its wire type; a *known* field sent with the wrong wire type fails to
parse, and the camera never replies. So tags 3 and 4 are genuine varint fields
on `Playback` — but values 0-3 on either changed nothing, so neither selects the
track.

Nothing on `Playback` selects a track. The selection lives elsewhere.

### Other probes, for the record

- `GET_STATUS`, `GET_CONTROL` (no flags): 200, **no** unmapped fields.
- `GET_SETTINGS` via the debug endpoint: timed out. Worth retrying; the startup
  path issues it successfully without awaiting a matching reply.
- `GET_CONTROL` with selector flags 1-8: failed. Probably `ptz` (tag 1) on a
  camera without it — narrow to `[2,3,4]` plus one unmapped tag at a time.

---

## 2. Next experiments

### Enable the harness

```bash
NANIT_LOG_LEVEL=trace
NANIT_DEBUG_CONTROL=true
```

> Shell note: `zsh` does not treat `#` as a comment when pasted interactively.
> Paste the commands without the comment lines, or the shell reports
> `command not found: #`.

### A. Read playback state *while a track is playing* ← start here

This is read-only and needs no guessing.

1. In the Nanit app, start **Wind**.
2. While it plays:

```bash
curl -s -X POST http://localhost:8080/api/debug/get \
  -H 'Content-Type: application/json' -d '{"type":"GET_PLAYBACK"}' | jq
```

3. Look at `unknown_fields`. Anything inside the playback message is reported
   with the path `Response.playback`, e.g.:

```json
{"path":"Response.playback","tag":6,"wire_type":"bytes","value":"\"Wind.wav\""}
```

4. Switch to **Birds** in the app and repeat. Whatever changes is the selector.

If `Response.playback` shows only `status` while a track is playing, the camera
does not report the selection over the socket at all, and B becomes the lead.

### B. Diff Nanit's cloud API

The phone app talks to `api.nanit.com` as well as the camera. A remembered
setting may be stored server-side, which would explain why changing tracks
produces nothing on the socket.

```bash
curl -s -X POST http://localhost:8080/api/debug/rest \
  -H 'Content-Type: application/json' -d '{"path":"/babies"}' | jq
```

The response is passed through untouched so it can be diffed directly:

```bash
# with Wind selected
curl -s -X POST http://localhost:8080/api/debug/rest \
  -H 'Content-Type: application/json' -d '{"path":"/babies"}' | jq -S . > wind.json

# switch to Birds in the app, then
curl -s -X POST http://localhost:8080/api/debug/rest \
  -H 'Content-Type: application/json' -d '{"path":"/babies"}' | jq -S . > birds.json

diff wind.json birds.json
```

Other paths worth trying, using your camera UID:

```bash
/babies/<uid>
/babies/<uid>/settings
/babies/<uid>/soundtracks
/babies/<uid>/playback
```

A 404 is a fine answer — it rules a path out. Anything that returns JSON
mentioning a sound name is the thing.

### C. Re-run the Control probe, narrowed

```bash
curl -s -X POST http://localhost:8080/api/debug/get \
  -H 'Content-Type: application/json' -d '{"type":"GET_CONTROL","flags":[2,3,4]}' | jq
```

Then add one unmapped selector tag at a time (`[2,3,4,5]`, `[2,3,4,6]`, …). A
selector the camera knows may unlock a `Control` field that is never otherwise
reported. Debug endpoints now return JSON on failure, so `jq` keeps working when
a request times out.

### Record the answer

Two constants in `pkg/client/soundtrack.go`:

```go
const (
	SoundtrackNameFieldTag int32 = 2      // <- the confirmed tag
	SoundtrackSelectionVerified = false   // <- flip to true once it works
)
```

Flipping `SoundtrackSelectionVerified` publishes the Home Assistant **select**
entity. It is withheld while selection does not work, because a dropdown that
silently plays a different sound is worse on a baby monitor than no dropdown.

If the selector turns out to live somewhere other than `Playback` — a different
request type, or the cloud API — `sendSoundCommand` in
`pkg/app/websocket_handlers.go` is the single place that needs rerouting.

Once confirmed, fold the field into `pkg/client/websocket.proto` and regenerate
with protoc **29.3** and protoc-gen-go **v1.36.5**, which reproduce the
committed `websocket.pb.go` byte for byte:

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
