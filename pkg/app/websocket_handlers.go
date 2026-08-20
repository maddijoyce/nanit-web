package app

import (
	"errors"
	"strings"
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

// ---------------------------------------------------------------------------
// Soundtrack (white noise) control
//
// Playback is driven by PUT_PLAYBACK carrying a Playback message, not by
// PUT_CONTROL as the unmapped Control tags first suggested. Captured from a
// live camera:
//
//	stop:  request:{type:PUT_PLAYBACK playback:{status:STOPPED}}
//	reply: response:{requestType:PUT_PLAYBACK statusCode:200 statusMessage:"OK"}
//
// GET_SOUNDTRACKS lists the built-in sounds as Response.soundtracks (field 12),
// each an optional type (0 on every built-in seen) and a filename. The filename
// is the identifier — there is no numeric id.
//
// One thing is still unconfirmed: which Playback field names the track to play.
// Stopping needs no name, and the camera does not broadcast track changes, so it
// could not be read off the wire. client.SoundtrackNameFieldTag holds the current
// hypothesis and is written as an unknown field, so other tags can be tried
// without regenerating the schema. See docs/soundtrack-capture.md.
// ---------------------------------------------------------------------------

// sendSoundCommand - starts the named soundtrack, or stops playback when given
// an empty name or baby.SoundtrackOffName.
//
// State is set optimistically because the camera broadcasts a stop but never a
// start; without it a switch turned on in Home Assistant would spring back to
// off. It is then reconciled against GET_PLAYBACK, so what ends up published is
// what the camera reports rather than what was asked for.
func sendSoundCommand(babyUID string, name string, conn *client.WebsocketConnection, stateManager *baby.StateManager) error {
	if conn == nil {
		return errors.New("no websocket connection")
	}

	if name == "" || strings.EqualFold(name, baby.SoundtrackOffName) {
		log.Info().Str("baby_uid", babyUID).Msg("Stopping soundtrack playback")

		conn.SendRequest(client.RequestType_PUT_PLAYBACK, &client.Request{
			Playback: &client.Playback{
				Status: client.Playback_STOPPED.Enum(),
			},
		})

		stateManager.Update(babyUID, *baby.NewState().SetSoundtrack(baby.SoundtrackOffName))
		reconcilePlaybackState(babyUID, conn, stateManager)
		return nil
	}

	log.Info().Str("baby_uid", babyUID).Str("soundtrack", name).Msg("Starting soundtrack playback")

	// The camera reports the track on both fields; mirror that when setting it.
	conn.SendRequest(client.RequestType_PUT_PLAYBACK, &client.Request{
		Playback: &client.Playback{
			Status:             client.Playback_STARTED.Enum(),
			Soundtrack:         client.NewSoundtrackMessage(name),
			SelectedSoundtrack: client.NewSoundtrackMessage(name),
		},
	})

	stateManager.Update(babyUID, *baby.NewState().SetSoundtrack(name))
	reconcilePlaybackState(babyUID, conn, stateManager)
	return nil
}

// playbackReconcileDelay - how long to let a command take effect before asking
// the camera what actually happened
const playbackReconcileDelay = 1500 * time.Millisecond

// reconcilePlaybackState - re-reads playback state shortly after a command, so
// a command that did not take effect cannot leave a wrong value published
func reconcilePlaybackState(babyUID string, conn *client.WebsocketConnection, stateManager *baby.StateManager) {
	go func() {
		time.Sleep(playbackReconcileDelay)
		requestPlaybackState(babyUID, conn, stateManager)
	}()
}

// processPlayback - reflects a playback state reported by the camera.
//
// source names where the report came from, so an unexpected stop can be traced
// to the camera announcing it rather than to a read returning stale state.
func processPlayback(babyUID string, playback *client.Playback, source string, stateManager *baby.StateManager) {
	if playback == nil || playback.Status == nil {
		return
	}

	stateUpdate := baby.State{}

	if *playback.Status == client.Playback_STOPPED {
		stateUpdate.SetSoundtrack(baby.SoundtrackOffName)
		stateManager.Update(babyUID, stateUpdate)

		log.Info().Str("baby_uid", babyUID).Str("source", source).Msg("Camera reported soundtrack stopped")
		return
	}

	name := client.PlaybackSoundtrackName(playback)
	if name == "" {
		// A stop broadcast names no track, and neither does a bare start.
		// Keep what is known rather than blanking it.
		stateUpdate.SetSoundtrackPlaying(true)
		stateManager.Update(babyUID, stateUpdate)

		log.Debug().Str("baby_uid", babyUID).Str("source", source).Msg("Camera reported playback started without naming a track")
		return
	}

	stateUpdate.SetSoundtrack(name)
	stateManager.Update(babyUID, stateUpdate)

	log.Debug().Str("baby_uid", babyUID).Str("source", source).Str("soundtrack", name).Msg("Camera reported soundtrack playing")
}

// requestPlaybackState - asks the camera what it is currently playing.
//
// GET_PLAYBACK answers with Response.playback. Nothing else reports playback
// state: the camera broadcasts a stop but never a start or a track change, so
// polling this is the only way to learn what is playing after the fact.
func requestPlaybackState(babyUID string, conn *client.WebsocketConnection, stateManager *baby.StateManager) {
	awaitResponse := conn.SendRequest(client.RequestType_GET_PLAYBACK, &client.Request{})

	go func() {
		res, err := awaitResponse(30 * time.Second)
		if err != nil {
			log.Warn().Err(err).Str("baby_uid", babyUID).Msg("GET_PLAYBACK request failed")
			return
		}

		// Any field the schema still cannot name is worth seeing: the track
		// selector is expected to show up here once something is playing.
		if unknown := client.DescribeUnknownFields(res); len(unknown) > 0 {
			log.Info().
				Str("baby_uid", babyUID).
				Interface("unknown_fields", unknown).
				Str("raw", res.String()).
				Msg("GET_PLAYBACK response carried unmapped fields")
		}

		processPlayback(babyUID, res.GetPlayback(), "get-playback", stateManager)
	}()
}

// playbackPollInterval - how often playback state is re-read from the camera.
//
// The camera broadcasts a stop but never a start and never a track change, so a
// sound started from the Nanit app is invisible until something asks. Polling
// closes that gap; five minutes keeps the traffic negligible while bounding how
// stale the reported track can be.
const playbackPollInterval = 5 * time.Minute

// pollPlaybackState - re-reads playback state until the connection ends
func pollPlaybackState(babyUID string, conn *client.WebsocketConnection, stateManager *baby.StateManager, done <-chan struct{}) {
	go func() {
		ticker := time.NewTicker(playbackPollInterval)
		defer ticker.Stop()

		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				requestPlaybackState(babyUID, conn, stateManager)
			}
		}
	}()
}

// requestSoundtracks - asks the camera for its built-in sound catalog
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

// processSoundtracksResponse - records the camera's soundtrack catalog
func processSoundtracksResponse(babyUID string, res *client.Response, stateManager *baby.StateManager) {
	catalog := make([]baby.Soundtrack, 0, len(res.GetSoundtracks()))
	for _, entry := range res.GetSoundtracks() {
		if entry.GetName() == "" {
			continue
		}

		catalog = append(catalog, baby.NewSoundtrack(entry.GetName()))
	}

	if len(catalog) == 0 {
		// Anything the schema failed to account for is still worth seeing
		log.Warn().
			Str("baby_uid", babyUID).
			Interface("unknown_fields", client.DescribeUnknownFields(res)).
			Str("raw", res.String()).
			Msg("GET_SOUNDTRACKS returned no usable soundtracks")
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

// resumeSoundtrackName - the track to start when playback is switched on without
// naming one: the last one chosen, else the camera's first sound
func resumeSoundtrackName(state *baby.State) string {
	catalog := state.GetAvailableSoundtracks()

	// SoundtrackName is "Off" once stopped, so the remembered selection is what
	// answers "what was playing".
	if selected := state.GetSelectedSoundtrack(); selected != "" {
		for _, entry := range catalog {
			if entry.Name == selected {
				return selected
			}
		}
	}

	if len(catalog) > 0 {
		return catalog[0].Name
	}

	return ""
}
