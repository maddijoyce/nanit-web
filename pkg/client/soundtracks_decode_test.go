package client_test

import (
	"testing"

	"github.com/indiefan/home_assistant_nanit/pkg/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

// Bytes captured from a real Nanit Pro GET_SOUNDTRACKS reply
func TestDecodeRealSoundtracksResponse(t *testing.T) {
	entries := []string{"White Noise.wav", "Birds.wav", "Waves.wav", "Wind.wav"}

	var buf []byte
	buf = protowire.AppendTag(buf, 1, protowire.VarintType)
	buf = protowire.AppendVarint(buf, 5) // requestId
	buf = protowire.AppendTag(buf, 2, protowire.VarintType)
	buf = protowire.AppendVarint(buf, uint64(client.RequestType_GET_SOUNDTRACKS))
	buf = protowire.AppendTag(buf, 3, protowire.VarintType)
	buf = protowire.AppendVarint(buf, 200) // statusCode

	for _, name := range entries {
		var entry []byte
		entry = protowire.AppendTag(entry, 1, protowire.VarintType)
		entry = protowire.AppendVarint(entry, 0)
		entry = protowire.AppendTag(entry, 2, protowire.BytesType)
		entry = protowire.AppendString(entry, name)

		buf = protowire.AppendTag(buf, 12, protowire.BytesType)
		buf = protowire.AppendBytes(buf, entry)
	}

	res := &client.Response{}
	require.NoError(t, proto.Unmarshal(buf, res))

	require.Len(t, res.GetSoundtracks(), 4)
	for i, entry := range res.GetSoundtracks() {
		assert.Equal(t, entries[i], entry.GetName())
		assert.Equal(t, int32(0), entry.GetType())
	}

	// Nothing should be left unmapped now
	assert.Empty(t, client.DescribeUnknownFields(res), "soundtracks should decode through the schema")
}

// Captured from a GET_PLAYBACK reply: Response field 11 is an embedded message
// carrying status=STOPPED, i.e. a Playback.
func TestDecodeRealPlaybackResponse(t *testing.T) {
	var inner []byte
	inner = protowire.AppendTag(inner, 1, protowire.VarintType)
	inner = protowire.AppendVarint(inner, uint64(client.Playback_STOPPED))

	var buf []byte
	buf = protowire.AppendTag(buf, 1, protowire.VarintType)
	buf = protowire.AppendVarint(buf, 9)
	buf = protowire.AppendTag(buf, 2, protowire.VarintType)
	buf = protowire.AppendVarint(buf, uint64(client.RequestType_GET_PLAYBACK))
	buf = protowire.AppendTag(buf, 3, protowire.VarintType)
	buf = protowire.AppendVarint(buf, 200)
	buf = protowire.AppendTag(buf, 11, protowire.BytesType)
	buf = protowire.AppendBytes(buf, inner)

	res := &client.Response{}
	require.NoError(t, proto.Unmarshal(buf, res))

	require.NotNil(t, res.GetPlayback())
	assert.Equal(t, client.Playback_STOPPED, res.GetPlayback().GetStatus())

	// Field 11 must no longer be reported as unmapped
	assert.Empty(t, client.DescribeUnknownFields(res))
}

// Once something is playing, any extra field inside Playback should surface
// with a path naming it, which is how the track selector will be found.
func TestUnknownFieldsInsidePlaybackAreLabelled(t *testing.T) {
	var inner []byte
	inner = protowire.AppendTag(inner, 1, protowire.VarintType)
	inner = protowire.AppendVarint(inner, uint64(client.Playback_STARTED))
	inner = protowire.AppendTag(inner, 6, protowire.BytesType)
	inner = protowire.AppendString(inner, "Wind.wav")

	var buf []byte
	buf = protowire.AppendTag(buf, 1, protowire.VarintType)
	buf = protowire.AppendVarint(buf, 9)
	buf = protowire.AppendTag(buf, 2, protowire.VarintType)
	buf = protowire.AppendVarint(buf, uint64(client.RequestType_GET_PLAYBACK))
	buf = protowire.AppendTag(buf, 3, protowire.VarintType)
	buf = protowire.AppendVarint(buf, 200)
	buf = protowire.AppendTag(buf, 11, protowire.BytesType)
	buf = protowire.AppendBytes(buf, inner)

	res := &client.Response{}
	require.NoError(t, proto.Unmarshal(buf, res))

	unknown := client.DescribeUnknownFields(res)
	require.Len(t, unknown, 1)
	assert.Equal(t, "Response.playback", unknown[0].Path)
	assert.Equal(t, int32(6), unknown[0].Tag)
	assert.Equal(t, `"Wind.wav"`, unknown[0].Value)
}

// The exact 30 bytes the camera sent on Response.playback while Wind was
// playing. Encoding the same message here must reproduce them, which is what
// makes a PUT_PLAYBACK look to the camera like its own representation.
func TestPlaybackEncodingMatchesCapturedBytes(t *testing.T) {
	captured := []byte("\x08\x00\x1a\x0c\x08\x00\x12\x08Wind.wav\x22\x0c\x08\x00\x12\x08Wind.wav")

	encoded, err := proto.Marshal(&client.Playback{
		Status:             client.Playback_STARTED.Enum(),
		Soundtrack:         client.NewSoundtrackMessage("Wind.wav"),
		SelectedSoundtrack: client.NewSoundtrackMessage("Wind.wav"),
	})
	require.NoError(t, err)

	assert.Equal(t, captured, encoded)
}

// And the reverse: the captured bytes must decode into the mapped fields
func TestCapturedPlaybackDecodesToSoundtrack(t *testing.T) {
	captured := []byte("\x08\x00\x1a\x0c\x08\x00\x12\x08Wind.wav\x22\x0c\x08\x00\x12\x08Wind.wav")

	playback := &client.Playback{}
	require.NoError(t, proto.Unmarshal(captured, playback))

	assert.Equal(t, client.Playback_STARTED, playback.GetStatus())
	assert.Equal(t, "Wind.wav", playback.GetSoundtrack().GetName())
	assert.Equal(t, "Wind.wav", playback.GetSelectedSoundtrack().GetName())
	assert.Equal(t, "Wind.wav", client.PlaybackSoundtrackName(playback))
	assert.Empty(t, client.DescribeUnknownFields(playback))
}

// Stopped carries only the status
func TestCapturedStoppedPlayback(t *testing.T) {
	playback := &client.Playback{}
	require.NoError(t, proto.Unmarshal([]byte("\x08\x01"), playback))

	assert.Equal(t, client.Playback_STOPPED, playback.GetStatus())
	assert.Equal(t, "", client.PlaybackSoundtrackName(playback))
}
