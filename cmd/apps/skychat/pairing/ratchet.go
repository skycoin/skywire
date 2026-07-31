// Package pairing cmd/apps/skychat/pairing/ratchet.go c4-app-chat
// the per-pair ratchet: which short-lived keys we hold, which the peer
// has announced, and when to move on.
//
// # The announcement
//
// Each side publishes its current ratchet PUBLIC key as a leaf on its own
// feed, at ratchet/<generation>. That is the whole handshake — there is
// no round trip, no negotiation, and no ordering requirement. Each side
// derives every epoch it can from (its own ratchet secrets) × (the peer's
// announced ratchet keys), so the two ends converge on the same set of
// epoch keys whichever order the leaves arrive in.
//
// Announcements are signed with the visor's IDENTITY key. The CXO Root
// that carries the leaf is already signed by the feed owner, so this is
// belt-and-braces — but the field it protects is the one that decides
// which key we derive, and an attacker who could rewrite a leaf without
// the signature would be able to steer us onto a key of their choosing.
// The cost is one signature per rotation, which is hours apart.
//
// # Why announcements are re-published, not just published once
//
// A pair's CXO tree is the durable record: a subscriber that resyncs
// walks the whole tree and sees every ratchet/<gen> leaf, including ones
// published while it was offline. Keeping the past generations in the
// tree (rather than overwriting one path) is deliberate — a peer that was
// away for three of our rotations still needs the announcements for the
// epochs whose messages it hasn't read yet, and pruning them would make
// that history unopenable for a reason unrelated to forward secrecy.
//
// The ratchet SECRETS are the ones that get dropped, and dropping them is
// what makes those old epochs forward-secret: the announcement being
// public is fine, since a public key alone derives nothing.
//
// # What is deliberately not here
//
// No per-message chain key. See epoch.go for why the epoch, not the
// message, is the unit of forward secrecy on a CXO feed.
package pairing

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
)

// RatchetPathPrefix is the CXO path prefix for ratchet announcements on
// a pair feed: "<RatchetPathPrefix>/<generation>". Distinct from
// MessagePathPrefix so a subscriber can route the two without parsing.
const RatchetPathPrefix = "ratchet"

const (
	// ratchetMaxAge is how long one ratchet key stays current.
	//
	// A day is the unit that makes the guarantee describable to a human
	// — "an identity leak exposes at most today's messages" — and it
	// keeps the epoch ring (ratchetRingCap) covering roughly eight
	// months of history, comfortably longer than anyone scrolls back.
	ratchetMaxAge = 24 * time.Hour

	// ratchetMaxMessages is how many messages one ratchet key may cover
	// before we move on, bounding a busy conversation the way
	// ratchetMaxAge bounds a slow one.
	ratchetMaxMessages = 512

	// ratchetRingCap is how many past epoch keys a pair keeps so it can
	// still read its own history.
	//
	// This is the honest limit of the whole scheme, and it is a limit by
	// DESIGN rather than an implementation shortcut: a key we keep is a
	// key a full-disk compromise finds, so "keep everything forever"
	// would give back most of what the ratchet just bought. 256 epochs
	// at the default cadence is on the order of eight months of readable
	// history; past that the feed's own ciphertext stops opening, for us
	// as much as for an attacker.
	ratchetRingCap = 256
)

// errNoPeerRatchet means we have not yet observed a ratchet announcement
// from the peer, so no epoch exists and the caller must fall back to the
// legacy static pair key.
var errNoPeerRatchet = errors.New("pairing: peer has not announced a ratchet key")

// RatchetAnnounce is one side's published ratchet public key.
type RatchetAnnounce struct {
	// Generation counts this side's ratchet keys, starting at 1. Used
	// for the leaf path and for picking the newest announcement when
	// several arrive together; it is NOT the epoch (an epoch is formed
	// by one of these and one of the peer's).
	Generation uint64 `json:"generation"`

	// RatchetPK is the public half of the announcing side's current
	// ratchet keypair. Its secret half never leaves that visor and is
	// destroyed when the generation is retired.
	RatchetPK cipher.PubKey `json:"ratchet_pk"`

	IssuedAt  time.Time     `json:"issued_at"`
	IssuerPK  cipher.PubKey `json:"issuer_pk"`
	Signature cipher.Sig    `json:"signature"`
}

// ratchetCanonicalVersion prefixes the signed bytes so a future layout
// change cannot be confused with this one.
const ratchetCanonicalVersion byte = 1

// canonicalBytesRatchet returns the deterministic signed byte sequence
// for an announcement. Fixed-width fields throughout, so there is no
// concatenation ambiguity to guard against.
func canonicalBytesRatchet(a RatchetAnnounce) []byte {
	buf := make([]byte, 0, 1+8+33+8+33)
	buf = append(buf, ratchetCanonicalVersion)
	var gen [8]byte
	binary.BigEndian.PutUint64(gen[:], a.Generation)
	buf = append(buf, gen[:]...)
	buf = append(buf, a.RatchetPK[:]...)
	var ts [8]byte
	binary.BigEndian.PutUint64(ts[:], uint64(a.IssuedAt.UTC().UnixNano())) //nolint:gosec
	buf = append(buf, ts[:]...)
	buf = append(buf, a.IssuerPK[:]...)
	return buf
}

// signRatchet fills in IssuerPK + Signature using sec.
func signRatchet(a *RatchetAnnounce, sec cipher.SecKey) error {
	if a == nil || a.Generation == 0 || a.RatchetPK == (cipher.PubKey{}) || a.IssuedAt.IsZero() {
		return errors.New("pairing: ratchet: announcement is missing required fields")
	}
	pk, err := sec.PubKey()
	if err != nil {
		return fmt.Errorf("pairing: ratchet: derive issuer pubkey: %w", err)
	}
	a.IssuerPK = pk
	sig, err := cipher.SignPayload(canonicalBytesRatchet(*a), sec)
	if err != nil {
		return fmt.Errorf("pairing: ratchet: sign: %w", err)
	}
	a.Signature = sig
	return nil
}

// verifyRatchet checks the signature and that it was issued by expectPK
// — the peer whose feed we read. Without the issuer check a valid
// announcement signed by anyone at all would be accepted, and the
// signature would be authenticating nothing that matters.
func verifyRatchet(a RatchetAnnounce, expectPK cipher.PubKey) error {
	if a.Generation == 0 || a.RatchetPK == (cipher.PubKey{}) || a.IssuedAt.IsZero() ||
		a.IssuerPK == (cipher.PubKey{}) || a.Signature == (cipher.Sig{}) {
		return errors.New("pairing: ratchet: announcement is missing required fields")
	}
	if a.IssuerPK != expectPK {
		return fmt.Errorf("pairing: ratchet: announcement issued by %s, want %s", a.IssuerPK, expectPK)
	}
	if err := cipher.VerifyPubKeySignedPayload(a.IssuerPK, a.Signature, canonicalBytesRatchet(a)); err != nil {
		return fmt.Errorf("pairing: ratchet: bad signature: %w", err)
	}
	return nil
}

// marshalRatchet / unmarshalRatchet are the wire codec for the leaf value.
func marshalRatchet(a RatchetAnnounce) ([]byte, error) { return json.Marshal(a) }

func unmarshalRatchet(body []byte, expectPK cipher.PubKey) (RatchetAnnounce, error) {
	var a RatchetAnnounce
	if err := json.Unmarshal(body, &a); err != nil {
		return RatchetAnnounce{}, fmt.Errorf("pairing: ratchet: decode: %w", err)
	}
	if err := verifyRatchet(a, expectPK); err != nil {
		return RatchetAnnounce{}, err
	}
	return a, nil
}

// epochEntry is one derived epoch key plus the bookkeeping that decides
// when it may be dropped.
type epochEntry struct {
	ID      EpochID
	Key     pairKey
	AddedAt time.Time
}

// ratchetState is a pair's live ratchet: our current secret, the peer's
// latest announced public key, and the ring of epoch keys we can still
// open messages with.
//
// Guarded by its own mutex rather than the Pair's, because Send (which
// reads the current epoch) and onUpdate (which installs a new one) run
// concurrently by construction and neither should serialize on anything
// broader.
type ratchetState struct {
	mu sync.Mutex

	// myGen / myPK / mySK are our current ratchet keypair. mySK is the
	// value whose disposal is the forward-secrecy property: rotate()
	// overwrites it and the previous secret becomes unrecoverable.
	myGen uint64
	myPK  cipher.PubKey
	mySK  cipher.SecKey

	// peerGen / peerPK track the newest announcement we have accepted
	// from the peer. Zero until the first one arrives.
	peerGen uint64
	peerPK  cipher.PubKey

	// current names the epoch Send seals under, derived from (mySK,
	// peerPK). Zero when we have no peer announcement yet.
	current EpochID

	// ring holds every epoch key we can still open with, newest first,
	// capped at ratchetRingCap. Includes the current epoch.
	ring []epochEntry

	// sealed counts messages sent under the current epoch, for the
	// volume half of the rotation policy.
	sealed uint64

	// rotatedAt is when the current ratchet secret was minted.
	rotatedAt time.Time
}

// newRatchetState mints a first ratchet keypair. A pair always has a
// ratchet secret, even before the peer has announced one, so its own
// announcement can go out immediately at Open.
func newRatchetState(now time.Time) *ratchetState {
	pk, sk := cipher.GenerateKeyPair()
	return &ratchetState{myGen: 1, myPK: pk, mySK: sk, rotatedAt: now.UTC()}
}

// announce returns the announcement for our current generation.
func (r *ratchetState) announce(sec cipher.SecKey, now time.Time) (RatchetAnnounce, error) {
	r.mu.Lock()
	a := RatchetAnnounce{Generation: r.myGen, RatchetPK: r.myPK, IssuedAt: now.UTC()}
	r.mu.Unlock()
	if err := signRatchet(&a, sec); err != nil {
		return RatchetAnnounce{}, err
	}
	return a, nil
}

// observePeer records a peer announcement and returns true if it moved
// us onto a new epoch.
//
// Older generations are ignored rather than applied: announcements are
// durable leaves, so a resync replays every one the peer ever made, and
// walking backwards through them would leave us encrypting under a
// retired epoch. The epoch keys those old announcements form are still
// derived — that is what deriveMissing is for — they just do not become
// current.
func (r *ratchetState) observePeer(a RatchetAnnounce, now time.Time) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Derive the epoch this announcement forms with our CURRENT secret,
	// whether or not it is the newest: a message may already be sitting
	// in the feed under it.
	id, key, err := deriveEpochKey(r.mySK, r.myPK, a.RatchetPK)
	if err != nil {
		return false, err
	}
	r.installLocked(id, key, now)

	if a.Generation <= r.peerGen {
		return false, nil
	}
	r.peerGen = a.Generation
	r.peerPK = a.RatchetPK
	r.current = id
	r.sealed = 0
	return true, nil
}

// installLocked adds an epoch key to the ring, newest first, dropping
// the oldest past the cap. Idempotent on ID.
func (r *ratchetState) installLocked(id EpochID, key pairKey, now time.Time) {
	for _, e := range r.ring {
		if e.ID == id {
			return
		}
	}
	r.ring = append([]epochEntry{{ID: id, Key: key, AddedAt: now.UTC()}}, r.ring...)
	if len(r.ring) > ratchetRingCap {
		// Truncating drops the key, and dropping the key is the point:
		// past this horizon the feed's own ciphertext stops opening for
		// us as much as for anyone who later steals this disk.
		r.ring = r.ring[:ratchetRingCap]
	}
}

// currentEpoch returns the epoch to seal with, or false when the peer
// has not announced yet and the caller must use the legacy pair key.
func (r *ratchetState) currentEpoch() (EpochID, pairKey, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.current.IsZero() {
		return EpochID{}, nil, false
	}
	for _, e := range r.ring {
		if e.ID == r.current {
			return e.ID, e.Key, true
		}
	}
	return EpochID{}, nil, false
}

// keyFor returns the epoch key for id.
func (r *ratchetState) keyFor(id EpochID) (pairKey, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range r.ring {
		if e.ID == id {
			return e.Key, true
		}
	}
	return nil, false
}

// generation returns our current ratchet generation.
func (r *ratchetState) generation() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.myGen
}

// noteSent bumps the volume counter for the current epoch.
func (r *ratchetState) noteSent() {
	r.mu.Lock()
	r.sealed++
	r.mu.Unlock()
}

// rotationDue reports whether our ratchet secret has covered enough to
// be replaced. Never true before the peer has announced: rotating then
// would burn generations against a peer that cannot form an epoch with
// any of them, and the pair is still on the legacy key anyway.
func (r *ratchetState) rotationDue(now time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.current.IsZero() {
		return false
	}
	if r.sealed >= ratchetMaxMessages {
		return true
	}
	return now.UTC().Sub(r.rotatedAt) >= ratchetMaxAge
}

// rotate mints a fresh ratchet keypair, DESTROYS the previous secret,
// and re-derives the current epoch against the peer's latest announced
// key. Returns the new generation.
//
// The overwrite of r.mySK is the entire feature: once it is gone, every
// epoch formed with it can no longer be derived from any combination of
// long-term identity keys, so those messages stay closed even to someone
// who later obtains this visor's identity secret.
func (r *ratchetState) rotate(now time.Time) uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()

	pk, sk := cipher.GenerateKeyPair()
	r.myGen++
	r.myPK, r.mySK = pk, sk
	r.rotatedAt = now.UTC()
	r.sealed = 0

	if r.peerPK != (cipher.PubKey{}) {
		if id, key, err := deriveEpochKey(r.mySK, r.myPK, r.peerPK); err == nil {
			r.installLocked(id, key, now)
			r.current = id
		}
	}
	return r.myGen
}

// snapshot returns the persistable form of this ratchet. Called on every
// change so a restart resumes on the same epoch rather than minting a
// fresh generation and stranding the peer on a key we no longer hold.
func (r *ratchetState) snapshot() RatchetState {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := RatchetState{
		Generation:     r.myGen,
		RatchetPK:      r.myPK,
		RatchetSK:      r.mySK,
		PeerGeneration: r.peerGen,
		PeerRatchetPK:  r.peerPK,
		Current:        r.current,
		RotatedAt:      r.rotatedAt,
		Sealed:         r.sealed,
		Ring:           make([]PersistedEpoch, 0, len(r.ring)),
	}
	for _, e := range r.ring {
		out.Ring = append(out.Ring, PersistedEpoch{ID: e.ID, Key: append(pairKey(nil), e.Key...), AddedAt: e.AddedAt})
	}
	return out
}

// restoreRatchetState rebuilds live state from its persisted form.
// A snapshot without a usable secret is discarded and a fresh ratchet
// minted — better one lost epoch than a pair that cannot seal at all.
func restoreRatchetState(st RatchetState, now time.Time) *ratchetState {
	if st.Generation == 0 || st.RatchetPK == (cipher.PubKey{}) || st.RatchetSK == (cipher.SecKey{}) {
		return newRatchetState(now)
	}
	r := &ratchetState{
		myGen:     st.Generation,
		myPK:      st.RatchetPK,
		mySK:      st.RatchetSK,
		peerGen:   st.PeerGeneration,
		peerPK:    st.PeerRatchetPK,
		current:   st.Current,
		rotatedAt: st.RotatedAt,
		sealed:    st.Sealed,
		ring:      make([]epochEntry, 0, len(st.Ring)),
	}
	for _, e := range st.Ring {
		if len(e.Key) != 32 {
			continue
		}
		r.ring = append(r.ring, epochEntry{ID: e.ID, Key: append(pairKey(nil), e.Key...), AddedAt: e.AddedAt})
	}
	if r.rotatedAt.IsZero() {
		r.rotatedAt = now.UTC()
	}
	return r
}
