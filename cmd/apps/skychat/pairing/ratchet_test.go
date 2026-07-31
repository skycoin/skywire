// Package pairing cmd/apps/skychat/pairing/ratchet_test.go
//
// The epoch machinery without a transport: derivation symmetry, the
// announcement envelope, the ring, and the property the whole thing
// exists for — that a rotated-past epoch cannot be reconstructed from
// the identity keys.
package pairing

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

// Both ends must land on the same epoch key from opposite halves —
// this is the ECDH symmetry the whole scheme rests on, now over ratchet
// keys instead of identity keys.
func TestEpochKeyIsSymmetric(t *testing.T) {
	aPK, aSK := cipher.GenerateKeyPair()
	bPK, bSK := cipher.GenerateKeyPair()

	idA, keyA, err := deriveEpochKey(aSK, aPK, bPK)
	require.NoError(t, err)
	idB, keyB, err := deriveEpochKey(bSK, bPK, aPK)
	require.NoError(t, err)

	require.Equal(t, idA, idB, "the two ends computed different epoch IDs for the same pair of ratchet keys")
	require.Equal(t, []byte(keyA), []byte(keyB), "the two ends derived different keys — nothing either sends would open")
	require.Len(t, keyA, 32)
}

// A different ratchet key is a different epoch, which is what makes
// rotation mean anything.
func TestEpochKeyChangesWithRatchetKey(t *testing.T) {
	aPK, aSK := cipher.GenerateKeyPair()
	b1PK, _ := cipher.GenerateKeyPair()
	b2PK, _ := cipher.GenerateKeyPair()

	id1, key1, err := deriveEpochKey(aSK, aPK, b1PK)
	require.NoError(t, err)
	id2, key2, err := deriveEpochKey(aSK, aPK, b2PK)
	require.NoError(t, err)

	require.NotEqual(t, id1, id2)
	require.NotEqual(t, []byte(key1), []byte(key2))
}

// THE property. An attacker who later obtains BOTH identity secret keys
// still cannot derive an epoch whose ratchet secret has been dropped —
// which is precisely what could not be said of the static pair key,
// where those two identity keys were the whole input.
func TestRotatedEpochIsNotDerivableFromIdentityKeys(t *testing.T) {
	myIDPK, myIDSK := cipher.GenerateKeyPair()
	peerIDPK, peerIDSK := cipher.GenerateKeyPair()

	// The old world: the pair key IS a function of the identity keys, so
	// an attacker holding either secret recomputes it at will, forever.
	staticMine, err := derivePairKey(myIDSK, peerIDPK)
	require.NoError(t, err)
	staticTheirs, err := derivePairKey(peerIDSK, myIDPK)
	require.NoError(t, err)
	require.Equal(t, []byte(staticMine), []byte(staticTheirs),
		"sanity: the legacy static key really is recomputable from identity keys alone")

	// The new world: an epoch under ratchet keys.
	rt := newRatchetState(time.Now().UTC())
	peerRatPK, _ := cipher.GenerateKeyPair()
	epochID, epochKey, err := deriveEpochKey(rt.mySK, rt.myPK, peerRatPK)
	require.NoError(t, err)

	// Rotating drops the secret half that formed it.
	before := rt.mySK
	rt.rotate(time.Now().UTC())
	require.NotEqual(t, before, rt.mySK, "rotate did not replace the ratchet secret")

	// Every combination of long-term identity material an attacker could
	// hold, tried against the retired epoch.
	for name, sk := range map[string]cipher.SecKey{"my identity": myIDSK, "peer identity": peerIDSK} {
		for pkName, pk := range map[string]cipher.PubKey{
			"my identity": myIDPK, "peer identity": peerIDPK,
			"my retired ratchet": rt.myPK, "peer ratchet": peerRatPK,
		} {
			_, guess, derr := deriveEpochKey(sk, pk, peerRatPK)
			if derr != nil {
				continue
			}
			require.NotEqual(t, []byte(epochKey), []byte(guess),
				"the retired epoch key was reconstructed from %s + %s — the ratchet secret was not the only input", name, pkName)
		}
	}

	// And the epoch it formed is genuinely gone from this visor too:
	// that symmetry is the honest cost, and the ring is what mitigates
	// it for history we still want.
	_, held := rt.keyFor(epochID)
	require.False(t, held, "the retired epoch should not be current; only the ring may still hold it")
}

func TestRatchetAnnounceSignVerify(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	ratPK, _ := cipher.GenerateKeyPair()

	a := RatchetAnnounce{Generation: 3, RatchetPK: ratPK, IssuedAt: time.Now().UTC()}
	require.NoError(t, signRatchet(&a, sk))
	require.Equal(t, pk, a.IssuerPK)
	require.NoError(t, verifyRatchet(a, pk))

	// Signed by the right key but attributed to the wrong peer: the
	// issuer check is what stops a valid announcement from ANY visor
	// steering an unrelated pair onto a key of the attacker's choosing.
	other, _ := cipher.GenerateKeyPair()
	require.Error(t, verifyRatchet(a, other))
}

func TestRatchetAnnounceRejectsTampering(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	ratPK, _ := cipher.GenerateKeyPair()
	evilPK, _ := cipher.GenerateKeyPair()

	base := RatchetAnnounce{Generation: 1, RatchetPK: ratPK, IssuedAt: time.Now().UTC()}
	require.NoError(t, signRatchet(&base, sk))

	for name, mutate := range map[string]func(*RatchetAnnounce){
		"ratchet key swapped": func(a *RatchetAnnounce) { a.RatchetPK = evilPK },
		"generation bumped":   func(a *RatchetAnnounce) { a.Generation = 99 },
		"issued_at moved":     func(a *RatchetAnnounce) { a.IssuedAt = a.IssuedAt.Add(time.Hour) },
	} {
		t.Run(name, func(t *testing.T) {
			tampered := base
			mutate(&tampered)
			require.Error(t, verifyRatchet(tampered, pk),
				"a tampered announcement verified; the field it changed decides which key we derive")
		})
	}
}

func TestRatchetAnnounceRoundTripsThroughJSON(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	ratPK, _ := cipher.GenerateKeyPair()
	a := RatchetAnnounce{Generation: 7, RatchetPK: ratPK, IssuedAt: time.Now().UTC().Truncate(time.Millisecond)}
	require.NoError(t, signRatchet(&a, sk))

	body, err := marshalRatchet(a)
	require.NoError(t, err)
	got, err := unmarshalRatchet(body, pk)
	require.NoError(t, err)
	require.Equal(t, a.Generation, got.Generation)
	require.Equal(t, a.RatchetPK, got.RatchetPK)
}

// observePeer must fold in every announcement it can derive an epoch
// from, but only ADVANCE on a newer generation — a resync replays old
// announcements and walking backwards would strand us on a retired key.
func TestObservePeerAdvancesOnlyForward(t *testing.T) {
	rt := newRatchetState(time.Now().UTC())
	peerPK, peerSK := cipher.GenerateKeyPair()
	newerPK, _ := cipher.GenerateKeyPair()

	gen1 := RatchetAnnounce{Generation: 1, RatchetPK: peerPK, IssuedAt: time.Now().UTC()}
	require.NoError(t, signRatchet(&gen1, peerSK))
	gen2 := RatchetAnnounce{Generation: 2, RatchetPK: newerPK, IssuedAt: time.Now().UTC()}
	require.NoError(t, signRatchet(&gen2, peerSK))

	advanced, err := rt.observePeer(gen1, time.Now().UTC())
	require.NoError(t, err)
	require.True(t, advanced)
	first, _, ok := rt.currentEpoch()
	require.True(t, ok)

	advanced, err = rt.observePeer(gen2, time.Now().UTC())
	require.NoError(t, err)
	require.True(t, advanced)
	second, _, ok := rt.currentEpoch()
	require.True(t, ok)
	require.NotEqual(t, first, second)

	// Replaying the old one must not move us back...
	advanced, err = rt.observePeer(gen1, time.Now().UTC())
	require.NoError(t, err)
	require.False(t, advanced, "an older announcement moved the current epoch backwards")
	nowID, _, ok := rt.currentEpoch()
	require.True(t, ok)
	require.Equal(t, second, nowID)

	// ...but its key must still be held, because messages sealed under
	// it are sitting in the feed.
	_, held := rt.keyFor(first)
	require.True(t, held, "the superseded epoch's key was dropped; its messages would be unreadable")
}

func TestRingIsBoundedAndDropsOldest(t *testing.T) {
	rt := newRatchetState(time.Now().UTC())
	var firstID EpochID
	for i := 0; i < ratchetRingCap+5; i++ {
		peerPK, _ := cipher.GenerateKeyPair()
		id, key, err := deriveEpochKey(rt.mySK, rt.myPK, peerPK)
		require.NoError(t, err)
		rt.installLocked(id, key, time.Now().UTC())
		if i == 0 {
			firstID = id
		}
	}
	require.Len(t, rt.ring, ratchetRingCap)
	_, held := rt.keyFor(firstID)
	require.False(t, held, "the oldest epoch key survived past the ring cap")
}

func TestRotationDuePolicy(t *testing.T) {
	now := time.Now().UTC()
	rt := newRatchetState(now)

	require.False(t, rt.rotationDue(now.Add(10*ratchetMaxAge)),
		"rotated with no peer announcement — there is no epoch to rotate and it would just burn generations")

	peerPK, peerSK := cipher.GenerateKeyPair()
	a := RatchetAnnounce{Generation: 1, RatchetPK: peerPK, IssuedAt: now}
	require.NoError(t, signRatchet(&a, peerSK))
	_, err := rt.observePeer(a, now)
	require.NoError(t, err)

	require.False(t, rt.rotationDue(now))
	require.True(t, rt.rotationDue(now.Add(ratchetMaxAge)), "the age threshold did not trip")

	rt2 := newRatchetState(now)
	_, err = rt2.observePeer(a, now)
	require.NoError(t, err)
	for i := 0; i < ratchetMaxMessages; i++ {
		rt2.noteSent()
	}
	require.True(t, rt2.rotationDue(now), "the volume threshold did not trip")
}

// A restart must resume the same generation and the same ring; minting a
// fresh keypair would strand the peer on an announcement whose secret
// half no longer exists.
func TestRatchetStateSurvivesSnapshotRestore(t *testing.T) {
	now := time.Now().UTC()
	rt := newRatchetState(now)
	peerPK, peerSK := cipher.GenerateKeyPair()
	a := RatchetAnnounce{Generation: 1, RatchetPK: peerPK, IssuedAt: now}
	require.NoError(t, signRatchet(&a, peerSK))
	_, err := rt.observePeer(a, now)
	require.NoError(t, err)

	wantID, wantKey, ok := rt.currentEpoch()
	require.True(t, ok)

	restored := restoreRatchetState(rt.snapshot(), now)
	gotID, gotKey, ok := restored.currentEpoch()
	require.True(t, ok, "the restored ratchet has no current epoch")
	require.Equal(t, wantID, gotID)
	require.Equal(t, []byte(wantKey), []byte(gotKey))
	require.Equal(t, rt.myGen, restored.myGen)
	require.Equal(t, rt.mySK, restored.mySK)
}

// A snapshot with no usable secret must not produce a ratchet that
// cannot seal — better one lost epoch than a mute pair.
func TestRestoreFallsBackToAFreshRatchet(t *testing.T) {
	got := restoreRatchetState(RatchetState{}, time.Now().UTC())
	require.EqualValues(t, 1, got.myGen)
	require.NotEqual(t, cipher.SecKey{}, got.mySK)
}

// The envelope has to be distinguishable from the pre-epoch leaf format
// in both directions, since both live in the same feed forever.
func TestEnvelopeRoundTripAndLegacyPassthrough(t *testing.T) {
	_, sk := cipher.GenerateKeyPair()
	peerPK, _ := cipher.GenerateKeyPair()
	static, err := derivePairKey(sk, peerPK)
	require.NoError(t, err)

	ratPK, ratSK := cipher.GenerateKeyPair()
	otherPK, _ := cipher.GenerateKeyPair()
	id, key, err := deriveEpochKey(ratSK, ratPK, otherPK)
	require.NoError(t, err)

	sealed, err := sealEnvelope(id, key, []byte(`{"text":"hi"}`))
	require.NoError(t, err)

	gotID, body, tagged := parseEnvelope(sealed)
	require.True(t, tagged, "an epoch envelope was not recognized as one")
	require.Equal(t, id, gotID)
	pt, err := openMessage(key, body)
	require.NoError(t, err)
	require.JSONEq(t, `{"text":"hi"}`, string(pt))

	// A legacy blob must NOT be mistaken for an envelope.
	legacy, err := sealMessage(static, []byte(`{"text":"old"}`))
	require.NoError(t, err)
	_, _, tagged = parseEnvelope(legacy)
	require.False(t, tagged, "a pre-epoch leaf was parsed as an epoch envelope")
}

func TestEpochIDTextRoundTrip(t *testing.T) {
	aPK, aSK := cipher.GenerateKeyPair()
	bPK, _ := cipher.GenerateKeyPair()
	id, _, err := deriveEpochKey(aSK, aPK, bPK)
	require.NoError(t, err)

	blob, err := json.Marshal(struct {
		ID EpochID `json:"id"`
	}{id})
	require.NoError(t, err)
	require.Contains(t, string(blob), id.String(), "an epoch ID should persist as hex, not as a byte array")

	var back struct {
		ID EpochID `json:"id"`
	}
	require.NoError(t, json.Unmarshal(blob, &back))
	require.Equal(t, id, back.ID)
}
