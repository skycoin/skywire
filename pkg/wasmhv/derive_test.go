package wasmhv

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

func TestDeriveStandaloneKey(t *testing.T) {
	_, parentSK := cipher.GenerateKeyPair()

	// Deterministic: same parent + index → same key, every time.
	pk0a, sk0a, err := DeriveStandaloneKey(parentSK, 0)
	require.NoError(t, err)
	pk0b, sk0b, err := DeriveStandaloneKey(parentSK, 0)
	require.NoError(t, err)
	require.Equal(t, pk0a, pk0b)
	require.Equal(t, sk0a, sk0b)

	// The derived keypair is valid + self-consistent (pk matches sk).
	got, err := sk0a.PubKey()
	require.NoError(t, err)
	require.Equal(t, pk0a, got)

	// Indexed: distinct indices → distinct identities.
	pk1, _, err := DeriveStandaloneKey(parentSK, 1)
	require.NoError(t, err)
	require.NotEqual(t, pk0a, pk1)

	// Bound to the parent: a different parent → a different derived key.
	_, otherParent := cipher.GenerateKeyPair()
	pkOther, _, err := DeriveStandaloneKey(otherParent, 0)
	require.NoError(t, err)
	require.NotEqual(t, pk0a, pkOther)

	// One-way: the derived secret key must NOT equal the parent (it's a PRF
	// output, not the parent itself), so it can't trivially leak the parent.
	require.NotEqual(t, parentSK, sk0a)

	// Null parent is rejected.
	_, _, err = DeriveStandaloneKey(cipher.SecKey{}, 0)
	require.Error(t, err)
}
