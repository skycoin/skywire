package visorconfig

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

func testV1(t *testing.T, sk cipher.SecKey) *V1 {
	pk, err := sk.PubKey()
	require.NoError(t, err)
	return &V1{Common: &Common{SK: sk, PK: pk}}
}

func TestKeyRingMintAndDerive(t *testing.T) {
	const label = "skywire-standalone-hypervisor:v1:"
	_, sk := cipher.GenerateKeyPair()
	v1 := testV1(t, sk)

	// Mint advances NextIndex and records entries.
	e0, err := v1.MintKey(label)
	require.NoError(t, err)
	require.Equal(t, uint32(0), e0.Index)
	e1, err := v1.MintKey(label)
	require.NoError(t, err)
	require.Equal(t, uint32(1), e1.Index)

	require.NotNil(t, v1.KeyRing)
	require.Equal(t, KeyRingTypeDeterministic, v1.KeyRing.Type)
	require.Equal(t, uint32(2), v1.KeyRing.NextIndex)
	require.Len(t, v1.KeyRing.Entries, 2)
	require.NotEqual(t, e0.PublicKey, e1.PublicKey, "distinct indices → distinct keys")

	// Read-only derive reproduces a minted entry exactly (deterministic).
	got, err := v1.DeriveKeyEntry(label, 0)
	require.NoError(t, err)
	require.Equal(t, e0, got)

	// Entry is self-consistent: the recorded PK matches the recorded SK, and
	// matches the shared cipher.DeriveChildKey primitive (the one
	// wasmhv.DeriveStandaloneKey also delegates to).
	var esk cipher.SecKey
	require.NoError(t, esk.Set(e0.SecretKey))
	derivedPK, err := esk.PubKey()
	require.NoError(t, err)
	require.Equal(t, e0.PublicKey, derivedPK.Hex())

	wantPK, _, err := cipher.DeriveChildKey(sk, label, 0)
	require.NoError(t, err)
	require.Equal(t, wantPK.Hex(), e0.PublicKey)
}
