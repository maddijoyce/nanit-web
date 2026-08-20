package app

import (
	"errors"
	"testing"

	"github.com/indiefan/home_assistant_nanit/pkg/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Shape 1: repeated embedded messages carrying an id and a name
func TestExtractSoundtrackCatalogFromNestedMessages(t *testing.T) {
	fields := []client.UnknownField{
		{Tag: 14, WireType: "bytes", Value: "<21 bytes>", Nested: []client.UnknownField{
			{Tag: 1, WireType: "varint", Value: uint64(1)},
			{Tag: 2, WireType: "bytes", Value: `"White Noise"`},
		}},
		{Tag: 14, WireType: "bytes", Value: "<19 bytes>", Nested: []client.UnknownField{
			{Tag: 1, WireType: "varint", Value: uint64(2)},
			{Tag: 2, WireType: "bytes", Value: `"Ocean"`},
		}},
	}

	catalog := extractSoundtrackCatalog(fields)
	require.Len(t, catalog, 2)
	assert.Equal(t, int32(1), catalog[0].ID)
	assert.Equal(t, "White Noise", catalog[0].Name)
	assert.Equal(t, int32(2), catalog[1].ID)
	assert.Equal(t, "Ocean", catalog[1].Name)
}

// Shape 2: repeated bare strings, where position stands in for the id
func TestExtractSoundtrackCatalogFromBareStrings(t *testing.T) {
	fields := []client.UnknownField{
		{Tag: 14, WireType: "bytes", Value: `"Rain"`},
		{Tag: 14, WireType: "bytes", Value: `"Lullaby"`},
	}

	catalog := extractSoundtrackCatalog(fields)
	require.Len(t, catalog, 2)
	assert.Equal(t, int32(1), catalog[0].ID)
	assert.Equal(t, "Rain", catalog[0].Name)
	assert.Equal(t, int32(2), catalog[1].ID)
	assert.Equal(t, "Lullaby", catalog[1].Name)
}

// Unrecognised shapes must yield nothing rather than invented entries, leaving
// the logged dump as the source of truth.
func TestExtractSoundtrackCatalogIgnoresUnrecognisedShapes(t *testing.T) {
	fields := []client.UnknownField{
		{Tag: 7, WireType: "varint", Value: uint64(3)},
		{Tag: 9, WireType: "bytes", Value: "<64 bytes>"},
		{Tag: 11, WireType: "bytes", Value: "<8 bytes>", Nested: []client.UnknownField{
			{Tag: 1, WireType: "varint", Value: uint64(5)},
		}},
	}

	assert.Empty(t, extractSoundtrackCatalog(fields))
}

// The feature must stay inert until the controlling field tag is filled in.
func TestSoundtrackSendRefusedWhileFieldUnknown(t *testing.T) {
	if soundtrackControlConfigured() {
		t.Skip("soundtrack field tag has been configured; guard no longer applies")
	}

	// A nil connection is safe precisely because the guard returns first
	err := sendSoundCommand(1, nil)
	assert.True(t, errors.Is(err, ErrSoundtrackFieldUnknown), "Expected ErrSoundtrackFieldUnknown, got %v", err)
}
