// Package group pkg/skychat/group/gossip_seal_test.go c4-app-chat
//
// Coverage for governance privacy: the roster/admin/mod leaves of an
// encrypted group must be sealed on the feed, must still converge, and
// must survive arriving before the key that opens them. Plus the other
// half of the same property — a rotation's wrap list no longer naming its
// recipients, which would otherwise republish the roster in the clear on
// every re-key.
package group

import (
	"bytes"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
)

// sealTestKey is a fixed 32-byte group key. Fixed rather than generated
// so a failure prints the same bytes twice.
func sealTestKey(b byte) []byte { return bytes.Repeat([]byte{b}, 32) }

// pathFor is the leaf path a publish helper writes to, for read-back.
func pathFor(prefix string, seq uint64) string {
	return fmt.Sprintf("%s/%05d", prefix, seq)
}

// encryptedPublisherSession is testPublisherSession with the record made
// private and given a key, i.e. the shape that seals its governance.
func encryptedPublisherSession(t *testing.T, key []byte) *Session {
	t.Helper()
	s := testPublisherSession(t)
	s.cfg.Record.Mode = ModePrivate
	s.cfg.Record.Kind = KindPrivate
	s.cfg.Record.AESKey = key
	s.cfg.Record.Admins = []cipher.PubKey{s.cfg.MyPK}
	return s
}

// THE property. A ban leaf used to sit on the feed as readable JSON, so
// anyone who could pull the tree — an evicted member with a copy, a peer
// serving backfill — read who was banned and when.
func TestModerationLeafIsSealedOnAnEncryptedGroup(t *testing.T) {
	key := sealTestKey(0x11)
	s := encryptedPublisherSession(t, key)
	victim, _ := cipher.GenerateKeyPair()

	seq, err := s.PublishModMutation(ModOpBan, victim, 0)
	require.NoError(t, err)

	body := pubReadable(t, s.pub, pathFor(ModerationPathPrefix, seq))
	require.True(t, isSealedGossip(body), "the moderation leaf went onto the feed unsealed")
	require.NotContains(t, string(body), victim.Hex(),
		"the banned member's PK is readable in the leaf bytes")
	_, err = UnmarshalMod(body)
	require.Error(t, err, "a sealed leaf decoded as a plaintext envelope")

	// A member holding the key reads it exactly as before.
	pt, err := openGossip([][]byte{key}, body)
	require.NoError(t, err)
	m, err := UnmarshalMod(pt)
	require.NoError(t, err)
	require.Equal(t, ModOpBan, m.Op)
	require.Equal(t, victim, m.PeerPK)
	require.Equal(t, s.cfg.MyPK, m.IssuerPK, "sealing must not disturb the signature or the issuer")
}

// Roster and admin leaves take the same treatment — membership is as much
// of a fact about a person as a ban is.
func TestRosterAndAdminLeavesAreSealedOnAnEncryptedGroup(t *testing.T) {
	key := sealTestKey(0x22)
	s := encryptedPublisherSession(t, key)
	peer, _ := cipher.GenerateKeyPair()

	rSeq, err := s.PublishRosterMutation(RosterOpAdd, peer, 0)
	require.NoError(t, err)
	aSeq, err := s.PublishAdminMutation(AdminOpPromote, peer, 0)
	require.NoError(t, err)

	for _, tc := range []struct {
		name string
		body []byte
	}{
		{"roster", pubReadable(t, s.pub, pathFor(RosterPathPrefix, rSeq))},
		{"admin", pubReadable(t, s.pub, pathFor(AdminPathPrefix, aSeq))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.True(t, isSealedGossip(tc.body))
			require.NotContains(t, string(tc.body), peer.Hex())
			pt, err := openGossip([][]byte{key}, tc.body)
			require.NoError(t, err)
			require.NotContains(t, string(pt), "SKG1")
			require.Contains(t, string(pt), peer.Hex(), "the sealed envelope did not survive the round trip")
		})
	}
}

// A public group has no key by design — admission is open, so the key
// would go to any stranger who asked. Its governance stays plaintext
// rather than failing to publish.
func TestGovernanceStaysPlaintextOnAPublicGroup(t *testing.T) {
	s := testPublisherSession(t)
	s.cfg.Record.Mode = ModePublic
	s.cfg.Record.Kind = KindPublic
	s.cfg.Record.Admins = []cipher.PubKey{s.cfg.MyPK}
	peer, _ := cipher.GenerateKeyPair()

	seq, err := s.PublishRosterMutation(RosterOpAdd, peer, 0)
	require.NoError(t, err)

	body := pubReadable(t, s.pub, pathFor(RosterPathPrefix, seq))
	require.False(t, isSealedGossip(body))
	m, err := UnmarshalRoster(body)
	require.NoError(t, err)
	require.Equal(t, peer, m.PeerPK)
}

// Fails closed. An encrypted group that holds no key must NOT fall back
// to publishing plaintext: the leaf would sit on the feed readable
// forever and no caller could tell from the return value.
func TestPublishRefusesGovernanceWhenAnEncryptedGroupHoldsNoKey(t *testing.T) {
	s := testPublisherSession(t)
	s.cfg.Record.Mode = ModePrivate
	s.cfg.Record.Kind = KindPrivate
	s.cfg.Record.Admins = []cipher.PubKey{s.cfg.MyPK}
	peer, _ := cipher.GenerateKeyPair()

	_, err := s.PublishRosterMutation(RosterOpAdd, peer, 0)
	require.ErrorIs(t, err, errGossipNoSealKey)
	_, err = s.PublishAdminMutation(AdminOpPromote, peer, 0)
	require.ErrorIs(t, err, errGossipNoSealKey)
	_, err = s.PublishModMutation(ModOpBan, peer, 0)
	require.ErrorIs(t, err, errGossipNoSealKey)
}

// sealedGovernanceSession is a member-role session on an encrypted group,
// optionally already holding the key.
func sealedGovernanceSession(t *testing.T, key []byte) (s *Session, gid uuid.UUID, adminSK cipher.SecKey, victim cipher.PubKey) {
	t.Helper()
	gid = uuid.New()
	adminPK, adminSK := cipher.GenerateKeyPair()
	myPK, mySK := cipher.GenerateKeyPair()
	victim, _ = cipher.GenerateKeyPair()
	s = &Session{
		cfg: Config{
			MyPK: myPK,
			MySK: mySK,
			Record: Record{
				ID:      gid.String(),
				OwnerPK: adminPK,
				Admins:  []cipher.PubKey{adminPK},
				Members: []cipher.PubKey{adminPK, myPK, victim},
				Kind:    KindPrivate,
				Mode:    ModePrivate,
				AESKey:  key,
			},
		},
		members: []cipher.PubKey{adminPK, myPK, victim},
		log:     logging.MustGetLogger("group.gossip-seal-test"),
	}
	return s, gid, adminSK, victim
}

// The receive side of the same property: a sealed leaf converges exactly
// as the plaintext one did.
func TestSealedGovernanceLeafConverges(t *testing.T) {
	key := sealTestKey(0x33)
	s, gid, adminSK, victim := sealedGovernanceSession(t, key)

	sealed, err := sealGossip(key, signedMod(t, gid.String(), ModOpBan, victim, adminSK, time.Now().UTC()))
	require.NoError(t, err)
	s.applyModLeaf(sealed)

	require.True(t, s.IsBanned(victim), "a sealed ban did not converge")
}

// Compatibility in the direction that matters: leaves already sitting in
// feeds are plaintext, and an upgraded reader still applies them.
func TestLegacyPlaintextGovernanceStillAppliesOnAnEncryptedGroup(t *testing.T) {
	key := sealTestKey(0x44)
	s, gid, adminSK, victim := sealedGovernanceSession(t, key)

	s.applyModLeaf(signedMod(t, gid.String(), ModOpBan, victim, adminSK, time.Now().UTC()))

	require.True(t, s.IsBanned(victim), "a pre-seal plaintext leaf stopped converging")
}

// The parking lot. A sealed governance leaf reaches us through a
// different feed than the key that opens it, so arriving first is normal.
// Subscriber callbacks fire once per leaf — dropping it here would drop
// it permanently, and the ban would never take effect on this visor.
func TestSealedGovernanceLeafParksUntilTheKeyArrives(t *testing.T) {
	key := sealTestKey(0x55)
	// No key in hand: the shape of a joiner whose admission response is
	// still in flight.
	s, gid, adminSK, victim := sealedGovernanceSession(t, nil)

	sealed, err := sealGossip(key, signedMod(t, gid.String(), ModOpBan, victim, adminSK, time.Now().UTC()))
	require.NoError(t, err)
	s.applyModLeaf(sealed)

	require.False(t, s.IsBanned(victim), "a leaf we cannot read was applied anyway")
	s.deferredGossipMu.Lock()
	parked := len(s.deferredGossip)
	s.deferredGossipMu.Unlock()
	require.Equal(t, 1, parked, "the unreadable leaf was dropped instead of parked")

	// The key arrives — the admission response, or a rotation.
	s.SetGroupKey(KeyState{Epoch: 1, Key: key})

	require.True(t, s.IsBanned(victim), "the parked ban was not replayed once its key arrived")
	s.deferredGossipMu.Lock()
	parked = len(s.deferredGossip)
	s.deferredGossipMu.Unlock()
	require.Zero(t, parked, "the parking lot was not drained")
}

// A leaf sealed to some other group's key never becomes readable. It may
// park (we cannot distinguish it from a key we have not received yet) but
// it must never apply, and it must not grow without bound.
func TestSealedGovernanceLeafFromAnotherKeyNeverApplies(t *testing.T) {
	s, gid, adminSK, victim := sealedGovernanceSession(t, sealTestKey(0x66))

	sealed, err := sealGossip(sealTestKey(0x99), signedMod(t, gid.String(), ModOpBan, victim, adminSK, time.Now().UTC()))
	require.NoError(t, err)
	for i := 0; i < deferredGossipCap+10; i++ {
		s.applyModLeaf(sealed)
	}

	require.False(t, s.IsBanned(victim))
	s.deferredGossipMu.Lock()
	parked := len(s.deferredGossip)
	s.deferredGossipMu.Unlock()
	require.LessOrEqual(t, parked, deferredGossipCap, "the parking lot is unbounded")
}

// Truncated or malformed frames are dropped outright, not parked: no key
// will ever make them readable.
func TestMalformedSealedFrameIsDroppedNotParked(t *testing.T) {
	s, _, _, _ := sealedGovernanceSession(t, sealTestKey(0x77))

	s.applyRosterLeaf(append([]byte(nil), gossipSealMagic...))
	s.applyRosterLeaf(append(append([]byte(nil), gossipSealMagic...), 0x01, 0x02))

	s.deferredGossipMu.Lock()
	parked := len(s.deferredGossip)
	s.deferredGossipMu.Unlock()
	require.Zero(t, parked, "a frame no key could ever open was parked for replay")
}

func TestSealGossipRoundTripAndKeyRing(t *testing.T) {
	cur, old := sealTestKey(0x01), sealTestKey(0x02)
	plaintext := []byte(`{"op":1}`)

	sealedOld, err := sealGossip(old, plaintext)
	require.NoError(t, err)

	// Current key first, ring behind it — the same order messages use.
	got, err := openGossip([][]byte{cur, old}, sealedOld)
	require.NoError(t, err)
	require.Equal(t, plaintext, got)

	// No key that opens it.
	_, err = openGossip([][]byte{cur}, sealedOld)
	require.ErrorIs(t, err, ErrGossipSealedUnreadable)

	// An unsealed body passes through untouched — that is the legacy
	// path, not an error.
	got, err = openGossip(nil, plaintext)
	require.NoError(t, err)
	require.Equal(t, plaintext, got)
}

// The other half of governance privacy. Sealing the roster leaves buys
// nothing if every rotation republishes the member list in the clear —
// and worse, diffing two rotations' recipient sets names whoever was just
// evicted.
func TestKeyRotationDoesNotNameItsRecipients(t *testing.T) {
	_, adminSK := cipher.GenerateKeyPair()
	aPK, aSK := cipher.GenerateKeyPair()
	bPK, bSK := cipher.GenerateKeyPair()
	_, outsiderSK := cipher.GenerateKeyPair()
	key, err := GenerateAESKey()
	require.NoError(t, err)

	m, skipped, err := buildKeyMutation(uuid.New(), 7, key, []cipher.PubKey{aPK, bPK}, adminSK, time.Now().UTC())
	require.NoError(t, err)
	require.Empty(t, skipped)

	wire, err := MarshalKey(m)
	require.NoError(t, err)
	require.NotContains(t, string(wire), aPK.Hex(), "the rotation names a recipient on the wire")
	require.NotContains(t, string(wire), bPK.Hex(), "the rotation names a recipient on the wire")
	require.Empty(t, m.Recipients(), "a blinded rotation should expose no recipient list")

	// Each recipient still finds its own wrap, and only its own.
	wa, ok := m.wrapForRecipient(aSK, aPK)
	require.True(t, ok, "a recipient could not find the wrap addressed to it")
	wb, ok := m.wrapForRecipient(bSK, bPK)
	require.True(t, ok)
	require.NotEqual(t, wa.Tag, wb.Tag)
	require.NotEqual(t, wa.Sealed, wb.Sealed)

	opened, err := openGroupKey(aSK, wa.sealerPK(m.IssuerPK), wa.Sealed)
	require.NoError(t, err)
	require.Equal(t, key, opened)

	// Someone who is not a recipient matches no tag at all — it cannot
	// even tell which wrap to try.
	_, ok = m.wrapForRecipient(outsiderSK, cipher.PubKey{})
	require.False(t, ok, "a non-recipient matched a wrap tag")
}

// The tag decides which wrap a member opens, so an attacker who could
// rewrite it could push a member off the key schedule silently.
func TestWrapTagIsSigned(t *testing.T) {
	_, adminSK := cipher.GenerateKeyPair()
	memberPK, _ := cipher.GenerateKeyPair()
	key, err := GenerateAESKey()
	require.NoError(t, err)

	m, _, err := buildKeyMutation(uuid.New(), 1, key, []cipher.PubKey{memberPK}, adminSK, time.Now().UTC())
	require.NoError(t, err)
	require.NoError(t, VerifyKey(m))

	swapped := m
	swapped.Wraps = append([]KeyWrap(nil), m.Wraps...)
	swapped.Wraps[0].Tag = bytes.Repeat([]byte{0xee}, wrapTagLen)
	require.Error(t, VerifyKey(swapped), "the wrap tag can be swapped without breaking the signature")

	stripped := m
	stripped.Wraps = append([]KeyWrap(nil), m.Wraps...)
	stripped.Wraps[0].Tag = nil
	require.Error(t, VerifyKey(stripped), "the wrap tag can be stripped without breaking the signature")
}

// Blinded wraps all carry the same (zero) RecipientPK, so the canonical
// sort would be an all-ties comparison if it still keyed on that alone —
// and sort.Slice is not stable, which would make the digest, and with it
// the signature, nondeterministic.
func TestCanonicalKeyBytesAreStableAcrossBlindedWraps(t *testing.T) {
	_, adminSK := cipher.GenerateKeyPair()
	recipients := make([]cipher.PubKey, 0, 8)
	for i := 0; i < 8; i++ {
		pk, _ := cipher.GenerateKeyPair()
		recipients = append(recipients, pk)
	}
	key, err := GenerateAESKey()
	require.NoError(t, err)

	m, _, err := buildKeyMutation(uuid.New(), 2, key, recipients, adminSK, time.Now().UTC())
	require.NoError(t, err)

	want := canonicalBytesKey(m)
	for i := 0; i < 20; i++ {
		require.Equal(t, want, canonicalBytesKey(m), "canonical bytes changed between calls")
	}

	// And a re-serialized copy in a different wrap order still verifies.
	reordered := m
	reordered.Wraps = append([]KeyWrap(nil), m.Wraps...)
	for i, j := 0, len(reordered.Wraps)-1; i < j; i, j = i+1, j-1 {
		reordered.Wraps[i], reordered.Wraps[j] = reordered.Wraps[j], reordered.Wraps[i]
	}
	require.NoError(t, VerifyKey(reordered), "wrap order affected the signature")
}
