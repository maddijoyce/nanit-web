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

### Still unverified: which Playback field names the track

Stopping needs no name, and the camera **does not broadcast track changes**, so
the field carrying the filename could not be read off the wire.

The code sends it as field **2** on `Playback` — a hypothesis, not an
observation. It is the next free tag after the required `status`, and it is what
`Soundtrack` itself uses for a name. It is written as an unknown field, so
another tag can be tried by changing one constant, with no regeneration:

```go
// pkg/app/websocket_handlers.go
const soundtrackNameFieldTag int32 = 2
```

**What works regardless:** start and stop. Only choosing *which* track depends
on this tag.

---

## 2. Confirming the name field

### Enable the harness

```bash
NANIT_LOG_LEVEL=trace      # dump every websocket frame, including unmapped fields
NANIT_DEBUG_CONTROL=true   # expose /api/debug/* — off in normal operation
```

You should see on startup:

```
WRN NANIT_DEBUG_CONTROL is enabled: experimental /api/debug endpoints are exposed. Do not leave this on.
```

Follow the log in a second terminal (`docker logs -f nanit`).

### Check the catalog decodes

```bash
curl -s http://localhost:8080/api/debug/soundtracks | jq '.catalog, .unknown_fields'
```

`catalog` should list the four sounds. `unknown_fields` should be **empty** — if
it is not, the response carries something the schema still misses; paste it back
and it can be mapped.

### Try the hypothesis first

Volume down, then:

```bash
curl -s -X POST http://localhost:8080/api/debug/playback \
  -H 'Content-Type: application/json' \
  -d '{"tag":2,"name":"Wind.wav"}' | jq
```

If **Wind** plays, tag 2 is correct and nothing needs changing — the default is
already right. Stop it with:

```bash
curl -s -X POST http://localhost:8080/api/debug/playback \
  -H 'Content-Type: application/json' -d '{"stop":true}'
```

Distinguish the three outcomes:

| What happens | Meaning |
|---|---|
| Wind plays | Tag 2 is correct |
| A *different* sound plays (or a default one) | The command works but the name is ignored — wrong tag |
| Nothing plays, or a non-200 status | The camera rejected the message |

That middle case is the important one: playback starting is **not** by itself
proof the tag is right, because `status:STARTED` alone may start a default
track. Confirm by asking for a specific, distinctive sound and checking you get
*that* one.

### Sweep, if tag 2 is wrong

```bash
for tag in 2 3 4 5 6 7 8; do
  echo "--- tag $tag"
  curl -s -X POST http://localhost:8080/api/debug/playback \
    -H 'Content-Type: application/json' \
    -d "{\"tag\":$tag,\"name\":\"Wind.wav\"}" | jq -c '{tag,status:.status_code,msg:.status_message}'
  sleep 4
  curl -s -X POST http://localhost:8080/api/debug/playback \
    -H 'Content-Type: application/json' -d '{"stop":true}' > /dev/null
  sleep 1
done
```

Listen for the tag where **Wind specifically** plays. Alternate the requested
sound between runs (Wind, then Birds) so you can tell a real selection from a
default.

### Record the answer

Change the constant in `pkg/app/websocket_handlers.go`:

```go
const soundtrackNameFieldTag int32 = 2   // <- the confirmed tag
```

Then fold it into the schema properly. Edit `pkg/client/websocket.proto`:

```protobuf
message Playback {
  enum Status {
    STARTED = 0;
    STOPPED = 1;
  }

  required Status status = 1;
  optional string soundtrack = 2;   // the confirmed tag
}
```

Regenerate:

```bash
go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.5
protoc --go_out=. --go_opt=module=github.com/indiefan/home_assistant_nanit pkg/client/websocket.proto
```

protoc **29.3** with protoc-gen-go **v1.36.5** reproduces the committed
`websocket.pb.go` byte for byte, so any diff you see is genuinely your change.
Afterwards `sendSoundCommand` and `processPlayback` can use the generated
accessors instead of `SetUnknownBytesField` / `GetUnknownBytesField`.

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

In Home Assistant the camera device should carry **Soundtrack** (switch) and
**Soundtrack Selection** (select, options: Off, White Noise, Birds, Waves,
Wind).

### A known limitation

The camera broadcasts a stop, but not a start and not a track change. So:

- Stopping from the phone app **is** reflected here.
- Starting or switching tracks from the phone app is **not** — the entity will
  keep showing the last state this bridge knows about until something stops.

Nothing can be done about that from this side; it is what the camera sends. If
you ever see a `PUT_PLAYBACK` broadcast on start in the trace log, that
assumption is worth revisiting.

---

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
