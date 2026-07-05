// Package spec pkg/transport/network/spec/spec_test.go
//
// STCPConfig is a pure schema type with no executable statements, so there is no
// statement coverage to gain here. These tests guard its JSON wire format:
// STCPConfig is embedded in visorconfig.V1, so its field names and the hex
// encoding of PKTable keys are an on-disk config compatibility contract. The
// tests fail loudly if a tag is renamed or the pubkey-key encoding changes.
package spec

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

// TestSTCPConfigRoundTrip verifies a populated config survives a
// marshal/unmarshal cycle unchanged, including the PubKey-keyed table.
func TestSTCPConfigRoundTrip(t *testing.T) {
	pk1, _ := cipher.GenerateKeyPair()
	pk2, _ := cipher.GenerateKeyPair()

	want := STCPConfig{
		PKTable: map[cipher.PubKey]string{
			pk1: "127.0.0.1:7031",
			pk2: "127.0.0.1:7032",
		},
		ListeningAddress: ":7031",
	}

	raw, err := json.Marshal(want)
	require.NoError(t, err)

	var got STCPConfig
	require.NoError(t, json.Unmarshal(raw, &got))
	assert.Equal(t, want, got)
}

// TestSTCPConfigWireFormat pins the JSON key names and verifies PKTable entries
// are keyed by the hex form of the public key.
func TestSTCPConfigWireFormat(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()

	raw, err := json.Marshal(STCPConfig{
		PKTable:          map[cipher.PubKey]string{pk: "10.0.0.1:1000"},
		ListeningAddress: ":1000",
	})
	require.NoError(t, err)

	var m struct {
		PKTable          map[string]string `json:"pk_table"`
		ListeningAddress *string           `json:"listening_address"`
	}
	require.NoError(t, json.Unmarshal(raw, &m))

	require.NotNil(t, m.ListeningAddress, "listening_address key must be present")
	assert.Equal(t, ":1000", *m.ListeningAddress)

	addr, ok := m.PKTable[pk.Hex()]
	assert.True(t, ok, "PKTable must be keyed by the hex public key")
	assert.Equal(t, "10.0.0.1:1000", addr)
}

// TestSTCPConfigEmpty verifies a zero-valued config marshals without error and
// round-trips to an equal value (nil table, empty address).
func TestSTCPConfigEmpty(t *testing.T) {
	raw, err := json.Marshal(STCPConfig{})
	require.NoError(t, err)

	var got STCPConfig
	require.NoError(t, json.Unmarshal(raw, &got))
	assert.Nil(t, got.PKTable)
	assert.Empty(t, got.ListeningAddress)
}
