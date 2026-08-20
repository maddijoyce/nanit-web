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
