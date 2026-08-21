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
| **Play indefinitely** | **Solved — `Playback.duration = -1`** |

### Solved: playback stops when the clip ends, `duration` keeps it looping

A sound started from this bridge used to stop by itself once the clip ran out
(~20-50s depending on the track), while a sound started from the Nanit app
played forever. The missing piece was a field on the play command, not any timer
in this code.

`Playback` has a **`duration` field at tag 2 (int32)** that the earlier captures
never revealed, because a stop broadcast and a bare start never carry it. It is
the seconds-to-play the app's per-track **30min / 60min / infinite** choice sets:

- omitted → the camera plays the clip once and stops (the old behaviour)
- a positive value → play for that many seconds
- **`-1` → play forever**

This bridge now sends `duration = -1` on every start (see `sendSoundCommand`),
so a sound loops until it is stopped, matching the app's "infinite" option.

#### How it was confirmed: the Nanit Android app

Field guessing on the wire had exhausted the local websocket, so the answer came
from decompiling the Nanit app (v4.78.0) instead. Its own protobuf schema,
generated by Square's Wire and readable straight out of the APK, spells the
message out in full:

```protobuf
message Playback {
  enum EStatus { STARTED = 0; STOPPED = 1; }
  required EStatus status = 1;
  optional int32 duration = 2;        // <- the field
  repeated Soundtrack tracks = 3;
  optional Soundtrack currentTrack = 4;
}

message Soundtrack {
  enum EStorage { FACTORY = 0; USER = 1; }
  required EStorage storage = 1;      // 0 = built-in, matches the "type" seen here
  required string filename = 2;
}
```

The app's `CameraManager.playSound` turns a Kotlin `Duration` into this field:

```kotlin
// duration in whole seconds; a zero Duration (the app's "infinite" choice)
// becomes null, which is sent as -1
val secs = duration.toInt(SECONDS)
api.playSoundtrack(babyId, filename, if (duration == ZERO) -1 else secs)
```

So the app's "infinite" is `-1` on the wire, and the earlier assumption that the
loop instruction lived off this websocket entirely was wrong — it was one
unmapped field on the play command all along.

Clip lengths, for reference, measured against a Nanit Pro before the fix:

| Sound | Clip length |
|---|---|
| Birds | ~20s |
| Wind | ~45s |
| White Noise | ~50s |

These are one full pass of each file, which is what the camera plays when no
`duration` is set.

#### Why the earlier wire probes missed it

The play command was swept field by field before, and `duration` (tag 2) was in
that sweep — but only ever probed with a *bytes* payload, which a varint field
silently discards, so it read as "not a known field" like the truly absent tags.
A stop broadcast and a bare start never carry `duration`, and `GET_PLAYBACK`
replies in these captures happened not to include it either, so nothing on the
wire ever revealed it. The field was reachable from the local websocket the
whole time; the earlier conclusion that the loop instruction must travel by some
other route (Nanit's cloud, a STING request, an unmapped GET) was wrong. Reading
the app's own schema settled in minutes what field-guessing could not.

The `currentTrack` (tag 4) idea — that setting only field 4 might mean "play this
next" and hold the loop — is also moot: the app never sets field 4 on a play. It
sends `status`, `duration`, and a one-element `tracks` list, and that is all.

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
