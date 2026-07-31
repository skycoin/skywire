// Package group cmd/apps/skychat/group/gossip_seal.go c4-app-chat
// sealing the governance leaves — roster/, admin/ and mod/ — under the
// group key, so an encrypted group's membership and moderation history is
// as private as the messages it protects.
//
// # What was readable
//
// Message bodies in a private group have been encrypted from the start.
// The leaves that say who is IN the group, who holds authority, and who
// was banned or muted were not. They are signed, so nobody could forge
// one — but the bytes sitting on the feed were plain JSON:
//
//	{"group_id":"…","op":1,"peer_pk":"02ab…","issued_at":"…"}
//
// Anyone who could pull the feed read the group's entire moderation
// history in the clear: every eviction, every mute, every promotion, and
// the PK each one named. That set includes an evicted member who kept its
// copy of the tree, any peer serving backfill, and anyone who got hold of
// a member's CXO database. "Alice was banned on Tuesday" is exactly the
// kind of fact about Alice that encrypting the message bodies was
// carefully hiding, and it was one grep away.
//
// # The seal
//
// A governance leaf is now sealed under the same AES-256-GCM group key as
// message bodies, opened with the same trial-decryption over the key ring
// (keyring.go), and published as:
//
//	"SKG1" | 12-byte nonce | ciphertext+tag
//
// The magic prefix is what makes this compatible in the direction that
// matters: a leaf that does NOT start with it is a plaintext envelope
// from an older publisher and is applied exactly as before, so a group
// that has been running for months keeps converging on the leaves already
// in its feeds. The reverse does not hold — an old binary reading a
// sealed leaf sees bytes that are not JSON and drops them, so governance
// stops flowing toward un-upgraded members until they upgrade. That is
// the same one-way compatibility window the domain-tag fix in gossip.go
// took deliberately, for the same reason: it heals on upgrade, and the
// alternative is keeping the plaintext forever.
//
// The signature stays INSIDE the seal, over the same canonical bytes as
// before. Sealing is confidentiality only; authority is still the
// signature plus the current-admin gate in the reconcilers, and neither
// property depends on the other.
//
// # What a sealed leaf still shows
//
// The path prefix (roster/ vs mod/), the byte length, and the timing. An
// observer learns that some admin issued some moderation decision at some
// moment — not which member it named, nor what it did. Hiding the fact of
// governance as well would mean padding every family to one size and
// publishing decoy leaves; that is a different and much more expensive
// design, and it is not what this file does.
//
// # Only encrypted groups
//
// A public group has no key: admission is open, so the key would go to
// any stranger who asked and sealing with it would protect nothing (see
// the Kind/Mode reasoning in group.go). Public governance stays plaintext
// and the publish path leaves it alone.
//
// # Why a joiner still converges
//
// A member holds every key it has ever been given (Record.KeyRing), so
// sealing costs it nothing: leaves from before the last rotation open on
// the second or third key it tries. A JOINER is the interesting case,
// because the admission response hands it the current key only — by
// design, so admission does not retroactively grant the group's history.
// Governance leaves published under retired epochs are therefore opaque
// to it, and this does not break convergence for two independent reasons:
//
//  1. The admission response already carries the full governance
//     snapshot — members, admins, mutes, read-only, backfill (see
//     recordFromInvite) — so the joiner starts converged rather than
//     replaying its way there.
//  2. Everything issued from that moment on is sealed under a key it
//     holds, and an admin re-asserts current state under the current key
//     whenever its session opens (BroadcastRoster / BroadcastModeration).
//
// The property that changes is one that was never load-bearing: a joiner
// can no longer reconstruct the group's moderation history from before it
// arrived. That is the feature, not a regression.
//
// # The parking lot
//
// A sealed leaf can arrive before the key that opens it. The rotation
// lands on the rotating admin's feed while the roster leaf comes from
// another admin's, each through its own subscriber, so batch order says
// nothing about which we see first. Subscriber callbacks fire once per
// leaf — a leaf we drop because we cannot read it yet is dropped
// PERMANENTLY, not late. So an unopenable governance leaf is parked here
// and replayed the moment a key install gives us something new to try,
// the same lesson the DM ratchet learned in pairing/ratchet.go.
package group

import (
	"bytes"
	"errors"
	"fmt"
)

// gossipSealMagic marks a sealed governance leaf. Four bytes, ending in a
// format digit: a future layout takes SKG2 and old binaries fast-reject it
// instead of trying to decrypt a shape they don't know.
var gossipSealMagic = []byte("SKG1")

// gossipSealNonceLen is the AES-GCM nonce width, fixed by crypto.go's
// Encrypt. Kept as a constant here so the framing math reads without
// having to construct a GCM to ask.
const gossipSealNonceLen = 12

// deferredGossipCap bounds the parking lot of governance leaves we cannot
// open yet.
//
// A bound is needed because the lot is attacker-influenced: anyone whose
// feed we subscribe to can publish garbage that starts with the magic and
// never opens. 256 is far more than the handful of leaves a real key race
// produces (one rotation's worth of concurrent governance) while keeping
// the memory cost trivial. Overflow drops the OLDEST entry: the newest
// leaf is the one most likely to still matter, and an old one that never
// opened is one we were never going to apply.
const deferredGossipCap = 256

// ErrGossipSealedUnreadable is returned when a leaf is sealed and no key
// this visor holds opens it. Distinct from a decode failure because the
// caller's response is different: a leaf we cannot read YET is parked for
// replay, not dropped.
var ErrGossipSealedUnreadable = errors.New("group: sealed governance leaf: no key opens it")

// errGossipNoSealKey is returned when an encrypted group asks us to
// publish governance and we hold no key to seal it with — a joiner whose
// admission response is still in flight. Publishing plaintext instead
// would silently undo the whole point of this file, so the publish fails.
var errGossipNoSealKey = errors.New("group: cannot seal governance leaf: no group key held")

// isSealedGossip reports whether body carries the sealed framing rather
// than a plaintext JSON envelope.
func isSealedGossip(body []byte) bool {
	return len(body) > len(gossipSealMagic) && bytes.HasPrefix(body, gossipSealMagic)
}

// sealGossip wraps a marshaled governance envelope for the feed.
func sealGossip(key, plaintext []byte) ([]byte, error) {
	ciphertext, nonce, err := Encrypt(key, plaintext)
	if err != nil {
		return nil, fmt.Errorf("group: seal governance leaf: %w", err)
	}
	if len(nonce) != gossipSealNonceLen {
		return nil, fmt.Errorf("group: seal governance leaf: nonce is %d bytes, want %d", len(nonce), gossipSealNonceLen)
	}
	out := make([]byte, 0, len(gossipSealMagic)+len(nonce)+len(ciphertext))
	out = append(out, gossipSealMagic...)
	out = append(out, nonce...)
	out = append(out, ciphertext...)
	return out, nil
}

// openGossip returns the plaintext envelope inside body, trying each key
// in turn. An unsealed body is returned unchanged — that is the legacy
// path, not an error.
func openGossip(keys [][]byte, body []byte) ([]byte, error) {
	if !isSealedGossip(body) {
		return body, nil
	}
	framed := body[len(gossipSealMagic):]
	if len(framed) <= gossipSealNonceLen {
		return nil, fmt.Errorf("group: sealed governance leaf: %d bytes after the magic, want more than %d",
			len(framed), gossipSealNonceLen)
	}
	nonce := framed[:gossipSealNonceLen]
	ciphertext := framed[gossipSealNonceLen:]
	pt, err := decryptWithRing(keys, ciphertext, nonce)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrGossipSealedUnreadable, err)
	}
	return pt, nil
}

// sealGossipBody prepares a marshaled governance envelope for publishing:
// sealed for an encrypted group, untouched for a plaintext one.
//
// Fails closed. An encrypted group with no key in hand gets an error
// rather than a plaintext leaf, because the caller cannot tell the
// difference from the return value and the leaf would sit on the feed
// readable forever.
func (s *Session) sealGossipBody(body []byte) ([]byte, error) {
	s.membersMu.RLock()
	encrypted := s.cfg.Record.Encrypted()
	key := append([]byte(nil), s.cfg.Record.AESKey...)
	s.membersMu.RUnlock()
	if !encrypted {
		return body, nil
	}
	if len(key) != 32 {
		return nil, errGossipNoSealKey
	}
	return sealGossip(key, body)
}

// deferredGossipLeaf is one parked leaf plus the family it belongs to, so
// the drain can route it back to the right reconciler.
type deferredGossipLeaf struct {
	family string
	body   []byte
}

// openOrPark is the receive-side counterpart of sealGossipBody: it returns
// the plaintext envelope for a governance leaf, or false when the leaf
// cannot be read.
//
// "Cannot be read" splits two ways and the difference is the whole reason
// this helper exists. A leaf sealed under a key we do not hold YET is
// parked and retried on the next key install. Anything else — a truncated
// frame, a leaf sealed to a different group's key that will never arrive —
// is dropped.
func (s *Session) openOrPark(family string, body []byte) ([]byte, bool) {
	if !isSealedGossip(body) {
		return body, true
	}
	pt, err := openGossip(s.decryptionKeys(), body)
	if err == nil {
		return pt, true
	}
	if errors.Is(err, ErrGossipSealedUnreadable) {
		s.parkGossipLeaf(family, body)
		s.log.WithField("family", family).
			Debug("group: reconcile: parking a sealed governance leaf until a key arrives that opens it")
		return nil, false
	}
	s.log.WithError(err).WithField("family", family).
		Debug("group: reconcile: dropping a malformed sealed governance leaf")
	return nil, false
}

// parkGossipLeaf files a leaf for replay after the next key install.
func (s *Session) parkGossipLeaf(family string, body []byte) {
	s.deferredGossipMu.Lock()
	defer s.deferredGossipMu.Unlock()
	// Copy: the body belongs to the subscriber's event batch, which the
	// caller is free to reuse once the callback returns.
	s.deferredGossip = append(s.deferredGossip, deferredGossipLeaf{
		family: family,
		body:   append([]byte(nil), body...),
	})
	if len(s.deferredGossip) > deferredGossipCap {
		s.deferredGossip = s.deferredGossip[len(s.deferredGossip)-deferredGossipCap:]
	}
}

// drainDeferredGossip replays every parked leaf through its reconciler.
// Called after a key install, when there is finally something new to try.
//
// Must NOT be called with membersMu held — every reconciler takes it.
//
// A leaf that still cannot be opened re-parks itself, which is correct and
// terminating: the list is taken by swap, so a replay round can only ever
// re-add what it just removed, never loop on it.
func (s *Session) drainDeferredGossip() {
	s.deferredGossipMu.Lock()
	parked := s.deferredGossip
	s.deferredGossip = nil
	s.deferredGossipMu.Unlock()
	if len(parked) == 0 {
		return
	}
	s.log.WithField("count", len(parked)).
		Debug("group: reconcile: replaying governance leaves parked before the key arrived")
	for _, d := range parked {
		switch d.family {
		case familyRoster:
			s.applyRosterLeaf(d.body)
		case familyAdmin:
			s.applyAdminLeaf(d.body)
		case familyMod:
			s.applyModLeaf(d.body)
		}
	}
}
