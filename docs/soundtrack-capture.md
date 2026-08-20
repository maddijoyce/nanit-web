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

#### It is the clip ending, confirmed

Stop times vary by track, measured against a Nanit Pro:

| Sound | Plays for |
|---|---|
| Birds | ~20s |
| Wind | ~45s |
| White Noise | ~50s |

These are clip lengths. The camera plays a file once through and stops. The
app's per-track **30min / 60min / loop** choice is a repeat instruction, and
without it the camera does exactly one pass.

#### The camera does the repeating, not the app

Force-quitting the Nanit app leaves a looping sound playing. So the repeat is
camera-side state, not the app re-issuing a play command each time a clip ends.
Something tells the camera to keep going, and it remembers.

#### Where it is not

`PUT_PLAYBACK` has been swept thoroughly and carries nothing else:

| Probe | Result | Reading |
|---|---|---|
| `Playback` tags 2, 5-8, bytes | 200, ignored | Not known fields |
| `Playback` tags 9-16, varint | 200, ignored | Not known fields |
| `Playback` tags 3, 4, bytes | Timeout | Known — the two Soundtrack messages |
| `Soundtrack.type` = 1 | `Bad Request: User storage is not supported` | Storage location, not a mode |
| `Soundtrack.type` = 2 | Timeout | Out of range for a small enum |

That error message is worth keeping: `Soundtrack.type` says *where the file
lives* — 0 for the camera's built-ins, 1 for user storage, which this model does
not support. It is not the repeat mode, and the schema now says so.

Neither `GET_SOUNDTRACKS` nor `GET_PLAYBACK` reports the per-track setting, and
`GET_PLAYBACK` looks identical whichever timer the app has set.

#### Everywhere it is not

The search space on the play command is now exhausted:

| Surface | Probe | Result |
|---|---|---|
| `Playback` tags 2, 5-8 | bytes | Ignored — not known fields |
| `Playback` tags 9-16 | varint | Ignored — not known fields |
| `Playback` tags 3, 4 | bytes | Timeout — these are the Soundtrack messages |
| `Soundtrack` tags 3-6 | varint | Ignored — not known fields |
| `Soundtrack.type` 1 / 2 | varint | Storage location, not a mode |
| `Settings` | full read, diffed across an app change | No field moves |
| `GET_SOUNDTRACKS` | full read | Only `{type, name}` per sound |
| `GET_PLAYBACK` | full read while playing | Only status and the two Soundtracks |

Nothing on the camera changes when the app's per-track timer changes, and
nothing in the play command carries a mode.

#### The premise worth re-testing

The reasoning so far rested on one inference: **a looping sound survives the
Nanit app being force-quit, therefore the camera is looping by itself.**

That does not follow. The camera holds a permanent connection to Nanit's cloud,
and the cloud could just as easily be re-issuing the play command each time a
clip ends. Killing the phone app would not interrupt that.

If the cloud is the repeater, there is no camera-side mode to find — which
matches every negative result above — and the right implementation here is to do
the same thing: re-issue the command when a clip finishes.

#### The observation that settles it

Playback broadcasts are now logged at info, so this needs no special tooling:

1. Start a sound from the Nanit app with **loop** selected.
2. Watch the log for two or three minutes.

```bash
docker logs -f nanit 2>&1 | grep "Camera broadcast a playback change"
```

**Repeated stop/start pairs, one per clip length** (~20s for Birds) mean
something outside the camera is driving the repeat. The camera is not looping;
it is being told to play again, and this project can do exactly the same.

**Silence for the whole time** means the camera really does loop internally, and
the instruction reaches it by a route not yet found — most likely Nanit's cloud
API rather than the local websocket, which the REST probe can chase:

```bash
curl -s -X POST http://localhost:8080/api/debug/rest \
  -H 'Content-Type: application/json' -d '{"path":"/babies"}' | jq -S . > before.json
# change a track's timer in the app, then
curl -s -X POST http://localhost:8080/api/debug/rest \
  -H 'Content-Type: application/json' -d '{"path":"/babies"}' | jq -S . > after.json
diff before.json after.json
```

#### If it turns out to be a re-issue loop

Then repeating here is not a workaround, it is the same mechanism. The pieces
are already in place: a confirmed stop arrives through `requestPlaybackState`,
which knows the connection and can send the play command again. The design that
was drafted and then set aside:

- remember the track and when it started, per baby
- on a confirmed stop, restart it
- learn each clip's length from the first full play, and treat a stop well short
  of that as somebody stopping deliberately rather than a clip ending, so a stop
  from the phone is honoured
- bound the total run so a sound cannot play forever through a bug
- clear the intent whenever a stop is commanded from here

The one genuinely unpleasant case is a stop from the Nanit app landing close to
a clip boundary, which is indistinguishable from the clip ending. The learned
clip length narrows it but cannot eliminate it.

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
