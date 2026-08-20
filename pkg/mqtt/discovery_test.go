package mqtt

import (
	"encoding/json"
	"testing"

	"github.com/indiefan/home_assistant_nanit/pkg/baby"
	"github.com/indiefan/home_assistant_nanit/pkg/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testConnection() *Connection {
	return NewConnection(Opts{
		TopicPrefix:      "nanit",
		DiscoveryEnabled: true,
		DiscoveryPrefix:  "homeassistant",
	})
}

// Command topics in the discovery documents must match the topics the
// subscribers actually listen on, or Home Assistant's controls do nothing.
func TestDiscoveryCommandTopicsMatchSubscriptions(t *testing.T) {
	conn := testConnection()
	entities := conn.staticEntities("baby1", discoveryDevice{})

	byObjectID := map[string]discoveryEntity{}
	for _, e := range entities {
		byObjectID[e.objectID] = e.entity
	}

	assert.Equal(t, "nanit/babies/baby1/night_light/switch", byObjectID["night_light"].CommandTopic)
	assert.Equal(t, "nanit/babies/baby1/standby/switch", byObjectID["standby"].CommandTopic)
	assert.Equal(t, "nanit/babies/baby1/volume/set", byObjectID["volume"].CommandTopic)
	assert.Equal(t, "nanit/babies/baby1/soundtrack/switch", byObjectID["soundtrack_playing"].CommandTopic)

	// State topics must match the keys AsMap produces
	assert.Equal(t, "nanit/babies/baby1/volume", byObjectID["volume"].StateTopic)
	assert.Equal(t, "nanit/babies/baby1/soundtrack_playing", byObjectID["soundtrack_playing"].StateTopic)
}

// Switch payloads have to line up with what the publisher emits ("%v" of a Go
// bool) and what the command handlers parse.
func TestDiscoverySwitchPayloadsMatchWireFormat(t *testing.T) {
	conn := testConnection()
	entities := conn.staticEntities("baby1", discoveryDevice{})

	for _, e := range entities {
		if e.component != "switch" {
			continue
		}

		assert.Equal(t, "true", e.entity.PayloadOn, "%v payload_on", e.objectID)
		assert.Equal(t, "false", e.entity.PayloadOff, "%v payload_off", e.objectID)
		assert.Equal(t, "true", e.entity.StateOn, "%v state_on", e.objectID)
		assert.Equal(t, "false", e.entity.StateOff, "%v state_off", e.objectID)
	}
}

func TestDiscoveryVolumeIsBoundedNumber(t *testing.T) {
	conn := testConnection()
	entities := conn.staticEntities("baby1", discoveryDevice{})

	var volume *discoveryEntity
	for _, e := range entities {
		if e.objectID == "volume" {
			assert.Equal(t, "number", e.component)
			entity := e.entity
			volume = &entity
		}
	}

	require.NotNil(t, volume)
	require.NotNil(t, volume.Min)
	require.NotNil(t, volume.Max)
	assert.Equal(t, 0.0, *volume.Min)
	assert.Equal(t, 100.0, *volume.Max)
}

// The select's options come from the camera; "Off" is always available so
// playback can be stopped even before a catalog has been reported.
func TestSoundtrackSelectUsesCameraCatalog(t *testing.T) {
	conn := testConnection()

	empty := conn.soundtrackSelectEntity("baby1", discoveryDevice{}, nil)
	assert.Equal(t, []string{"Off"}, empty.Options)

	// Options carry display names; the command handler maps them back to filenames
	populated := conn.soundtrackSelectEntity("baby1", discoveryDevice{}, []baby.Soundtrack{
		baby.NewSoundtrack("White Noise.wav"),
		baby.NewSoundtrack("Ocean.wav"),
	})
	assert.Equal(t, []string{"Off", "White Noise", "Ocean"}, populated.Options)
	assert.Equal(t, "nanit/babies/baby1/soundtrack/select", populated.CommandTopic)
	assert.Equal(t, "nanit/babies/baby1/soundtrack_name", populated.StateTopic)
}

// Components must not receive keys they do not understand, so the optional
// fields have to stay omitted when unset.
func TestDiscoveryOmitsUnsetFields(t *testing.T) {
	conn := testConnection()
	entities := conn.staticEntities("baby1", discoveryDevice{Identifiers: []string{"nanit_baby1"}})

	for _, e := range entities {
		payload, err := json.Marshal(e.entity)
		require.NoError(t, err)

		var decoded map[string]interface{}
		require.NoError(t, json.Unmarshal(payload, &decoded))

		if e.component == "sensor" || e.component == "binary_sensor" {
			assert.NotContains(t, decoded, "command_topic", "%v should not be commandable", e.objectID)
		}

		if e.objectID != "volume" {
			assert.NotContains(t, decoded, "min", "%v should not carry number bounds", e.objectID)
			assert.NotContains(t, decoded, "max", "%v should not carry number bounds", e.objectID)
		}

		assert.NotContains(t, decoded, "options", "static entities carry no options list")
	}
}

func TestDiscoveryCatalogKeyDetectsChanges(t *testing.T) {
	a := []baby.Soundtrack{baby.NewSoundtrack("White Noise.wav")}
	b := []baby.Soundtrack{baby.NewSoundtrack("White Noise.wav"), baby.NewSoundtrack("Ocean.wav")}

	assert.Equal(t, soundtrackCatalogKey(a), soundtrackCatalogKey(a))
	assert.NotEqual(t, soundtrackCatalogKey(a), soundtrackCatalogKey(b))
	assert.NotEqual(t, soundtrackCatalogKey(nil), soundtrackCatalogKey(a))
}

// Long-term statistics in Home Assistant require state_class on numeric
// sensors, and it must not appear on non-numeric ones.
func TestDiscoverySensorsCarryStateClass(t *testing.T) {
	conn := testConnection()

	for _, e := range conn.staticEntities("baby1", discoveryDevice{}) {
		switch e.objectID {
		case "temperature", "humidity":
			assert.Equal(t, "measurement", e.entity.StateClass, "%v", e.objectID)
		default:
			assert.Empty(t, e.entity.StateClass, "%v should not declare a state class", e.objectID)
		}
	}
}

// The select publishes display names, but the camera expects filenames.
func TestResolveSoundtrackNameMapsDisplayToFilename(t *testing.T) {
	conn := testConnection()
	conn.StateManager = baby.NewStateManager()
	conn.StateManager.Update("baby1", *baby.NewState().SetDeviceInfo(&baby.DeviceInfo{
		AvailableSoundtracks: []baby.Soundtrack{
			baby.NewSoundtrack("White Noise.wav"),
			baby.NewSoundtrack("Wind.wav"),
		},
	}))

	name, ok := conn.resolveSoundtrackName("baby1", "White Noise")
	assert.True(t, ok)
	assert.Equal(t, "White Noise.wav", name)

	// The filename itself is accepted too
	name, ok = conn.resolveSoundtrackName("baby1", "Wind.wav")
	assert.True(t, ok)
	assert.Equal(t, "Wind.wav", name)

	// "Off" always resolves, even with no catalog
	name, ok = conn.resolveSoundtrackName("baby1", "Off")
	assert.True(t, ok)
	assert.Equal(t, baby.SoundtrackOffName, name)

	_, ok = conn.resolveSoundtrackName("baby1", "Nonexistent")
	assert.False(t, ok, "Unknown names must be rejected rather than sent to the camera")
}

// Switching on with no catalog must not invent a filename
func TestResolveSoundtrackSwitchWithoutCatalog(t *testing.T) {
	conn := testConnection()
	conn.StateManager = baby.NewStateManager()

	assert.Equal(t, baby.SoundtrackOffName, conn.resolveSoundtrackSwitch("baby1", true))
	assert.Equal(t, baby.SoundtrackOffName, conn.resolveSoundtrackSwitch("baby1", false))
}

func TestResolveSoundtrackSwitchUsesFirstSound(t *testing.T) {
	conn := testConnection()
	conn.StateManager = baby.NewStateManager()
	conn.StateManager.Update("baby1", *baby.NewState().SetDeviceInfo(&baby.DeviceInfo{
		AvailableSoundtracks: []baby.Soundtrack{
			baby.NewSoundtrack("White Noise.wav"),
			baby.NewSoundtrack("Wind.wav"),
		},
	}))

	assert.Equal(t, "White Noise.wav", conn.resolveSoundtrackSwitch("baby1", true))
}

// The select is published once track selection is verified; while it is not,
// it must stay withheld rather than offer a control that plays the wrong sound.
func TestSoundtrackSelectGating(t *testing.T) {
	conn := testConnection()

	var hasSwitch bool
	for _, e := range conn.staticEntities("baby1", discoveryDevice{}) {
		if e.objectID == "soundtrack_playing" {
			hasSwitch = true
		}
		assert.NotEqual(t, "select", e.component, "the select is announced separately, not as a static entity")
	}

	// Start/stop works either way and must always be available
	assert.True(t, hasSwitch, "Soundtrack switch should always be announced")

	if !client.SoundtrackSelectionVerified {
		t.Skip("selection unverified; the select is intentionally withheld")
	}

	// Verified: the select must carry the camera's sounds plus Off
	entity := conn.soundtrackSelectEntity("baby1", discoveryDevice{}, []baby.Soundtrack{
		baby.NewSoundtrack("White Noise.wav"),
		baby.NewSoundtrack("Birds.wav"),
	})

	assert.Equal(t, []string{"Off", "White Noise", "Birds"}, entity.Options)
	assert.Equal(t, "nanit/babies/baby1/soundtrack/select", entity.CommandTopic)
}
