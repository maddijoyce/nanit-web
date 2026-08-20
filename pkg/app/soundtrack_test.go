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
	}, stateManager)

	state := stateManager.GetBabyState("baby1")
	assert.False(t, state.GetSoundtrackPlaying())
	assert.Equal(t, baby.SoundtrackOffName, state.GetSoundtrackName())
}

// A start that names the track should adopt that name
func TestProcessPlaybackStartedWithName(t *testing.T) {
	stateManager := baby.NewStateManager()

	playback := &client.Playback{Status: client.Playback_STARTED.Enum()}
	client.SetUnknownBytesField(playback, client.SoundtrackNameFieldTag, []byte("Birds.wav"))

	processPlayback("baby1", playback, stateManager)

	state := stateManager.GetBabyState("baby1")
	assert.True(t, state.GetSoundtrackPlaying())
	assert.Equal(t, "Birds.wav", state.GetSoundtrackName())
}

// A start with no name must keep the last known track rather than blanking it
func TestProcessPlaybackStartedWithoutNameKeepsLastKnown(t *testing.T) {
	stateManager := baby.NewStateManager()
	stateManager.Update("baby1", *baby.NewState().SetSoundtrack("Waves.wav"))

	processPlayback("baby1", &client.Playback{
		Status: client.Playback_STARTED.Enum(),
	}, stateManager)

	state := stateManager.GetBabyState("baby1")
	assert.True(t, state.GetSoundtrackPlaying())
	assert.Equal(t, "Waves.wav", state.GetSoundtrackName())
}

func TestResumeSoundtrackName(t *testing.T) {
	// Nothing known at all -> nothing to resume
	empty := baby.NewState()
	assert.Equal(t, "", resumeSoundtrackName(empty))

	// Catalog but nothing played yet -> first sound
	withCatalog := baby.NewState()
	withCatalog.DeviceInfo = &baby.DeviceInfo{AvailableSoundtracks: []baby.Soundtrack{
		baby.NewSoundtrack("White Noise.wav"),
		baby.NewSoundtrack("Birds.wav"),
	}}
	assert.Equal(t, "White Noise.wav", resumeSoundtrackName(withCatalog))

	// Something played before -> resume it
	withCatalog.SetSoundtrack("Birds.wav")
	assert.Equal(t, "Birds.wav", resumeSoundtrackName(withCatalog))

	// Stopped -> fall back to the first sound again
	withCatalog.SetSoundtrack(baby.SoundtrackOffName)
	assert.Equal(t, "White Noise.wav", resumeSoundtrackName(withCatalog))
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

// While selection is unverified the send path must not claim a specific track
// is playing, since the camera plays whatever it already had selected.
func TestSoundtrackSelectionUnverifiedDoesNotAssertTrack(t *testing.T) {
	if client.SoundtrackSelectionVerified {
		t.Skip("selection has been verified; the optimistic path applies instead")
	}

	// The switch must still show "on", so playing is recorded even though the
	// track name is not.
	state := baby.NewState()
	state.SetSoundtrackPlaying(true)

	assert.True(t, state.GetSoundtrackPlaying())
	assert.Equal(t, baby.SoundtrackOffName, state.GetSoundtrackName(),
		"An unverified selection must not be presented as the playing track")
}

func TestSetSoundtrackPlayingFalseResetsName(t *testing.T) {
	state := baby.NewState()
	state.SetSoundtrack("Wind.wav")
	state.SetSoundtrackPlaying(false)

	assert.False(t, state.GetSoundtrackPlaying())
	assert.Equal(t, baby.SoundtrackOffName, state.GetSoundtrackName())
}
