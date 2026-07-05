// Package spec pkg/transport/spec/spec_test.go
//
// PersistentTransports is a pure schema type with no executable statements, so
// there is no statement coverage to gain here. These tests guard its JSON wire
// format: visorconfig.V1 embeds a []PersistentTransports, so its field names and
// the hex/string encodings of PK and NetType are an on-disk config
// compatibility contract. The tests fail loudly if a tag is renamed or an
// encoding changes.
package spec

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

// TestPersistentTransportsRoundTrip verifies a populated entry survives a
// marshal/unmarshal cycle unchanged.
func TestPersistentTransportsRoundTrip(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	want := PersistentTransports{PK: pk, NetType: types.STCPR}

	raw, err := json.Marshal(want)
	require.NoError(t, err)

	var got PersistentTransports
	require.NoError(t, json.Unmarshal(raw, &got))
	assert.Equal(t, want, got)
}

// TestPersistentTransportsWireFormat pins the JSON key names and verifies PK is
// hex-encoded and NetType is its string form.
func TestPersistentTransportsWireFormat(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()

	raw, err := json.Marshal(PersistentTransports{PK: pk, NetType: types.DMSG})
	require.NoError(t, err)

	var m struct {
		PK      *string `json:"pk"`
		NetType *string `json:"type"`
	}
	require.NoError(t, json.Unmarshal(raw, &m))

	require.NotNil(t, m.PK, "pk key must be present")
	assert.Equal(t, pk.Hex(), *m.PK)

	require.NotNil(t, m.NetType, "type key must be present")
	assert.Equal(t, "dmsg", *m.NetType)
}

// TestPersistentTransportsSliceRoundTrip verifies the []PersistentTransports
// shape that V1 actually embeds round-trips intact.
func TestPersistentTransportsSliceRoundTrip(t *testing.T) {
	pk1, _ := cipher.GenerateKeyPair()
	pk2, _ := cipher.GenerateKeyPair()
	want := []PersistentTransports{
		{PK: pk1, NetType: types.STCP},
		{PK: pk2, NetType: types.SUDPH},
	}

	raw, err := json.Marshal(want)
	require.NoError(t, err)

	var got []PersistentTransports
	require.NoError(t, json.Unmarshal(raw, &got))
	assert.Equal(t, want, got)
}
