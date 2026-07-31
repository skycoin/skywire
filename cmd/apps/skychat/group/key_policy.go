// Package group cmd/apps/skychat/group/key_policy.go c4-app-chat
// when a group key has covered enough and should be replaced.
//
// # Why a group needs this at all
//
// Rotation already existed, but every trigger was an eviction: kick, ban,
// or an admin clicking the button. A group that nobody is ever removed
// from therefore ran its ENTIRE life — years of messages — under the one
// key minted at create time. Every property the epoch machinery looks
// like it provides collapsed into that: one key compromised is one group
// compromised, start to finish, and "which epoch" was a field that never
// changed.
//
// So epochs advance on their own here, on whichever comes first:
//
//   - AGE. The wall-clock window a single key may cover. This is the one
//     that actually bounds "how much of my history does this key open" for
//     a quiet group, where the message count would take years to trip.
//   - VOLUME. Messages published under the current key. Bounds a busy
//     group, where a week is a lot of traffic, and bounds AEAD nonce reuse
//     risk far below the birthday bound for random 96-bit nonces.
//
// # Who rotates
//
// Only an admin can mint a key (applyKeyLeaf refuses non-admin issuers),
// so only admins evaluate this. In a group with several admins they all
// see the same due epoch at roughly the same moment, and a naive
// implementation has every one of them rotate at once — N rotations, N-1
// of them immediately superseded, and each one a leaf every member has to
// fetch and try to unwrap.
//
// installKey already resolves that race correctly (higher epoch wins;
// same epoch breaks the tie on IssuedAt), so the duplicates are safe, just
// wasteful. rotationJitter spreads them: each admin waits an extra
// deterministic slice of the rotation window derived from its own PK, so
// in practice one admin reaches the threshold first, publishes, and the
// rest see a fresh KeyIssuedAt and stand down before their own timer
// fires. No coordination, no leader election, no shared clock beyond the
// IssuedAt already in the envelope.
package group

import (
	"crypto/sha256"
	"encoding/binary"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
)

const (
	// DefaultKeyMaxAge is how long one group key may stay current before
	// an admin replaces it.
	//
	// Seven days is a deliberate middle: short enough that a stolen key
	// opens a week of history rather than a lifetime, long enough that a
	// member who is offline for a weekend comes back inside the bounded
	// KeyRing (maxKeyRing = 32 epochs ≈ 7 months at this cadence) and can
	// still read what it missed. Dropping it to hours would push a normal
	// vacation past the ring and lose history for no attacker-facing gain.
	DefaultKeyMaxAge = 7 * 24 * time.Hour

	// DefaultKeyMaxMessages is how many messages one group key may seal
	// before an admin replaces it.
	//
	// 4096 is a traffic bound, not a cryptographic one: AES-GCM with
	// random 96-bit nonces only becomes interesting around 2^32 messages,
	// so this is far below any nonce-collision concern and is chosen for
	// the blast radius instead — one key opens at most a few thousand
	// messages of a busy group.
	DefaultKeyMaxMessages = 4096

	// rotationJitterWindow is the spread applied to the age threshold,
	// as a fraction of DefaultKeyMaxAge, so co-admins don't all rotate
	// on the same tick. See the package comment.
	rotationJitterWindow = DefaultKeyMaxAge / 4
)

// RotationReason names why a key came due, for logs and for the
// operator-facing status the UI shows. Empty means not due.
type RotationReason string

const (
	// RotationReasonAge means the current key has been current longer
	// than DefaultKeyMaxAge (plus this visor's jitter).
	RotationReasonAge RotationReason = "age"

	// RotationReasonVolume means the current key has sealed more than
	// DefaultKeyMaxMessages messages.
	RotationReasonVolume RotationReason = "volume"
)

// rotationJitter is this visor's deterministic offset into the rotation
// window, in [0, rotationJitterWindow).
//
// Derived from the PK rather than randomised so it is stable across
// restarts: a visor that re-rolled its jitter every boot would keep
// leapfrogging its co-admins and could rotate twice in a window it should
// have sat out.
func rotationJitter(pk cipher.PubKey) time.Duration {
	if rotationJitterWindow <= 0 {
		return 0
	}
	sum := sha256.Sum256(pk[:])
	n := binary.BigEndian.Uint64(sum[:8])
	return time.Duration(n % uint64(rotationJitterWindow)) //nolint:gosec // modulo of a positive constant
}

// keyRotationDue reports whether the key described by (issuedAt, sealed)
// has covered enough to be replaced by the admin identified by myPK.
//
// A zero issuedAt means the record predates KeyIssuedAt being stamped
// (its key is at least as old as the field's introduction), so it is
// treated as due on age: the whole point is that an unbounded-age key
// should not stay current.
func keyRotationDue(myPK cipher.PubKey, issuedAt time.Time, sealed uint64, now time.Time) (RotationReason, bool) {
	if sealed >= DefaultKeyMaxMessages {
		return RotationReasonVolume, true
	}
	if issuedAt.IsZero() {
		return RotationReasonAge, true
	}
	if now.Sub(issuedAt.UTC()) >= DefaultKeyMaxAge+rotationJitter(myPK) {
		return RotationReasonAge, true
	}
	return "", false
}

// MessagesSealed returns how many messages this session has published
// under its current key — the volume half of the rotation policy, and
// the number to reach for when diagnosing why a group did or didn't
// re-key when expected.
func (s *Session) MessagesSealed() uint64 { return s.msgsSealed.Load() }

// KeyRotationDue reports whether this session's key has covered enough
// to be replaced, and why. Always false for a plaintext group (no key)
// and for a session whose visor is not an admin (it could mint a key,
// but every other member would reject the leaf).
func (s *Session) KeyRotationDue(now time.Time) (RotationReason, bool) {
	s.membersMu.RLock()
	encrypted := s.cfg.Record.Encrypted()
	admin := s.cfg.Record.IsAdmin(s.cfg.MyPK)
	issuedAt := s.cfg.Record.KeyIssuedAt
	myPK := s.cfg.MyPK
	s.membersMu.RUnlock()
	if !encrypted || !admin {
		return "", false
	}
	return keyRotationDue(myPK, issuedAt, s.msgsSealed.Load(), now)
}

// rotateDueKeys walks every live session and re-keys the ones whose
// current key has aged or volumed out. Driven by the reconnect loop,
// which already ticks and already owns the manager's "keep the group
// healthy in the background" jobs.
//
// Best-effort throughout, exactly like rotateAfterEviction: a rotation
// that cannot be published (publisher mid-bootstrap, no live session)
// is logged and retried on the next tick. Nothing here may fail a
// caller — there is no caller, and a group that cannot rotate right now
// is still a working group on its current key.
func (m *Manager) rotateDueKeys(now time.Time) {
	records, err := m.store.List()
	if err != nil {
		return
	}
	for _, r := range records {
		if r.Status != StatusActive || !r.Encrypted() || !r.IsAdmin(m.myPK) {
			continue
		}
		m.mu.RLock()
		sess, live := m.sessions[r.ID]
		m.mu.RUnlock()
		if !live {
			continue
		}
		reason, due := sess.KeyRotationDue(now)
		if !due {
			continue
		}
		// Recipients come from the PERSISTED roster for the reason
		// RotateKeyFor documents at length: the session's own
		// cfg.Record.Members is a create-time snapshot.
		epoch, rotErr := sess.RotateKeyFor(m.keyRecipients(r))
		if rotErr != nil {
			m.log.WithError(rotErr).WithField("id", r.ID).WithField("reason", string(reason)).
				Debug("group: scheduled key rotation failed; will retry on the next tick")
			continue
		}
		m.log.WithField("id", r.ID).WithField("epoch", epoch).WithField("reason", string(reason)).
			Info("group: rotated the group key on schedule")
	}
}
