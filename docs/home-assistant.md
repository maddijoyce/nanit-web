# Home Assistant setup

Everything except the video feed arrives automatically over MQTT discovery.
The camera is the one piece that has to be added by hand, because it is an RTMP
stream rather than an MQTT entity.

## 1. Prerequisites

- An MQTT broker reachable from both this app and Home Assistant. If you use the
  **Mosquitto broker** add-on, its address from another container is usually
  `tcp://homeassistant.local:1883` or `tcp://<HA_IP>:1883`.
- The **MQTT integration** set up in Home Assistant
  (*Settings → Devices & Services → Add Integration → MQTT*).

## 2. Point the bridge at your broker

Set these on the container and restart it:

```bash
-e NANIT_MQTT_ENABLED=true \
-e NANIT_MQTT_BROKER_URL=tcp://homeassistant.local:1883 \
-e NANIT_MQTT_USERNAME=your_mqtt_user \
-e NANIT_MQTT_PASSWORD=your_mqtt_password
```

In `docker-compose.yml`:

```yaml
environment:
  NANIT_MQTT_ENABLED: "true"
  NANIT_MQTT_BROKER_URL: "tcp://homeassistant.local:1883"
  NANIT_MQTT_USERNAME: "your_mqtt_user"
  NANIT_MQTT_PASSWORD: "your_mqtt_password"
```

Username and password can be omitted if your broker allows anonymous access.

Discovery is on by default. The other MQTT settings rarely need changing:

| Variable | Default | Notes |
|---|---|---|
| `NANIT_MQTT_PREFIX` | `nanit` | Prefix for state and command topics |
| `NANIT_MQTT_CLIENT_ID` | `nanit` | Must be unique per broker connection |
| `NANIT_MQTT_DISCOVERY` | `true` | Set `false` to publish state without creating entities |
| `NANIT_MQTT_DISCOVERY_PREFIX` | `homeassistant` | Only change if you changed it in the HA MQTT integration |

## 3. Check it worked

In the logs you should see:

```
Successfully connected to MQTT broker
Published Home Assistant discovery configuration
```

Then in Home Assistant, look under *Settings → Devices & Services → MQTT →
Devices* for a device named **Nanit \<baby_uid\>**. Entities appear once the
first state update arrives, usually within a few seconds of the camera
connecting.

### Entities

| Entity | Type | Notes |
|---|---|---|
| Temperature | sensor | °C, recorded in long-term statistics |
| Humidity | sensor | %, recorded in long-term statistics |
| Night | binary sensor | The camera's day/night detection |
| Stream Alive | binary sensor | Local RTMP stream health |
| Night Light | switch | |
| Standby | switch | |
| Volume | number | 0-100 |
| Soundtrack | switch | Starts and stops the built-in sounds |
| Soundtrack Selection | select | Off plus the sounds the camera reports |

The soundtrack list comes from the camera itself, so it matches whatever your
model ships with — typically White Noise, Birds, Waves and Wind.

## 4. The camera, added manually

Discovery does not cover the video feed. Add it to `configuration.yaml`:

```yaml
camera:
  - name: Nanit
    platform: ffmpeg
    input: rtmp://YOUR_LOCAL_IP:1935/local/YOUR_BABY_UID
```

`YOUR_LOCAL_IP:1935` is whatever you set as `NANIT_RTMP_ADDR`. The baby UID is
shown on the dashboard at `http://localhost:8080` and in the logs.

## Troubleshooting

**No device appears.** Check the log for `Successfully connected to MQTT
broker`. If that line is missing the broker URL, credentials or network path is
wrong. If it is present but no device shows up, confirm the discovery prefix
matches the one configured in the HA MQTT integration.

**The Soundtrack Selection dropdown snaps back to Off.** Home Assistant
discards a select state that is not one of the entity's options. The state topic
`soundtrack_display_name` carries the name without its file extension, matching
the options; `soundtrack_name` carries the raw filename and is not what the
select reads. If you see this after an upgrade, the retained discovery config is
stale — restart the bridge so it republishes.

**Entities exist but never update.** State is published to
`nanit/babies/<baby_uid>/<key>`. Watch it with:

```bash
mosquitto_sub -h YOUR_BROKER -t 'nanit/#' -v
```

If nothing arrives, the camera websocket is probably not connected — check the
dashboard.

**Controls do nothing.** Commands are read from
`nanit/babies/<baby_uid>/<control>/set` or `.../switch`. Publish one by hand to
isolate whether the problem is Home Assistant or the bridge:

```bash
mosquitto_pub -h YOUR_BROKER -t 'nanit/babies/YOUR_BABY_UID/night_light/switch' -m 'true'
```

**Stale entities after changing the topic prefix or baby UID.** Discovery
messages are retained, so old entities persist until their config topic is
cleared:

```bash
mosquitto_pub -h YOUR_BROKER -t 'homeassistant/switch/nanit_OLD_UID/night_light/config' -r -n
```

**Sound stops on its own after about ten seconds.** Known limitation, most
likely the length of the audio clip. The Nanit app offers 30min, 60min and loop
per sound, so the camera appears to play a track once through unless told to
repeat, and this project does not yet know how to ask for that. Starting the
sound from the Nanit app instead plays it as configured. See
[soundtrack-capture.md](soundtrack-capture.md).

**A sound started from the Nanit app takes a while to show.** Expected: the
camera announces playback stops but not starts or track changes, so playback
state is polled every five minutes. See
[soundtrack-capture.md](soundtrack-capture.md).

## See also

- [Setup with NVR/Zoneminder](https://community.home-assistant.io/t/nanit-showing-in-ha-via-nvr-zoneminder/251641) by @jaburges
