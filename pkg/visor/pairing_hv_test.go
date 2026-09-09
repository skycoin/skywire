package visor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

func TestHypervisorFingerprint(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	fp := HypervisorFingerprint(pk)
	require.Len(t, fp, 11)
	require.Equal(t, fp, HypervisorFingerprint(pk), "stable")
	pk2, _ := cipher.GenerateKeyPair()
	require.NotEqual(t, fp, HypervisorFingerprint(pk2))
}

func TestHVPairing_PendingAndResolve(t *testing.T) {
	p := newHVPairing()
	now := time.Now()
	pk, _ := cipher.GenerateKeyPair()
	require.True(t, p.note(pk, "transport", now))
	require.False(t, p.note(pk, "rpc", now.Add(time.Second)), "second sighting refreshes, not duplicates")
	list := p.list()
	require.Len(t, list, 1)
	require.Equal(t, "rpc", list[0].Via, "an RPC attempt upgrades the reason")
	require.Equal(t, now.Add(time.Second), list[0].LastSeen)

	fp := HypervisorFingerprint(pk)
	got, err := p.resolve(fp)
	require.NoError(t, err)
	require.Equal(t, pk, got)
	got, err = p.resolve(fp[:4])
	require.NoError(t, err)
	require.Equal(t, pk, got, "a unique prefix resolves")
	got, err = p.resolve(pk.Hex())
	require.NoError(t, err)
	require.Equal(t, pk, got, "a full key resolves without being pending")
	_, err = p.resolve("zzzzz-zzzzz")
	require.ErrorIs(t, err, ErrNoPendingMatch)

	p.forget(pk)
	require.False(t, p.isPending(pk))

	// The list is bounded; the oldest sighting is evicted first.
	oldest, _ := cipher.GenerateKeyPair()
	p.note(oldest, "transport", now.Add(-time.Hour))
	for i := 0; i < pairPendingMax; i++ {
		k, _ := cipher.GenerateKeyPair()
		p.note(k, "transport", now)
	}
	require.Len(t, p.list(), pairPendingMax)
	require.False(t, p.isPending(oldest))
}

func TestHVPairing_Codes(t *testing.T) {
	p := newHVPairing()
	now := time.Now()
	c, err := p.newCode(0, now)
	require.NoError(t, err)
	require.Len(t, c.Code, pairCodeLen)
	require.Equal(t, now.Add(pairCodeDefaultTTL), c.Expires)

	require.ErrorIs(t, p.consume("nope", now), ErrPairCodeInvalid)
	// Case and separators do not matter to a human typing it.
	spaced := c.Code[:4] + "-" + c.Code[4:]
	require.NoError(t, p.consume(spaced, now))
	require.ErrorIs(t, p.consume(c.Code, now), ErrPairCodeInvalid, "single use")

	// Expiry.
	c2, err := p.newCode(time.Minute, now)
	require.NoError(t, err)
	require.ErrorIs(t, p.consume(c2.Code, now.Add(2*time.Minute)), ErrPairCodeInvalid)

	// Too many wrong codes revoke the outstanding ones.
	c3, err := p.newCode(time.Hour, now)
	require.NoError(t, err)
	for i := 0; i < pairCodeMaxTries; i++ {
		require.ErrorIs(t, p.consume("WRONG"+string(rune('A'+i)), now), ErrPairCodeInvalid)
	}
	require.ErrorIs(t, p.consume(c3.Code, now), ErrPairCodeInvalid, "revoked after repeated wrong codes")
}
