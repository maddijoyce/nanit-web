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

#### It is probably looping, not a timer

The app's sound list offers **30min**, **60min** and a **loop icon** per track —
not a plain duration. That reframes the problem: the third option is repeat, not
"infinite minutes".

Which suggests the ten seconds is simply **the length of the audio clip**. Told
to play without any repeat instruction, the camera plays the file once through
and stops. The app's three choices then read as "loop for 30 minutes", "loop for
60 minutes", "loop forever".

If that is right, the missing field is a loop or mode flag rather than a
duration, and the fix may be as small as one boolean.

#### What the captures rule out, and what they do not

Changing the timer in the app does **not** change the `GET_PLAYBACK` reply.

That does not mean the field is absent from `PUT_PLAYBACK`. `GET_PLAYBACK`
reports current playback state, and the camera may simply not echo the mode back
— exactly as it never echoes a track change. So a loop flag on the play command
remains entirely possible; it just cannot be found by reading playback state.

The per-track setting also persists across a force-quit of the app, so it is
stored somewhere: on the camera, in Nanit's cloud, or in the app's own storage.
All three are worth ruling in or out.

#### The sharpest remaining test

The app currently has **Birds set to 30min** and the other three set to **loop**.
If the camera stores that, `GET_SOUNDTRACKS` should now report Birds differently
from the rest:

```bash
curl -s -X POST http://localhost:8080/api/debug/get \
  -H 'Content-Type: application/json' -d '{"type":"GET_SOUNDTRACKS"}' | jq
```

Every entry read `{type: 0, name: "..."}` when all four were on the same
setting. If Birds now differs, `Soundtrack.type` is the mode field and the whole
problem collapses to sending the right value. Change a track's setting in the
app and re-run to confirm.

#### If the camera does not store it

Then the app sends the mode with the play command, and it has to be found by
writing. Three things to try, in order:

1. **`Soundtrack.type`.** It has been `0` in every capture, which would be the
   default. Try other values:

```bash
curl -s -X POST http://localhost:8080/api/debug/playback \
  -H 'Content-Type: application/json' \
  -d '{"name":"Wind.wav","soundtrack_type":1}' | jq
```

   Wait past fifteen seconds. Values 1, 2 and 3 map naturally onto 30min, 60min
   and loop.

2. **Unswept `Playback` tags.** Tags 2, 5, 6, 7 and 8 accepted bytes and ignored
   them, so none is a varint. Tags 9 and above have never been tried:

```bash
for tag in 9 10 11 12 13 14 15 16; do
  echo "--- tag $tag"
  curl -s -X POST http://localhost:8080/api/debug/playback \
    -H 'Content-Type: application/json' \
    -d "{\"name\":\"Wind.wav\",\"tag\":$tag,\"varint\":true,\"value\":1}" \
    | jq -c '{tag,status:.status_code,msg:.status_message}'
  sleep 15
  curl -s -X POST http://localhost:8080/api/debug/playback \
    -H 'Content-Type: application/json' -d '{"stop":true}' > /dev/null
done
```

   A timeout is a positive result: it means the tag exists with a different wire
   type, which is how tags 3 and 4 were found.

3. **Nanit's cloud.** If the setting is stored server-side it will show up in a
   diff. Capture, change a track's timer in the app, capture again:

```bash
curl -s -X POST http://localhost:8080/api/debug/rest \
  -H 'Content-Type: application/json' -d '{"path":"/babies"}' | jq -S . > before.json
# change Birds from 30min to loop in the app, then
curl -s -X POST http://localhost:8080/api/debug/rest \
  -H 'Content-Type: application/json' -d '{"path":"/babies"}' | jq -S . > after.json
diff before.json after.json
```

   Also worth trying `/babies/<uid>`, `/babies/<uid>/settings` and
   `/babies/<uid>/soundtracks`.

If all three come up empty, the setting lives in the app's own storage and the
mode must still reach the camera somehow — in which case the play command is the
only place left, and the tag sweep above is the way to find it.

#### A note on ordering

The app lists the sounds alphabetically (Birds, Waves, White Noise, Wind) and
numbers them 1-4 for display. `GET_SOUNDTRACKS` returns them in a different
order (White Noise, Birds, Waves, Wind). The numbers in the app are not
identifiers, and the catalog order is the camera's own — this project keys off
filenames, so neither ordering matters, but it is worth knowing before reading
an index into anything.

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
