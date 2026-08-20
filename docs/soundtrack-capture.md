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

### GET_PLAYBACK carries the track — the whole protocol

`GET_PLAYBACK` (19) answers on `Response.playback` (field 11). Read while
**Wind** was playing, the 30-byte payload decodes as:

```
tag 1 varint = 0                      -> status = STARTED
tag 3 bytes  = {1: 0, 2: "Wind.wav"}  -> Soundtrack
tag 4 bytes  = {1: 0, 2: "Wind.wav"}  -> Soundtrack
```

With **Birds** playing, both carried `"Birds.wav"`. Stopped, the payload is two
bytes: `status = STOPPED` and nothing else.

So `Playback` fields 3 and 4 are **embedded `Soundtrack` messages**, the same
type `GET_SOUNDTRACKS` returns. Both carried the same value in every capture, so
the distinction between them is unknown; commands set both.

```protobuf
message Playback {
  required Status status = 1;
  optional Soundtrack soundtrack = 3;
  optional Soundtrack selectedSoundtrack = 4;
}
```

A test encodes `Playback{STARTED, Wind.wav, Wind.wav}` and asserts it reproduces
the captured 30 bytes exactly, so a command looks to the camera like its own
representation.

#### How the earlier sweeps pointed here

Worth recording, because the reasoning generalises. On `Playback`:

| Tags | As bytes | As varint |
|---|---|---|
| 2, 5, 6, 7, 8 | played, no change | — |
| 3, 4 | **timeout** | 200 OK, no change |

Unknown fields are discarded whatever their wire type, so the ignored tags were
genuinely absent from the camera's schema. Tags 3 and 4 rejecting a bare string
meant they *were* mapped — a known field with an unparseable payload fails the
whole message, and the camera never replies. The inference that they were
therefore varints was **wrong**: they are message fields, and `"Wind.wav"` is
simply not a valid submessage. The varint probes returning 200 was the misleading
part; only reading `GET_PLAYBACK` while a track was playing settled it.

The lesson: a timeout means "this tag exists and I could not parse it", which is
far more informative than a 200, and reading state beats writing guesses.

### Why nothing showed up when changing tracks

The camera broadcasts a `PUT_PLAYBACK` request when playback **stops**, but not
when it starts and not when the track changes. So the only way to learn the
current track is to ask: `GET_PLAYBACK`. This bridge does that at startup and
again shortly after every command it sends.

---

## 2. Status

| Capability | State |
|---|---|
| List built-in sounds | Working — `GET_SOUNDTRACKS` |
| Start / stop | Working — `PUT_PLAYBACK` |
| Choose a track | Working — `Playback.soundtrack` + `selectedSoundtrack` |
| Read what is playing | Working — `GET_PLAYBACK`, polled every 5 minutes |
| Volume | Working — `Settings.volume` |
| **Play for a set duration** | **Unsolved — playback stops on its own after ~10s** |

### Open problem: playback stops after about 10 seconds

A sound started from this bridge stops by itself after roughly ten seconds. A
sound started from the Nanit app plays indefinitely.

Nothing here does that: there is no ten-second timer anywhere in the code, and
no path stops playback except an explicit command. The reconcile read happens at
1.5s and the poll every 5 minutes. So the camera is ending it.

**The likely cause is a missing duration.** The Nanit app offers 30 minute, 60
minute and infinite timers when starting a sound, so `Playback` must carry a
duration somewhere. This project never sends one, and the camera appears to fall
back to a brief default rather than to "keep playing".

Where it is *not*, from the earlier sweeps: tags 2, 5, 6, 7 and 8 accepted a
length-delimited payload and ignored it, so none of them is a varint duration —
a known varint field rejects bytes outright. That leaves:

- a tag of 9 or above, never swept
- the `Soundtrack` message's own field 1, which has been `0` in every capture and
  is currently guessed at as `type`

### The decisive experiment (read-only)

The app already knows how to set a duration, so let it, and read the result
back. Three captures, no writes to the camera:

1. In the Nanit app, start a sound with the **30 minute** timer. Then:

```bash
curl -s -X POST http://localhost:8080/api/debug/get \
  -H 'Content-Type: application/json' -d '{"type":"GET_PLAYBACK"}' | jq
```

2. Stop it, restart with the **60 minute** timer, capture again.
3. Stop it, restart with **infinite**, capture again.

Diff the three. A field that reads 30/60, or 1800/3600, or 1800000/3600000, is
the duration; whatever infinite uses (likely `0`, or the field being absent) is
the value to send for continuous play.

Watch two places in particular:

- **New fields on `Playback`.** Anything beyond tags 1, 3 and 4 shows up under
  `unknown_fields` with the path `Response.playback`.
- **`Soundtrack` field 1.** If it stops being `0`, it is not a type — it is the
  duration, and it sits inside the Soundtrack rather than beside it. The path
  would read `Response.playback.soundtrack`.

### Testing a candidate

Once a field and value look right, `/api/debug/playback` sends a real command
plus one extra field. It sets the mapped Soundtrack from `name`, exactly as a
normal command does, so this tests the duration in isolation:

```bash
# Play Wind with a candidate duration of 1800 at tag 5
curl -s -X POST http://localhost:8080/api/debug/playback \
  -H 'Content-Type: application/json' \
  -d '{"name":"Wind.wav","tag":5,"varint":true,"value":1800}' | jq
```

The response echoes the exact message sent under `sent`, so you can check it
against what the camera reported. Then wait past the ten-second mark and see
whether it survives.

To probe the `Soundtrack`'s own field 1 instead:

```bash
curl -s -X POST http://localhost:8080/api/debug/playback \
  -H 'Content-Type: application/json' \
  -d '{"name":"Wind.wav","soundtrack_type":1800}' | jq
```

And to sweep the unswept tags:

```bash
for tag in 9 10 11 12 13 14 15 16; do
  echo "--- tag $tag"
  curl -s -X POST http://localhost:8080/api/debug/playback \
    -H 'Content-Type: application/json' \
    -d "{\"name\":\"Wind.wav\",\"tag\":$tag,\"varint\":true,\"value\":1800}" \
    | jq -c '{tag,status:.status_code,msg:.status_message}'
  sleep 15
  curl -s -X POST http://localhost:8080/api/debug/playback \
    -H 'Content-Type: application/json' -d '{"stop":true}' > /dev/null
done
```

Fifteen seconds per attempt so a tag that survives past the ten-second cutoff is
obvious. A timeout means the tag exists with a different wire type, which is a
positive result — see how tags 3 and 4 were found.

### Tracing the stop

`processPlayback` logs stops at info level with the source of the report:

```
INF Camera reported soundtrack stopped source=camera-broadcast
```

- `source=camera-broadcast` — the camera announced the stop, so it decided to
  end playback.
- `source=get-playback` — a read came back stopped, with no announcement.

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
# soundtrack_name carries the filename; soundtrack_display_name carries the
# name without its extension, which is what the HA select reads
mosquitto_sub -t 'nanit/babies/YOUR_UID/soundtrack_#' -v
```

In Home Assistant the camera device carries a **Soundtrack** switch (start/stop)
and a **Soundtrack Selection** select listing Off plus the camera's own sounds.

### Known limitations

**The camera broadcasts stops, but not starts and not track changes.** Nothing
pushes a track change to us, so playback state is read with `GET_PLAYBACK`
instead: at startup, shortly after every command this bridge sends, and on a
five-minute poll. A sound started or switched from the Nanit app therefore shows
up within five minutes rather than instantly.

Shortening `playbackPollInterval` in `pkg/app/websocket_handlers.go` trades
camera traffic for freshness.

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
