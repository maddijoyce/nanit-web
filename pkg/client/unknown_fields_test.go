package client_test

import (
	"testing"

	"github.com/indiefan/home_assistant_nanit/pkg/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

// A Control carrying both a mapped field (nightLight, tag 3) and an unmapped
// one (tag 7) — the shape we expect when the camera reports a soundtrack.
func TestDescribeUnknownFieldsFindsUnmappedVarint(t *testing.T) {
	var buf []byte
	buf = protowire.AppendTag(buf, 3, protowire.VarintType)
	buf = protowire.AppendVarint(buf, 1) // LIGHT_ON
	buf = protowire.AppendTag(buf, 7, protowire.VarintType)
	buf = protowire.AppendVarint(buf, 2)

	control := &client.Control{}
	require.NoError(t, proto.Unmarshal(buf, control))

	// The mapped field still decodes normally
	assert.Equal(t, client.Control_LIGHT_ON, control.GetNightLight())

	unknown := client.DescribeUnknownFields(control)
	require.Len(t, unknown, 1)
	assert.Equal(t, int32(7), unknown[0].Tag)
	assert.Equal(t, "varint", unknown[0].WireType)
	assert.Equal(t, uint64(2), unknown[0].Value)
	assert.Equal(t, "Control", unknown[0].Path)
}

// Unmapped fields on a nested message must be reported with their path, since
// a soundtrack field could live on response.control rather than at top level.
func TestDescribeUnknownFieldsWalksNestedMessages(t *testing.T) {
	var inner []byte
	inner = protowire.AppendTag(inner, 8, protowire.VarintType)
	inner = protowire.AppendVarint(inner, 3)

	control := &client.Control{}
	require.NoError(t, proto.Unmarshal(inner, control))

	res := &client.Response{
		RequestId:   proto.Int32(1),
		RequestType: client.RequestType_GET_CONTROL.Enum(),
		StatusCode:  proto.Int32(200),
		Control:     control,
	}

	// Round-trip through the wire so the unknown bytes survive as they would live
	encoded, err := proto.Marshal(res)
	require.NoError(t, err)
	decoded := &client.Response{}
	require.NoError(t, proto.Unmarshal(encoded, decoded))

	unknown := client.DescribeUnknownFields(decoded)
	require.Len(t, unknown, 1)
	assert.Equal(t, int32(8), unknown[0].Tag)
	assert.Equal(t, "Response.control", unknown[0].Path)
	assert.Equal(t, uint64(3), unknown[0].Value)
}

// A soundtrack list would most likely arrive as repeated embedded messages
// holding an id and a name.
func TestDescribeUnknownFieldsDecodesNestedStrings(t *testing.T) {
	var entry []byte
	entry = protowire.AppendTag(entry, 1, protowire.VarintType)
	entry = protowire.AppendVarint(entry, 4)
	entry = protowire.AppendTag(entry, 2, protowire.BytesType)
	entry = protowire.AppendString(entry, "White Noise")

	// Response has proto2 required fields, which must be present for Unmarshal
	var buf []byte
	buf = protowire.AppendTag(buf, 1, protowire.VarintType)
	buf = protowire.AppendVarint(buf, 7) // requestId
	buf = protowire.AppendTag(buf, 2, protowire.VarintType)
	buf = protowire.AppendVarint(buf, uint64(client.RequestType_GET_SOUNDTRACKS))
	buf = protowire.AppendTag(buf, 3, protowire.VarintType)
	buf = protowire.AppendVarint(buf, 200) // statusCode
	buf = protowire.AppendTag(buf, 14, protowire.BytesType)
	buf = protowire.AppendBytes(buf, entry)

	res := &client.Response{}
	require.NoError(t, proto.Unmarshal(buf, res))

	unknown := client.DescribeUnknownFields(res)
	require.Len(t, unknown, 1)
	assert.Equal(t, int32(14), unknown[0].Tag)
	assert.Equal(t, "bytes", unknown[0].WireType)

	require.Len(t, unknown[0].Nested, 2)
	assert.Equal(t, int32(1), unknown[0].Nested[0].Tag)
	assert.Equal(t, uint64(4), unknown[0].Nested[0].Value)
	assert.Equal(t, int32(2), unknown[0].Nested[1].Tag)
	assert.Equal(t, `"White Noise"`, unknown[0].Nested[1].Value)
}

func TestDescribeUnknownFieldsReturnsNothingForCleanMessage(t *testing.T) {
	control := &client.Control{NightLight: client.Control_LIGHT_ON.Enum()}
	assert.Empty(t, client.DescribeUnknownFields(control))
}

func TestUnknownVarintFieldRoundTrip(t *testing.T) {
	control := &client.Control{NightLight: client.Control_LIGHT_ON.Enum()}
	client.SetUnknownVarintField(control, 7, 3)

	// Must survive a trip through the wire, exactly as a camera would see it
	encoded, err := proto.Marshal(control)
	require.NoError(t, err)
	decoded := &client.Control{}
	require.NoError(t, proto.Unmarshal(encoded, decoded))

	// The mapped field is untouched by the unknown-field write
	assert.Equal(t, client.Control_LIGHT_ON, decoded.GetNightLight())

	value, ok := client.GetUnknownVarintField(decoded, 7)
	assert.True(t, ok)
	assert.Equal(t, uint64(3), value)

	_, ok = client.GetUnknownVarintField(decoded, 9)
	assert.False(t, ok, "Absent tag should report not-found")
}

// The reader must skip over other wire types rather than misreading them
func TestGetUnknownVarintFieldSkipsOtherTypes(t *testing.T) {
	control := &client.Control{}
	client.SetUnknownBytesField(control, 8, []byte("ocean"))
	client.SetUnknownVarintField(control, 11, 42)

	encoded, err := proto.Marshal(control)
	require.NoError(t, err)
	decoded := &client.Control{}
	require.NoError(t, proto.Unmarshal(encoded, decoded))

	value, ok := client.GetUnknownVarintField(decoded, 11)
	assert.True(t, ok)
	assert.Equal(t, uint64(42), value)
}
