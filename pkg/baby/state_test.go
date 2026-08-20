package baby_test

import (
	"testing"

	"github.com/indiefan/home_assistant_nanit/pkg/baby"
	"github.com/stretchr/testify/assert"
)

func TestStateAsMap(t *testing.T) {
	s := baby.State{}
	s.SetTemperatureMilli(1000)
	s.SetIsNight(true)
	s.SetStreamRequestState(baby.StreamRequestState_Requested)

	m := s.AsMap(false)

	assert.Equal(t, 1.0, m["temperature"], "The two words should be the same.")
	assert.Equal(t, true, m["is_night"], "The two words should be the same.")
	assert.NotContains(t, m, "is_stream_requested", "Should not contain internal fields")
}

func TestStateMergeSame(t *testing.T) {
	s1 := &baby.State{}
	s1.SetTemperatureMilli(10)

	s2 := &baby.State{}
	s2.SetTemperatureMilli(10)

	s3 := s1.Merge(s2)
	assert.Same(t, s1, s3)
}

func TestStateMergeDifferent(t *testing.T) {
	s1 := &baby.State{}
	s1.SetTemperatureMilli(10_000)
	s1.SetStreamState(baby.StreamState_Alive)

	s2 := &baby.State{}
	s2.SetTemperatureMilli(11_000)
	s2.SetHumidityMilli(20_000)
	s2.SetStreamState(baby.StreamState_Alive)

	s3 := s1.Merge(s2)
	assert.NotSame(t, s1, s3)
	assert.NotSame(t, s2, s3)
	assert.Equal(t, 10.0, s1.GetTemperature())

	assert.Equal(t, 11.0, s3.GetTemperature())
	assert.Equal(t, 20.0, s3.GetHumidity())
	assert.Equal(t, baby.StreamState_Alive, s3.GetStreamState())
}

func TestStateVolumeIsPublished(t *testing.T) {
	s := baby.State{}
	s.SetVolume(42)

	m := s.AsMap(false)

	// Volume must reach the MQTT state topics, so it has to survive AsMap
	// without the internal marker that hides DeviceInfo.
	assert.Equal(t, int64(42), m["volume"], "Volume should be exposed as a non-internal field")
}

func TestStateVolumeMerge(t *testing.T) {
	s1 := &baby.State{}
	s1.SetVolume(10)

	s2 := &baby.State{}
	s2.SetVolume(80)

	merged := s1.Merge(s2)
	assert.NotSame(t, s1, merged, "Changed volume should produce a new state")
	assert.Equal(t, int32(80), merged.GetVolume())

	// An update that does not touch volume must preserve it
	s3 := &baby.State{}
	s3.SetNightLight(true)
	preserved := merged.Merge(s3)
	assert.Equal(t, int32(80), preserved.GetVolume(), "Volume should survive unrelated updates")
}

// The MQTT discovery documents reference these exact key names, so a rename
// here would silently detach every Home Assistant entity from its state topic.
func TestStateSoundtrackMapKeys(t *testing.T) {
	s := baby.State{}
	s.SetSoundtrack("Ocean.wav")

	m := s.AsMap(false)

	assert.Equal(t, "Ocean.wav", m["soundtrack_name"])
	assert.Equal(t, true, m["soundtrack_playing"])
}

func TestStateSoundtrackOff(t *testing.T) {
	s := baby.State{}
	s.SetSoundtrack(baby.SoundtrackOffName)

	assert.False(t, s.GetSoundtrackPlaying())
	assert.Equal(t, "Off", s.GetSoundtrackName())

	// An empty name means the same thing
	s.SetSoundtrack("")
	assert.False(t, s.GetSoundtrackPlaying())
	assert.Equal(t, "Off", s.GetSoundtrackName())
}

// The camera identifies sounds by filename; the display name drops the
// extension for user-facing lists.
func TestNewSoundtrackDerivesDisplayName(t *testing.T) {
	entry := baby.NewSoundtrack("White Noise.wav")
	assert.Equal(t, "White Noise.wav", entry.Name)
	assert.Equal(t, "White Noise", entry.DisplayName)

	bare := baby.NewSoundtrack("Waves")
	assert.Equal(t, "Waves", bare.DisplayName)
}
