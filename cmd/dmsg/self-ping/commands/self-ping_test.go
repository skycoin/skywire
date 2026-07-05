// Package commands cmd/dmsg/self-ping/commands/self-ping_test.go
//
// Covers parseServerEntry, the pure pk@ip:port parser behind the --srv flag.
// The RunE closure performs a live dmsg self-ping and is not unit-testable
// as-is.
package commands

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

// TestParseServerEntryValid verifies a well-formed entry is parsed into a disc
// entry with the expected fields.
func TestParseServerEntryValid(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()

	e, err := parseServerEntry(pk.Hex() + "@1.2.3.4:8080")
	require.NoError(t, err)
	require.NotNil(t, e)

	assert.Equal(t, pk, e.Static)
	require.NotNil(t, e.Server)
	assert.Equal(t, "1.2.3.4:8080", e.Server.Address)
	assert.Equal(t, "0.0.1", e.Version)
	assert.Equal(t, 2048, e.Server.AvailableSessions)
}

// TestParseServerEntryInvalid covers the malformed-input branches.
func TestParseServerEntryInvalid(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()

	cases := []struct {
		name string
		in   string
	}{
		{"no at sign", pk.Hex()},
		{"at sign at start", "@1.2.3.4:8080"},
		{"at sign at end", pk.Hex() + "@"},
		{"invalid public key", "nothex@1.2.3.4:8080"},
		{"empty", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e, err := parseServerEntry(tc.in)
			assert.Error(t, err)
			assert.Nil(t, e)
		})
	}
}
