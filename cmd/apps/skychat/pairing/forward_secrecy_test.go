// Package pairing cmd/apps/skychat/pairing/forward_secrecy_test.go
//
// The dmsg-backed lane for epoch keys: two real visors have to converge
// on an epoch with no coordination beyond the announcements they publish
// on their own feeds, and what they then send must NOT be openable with
// the identity keys alone.
//
// Reuses newDMRig from pair_dm_comparison_test.go; skipped under -short
// like every other dmsg-backed test in this package.
package pairing

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

// hasText / hasFrom are testInbox predicates (see integration_test.go).
func hasText(text string) func([]testReceived) bool {
	return func(msgs []testReceived) bool {
		for _, m := range msgs {
			if m.text == text {
				return true
			}
		}
		return false
	}
}

func hasFrom(peer cipher.PubKey, text string) func([]testReceived) bool {
	return func(msgs []testReceived) bool {
		for _, m := range msgs {
			if m.peer == peer && m.text == text {
				return true
			}
		}
		return false
	}
}

// lastPublishedLeaf reads the raw bytes of the newest message leaf on a
// pair's own publisher — i.e. exactly what an observer with feed access
// would see, before any decryption.
//
// Reading the publisher rather than the subscriber is deliberate: this
// asserts what we PUT on the wire, which is the thing forward secrecy is
// a claim about.
func lastPublishedLeaf(t *testing.T, p *Pair) []byte {
	t.Helper()
	var newest string
	var value []byte
	p.pub.Walk(MessagePathPrefix, func(path string, v []byte) bool {
		if strings.Compare(path, newest) > 0 {
			newest = path
			value = append([]byte(nil), v...)
		}
		return true
	})
	return value
}

// warmUpEpoch drives both sides through the handshake that establishes
// an epoch: each publishes its ratchet key as part of a Send, then each
// derives the shared epoch from the other's.
//
// Two sends, not one, and that is the actual contract — the epoch is
// formed from BOTH ratchet keys, so a side that has never spoken has
// never published its half and neither end can seal under it. See
// Pair.maybeAnnounce for why the announcement rides a Send.
func warmUpEpoch(t *testing.T, rig *dmRig) {
	t.Helper()
	require.NoError(t, rig.pairA.Send("hello from A"))
	require.NoError(t, rig.pairB.Send("hello from B"))
	require.NoError(t, rig.inboxB.waitFor(20*time.Second, hasText("hello from A")),
		"B never received A's opening message")
	require.NoError(t, rig.inboxA.waitFor(20*time.Second, hasText("hello from B")),
		"A never received B's opening message")
}

// waitEpoch polls until the pair has left the legacy static key behind.
//
// Polling rather than a signal because convergence is genuinely
// asynchronous: it takes the peer's announcement being published,
// batched by the publisher, synced to our subscriber, and folded in.
func waitEpoch(t *testing.T, p *Pair, what string) EpochID {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if id, ok := p.EpochID(); ok {
			return id
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
	return EpochID{}
}

// Both ends must land on the SAME epoch from opposite directions, with
// no handshake — each side publishes its ratchet key and derives from
// whatever the other published.
func TestForwardSecrecy_BothSidesConvergeOnAnEpoch(t *testing.T) {
	if testing.Short() {
		t.Skip("dmsg integration test; skipped under -short")
	}
	rig := newDMRig(t)
	warmUpEpoch(t, rig)

	idA := waitEpoch(t, rig.pairA, "A to derive an epoch from B's ratchet key")
	idB := waitEpoch(t, rig.pairB, "B to derive an epoch from A's ratchet key")
	require.Equal(t, idA, idB,
		"the two ends are on different epochs — neither could open the other's messages")
	require.False(t, idA.IsZero())
}

// The headline: a message sent after the epoch exists is sealed under
// the epoch key, and the identity keys that used to open EVERYTHING no
// longer do.
func TestForwardSecrecy_MessagesAreNotOpenableWithIdentityKeys(t *testing.T) {
	if testing.Short() {
		t.Skip("dmsg integration test; skipped under -short")
	}
	rig := newDMRig(t)
	warmUpEpoch(t, rig)
	waitEpoch(t, rig.pairA, "A's epoch")
	waitEpoch(t, rig.pairB, "B's epoch")

	const body = "sealed under an epoch, not an identity"
	require.NoError(t, rig.pairA.Send(body))
	require.NoError(t, rig.inboxB.waitFor(5*time.Second, hasFrom(rig.pkA, body)),
		"B never received the epoch-sealed message")

	// Reconstruct exactly what an attacker holding both identity secret
	// keys would compute — the old static pair key — and confirm it does
	// not open the leaf that was just published.
	sealed := lastPublishedLeaf(t, rig.pairA)
	require.NotNil(t, sealed, "no message leaf was published")

	id, envBody, tagged := parseEnvelope(sealed)
	require.True(t, tagged, "the message went out in the legacy untagged form — the epoch was not used")
	require.False(t, id.IsZero())

	staticKey, err := derivePairKey(rig.pairA.cfg.MySK, rig.pkB)
	require.NoError(t, err)
	_, err = openMessage(staticKey, envBody)
	require.Error(t, err,
		"the static identity-derived key opened an epoch-sealed message — forward secrecy is not actually in effect")

	// And the epoch key does open it, so the failure above is about the
	// key and not about the envelope being malformed.
	epochKey, ok := rig.pairA.ratchet.keyFor(id)
	require.True(t, ok)
	_, err = openMessage(epochKey, envBody)
	require.NoError(t, err)
}

// After a rotation, messages sealed under the RETIRED epoch must still
// be readable (the ring holds them) while the retired ratchet secret is
// gone. Both halves matter: dropping the secret without keeping the
// derived key would make a user's own history vanish.
func TestForwardSecrecy_RotationKeepsHistoryReadable(t *testing.T) {
	if testing.Short() {
		t.Skip("dmsg integration test; skipped under -short")
	}
	rig := newDMRig(t)
	warmUpEpoch(t, rig)
	oldEpoch := waitEpoch(t, rig.pairA, "A's first epoch")
	waitEpoch(t, rig.pairB, "B's first epoch")

	const before = "before the rotation"
	require.NoError(t, rig.pairA.Send(before))
	require.NoError(t, rig.inboxB.waitFor(5*time.Second, hasText(before)),
		"the pre-rotation message never arrived")

	// A rotates and re-announces; B picks the new key up and both move
	// to a new epoch.
	retiredSK := rig.pairA.ratchet.mySK
	rig.pairA.ratchet.rotate(time.Now().UTC())
	require.NotEqual(t, retiredSK, rig.pairA.ratchet.mySK)
	rig.pairA.saveRatchet()
	// Announce the new generation the way production does — as part of
	// a Send, so the ratchet leaf rides the message's publisher batch.
	// Publishing it standalone here is what wedged the CXO node about
	// one run in four; see maybeAnnounce for the lock inversion that
	// makes a bare publish unsafe.
	require.NoError(t, rig.pairA.Send("announcing the new epoch"))

	deadline := time.Now().Add(30 * time.Second)
	var newEpoch EpochID
	for time.Now().Before(deadline) {
		if id, ok := rig.pairB.EpochID(); ok && id != oldEpoch {
			newEpoch = id
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	require.False(t, newEpoch.IsZero(), "B never picked up A's rotated ratchet key")

	const after = "after the rotation"
	require.NoError(t, rig.pairA.Send(after))
	require.NoError(t, rig.inboxB.waitFor(10*time.Second, hasText(after)),
		"the post-rotation message never arrived — the two ends disagree on the current epoch")

	// The retired epoch's key is still in both rings, so the earlier
	// message stays readable.
	_, held := rig.pairA.ratchet.keyFor(oldEpoch)
	require.True(t, held, "A dropped the retired epoch key and can no longer read its own history")
	_, held = rig.pairB.ratchet.keyFor(oldEpoch)
	require.True(t, held, "B dropped the retired epoch key and can no longer read the history it received")
}

// A peer that never announces (an older build) must keep working: the
// pair stays on the legacy static key rather than failing closed.
func TestForwardSecrecy_FallsBackForAPeerWithNoRatchet(t *testing.T) {
	if testing.Short() {
		t.Skip("dmsg integration test; skipped under -short")
	}
	rig := newDMRig(t)

	// Simulate the old build by wiping what B announced, so A behaves as
	// if it had never seen a ratchet key from B.
	rig.pairA.ratchet.mu.Lock()
	rig.pairA.ratchet.peerPK = cipher.PubKey{}
	rig.pairA.ratchet.peerGen = 0
	rig.pairA.ratchet.current = EpochID{}
	rig.pairA.ratchet.mu.Unlock()

	_, ok := rig.pairA.EpochID()
	require.False(t, ok, "the pair should report no epoch once the peer's announcement is gone")

	const body = "legacy path still works"
	require.NoError(t, rig.pairA.Send(body))

	sealed := lastPublishedLeaf(t, rig.pairA)
	require.NotNil(t, sealed)
	_, _, tagged := parseEnvelope(sealed)
	require.False(t, tagged, "a pair with no epoch should fall back to the untagged legacy form")

	// And B, which does hold the static key, still reads it.
	require.NoError(t, rig.inboxB.waitFor(10*time.Second, hasText(body)),
		"the legacy fallback message never arrived")
}
