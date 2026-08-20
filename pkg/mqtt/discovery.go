package mqtt

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/indiefan/home_assistant_nanit/pkg/baby"
	"github.com/indiefan/home_assistant_nanit/pkg/client"
	"github.com/rs/zerolog/log"
)

// ---------------------------------------------------------------------------
// Home Assistant MQTT discovery
//
// Entities are announced by publishing a retained config document to
// <discovery_prefix>/<component>/<node_id>/<object_id>/config. Home Assistant
// reads those on startup and whenever they change, and creates the entity.
//
// Without this the state topics are still published, but nothing appears in
// Home Assistant unless the user hand-writes MQTT entity YAML.
// ---------------------------------------------------------------------------

// discoveryDevice - groups every entity of one camera under a single HA device
type discoveryDevice struct {
	Identifiers  []string `json:"identifiers"`
	Name         string   `json:"name"`
	Manufacturer string   `json:"manufacturer"`
	Model        string   `json:"model"`
	SWVersion    string   `json:"sw_version,omitempty"`
}

// discoveryEntity - one Home Assistant entity announcement.
//
// The field set is the union of what the components used here accept; unused
// fields are omitted so each component only receives keys it understands.
type discoveryEntity struct {
	Name              string          `json:"name"`
	UniqueID          string          `json:"unique_id"`
	StateTopic        string          `json:"state_topic"`
	CommandTopic      string          `json:"command_topic,omitempty"`
	DeviceClass       string          `json:"device_class,omitempty"`
	UnitOfMeasurement string          `json:"unit_of_measurement,omitempty"`
	StateClass        string          `json:"state_class,omitempty"`
	PayloadOn         string          `json:"payload_on,omitempty"`
	PayloadOff        string          `json:"payload_off,omitempty"`
	StateOn           string          `json:"state_on,omitempty"`
	StateOff          string          `json:"state_off,omitempty"`
	Min               *float64        `json:"min,omitempty"`
	Max               *float64        `json:"max,omitempty"`
	Step              *float64        `json:"step,omitempty"`
	Options           []string        `json:"options,omitempty"`
	Icon              string          `json:"icon,omitempty"`
	EntityCategory    string          `json:"entity_category,omitempty"`
	Device            discoveryDevice `json:"device"`
}

// discoveryState - tracks which babies have been announced, so the retained
// configs are published once per connection rather than on every state update
type discoveryState struct {
	mu               sync.Mutex
	announced        map[string]bool
	soundtrackHashes map[string]string
}

func newDiscoveryState() *discoveryState {
	return &discoveryState{
		announced:        make(map[string]bool),
		soundtrackHashes: make(map[string]string),
	}
}

func (d *discoveryState) reset() {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.announced = make(map[string]bool)
	d.soundtrackHashes = make(map[string]string)
}

// publishDiscovery - announces every entity for a baby, once per connection.
// The soundtrack select is re-announced whenever the camera's catalog changes,
// since its options list is part of the config document.
//
// Subscribers are handed the state *delta*, not the merged state, so the
// catalog is read from the manager instead: a delta that does not touch
// soundtracks would otherwise look like the catalog had been emptied.
func (conn *Connection) publishDiscovery(babyUID string) {
	if !conn.Opts.DiscoveryEnabled {
		return
	}

	catalog := conn.StateManager.GetBabyState(babyUID).GetAvailableSoundtracks()
	catalogKey := soundtrackCatalogKey(catalog)

	conn.discovery.mu.Lock()
	alreadyAnnounced := conn.discovery.announced[babyUID]
	catalogChanged := conn.discovery.soundtrackHashes[babyUID] != catalogKey
	conn.discovery.announced[babyUID] = true
	conn.discovery.soundtrackHashes[babyUID] = catalogKey
	conn.discovery.mu.Unlock()

	if alreadyAnnounced && !catalogChanged {
		return
	}

	device := discoveryDevice{
		Identifiers:  []string{fmt.Sprintf("nanit_%v", babyUID)},
		Name:         fmt.Sprintf("Nanit %v", babyUID),
		Manufacturer: "Nanit",
		Model:        "Baby Monitor",
	}

	if !alreadyAnnounced {
		for _, entity := range conn.staticEntities(babyUID, device) {
			conn.publishDiscoveryConfig(babyUID, entity.component, entity.objectID, entity.entity)
		}

		log.Info().Str("baby_uid", babyUID).Msg("Published Home Assistant discovery configuration")
	}

	// Choosing a specific track is not known to work yet: the camera plays
	// whatever it already had selected. Publishing the select would offer a
	// control that silently plays the wrong sound, so it is withheld until the
	// protocol is confirmed. Start/stop is unaffected — that is the switch.
	if !client.SoundtrackSelectionVerified {
		if !alreadyAnnounced {
			log.Info().
				Str("baby_uid", babyUID).
				Msg("Soundtrack select entity withheld: track selection is unverified (see SOUNDTRACK_CAPTURE.md)")
		}

		return
	}

	// The select's options come from the camera, so it is announced separately
	// and refreshed when the catalog arrives or changes.
	conn.publishDiscoveryConfig(babyUID, "select", "soundtrack", conn.soundtrackSelectEntity(babyUID, device, catalog))

	if catalogChanged && alreadyAnnounced {
		log.Info().
			Str("baby_uid", babyUID).
			Int("soundtracks", len(catalog)).
			Msg("Refreshed Home Assistant soundtrack options")
	}
}

type namedEntity struct {
	component string
	objectID  string
	entity    discoveryEntity
}

// staticEntities - the entities whose configuration never changes at runtime
func (conn *Connection) staticEntities(babyUID string, device discoveryDevice) []namedEntity {
	stateTopic := func(key string) string {
		return fmt.Sprintf("%v/babies/%v/%v", conn.Opts.TopicPrefix, babyUID, key)
	}
	uniqueID := func(key string) string {
		return fmt.Sprintf("nanit_%v_%v", babyUID, key)
	}

	volumeMin, volumeMax, volumeStep := 0.0, 100.0, 1.0

	return []namedEntity{
		{"sensor", "temperature", discoveryEntity{
			Name:              "Temperature",
			UniqueID:          uniqueID("temperature"),
			StateTopic:        stateTopic("temperature"),
			DeviceClass:       "temperature",
			UnitOfMeasurement: "°C",
			StateClass:        "measurement",
			Device:            device,
		}},
		{"sensor", "humidity", discoveryEntity{
			Name:              "Humidity",
			UniqueID:          uniqueID("humidity"),
			StateTopic:        stateTopic("humidity"),
			DeviceClass:       "humidity",
			UnitOfMeasurement: "%",
			StateClass:        "measurement",
			Device:            device,
		}},
		{"binary_sensor", "is_night", discoveryEntity{
			Name:       "Night",
			UniqueID:   uniqueID("is_night"),
			StateTopic: stateTopic("is_night"),
			PayloadOn:  "true",
			PayloadOff: "false",
			Icon:       "mdi:weather-night",
			Device:     device,
		}},
		{"binary_sensor", "stream_alive", discoveryEntity{
			Name:        "Stream Alive",
			UniqueID:    uniqueID("stream_alive"),
			StateTopic:  stateTopic("is_stream_alive"),
			PayloadOn:   "true",
			PayloadOff:  "false",
			DeviceClass: "connectivity",
			Device:      device,
		}},
		{"switch", "night_light", discoveryEntity{
			Name:         "Night Light",
			UniqueID:     uniqueID("night_light"),
			StateTopic:   stateTopic("night_light"),
			CommandTopic: stateTopic("night_light/switch"),
			PayloadOn:    "true",
			PayloadOff:   "false",
			StateOn:      "true",
			StateOff:     "false",
			Icon:         "mdi:lightbulb-night",
			Device:       device,
		}},
		{"switch", "standby", discoveryEntity{
			Name:         "Standby",
			UniqueID:     uniqueID("standby"),
			StateTopic:   stateTopic("standby"),
			CommandTopic: stateTopic("standby/switch"),
			PayloadOn:    "true",
			PayloadOff:   "false",
			StateOn:      "true",
			StateOff:     "false",
			Icon:         "mdi:power-standby",
			Device:       device,
		}},
		{"number", "volume", discoveryEntity{
			Name:         "Volume",
			UniqueID:     uniqueID("volume"),
			StateTopic:   stateTopic("volume"),
			CommandTopic: stateTopic("volume/set"),
			Min:          &volumeMin,
			Max:          &volumeMax,
			Step:         &volumeStep,
			Icon:         "mdi:volume-high",
			Device:       device,
		}},
		{"switch", "soundtrack_playing", discoveryEntity{
			Name:         "Soundtrack",
			UniqueID:     uniqueID("soundtrack_playing"),
			StateTopic:   stateTopic("soundtrack_playing"),
			CommandTopic: stateTopic("soundtrack/switch"),
			PayloadOn:    "true",
			PayloadOff:   "false",
			StateOn:      "true",
			StateOff:     "false",
			Icon:         "mdi:music-note",
			Device:       device,
		}},
	}
}

// soundtrackSelectEntity - the select whose options are the camera's own sounds
func (conn *Connection) soundtrackSelectEntity(babyUID string, device discoveryDevice, catalog []baby.Soundtrack) discoveryEntity {
	options := []string{baby.SoundtrackOffName}
	for _, entry := range catalog {
		options = append(options, entry.DisplayName)
	}

	return discoveryEntity{
		Name:         "Soundtrack Selection",
		UniqueID:     fmt.Sprintf("nanit_%v_soundtrack", babyUID),
		StateTopic:   fmt.Sprintf("%v/babies/%v/soundtrack_name", conn.Opts.TopicPrefix, babyUID),
		CommandTopic: fmt.Sprintf("%v/babies/%v/soundtrack/select", conn.Opts.TopicPrefix, babyUID),
		Options:      options,
		Icon:         "mdi:playlist-music",
		Device:       device,
	}
}

// publishDiscoveryConfig - publishes one retained entity announcement
func (conn *Connection) publishDiscoveryConfig(babyUID string, component string, objectID string, entity discoveryEntity) {
	topic := fmt.Sprintf("%v/%v/nanit_%v/%v/config", conn.Opts.DiscoveryPrefix, component, babyUID, objectID)

	payload, err := json.Marshal(entity)
	if err != nil {
		log.Error().Err(err).Str("topic", topic).Msg("Unable to encode discovery configuration")
		return
	}

	log.Trace().Str("topic", topic).Str("payload", string(payload)).Msg("MQTT discovery publish")

	// Retained: Home Assistant reads these when it starts, which may be long
	// after this process announced them.
	if token := conn.client.Publish(topic, 0, true, payload); token.Wait() && token.Error() != nil {
		log.Error().Err(token.Error()).Str("topic", topic).Msg("Unable to publish discovery configuration")
	}
}

// soundtrackCatalogKey - identity of a catalog, used to detect changes
func soundtrackCatalogKey(catalog []baby.Soundtrack) string {
	key := ""
	for _, entry := range catalog {
		key += fmt.Sprintf("%s;", entry.Name)
	}

	return key
}
