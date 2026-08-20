package app

import (
	"errors"
	"strconv"
	"time"

	"github.com/indiefan/home_assistant_nanit/pkg/baby"
	"github.com/indiefan/home_assistant_nanit/pkg/client"
	"github.com/indiefan/home_assistant_nanit/pkg/utils"
	"github.com/rs/zerolog/log"
)

func processSensorData(babyUID string, sensorData []*client.SensorData, stateManager *baby.StateManager) {
	// Parse sensor update
	stateUpdate := baby.State{}
	for _, sensorDataSet := range sensorData {
		if *sensorDataSet.SensorType == client.SensorType_TEMPERATURE {
			stateUpdate.SetTemperatureMilli(*sensorDataSet.ValueMilli)
		}
		if *sensorDataSet.SensorType == client.SensorType_HUMIDITY {
			stateUpdate.SetHumidityMilli(*sensorDataSet.ValueMilli)
		}
		if *sensorDataSet.SensorType == client.SensorType_NIGHT {
			stateUpdate.SetIsNight(*sensorDataSet.Value == 1)
		}
	}

	stateManager.Update(babyUID, stateUpdate)
}

func requestLocalStreaming(babyUID string, targetURL string, streamingStatus client.Streaming_Status, conn *client.WebsocketConnection, stateManager *baby.StateManager) {
	for {
		switch streamingStatus {
		case client.Streaming_STARTED:
			log.Info().Str("target", targetURL).Msg("Requesting local streaming")
		case client.Streaming_PAUSED:
			log.Info().Str("target", targetURL).Msg("Pausing local streaming")
		case client.Streaming_STOPPED:
			log.Info().Str("target", targetURL).Msg("Stopping local streaming")
		}

		awaitResponse := conn.SendRequest(client.RequestType_PUT_STREAMING, &client.Request{
			Streaming: &client.Streaming{
				Id:       client.StreamIdentifier(client.StreamIdentifier_MOBILE).Enum(),
				RtmpUrl:  utils.ConstRefStr(targetURL),
				Status:   client.Streaming_Status(streamingStatus).Enum(),
				Attempts: utils.ConstRefInt32(1),
			},
		})

		_, err := awaitResponse(30 * time.Second)

		if err != nil {
			if err.Error() == "Forbidden: Number of Mobile App connections above limit, declining connection" {
				log.Warn().Err(err).Msg("Too many app connections, will retry via background monitor...")
				stateManager.Update(babyUID, *baby.NewState().SetStreamRequestState(baby.StreamRequestState_RequestFailed))
				return // Exit and let the retry monitor handle it
			} else if err.Error() != "Request timeout" {
				if stateManager.GetBabyState(babyUID).GetStreamState() == baby.StreamState_Alive {
					log.Info().Err(err).Msg("Failed to request local streaming, but stream seems to be alive from previous run")
				} else if stateManager.GetBabyState(babyUID).GetStreamState() == baby.StreamState_Unhealthy {
					log.Error().Err(err).Msg("Failed to request local streaming and stream seems to be dead")
					stateManager.Update(babyUID, *baby.NewState().SetStreamRequestState(baby.StreamRequestState_RequestFailed))
				} else {
					log.Warn().Err(err).Msg("Failed to request local streaming, awaiting stream health check")
					stateManager.Update(babyUID, *baby.NewState().SetStreamRequestState(baby.StreamRequestState_RequestFailed))
				}

				return
			}

			if !stateManager.GetBabyState(babyUID).GetIsWebsocketAlive() {
				return
			}

			log.Warn().Msg("Streaming request timeout, trying again")

		} else {
			log.Info().Msg("Local streaming successfully requested")
			stateManager.Update(babyUID, *baby.NewState().SetStreamRequestState(baby.StreamRequestState_Requested))
			return
		}
	}
}

func processLight(babyUID string, control *client.Control, stateManager *baby.StateManager) {
	if control.NightLight != nil {
		stateUpdate := baby.State{}
		stateUpdate.SetNightLight(*control.NightLight == client.Control_LIGHT_ON)
		stateManager.Update(babyUID, stateUpdate)
	}
}

func sendLightCommand(nightLightState bool, conn *client.WebsocketConnection) {
	nightLight := client.Control_LIGHT_OFF
	if nightLightState {
		nightLight = client.Control_LIGHT_ON
	}
	conn.SendRequest(client.RequestType_PUT_CONTROL, &client.Request{
		Control: &client.Control{
			NightLight: &nightLight,
		},
	})
}

func processStandby(babyUID string, settings *client.Settings, stateManager *baby.StateManager) {
	stateUpdate := baby.State{}
	deviceInfo := &baby.DeviceInfo{}

	// Extract standby mode
	if settings.SleepMode != nil {
		stateUpdate.SetStandby(*settings.SleepMode)
		deviceInfo.SleepMode = settings.SleepMode
	}

	// Extract other device configuration
	if settings.NightVision != nil {
		deviceInfo.NightVision = settings.NightVision
	}
	if settings.Volume != nil {
		deviceInfo.Volume = settings.Volume
		// Mirror onto the top-level state as well: DeviceInfo is marked internal
		// and therefore never reaches the MQTT state topics.
		stateUpdate.SetVolume(*settings.Volume)
	}
	if settings.StatusLightOn != nil {
		deviceInfo.StatusLight = settings.StatusLightOn
	}
	if settings.MicMuteOn != nil {
		deviceInfo.MicMute = settings.MicMuteOn
	}
	if settings.AntiFlicker != nil {
		antiFlicker := ""
		switch *settings.AntiFlicker {
		case client.Settings_FR50HZ:
			antiFlicker = "50Hz"
		case client.Settings_FR60HZ:
			antiFlicker = "60Hz"
		default:
			antiFlicker = "Unknown"
		}
		deviceInfo.AntiFlicker = &antiFlicker
	}
	if settings.WifiBand != nil {
		wifiBand := ""
		switch *settings.WifiBand {
		case client.Settings_ANY:
			wifiBand = "Any"
		case client.Settings_FR2_4GHZ:
			wifiBand = "2.4GHz"
		case client.Settings_FR5_0GHZ:
			wifiBand = "5.0GHz"
		default:
			wifiBand = "Unknown"
		}
		deviceInfo.WiFiBand = &wifiBand
	}
	if settings.MountingMode != nil {
		mountingMode := int32(*settings.MountingMode)
		deviceInfo.MountingMode = &mountingMode
	}

	// Extract sensor thresholds
	for _, sensor := range settings.Sensors {
		if sensor.SensorType != nil {
			switch *sensor.SensorType {
			case client.SensorType_TEMPERATURE:
				if sensor.LowThreshold != nil {
					deviceInfo.TempLowThreshold = sensor.LowThreshold
				}
				if sensor.HighThreshold != nil {
					deviceInfo.TempHighThreshold = sensor.HighThreshold
				}
			case client.SensorType_HUMIDITY:
				if sensor.LowThreshold != nil {
					deviceInfo.HumidityLowThreshold = sensor.LowThreshold
				}
				if sensor.HighThreshold != nil {
					deviceInfo.HumidityHighThreshold = sensor.HighThreshold
				}
			}
		}
	}

	// Extract stream settings
	for _, stream := range settings.Streams {
		if stream.Id != nil {
			switch *stream.Id {
			case client.StreamIdentifier_MOBILE:
				if stream.Bitrate != nil {
					deviceInfo.MobileBitrate = stream.Bitrate
				}
				if stream.BestFps != nil {
					deviceInfo.MobileFPS = stream.BestFps
				}
			case client.StreamIdentifier_DVR:
				if stream.Bitrate != nil {
					deviceInfo.DVRBitrate = stream.Bitrate
				}
				if stream.BestFps != nil {
					deviceInfo.DVRFPS = stream.BestFps
				}
			case client.StreamIdentifier_ANALYTICS:
				if stream.Bitrate != nil {
					deviceInfo.AnalyticsBitrate = stream.Bitrate
				}
				if stream.BestFps != nil {
					deviceInfo.AnalyticsFPS = stream.BestFps
				}
			}
		}
	}

	// Set last updated timestamp
	timestamp := time.Now().Unix()
	deviceInfo.LastUpdated = &timestamp

	// Set device info in state
	stateUpdate.DeviceInfo = deviceInfo
	stateManager.Update(babyUID, stateUpdate)

	log.Debug().Str("baby_uid", babyUID).Interface("device_info", deviceInfo).Msg("Updated device info from settings")
}

func sendStandbyCommand(standbyState bool, conn *client.WebsocketConnection) {
	conn.SendRequest(client.RequestType_PUT_SETTINGS, &client.Request{
		Settings: &client.Settings{
			SleepMode: &standbyState,
		},
	})
}

// Bounds for Settings.volume. The protobuf schema types it as a bare int32 and
// does not document a range; 0-100 matches the percentage the Nanit app shows.
// The value reported by GET_SETTINGS is logged at debug level (see
// processStandby) so the real range can be confirmed against a live camera.
const (
	MinVolume int32 = 0
	MaxVolume int32 = 100
)

// clampVolume - constrains a requested level to the range the camera accepts
func clampVolume(level int32) int32 {
	if level < MinVolume {
		return MinVolume
	}
	if level > MaxVolume {
		return MaxVolume
	}
	return level
}

func sendVolumeCommand(level int32, conn *client.WebsocketConnection) {
	volume := clampVolume(level)
	if volume != level {
		log.Warn().Int32("requested", level).Int32("clamped", volume).Msg("Volume out of range, clamping")
	}

	log.Debug().Int32("volume", volume).Msg("Sending volume command")

	conn.SendRequest(client.RequestType_PUT_SETTINGS, &client.Request{
		Settings: &client.Settings{
			Volume: &volume,
		},
	})
}

func processStatus(babyUID string, status *client.Status, stateManager *baby.StateManager) {
	stateUpdate := baby.State{}
	deviceInfo := &baby.DeviceInfo{}

	// Extract device information from status
	if status.CurrentVersion != nil {
		deviceInfo.FirmwareVersion = status.CurrentVersion
	}
	if status.HardwareVersion != nil {
		deviceInfo.HardwareVersion = status.HardwareVersion
	}
	if status.Mode != nil {
		mode := ""
		switch *status.Mode {
		case client.MountingMode_STAND:
			mode = "Stand"
		case client.MountingMode_TRAVEL:
			mode = "Travel"
		case client.MountingMode_SWITCH:
			mode = "Switch"
		default:
			mode = "Unknown"
		}
		deviceInfo.DeviceMode = &mode
	}
	if status.UpgradeDownloaded != nil {
		deviceInfo.UpgradeDownloaded = status.UpgradeDownloaded
	}

	// Set last updated timestamp
	timestamp := time.Now().Unix()
	deviceInfo.LastUpdated = &timestamp

	// Set device info in state
	stateUpdate.DeviceInfo = deviceInfo
	stateManager.Update(babyUID, stateUpdate)

	log.Debug().Str("baby_uid", babyUID).Interface("device_info", deviceInfo).Msg("Updated device info from status")
}

// ---------------------------------------------------------------------------
// Soundtrack (white noise) control
//
// The camera can play a small set of built-in sounds. Which Control field
// selects them is NOT part of the reverse-engineered schema: tags 1, 2 and 7+
// on Control are unmapped, and GET_SOUNDTRACKS (21) has no mapped response
// field either.
//
// soundtrackControlFieldTag below is the single value that needs filling in
// once the tag has been identified against a live camera. SOUNDTRACK_CAPTURE.md
// documents how to find it. While it is 0 the feature stays inert: sends are
// refused and inbound parsing is skipped, so an unconfigured build never puts
// guessed bytes on the wire to a baby monitor.
// ---------------------------------------------------------------------------

// soundtrackControlFieldTag - protobuf field number on Control that selects the
// active soundtrack. 0 means "not yet identified".
//
// TODO: set this to the tag discovered via SOUNDTRACK_CAPTURE.md.
const soundtrackControlFieldTag int32 = 0

// ErrSoundtrackFieldUnknown - returned by every soundtrack send path while the
// controlling field tag has not been identified
var ErrSoundtrackFieldUnknown = errors.New("soundtrack control field is not identified yet; see SOUNDTRACK_CAPTURE.md")

// soundtrackControlConfigured - whether the controlling field tag is known
func soundtrackControlConfigured() bool {
	return soundtrackControlFieldTag != 0
}

// sendSoundCommand - plays the given soundtrack, or stops playback when passed
// baby.SoundtrackOff
func sendSoundCommand(soundtrackID int32, conn *client.WebsocketConnection) error {
	if !soundtrackControlConfigured() {
		log.Warn().Msg("Ignoring soundtrack command: controlling field tag is not configured")
		return ErrSoundtrackFieldUnknown
	}

	log.Info().
		Int32("soundtrack", soundtrackID).
		Int32("field_tag", soundtrackControlFieldTag).
		Msg("Sending soundtrack command")

	control := &client.Control{}
	client.SetUnknownVarintField(control, soundtrackControlFieldTag, uint64(soundtrackID))

	conn.SendRequest(client.RequestType_PUT_CONTROL, &client.Request{
		Control: control,
	})

	return nil
}

// processSoundtrack - reads the active soundtrack out of an inbound Control
func processSoundtrack(babyUID string, control *client.Control, stateManager *baby.StateManager) {
	if !soundtrackControlConfigured() {
		return
	}

	value, ok := client.GetUnknownVarintField(control, soundtrackControlFieldTag)
	if !ok {
		return
	}

	catalog := stateManager.GetBabyState(babyUID).GetAvailableSoundtracks()

	stateUpdate := baby.State{}
	stateUpdate.SetSoundtrack(int32(value), catalog)
	stateManager.Update(babyUID, stateUpdate)

	log.Debug().
		Str("baby_uid", babyUID).
		Int32("soundtrack", int32(value)).
		Msg("Updated soundtrack state from camera")
}

// requestSoundtracks - asks the camera for its built-in sound catalog.
//
// The response shape is unmapped, so the reply is dumped field by field rather
// than decoded through the schema.
func requestSoundtracks(babyUID string, conn *client.WebsocketConnection, stateManager *baby.StateManager) {
	awaitResponse := conn.SendRequest(client.RequestType_GET_SOUNDTRACKS, &client.Request{})

	go func() {
		res, err := awaitResponse(30 * time.Second)
		if err != nil {
			log.Warn().Err(err).Str("baby_uid", babyUID).Msg("GET_SOUNDTRACKS request failed")
			return
		}

		processSoundtracksResponse(babyUID, res, stateManager)
	}()
}

// processSoundtracksResponse - logs a GET_SOUNDTRACKS reply in full and makes a
// best-effort attempt at extracting the catalog from it
func processSoundtracksResponse(babyUID string, res *client.Response, stateManager *baby.StateManager) {
	unknown := client.DescribeUnknownFields(res)

	// Always dump the raw shape: this is the payload SOUNDTRACK_CAPTURE.md asks
	// the operator to read the soundtrack identifiers out of.
	log.Info().
		Str("baby_uid", babyUID).
		Int32("status_code", res.GetStatusCode()).
		Str("status_message", res.GetStatusMessage()).
		Interface("unknown_fields", unknown).
		Str("raw", res.String()).
		Msg("GET_SOUNDTRACKS response")

	if len(unknown) == 0 {
		log.Warn().Str("baby_uid", babyUID).Msg("GET_SOUNDTRACKS returned no unmapped fields; the camera may not report a catalog")
		return
	}

	catalog := extractSoundtrackCatalog(unknown)
	if len(catalog) == 0 {
		log.Warn().Str("baby_uid", babyUID).Msg("Unable to infer a soundtrack catalog from the response; read the dump above and map it by hand")
		return
	}

	log.Info().
		Str("baby_uid", babyUID).
		Interface("soundtracks", catalog).
		Msg("Discovered camera soundtrack catalog")

	stateUpdate := baby.State{}
	stateUpdate.DeviceInfo = &baby.DeviceInfo{AvailableSoundtracks: catalog}
	stateManager.Update(babyUID, stateUpdate)
}

// extractSoundtrackCatalog - infers a soundtrack list from unmapped fields.
//
// Two shapes are recognised, covering the plausible encodings:
//   - repeated embedded messages holding a varint id and a string name
//   - repeated bare strings, in which case position is used as the id
//
// Anything else returns nothing, leaving the logged dump as the source of truth.
func extractSoundtrackCatalog(fields []client.UnknownField) []baby.Soundtrack {
	catalog := make([]baby.Soundtrack, 0)

	for _, field := range fields {
		if len(field.Nested) > 0 {
			entry, ok := soundtrackFromNested(field.Nested)
			if ok {
				catalog = append(catalog, entry)
			}
			continue
		}

		if name, ok := unquoteFieldValue(field.Value); ok {
			catalog = append(catalog, baby.Soundtrack{ID: int32(len(catalog) + 1), Name: name})
		}
	}

	if len(catalog) == 0 {
		return nil
	}

	return catalog
}

// soundtrackFromNested - pulls an (id, name) pair out of an embedded message
func soundtrackFromNested(fields []client.UnknownField) (baby.Soundtrack, bool) {
	entry := baby.Soundtrack{}
	haveID := false
	haveName := false

	for _, field := range fields {
		if !haveID && field.WireType == "varint" {
			if id, ok := field.Value.(uint64); ok {
				entry.ID = int32(id)
				haveID = true
				continue
			}
		}

		if !haveName {
			if name, ok := unquoteFieldValue(field.Value); ok {
				entry.Name = name
				haveName = true
			}
		}
	}

	// A name is what makes an entry useful; an id-only entry is not a catalog.
	if !haveName {
		return entry, false
	}

	if !haveID {
		return entry, false
	}

	return entry, true
}

// unquoteFieldValue - recovers the text of a printable length-delimited field,
// which DescribeUnknownFields renders as a quoted Go string literal
func unquoteFieldValue(value interface{}) (string, bool) {
	raw, ok := value.(string)
	if !ok {
		return "", false
	}

	unquoted, err := strconv.Unquote(raw)
	if err != nil {
		return "", false
	}

	if unquoted == "" {
		return "", false
	}

	return unquoted, true
}

// logInboundFrame - dumps a complete inbound message at trace level, including
// any field the schema does not map.
//
// Nanit propagates state changes to every connected session, so toggling a
// control in the phone app produces a broadcast on this connection too. That
// makes this the cheapest way to observe an unmapped control: watch the trace
// log while operating the app, and the field tag and value appear directly.
func logInboundFrame(babyUID string, m *client.Message) {
	event := log.Trace().
		Str("baby_uid", babyUID).
		Str("message_type", m.GetType().String())

	if m.Request != nil {
		event = event.
			Str("direction", "camera->us").
			Str("request_type", m.Request.GetType().String()).
			Int32("request_id", m.Request.GetId())
	}

	if m.Response != nil {
		event = event.
			Str("direction", "response").
			Str("request_type", m.Response.GetRequestType().String()).
			Int32("request_id", m.Response.GetRequestId()).
			Int32("status_code", m.Response.GetStatusCode())
	}

	if unknown := client.DescribeUnknownFields(m); len(unknown) > 0 {
		event = event.Interface("unknown_fields", unknown)
	}

	event.Str("raw", m.String()).Msg("Websocket frame")
}

// resumeSoundtrackID - the soundtrack to start when playback is switched on
// without naming one: whatever played last, else the camera's first sound
func resumeSoundtrackID(state *baby.State) int32 {
	if current := state.GetSoundtrack(); current != baby.SoundtrackOff {
		return current
	}

	if catalog := state.GetAvailableSoundtracks(); len(catalog) > 0 {
		return catalog[0].ID
	}

	return 1
}
