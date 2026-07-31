// Package group cmd/apps/skychat/group/key_policy_test.go
//
// The two halves of bounded key epochs: the ephemeral wrap (a rotation
// an admin's identity key can no longer open) and the age/volume policy
// that makes epochs advance without an eviction to trigger them.
package group

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

// THE property. Before ephemeral wraps, an attacker who captured a feed
// and later obtained an admin's identity secret could unwrap every group
// key that admin ever issued — the epochs were decorative against
// exactly that threat. Now the issuer's own identity key opens nothing.
func TestRotationIsNotOpenableWithTheIssuersIdentityKey(t *testing.T) {
	issuerPK, issuerSK := cipher.GenerateKeyPair()
	memberPK, memberSK := cipher.GenerateKeyPair()
	gid := uuid.New()
	key, err := GenerateAESKey()
	require.NoError(t, err)

	m, skipped, err := buildKeyMutation(gid, 1, key, []cipher.PubKey{memberPK}, issuerSK, time.Now().UTC())
	require.NoError(t, err)
	require.Empty(t, skipped)
	require.True(t, m.Ephemeral(), "the rotation did not use an ephemeral wrap key")

	wrap, ok := m.wrapForRecipient(memberSK, memberPK)
	require.True(t, ok)
	require.NotEqual(t, issuerPK, wrap.EphPK, "the wrap names the issuer's identity key, not a throwaway one")

	// The recipient opens it, as always.
	got, err := openGroupKey(memberSK, wrap.sealerPK(m.IssuerPK), wrap.Sealed)
	require.NoError(t, err)
	require.Equal(t, key, got)

	// The ISSUER cannot — its identity secret is no longer an input to
	// the wrap, and the ephemeral secret is gone.
	_, err = openGroupKey(issuerSK, memberPK, wrap.Sealed)
	require.Error(t, err,
		"the issuer's identity key opened its own rotation — a later compromise of an admin still unwraps the group's whole key history")
	_, err = openGroupKey(issuerSK, issuerPK, wrap.Sealed)
	require.Error(t, err)
}

// One ephemeral key per rotation, shared across wraps — the wraps differ
// because the recipient half differs, and each member opens only its own.
func TestRotationUsesOneEphemeralKeyAndIsolatesRecipients(t *testing.T) {
	_, issuerSK := cipher.GenerateKeyPair()
	aPK, aSK := cipher.GenerateKeyPair()
	bPK, bSK := cipher.GenerateKeyPair()
	key, err := GenerateAESKey()
	require.NoError(t, err)

	m, _, err := buildKeyMutation(uuid.New(), 4, key, []cipher.PubKey{aPK, bPK}, issuerSK, time.Now().UTC())
	require.NoError(t, err)
	require.Len(t, m.Wraps, 2)
	require.Equal(t, m.Wraps[0].EphPK, m.Wraps[1].EphPK, "a rotation should mint ONE ephemeral key, not one per recipient")

	wa, okA := m.wrapForRecipient(aSK, aPK)
	require.True(t, okA)
	wb, okB := m.wrapForRecipient(bSK, bPK)
	require.True(t, okB)
	require.NotEqual(t, wa.Sealed, wb.Sealed)

	openedA, err := openGroupKey(aSK, wa.sealerPK(m.IssuerPK), wa.Sealed)
	require.NoError(t, err)
	require.Equal(t, key, openedA)

	// A cannot open B's wrap.
	_, err = openGroupKey(aSK, wb.sealerPK(m.IssuerPK), wb.Sealed)
	require.Error(t, err, "one member opened another member's wrap")

	openedB, err := openGroupKey(bSK, wb.sealerPK(m.IssuerPK), wb.Sealed)
	require.NoError(t, err)
	require.Equal(t, key, openedB)
}

// EphPK decides which key a recipient derives against, so it has to be
// covered by the signature — otherwise anyone able to rewrite the leaf
// could make the wrap silently unopenable.
func TestEphemeralKeyIsSigned(t *testing.T) {
	_, issuerSK := cipher.GenerateKeyPair()
	memberPK, _ := cipher.GenerateKeyPair()
	evilPK, _ := cipher.GenerateKeyPair()
	key, err := GenerateAESKey()
	require.NoError(t, err)

	m, _, err := buildKeyMutation(uuid.New(), 2, key, []cipher.PubKey{memberPK}, issuerSK, time.Now().UTC())
	require.NoError(t, err)
	require.NoError(t, VerifyKey(m))

	swapped := m
	swapped.Wraps = append([]KeyWrap(nil), m.Wraps...)
	swapped.Wraps[0].EphPK = evilPK
	require.Error(t, VerifyKey(swapped), "the ephemeral key can be swapped without breaking the signature")

	stripped := m
	stripped.Wraps = append([]KeyWrap(nil), m.Wraps...)
	stripped.Wraps[0].EphPK = cipher.PubKey{}
	require.Error(t, VerifyKey(stripped),
		"the ephemeral key can be STRIPPED without breaking the signature — a downgrade to the old identity-wrapped form")
}

// A rotation from a build that predates ephemeral wraps must still
// verify and still open: those leaves are in feeds forever.
func TestLegacyIdentityWrappedRotationStillWorks(t *testing.T) {
	issuerPK, issuerSK := cipher.GenerateKeyPair()
	memberPK, memberSK := cipher.GenerateKeyPair()
	key, err := GenerateAESKey()
	require.NoError(t, err)

	// Hand-build the pre-ephemeral shape: sealed under the ISSUER's
	// identity key, no EphPK.
	sealed, _, err := sealGroupKey(issuerSK, memberPK, key)
	require.NoError(t, err)
	m := KeyMutation{
		GroupID:  uuid.New(),
		Epoch:    1,
		IssuedAt: time.Now().UTC(),
		Wraps:    []KeyWrap{{RecipientPK: memberPK, Sealed: sealed}},
	}
	require.NoError(t, SignKey(&m, issuerSK))
	require.NoError(t, VerifyKey(m), "a pre-ephemeral rotation stopped verifying")
	require.False(t, m.Ephemeral())

	wrap, ok := m.wrapFor(memberPK)
	require.True(t, ok)
	require.Equal(t, issuerPK, wrap.sealerPK(m.IssuerPK),
		"a wrap with no ephemeral key must fall back to the issuer's identity key")
	got, err := openGroupKey(memberSK, wrap.sealerPK(m.IssuerPK), wrap.Sealed)
	require.NoError(t, err)
	require.Equal(t, key, got)
}

func TestKeyRotationDuePolicy(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	now := time.Now().UTC()

	t.Run("fresh key is not due", func(t *testing.T) {
		_, due := keyRotationDue(pk, now, 0, now)
		require.False(t, due)
	})

	t.Run("age trips it", func(t *testing.T) {
		// Past the window PLUS this visor's jitter — at exactly the
		// window a jittered admin is deliberately still waiting.
		reason, due := keyRotationDue(pk, now, 0, now.Add(DefaultKeyMaxAge+rotationJitterWindow))
		require.True(t, due)
		require.Equal(t, RotationReasonAge, reason)
	})

	t.Run("jitter holds an admin back inside the window", func(t *testing.T) {
		// Find a PK whose jitter is non-trivial, then check it is not
		// yet due at exactly DefaultKeyMaxAge.
		for i := 0; i < 50; i++ {
			cand, _ := cipher.GenerateKeyPair()
			if rotationJitter(cand) < time.Hour {
				continue
			}
			_, due := keyRotationDue(cand, now, 0, now.Add(DefaultKeyMaxAge))
			require.False(t, due, "a jittered admin rotated at the un-jittered threshold")
			return
		}
		t.Skip("no sufficiently-jittered key sampled")
	})

	t.Run("volume trips it", func(t *testing.T) {
		reason, due := keyRotationDue(pk, now, DefaultKeyMaxMessages, now)
		require.True(t, due)
		require.Equal(t, RotationReasonVolume, reason)
	})

	t.Run("a key with no issued-at is due", func(t *testing.T) {
		// Records written before KeyIssuedAt existed: their key is of
		// unbounded age, which is the thing this policy exists to stop.
		reason, due := keyRotationDue(pk, time.Time{}, 0, now)
		require.True(t, due)
		require.Equal(t, RotationReasonAge, reason)
	})
}

// Jitter has to be stable per identity: an admin that re-rolled it every
// boot could leapfrog its co-admins and rotate twice in one window.
func TestRotationJitterIsDeterministicAndBounded(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	first := rotationJitter(pk)
	require.Equal(t, first, rotationJitter(pk))
	require.GreaterOrEqual(t, int64(first), int64(0))
	require.Less(t, int64(first), int64(rotationJitterWindow))

	other, _ := cipher.GenerateKeyPair()
	require.NotEqual(t, pk, other)
}
