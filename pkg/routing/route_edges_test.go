package routing

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

// A malformed edge pair is an error, not a panic: it comes from a request body.
func TestPathEdgesUnmarshalTextRejectsMalformed(t *testing.T) {
	pk1, _ := cipher.GenerateKeyPair()
	pk2, _ := cipher.GenerateKeyPair()
	var p PathEdges
	for _, s := range []string{"", pk1.Hex(), pk1.Hex() + "-" + pk2.Hex(), pk1.Hex() + ":" + pk2.Hex() + "x"} {
		require.Error(t, p.UnmarshalText([]byte(s)), "%q", s)
	}
	require.NoError(t, p.UnmarshalText([]byte(pk1.Hex()+":"+pk2.Hex())))
	require.Equal(t, PathEdges{pk1, pk2}, p)
}
