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
	client.SetUnknownBytesField(playback, soundtrackNameFieldTag, []byte("Birds.wav"))

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
	assert.Error(t, sendSoundCommand("Wind.wav", nil))
}
