package mqtt

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	MQTT "github.com/eclipse/paho.mqtt.golang"
	"github.com/indiefan/home_assistant_nanit/pkg/baby"
	"github.com/indiefan/home_assistant_nanit/pkg/utils"
	"github.com/rs/zerolog/log"
)

type SendLightCommandHandler func(nightLightState bool)
type SendStandbyCommandHandler func(standbyState bool)
type SendVolumeCommandHandler func(level int32)
type SendSoundtrackCommandHandler func(soundtrackName string)

// Connection - MQTT context
type Connection struct {
	Opts                      Opts
	StateManager              *baby.StateManager
	client                    MQTT.Client
	sendLightCommandHandler   SendLightCommandHandler
	sendStandbyCommandHandler SendStandbyCommandHandler
	sendVolumeCommandHandler  SendVolumeCommandHandler

	sendSoundtrackCommandHandler SendSoundtrackCommandHandler

	discovery *discoveryState
}

// NewConnection - constructor
func NewConnection(opts Opts) *Connection {
	return &Connection{
		Opts:      opts,
		discovery: newDiscoveryState(),
	}
}

// Run - runs the mqtt connection handler
func (conn *Connection) Run(manager *baby.StateManager, ctx utils.GracefulContext) {
	conn.StateManager = manager

	opts := MQTT.NewClientOptions()
	opts.AddBroker(conn.Opts.BrokerURL)
	// ClientID must be unique per broker connection. It was previously set from
	// the topic prefix, which silently ignored NANIT_MQTT_CLIENT_ID and made two
	// instances sharing a prefix fight over one client id.
	clientID := conn.Opts.ClientID
	if clientID == "" {
		clientID = conn.Opts.TopicPrefix
	}
	opts.SetClientID(clientID)
	opts.SetUsername(conn.Opts.Username)
	opts.SetPassword(conn.Opts.Password)
	opts.SetCleanSession(false)

	conn.client = MQTT.NewClient(opts)

	utils.RunWithPerseverance(func(attempt utils.AttemptContext) {
		runMqtt(conn, attempt)
	}, ctx, utils.PerseverenceOpts{
		RunnerID:       "mqtt",
		ResetThreshold: 2 * time.Second,
		Cooldown: []time.Duration{
			2 * time.Second,
			10 * time.Second,
			1 * time.Minute,
		},
	})
}

func (conn *Connection) RegisterLightHandler(sendLightCommandHandler SendLightCommandHandler) {
	conn.sendLightCommandHandler = sendLightCommandHandler
}

func (conn *Connection) subscribeToLightCommand() {
	commandTopic := fmt.Sprintf("%v/babies/+/night_light/switch", conn.Opts.TopicPrefix)
	log.Debug().
		Str("topic", commandTopic).
		Msg("Subscribing to command topic")

	lightMessageHandler := func(mqttConn MQTT.Client, msg MQTT.Message) {
		// Extract baby UID and command from topic
		parts := strings.Split(msg.Topic(), "/")
		if len(parts) < 5 {
			log.Error().Str("topic", msg.Topic()).Msg("Invalid command topic format")
			return
		}

		babyUID := parts[2]
		command := parts[4]

		// Validate baby UID
		if err := baby.EnsureValidBabyUID(babyUID); err != nil {
			log.Error().Err(err).Str("topic", msg.Topic()).Msg("Invalid baby UID in MQTT light topic")
			return
		}

		// Handle different commands
		switch command {
		case "switch":
			enabled := string(msg.Payload()) == "true"
			log.Debug().
				Str("baby", babyUID).
				Bool("enabled", enabled).
				Str("payload", string(msg.Payload())).
				Msg("Received light command")

			if conn.sendLightCommandHandler == nil {
				log.Warn().Str("baby", babyUID).Msg("Received light command before a camera connection was registered, ignoring")
				return
			}

			conn.sendLightCommandHandler(enabled)
		default:
			log.Warn().Str("command", command).Msg("Unknown command received")
		}
	}

	if token := conn.client.Subscribe(commandTopic, 0, lightMessageHandler); token.Wait() && token.Error() != nil {
		log.Error().Err(token.Error()).Str("topic", commandTopic).Msg("Failed to subscribe to command topic")
	}
}

func (conn *Connection) RegisterStandyHandler(sendStandbyCommandHandler SendStandbyCommandHandler) {
	conn.sendStandbyCommandHandler = sendStandbyCommandHandler
}

func (conn *Connection) subscribeToStandbyCommand() {
	commandTopic := fmt.Sprintf("%v/babies/+/standby/switch", conn.Opts.TopicPrefix)
	log.Debug().
		Str("topic", commandTopic).
		Msg("Subscribing to command topic")

	standbyMessageHandler := func(mqttConn MQTT.Client, msg MQTT.Message) {
		// Extract baby UID and command from topic
		parts := strings.Split(msg.Topic(), "/")
		if len(parts) < 5 {
			log.Error().Str("topic", msg.Topic()).Msg("Invalid command topic format")
			return
		}

		babyUID := parts[2]
		command := parts[4]

		// Validate baby UID
		if err := baby.EnsureValidBabyUID(babyUID); err != nil {
			log.Error().Err(err).Str("topic", msg.Topic()).Msg("Invalid baby UID in MQTT standby topic")
			return
		}

		// Handle different commands
		switch command {
		case "switch":
			enabled := string(msg.Payload()) == "true"
			log.Debug().
				Str("baby", babyUID).
				Bool("enabled", enabled).
				Str("payload", string(msg.Payload())).
				Msg("Received standby command")

			if conn.sendStandbyCommandHandler == nil {
				log.Warn().Str("baby", babyUID).Msg("Received standby command before a camera connection was registered, ignoring")
				return
			}

			conn.sendStandbyCommandHandler(enabled)
		default:
			log.Warn().Str("command", command).Msg("Unknown command received")
		}
	}

	if token := conn.client.Subscribe(commandTopic, 0, standbyMessageHandler); token.Wait() && token.Error() != nil {
		log.Error().Err(token.Error()).Str("topic", commandTopic).Msg("Failed to subscribe to command topic")
	}
}

func (conn *Connection) RegisterVolumeHandler(sendVolumeCommandHandler SendVolumeCommandHandler) {
	conn.sendVolumeCommandHandler = sendVolumeCommandHandler
}

func (conn *Connection) subscribeToVolumeCommand() {
	commandTopic := fmt.Sprintf("%v/babies/+/volume/set", conn.Opts.TopicPrefix)
	log.Debug().
		Str("topic", commandTopic).
		Msg("Subscribing to command topic")

	volumeMessageHandler := func(mqttConn MQTT.Client, msg MQTT.Message) {
		// Extract baby UID and command from topic
		parts := strings.Split(msg.Topic(), "/")
		if len(parts) < 5 {
			log.Error().Str("topic", msg.Topic()).Msg("Invalid command topic format")
			return
		}

		babyUID := parts[2]
		command := parts[4]

		// Validate baby UID
		if err := baby.EnsureValidBabyUID(babyUID); err != nil {
			log.Error().Err(err).Str("topic", msg.Topic()).Msg("Invalid baby UID in MQTT volume topic")
			return
		}

		// Handle different commands
		switch command {
		case "set":
			payload := strings.TrimSpace(string(msg.Payload()))

			// Home Assistant number entities publish floats ("42.0") as readily as ints
			level, err := strconv.ParseFloat(payload, 64)
			if err != nil {
				log.Error().Err(err).Str("payload", payload).Msg("Unable to parse volume command payload")
				return
			}

			log.Debug().
				Str("baby", babyUID).
				Float64("level", level).
				Str("payload", payload).
				Msg("Received volume command")

			if conn.sendVolumeCommandHandler == nil {
				log.Warn().Str("baby", babyUID).Msg("Received volume command before a camera connection was registered, ignoring")
				return
			}

			conn.sendVolumeCommandHandler(int32(math.Round(level)))
		default:
			log.Warn().Str("command", command).Msg("Unknown command received")
		}
	}

	if token := conn.client.Subscribe(commandTopic, 0, volumeMessageHandler); token.Wait() && token.Error() != nil {
		log.Error().Err(token.Error()).Str("topic", commandTopic).Msg("Failed to subscribe to command topic")
	}
}

func (conn *Connection) RegisterSoundtrackHandler(sendSoundtrackCommandHandler SendSoundtrackCommandHandler) {
	conn.sendSoundtrackCommandHandler = sendSoundtrackCommandHandler
}

// subscribeToSoundtrackCommand - handles both the on/off switch and the
// select-one-of-N topics, since they differ only in how the payload maps to a
// soundtrack id
func (conn *Connection) subscribeToSoundtrackCommand() {
	commandTopic := fmt.Sprintf("%v/babies/+/soundtrack/+", conn.Opts.TopicPrefix)
	log.Debug().
		Str("topic", commandTopic).
		Msg("Subscribing to command topic")

	soundtrackMessageHandler := func(mqttConn MQTT.Client, msg MQTT.Message) {
		// Extract baby UID and command from topic
		parts := strings.Split(msg.Topic(), "/")
		if len(parts) < 5 {
			log.Error().Str("topic", msg.Topic()).Msg("Invalid command topic format")
			return
		}

		babyUID := parts[2]
		command := parts[4]

		// Validate baby UID
		if err := baby.EnsureValidBabyUID(babyUID); err != nil {
			log.Error().Err(err).Str("topic", msg.Topic()).Msg("Invalid baby UID in MQTT soundtrack topic")
			return
		}

		payload := strings.TrimSpace(string(msg.Payload()))

		var soundtrackName string
		switch command {
		case "switch":
			enabled := payload == "true" || payload == "ON"
			soundtrackName = conn.resolveSoundtrackSwitch(babyUID, enabled)

			log.Debug().
				Str("baby", babyUID).
				Bool("enabled", enabled).
				Str("soundtrack", soundtrackName).
				Str("payload", payload).
				Msg("Received soundtrack switch command")

		case "select":
			resolved, ok := conn.resolveSoundtrackName(babyUID, payload)
			if !ok {
				log.Error().
					Str("baby", babyUID).
					Str("payload", payload).
					Msg("Unknown soundtrack name; the camera has not reported a matching sound")
				return
			}
			soundtrackName = resolved

			log.Debug().
				Str("baby", babyUID).
				Str("soundtrack", soundtrackName).
				Str("payload", payload).
				Msg("Received soundtrack select command")

		default:
			log.Warn().Str("command", command).Msg("Unknown command received")
			return
		}

		if conn.sendSoundtrackCommandHandler == nil {
			log.Warn().Str("baby", babyUID).Msg("Received soundtrack command before a camera connection was registered, ignoring")
			return
		}

		conn.sendSoundtrackCommandHandler(soundtrackName)
	}

	if token := conn.client.Subscribe(commandTopic, 0, soundtrackMessageHandler); token.Wait() && token.Error() != nil {
		log.Error().Err(token.Error()).Str("topic", commandTopic).Msg("Failed to subscribe to command topic")
	}
}

// resolveSoundtrackSwitch - maps an on/off request onto a track name.
// Turning on resumes whatever was last playing, falling back to the first sound
// the camera reports.
func (conn *Connection) resolveSoundtrackSwitch(babyUID string, enabled bool) string {
	if !enabled {
		return baby.SoundtrackOffName
	}

	state := conn.StateManager.GetBabyState(babyUID)
	if current := state.GetSoundtrackName(); current != "" && !strings.EqualFold(current, baby.SoundtrackOffName) {
		// Only resume a name the camera would recognise
		for _, entry := range state.GetAvailableSoundtracks() {
			if entry.Name == current {
				return current
			}
		}
	}

	if catalog := state.GetAvailableSoundtracks(); len(catalog) > 0 {
		return catalog[0].Name
	}

	// Nothing known yet; stopping is safer than guessing a filename
	return baby.SoundtrackOffName
}

// resolveSoundtrackName - maps a display name from the select entity onto the
// filename the camera expects
func (conn *Connection) resolveSoundtrackName(babyUID string, name string) (string, bool) {
	if name == "" || strings.EqualFold(name, baby.SoundtrackOffName) {
		return baby.SoundtrackOffName, true
	}

	for _, entry := range conn.StateManager.GetBabyState(babyUID).GetAvailableSoundtracks() {
		if strings.EqualFold(entry.DisplayName, name) || strings.EqualFold(entry.Name, name) {
			return entry.Name, true
		}
	}

	return "", false
}

func runMqtt(conn *Connection, attempt utils.AttemptContext) {

	if token := conn.client.Connect(); token.Wait() && token.Error() != nil {
		log.Error().Str("broker_url", conn.Opts.BrokerURL).Err(token.Error()).Msg("Unable to connect to MQTT broker")
		attempt.Fail(token.Error())
		return
	}

	log.Info().Str("broker_url", conn.Opts.BrokerURL).Msg("Successfully connected to MQTT broker")

	// A reconnect may mean a restarted broker with no retained messages left,
	// so the announcements are re-published for the new session.
	conn.discovery.reset()

	unsubscribe := conn.StateManager.Subscribe(func(babyUID string, state baby.State) {
		conn.publishDiscovery(babyUID)

		publish := func(key string, value interface{}) {
			topic := fmt.Sprintf("%v/babies/%v/%v", conn.Opts.TopicPrefix, babyUID, key)
			log.Trace().Str("topic", topic).Interface("value", value).Msg("MQTT publish")

			token := conn.client.Publish(topic, 0, false, fmt.Sprintf("%v", value))
			if token.Wait(); token.Error() != nil {
				log.Error().Err(token.Error()).Msgf("Unable to publish %v update", key)
			}
		}

		for key, value := range state.AsMap(false) {
			publish(key, value)
		}

		if state.StreamState != nil && *state.StreamState != baby.StreamState_Unknown {
			publish("is_stream_alive", *state.StreamState == baby.StreamState_Alive)
		}
	})

	// Subscribe to accept light mqtt messages
	conn.subscribeToLightCommand()
	conn.subscribeToStandbyCommand()
	conn.subscribeToVolumeCommand()
	conn.subscribeToSoundtrackCommand()

	// Wait until interrupt signal is received
	<-attempt.Done()

	log.Debug().Msg("Closing MQTT connection on interrupt")
	unsubscribe()
	conn.client.Disconnect(250)
}
