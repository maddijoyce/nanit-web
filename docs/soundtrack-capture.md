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

#### Where to look next

Two places remain untested.

**1. Settings.** Still the prime suspect: camera-side, persistent, and never
successfully read.

`GET_SETTINGS` fails, and the camera says why:

```
Bad Request: missed 'getsettings' field
```

It wants a selector sub-message on `Request` that **is not in this schema at
all**. The mapped selectors are `getSensorData = 12`, `getStatus = 8`,
`getControl = 17` and `getLogs = 18`; there is no `getSettings`. `Request` has
tags 3, 6, 9, 10, 11, 14 and 19+ free, and `getSettings = 6` would mirror
`settings = 5` the way `getStatus = 8` mirrors `status = 7`.

This also means the startup `GET_SETTINGS` has been failing all along. Its reply
is never awaited, so the error was discarded silently, and everything known
about settings has arrived through unsolicited `PUT_SETTINGS` broadcasts
instead.

Sweeping is unambiguous here, because the wrong tag keeps producing the same
error and the right one does not:

```bash
for tag in 6 3 9 10 11 14 19 20; do
  echo -n "tag $tag: "
  curl -s -X POST http://localhost:8080/api/debug/get \
    -H 'Content-Type: application/json' \
    -d "{\"type\":\"GET_SETTINGS\",\"selector_tag\":$tag}" \
    | jq -c '{error, status: .status_code, unknown: .unknown_fields}'
done
```

`selector_tag` attaches a boolean selector as an unknown field at that tag; a
test asserts the encoding is byte-identical to a generated one, so a hit is a
real hit. Add `"flags":[1,2,3]` to set several booleans if a bare `all` is not
enough.

Once a settings reply comes back, `processStandby` logs anything the schema
cannot name at info level:

```
INF Settings carried unmapped fields baby_uid=... unknown_fields=[...]
```

`Settings` maps tags 2, 7-12, 15, 18 and 20, leaving 1, 3-6, 13, 14, 16, 17, 19
and 21+ free — plenty of room for a soundtrack section. If a field appears,
change a track's timer in the app and compare.

`GET_LOGS` fails the same way with `missed 'getlogs' field`; that selector *is*
mapped, and the probe now takes `"logs_url"` to fill it. The camera uploads its
logs to that URL, which may well name the setting outright.

**Status carries an unmapped field too**, seen at startup:

```
INF Status carried unmapped fields unknown_fields=[{"path":"Status","tag":8,"value":125,"wire_type":"varint"}]
```

Tag 8, varint, 125. Meaning unknown and probably unrelated to soundtracks —
recorded here so it is not rediscovered from scratch.

**2. Inside the Soundtrack message.** `Soundtrack` has only tags 1 and 2 in the
schema and nothing has probed past them. A per-track setting belongs here rather
than on `Playback`, which matches the app offering the choice per sound:

```bash
for tag in 3 4 5 6; do
  echo "--- soundtrack tag $tag"
  curl -s -X POST http://localhost:8080/api/debug/playback \
    -H 'Content-Type: application/json' \
    -d "{\"name\":\"Birds.wav\",\"soundtrack_tag\":$tag,\"soundtrack_value\":1}" \
    | jq -c '{status:.status_code,msg:.status_message,sent:.sent}'
  sleep 30
  curl -s -X POST http://localhost:8080/api/debug/playback \
    -H 'Content-Type: application/json' -d '{"stop":true}' > /dev/null
done
```

Birds is the shortest clip at ~20s, so 30 seconds per attempt makes surviving
the end obvious. A timeout is a positive result — it means the tag exists with a
different wire type.

Also worth trying `PUT_SETTINGS` and the request types this project has never
called: `GET_UOM` (40) and `PUT_UOM` (41), and `GET_LOGS` (24), which might name
the setting outright.

#### The fallback, if the camera cannot be told

If no field turns up, the bridge could restart the track itself each time the
camera reports a stop, using the confirmed-stop path that already exists. That
was deliberately not built: the camera plainly can loop on its own, and a
restart loop would fight anyone stopping the sound from the app, since a
deliberate stop and a clip ending look identical on the wire. Worth revisiting
only once the protocol route is exhausted.

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
