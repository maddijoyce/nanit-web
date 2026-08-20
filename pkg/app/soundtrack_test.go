package app

import (
	"testing"

	"github.com/indiefan/home_assistant_nanit/pkg/baby"
	"github.com/indiefan/home_assistant_nanit/pkg/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

// The catalog captured from a real Nanit Pro
func realSoundtracksResponse() *client.Response {
	names := []string{"White Noise.wav", "Birds.wav", "Waves.wav", "Wind.wav"}

	res := &client.Response{
		RequestId:   proto.Int32(5),
		RequestType: client.RequestType_GET_SOUNDTRACKS.Enum(),
		StatusCode:  proto.Int32(200),
	}

	for _, name := range names {
		res.Soundtracks = append(res.Soundtracks, &client.Soundtrack{
			Type: proto.Int32(0),
			Name: proto.String(name),
		})
	}

	return res
}

func TestProcessSoundtracksResponseBuildsCatalog(t *testing.T) {
	stateManager := baby.NewStateManager()
	processSoundtracksResponse("baby1", realSoundtracksResponse(), stateManager)

	catalog := stateManager.GetBabyState("baby1").GetAvailableSoundtracks()
	require.Len(t, catalog, 4)

	assert.Equal(t, "White Noise.wav", catalog[0].Name)
	assert.Equal(t, "White Noise", catalog[0].DisplayName)
	assert.Equal(t, "Wind.wav", catalog[3].Name)
	assert.Equal(t, "Wind", catalog[3].DisplayName)
}

// A stop broadcast from the camera must clear the playing state
func TestProcessPlaybackStopped(t *testing.T) {
	stateManager := baby.NewStateManager()
	stateManager.Update("baby1", *baby.NewState().SetSoundtrack("Wind.wav"))

	processPlayback("baby1", &client.Playback{
		Status: client.Playback_STOPPED.Enum(),
	}, "test", stateManager)

	state := stateManager.GetBabyState("baby1")
	assert.False(t, state.GetSoundtrackPlaying())
	assert.Equal(t, baby.SoundtrackOffName, state.GetSoundtrackName())
}

// A start that names the track adopts it. Captured shape: the camera reports
// the Soundtrack on both field 3 and field 4.
func TestProcessPlaybackStartedWithName(t *testing.T) {
	stateManager := baby.NewStateManager()

	processPlayback("baby1", &client.Playback{
		Status:             client.Playback_STARTED.Enum(),
		Soundtrack:         client.NewSoundtrackMessage("Birds.wav"),
		SelectedSoundtrack: client.NewSoundtrackMessage("Birds.wav"),
	}, "test", stateManager)

	state := stateManager.GetBabyState("baby1")
	assert.True(t, state.GetSoundtrackPlaying())
	assert.Equal(t, "Birds.wav", state.GetSoundtrackName())
}

// Field 4 alone must still resolve, since the two fields are not known to be
// interchangeable in every case
func TestProcessPlaybackFallsBackToSelectedSoundtrack(t *testing.T) {
	stateManager := baby.NewStateManager()

	processPlayback("baby1", &client.Playback{
		Status:             client.Playback_STARTED.Enum(),
		SelectedSoundtrack: client.NewSoundtrackMessage("Waves.wav"),
	}, "test", stateManager)

	assert.Equal(t, "Waves.wav", stateManager.GetBabyState("baby1").GetSoundtrackName())
}

// A start with no track named must not blank what is known
func TestProcessPlaybackStartedWithoutNameKeepsLastKnown(t *testing.T) {
	stateManager := baby.NewStateManager()
	stateManager.Update("baby1", *baby.NewState().SetSoundtrack("Waves.wav"))

	processPlayback("baby1", &client.Playback{
		Status: client.Playback_STARTED.Enum(),
	}, "test", stateManager)

	state := stateManager.GetBabyState("baby1")
	assert.True(t, state.GetSoundtrackPlaying())
	assert.Equal(t, "Waves.wav", state.GetSoundtrackName())
}

func TestResumeSoundtrackName(t *testing.T) {
	// Nothing known at all -> nothing to resume
	empty := baby.NewState()
	assert.Equal(t, "", resumeSoundtrackName(empty))

	// Catalog but nothing chosen yet -> first sound
	state := baby.NewState()
	state.DeviceInfo = &baby.DeviceInfo{AvailableSoundtracks: []baby.Soundtrack{
		baby.NewSoundtrack("White Noise.wav"),
		baby.NewSoundtrack("Birds.wav"),
	}}
	assert.Equal(t, "White Noise.wav", resumeSoundtrackName(state))

	// Something chosen -> resume it
	state.SetSoundtrack("Birds.wav")
	assert.Equal(t, "Birds.wav", resumeSoundtrackName(state))

	// Still resumed after a stop. SoundtrackName is "Off" by now, so this is
	// what stops the switch restarting at the first sound in the catalog.
	state.SetSoundtrack(baby.SoundtrackOffName)
	assert.Equal(t, baby.SoundtrackOffName, state.GetSoundtrackName())
	assert.Equal(t, "Birds.wav", resumeSoundtrackName(state),
		"Turning playback back on should resume the last chosen track")

	// A remembered track the camera no longer lists falls back to the catalog
	state.DeviceInfo = &baby.DeviceInfo{AvailableSoundtracks: []baby.Soundtrack{
		baby.NewSoundtrack("Waves.wav"),
	}}
	assert.Equal(t, "Waves.wav", resumeSoundtrackName(state))
}

func TestSendSoundCommandRequiresConnection(t *testing.T) {
	assert.Error(t, sendSoundCommand("baby1", "Wind.wav", nil, baby.NewStateManager()))
}

// The debug probe sets selector flags as unknown varints. On the wire a proto2
// bool is a varint 1, so that must be byte-identical to setting the generated
// field — otherwise the probe would not be testing what the app really sends.
func TestDebugSelectorFlagsMatchGeneratedEncoding(t *testing.T) {
	probe := buildDebugGetRequest(client.RequestType_GET_CONTROL, []int32{2})
	probeBytes, err := proto.Marshal(probe.GetControl_)
	require.NoError(t, err)

	generated, err := proto.Marshal(&client.GetControl{NightLight: proto.Bool(true)})
	require.NoError(t, err)

	assert.Equal(t, generated, probeBytes)
}

// Unmapped selector tags must survive onto the wire, since that is the whole
// point of probing for fields the schema cannot name.
func TestDebugSelectorAcceptsUnmappedTags(t *testing.T) {
	probe := buildDebugGetRequest(client.RequestType_GET_CONTROL, []int32{5, 6})
	encoded, err := proto.Marshal(probe.GetControl_)
	require.NoError(t, err)

	decoded := &client.GetControl{}
	require.NoError(t, proto.Unmarshal(encoded, decoded))

	for _, tag := range []int32{5, 6} {
		value, ok := client.GetUnknownVarintField(decoded, tag)
		assert.True(t, ok, "tag %d should be present", tag)
		assert.Equal(t, uint64(1), value)
	}
}

// Request types with no selector must still produce a valid request
func TestDebugGetRequestWithoutSelector(t *testing.T) {
	probe := buildDebugGetRequest(client.RequestType_GET_SETTINGS, []int32{1, 2})
	assert.Nil(t, probe.GetControl_)
	assert.Nil(t, probe.GetStatus_)
	assert.Nil(t, probe.GetSensorData)
}

func TestSetSoundtrackPlayingFalseResetsName(t *testing.T) {
	state := baby.NewState()
	state.SetSoundtrack("Wind.wav")
	state.SetSoundtrackPlaying(false)

	assert.False(t, state.GetSoundtrackPlaying())
	assert.Equal(t, baby.SoundtrackOffName, state.GetSoundtrackName())
}

// A stop announced by the camera must not be applied directly. The camera
// announces one while switching tracks, which flipped the Home Assistant switch
// off and straight back on after every command.
func TestBroadcastStopIsConfirmedNotApplied(t *testing.T) {
	stateManager := baby.NewStateManager()
	stateManager.Update("baby1", *baby.NewState().SetSoundtrack("Wind.wav"))

	reread := 0
	verifyPlaybackState("baby1", &client.Playback{
		Status: client.Playback_STOPPED.Enum(),
	}, stateManager, func() { reread++ })

	assert.Equal(t, 1, reread, "a stop should trigger a confirming read")

	// State must be untouched until that read comes back
	state := stateManager.GetBabyState("baby1")
	assert.True(t, state.GetSoundtrackPlaying(), "a broadcast stop must not flip state on its own")
	assert.Equal(t, "Wind.wav", state.GetSoundtrackName())
}

// A start is unambiguous and applies immediately
func TestBroadcastStartAppliesImmediately(t *testing.T) {
	stateManager := baby.NewStateManager()

	reread := 0
	verifyPlaybackState("baby1", &client.Playback{
		Status:     client.Playback_STARTED.Enum(),
		Soundtrack: client.NewSoundtrackMessage("Birds.wav"),
	}, stateManager, func() { reread++ })

	assert.Equal(t, 0, reread, "a start needs no confirmation")

	state := stateManager.GetBabyState("baby1")
	assert.True(t, state.GetSoundtrackPlaying())
	assert.Equal(t, "Birds.wav", state.GetSoundtrackName())
}

// A confirming read that really does report stopped still turns things off
func TestConfirmedStopTurnsPlaybackOff(t *testing.T) {
	stateManager := baby.NewStateManager()
	stateManager.Update("baby1", *baby.NewState().SetSoundtrack("Wind.wav"))

	verifyPlaybackState("baby1", &client.Playback{
		Status: client.Playback_STOPPED.Enum(),
	}, stateManager, func() {
		// What requestPlaybackState does once GET_PLAYBACK answers
		processPlayback("baby1", &client.Playback{
			Status: client.Playback_STOPPED.Enum(),
		}, "get-playback", stateManager)
	})

	state := stateManager.GetBabyState("baby1")
	assert.False(t, state.GetSoundtrackPlaying())
	assert.Equal(t, baby.SoundtrackOffName, state.GetSoundtrackName())
}

// A transitional stop followed by a read showing the new track must leave
// playback on, which is the flap this prevents
func TestTransitionalStopKeepsPlaybackOn(t *testing.T) {
	stateManager := baby.NewStateManager()
	stateManager.Update("baby1", *baby.NewState().SetSoundtrack("Wind.wav"))

	verifyPlaybackState("baby1", &client.Playback{
		Status: client.Playback_STOPPED.Enum(),
	}, stateManager, func() {
		processPlayback("baby1", &client.Playback{
			Status:     client.Playback_STARTED.Enum(),
			Soundtrack: client.NewSoundtrackMessage("Birds.wav"),
		}, "get-playback", stateManager)
	})

	state := stateManager.GetBabyState("baby1")
	assert.True(t, state.GetSoundtrackPlaying(), "switching tracks must not read as a stop")
	assert.Equal(t, "Birds.wav", state.GetSoundtrackName())
}
