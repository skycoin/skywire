// Package group cmd/apps/skychat/group/replay_guard.go c4-app-chat
// freshness gating for signed gossip, so a superseded mutation cannot
// be replayed to resurrect state an admin already undid.
//
// # The hole this closes
//
// Every roster / admin / moderation mutation is signed, and the
// reconcilers checked two things: the signature verifies, and the
// issuer is a current admin. Both hold forever for a leaf that was
// legitimate when issued — signatures don't expire and admins usually
// stay admins. Nothing checked whether the mutation was still CURRENT.
//
// Signed leaves are durable and readable by every member, so any member
// could copy an admin's old `RosterOpAdd(X)` leaf verbatim onto their
// own feed at a fresh path. Other members decoded it, verified the
// signature (valid), checked the issuer (a real admin), and applied
// it — silently re-adding X after X had been removed. The same trick
// re-promotes a demoted admin (`AdminOpPromote`) or lifts a standing
// ban (`ModOpUnban`).
//
// Kick was the visible casualty: an eviction could be undone by anyone
// who had been present when the member was first added, with no forged
// signature and no admin key.
//
// # Why not ParentSeq
//
// The envelopes carry a ParentSeq field, signed and intended by the RFC
// as a causal chain. It was never populated with anything but zero and
// no reconciler ever read it. Building real causal ordering means a
// per-feed DAG with merge rules — a large change, and more than this
// needs. Last-writer-wins on the mutation's own signed timestamp gives
// the property that actually matters (superseded state cannot come
// back) at a fraction of the cost. ParentSeq stays in the signed bytes
// because removing a field would change every signature; it is simply
// not the mechanism.
//
// # The rule
//
// Per (family, target) keep the IssuedAt of the newest mutation applied
// so far. A mutation applies only if it is strictly newer. Replays are
// older, so they lose; genuine updates are newer, so they win.
//
// Two consequences worth stating plainly:
//
//   - Admin clocks must be roughly in agreement. Two admins acting on
//     the SAME target within each other's skew can land out of order,
//     and the later-timestamped one wins regardless of which happened
//     first. This is the normal cost of last-writer-wins and is
//     acceptable here: concurrent contradictory moderation of one PK by
//     two admins is a coordination problem, not a security boundary.
//
//   - A future-dated mutation would pin the watermark and lock the
//     target out of all further updates — a denial of service on the
//     roster requiring only one leaf. maxMutationSkew bounds it.
package group

import (
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
)

// Mutation families. Single bytes because they only ever appear as a
// key prefix, and the key is persisted — a compact prefix keeps the
// on-disk map small for a large roster.
const (
	familyRoster = "r"
	familyAdmin  = "a"
	familyMod    = "m"
	// familyKey covers key rotations. Group-scoped — a rotation addresses
	// the whole group rather than one peer — so its watermark always
	// lands on groupScopeKey and orders every rotation against every
	// other. That is what a replayed rotation must not be able to escape:
	// re-installing a superseded key would put the group back on a key an
	// evicted member still holds.
	familyKey = "k"
)

// groupScopeKey is the watermark target for mutations that address the
// group as a whole rather than a member (the read-only toggles). A
// literal that cannot collide with a PK hex string.
const groupScopeKey = "group"

// maxMutationSkew is how far into the future a mutation's IssuedAt may
// be before it is refused outright.
//
// Without this bound, a single leaf dated years ahead would raise the
// watermark past anything a legitimate admin could ever issue, freezing
// that target's roster/admin/moderation state permanently. Five minutes
// is generous for real clock drift between visors while keeping the
// damage from a hostile timestamp to five minutes of lockout.
const maxMutationSkew = 5 * time.Minute

// watermarkKey builds the per-(family, target) map key. Peer-scoped
// mutations key on the target's hex; group-scoped ones share
// groupScopeKey.
func watermarkKey(family string, pk cipher.PubKey) string {
	if pk == (cipher.PubKey{}) {
		return family + ":" + groupScopeKey
	}
	return family + ":" + pk.Hex()
}

// mutationFresh reports whether a mutation issued at issuedAt should be
// applied, given the watermarks seen so far, and returns the reason when
// it should not.
//
// now is passed in rather than read from the clock so the skew check is
// testable without sleeping.
//
// The zero IssuedAt is refused: every signer sets it (SignRoster and
// friends reject a zero value), so an unset timestamp here means a
// hand-built envelope trying to slip under every watermark at once.
func mutationFresh(seen map[string]time.Time, key string, issuedAt, now time.Time) (bool, string) {
	if issuedAt.IsZero() {
		return false, "mutation has no issue time"
	}
	if issuedAt.After(now.Add(maxMutationSkew)) {
		return false, "mutation is dated too far in the future"
	}
	last, ok := seen[key]
	if !ok {
		return true, ""
	}
	// Strictly newer. Equal timestamps mean the same leaf arriving
	// again (a re-broadcast, or the same mutation observed through two
	// feeds), which is exactly what should be dropped.
	if !issuedAt.After(last) {
		return false, "mutation is older than the last one applied for this target"
	}
	return true, ""
}

// recordMutation advances the watermark for key. Monotonic by
// construction: an older timestamp is ignored rather than written, so
// no caller can walk a watermark backwards and re-open a replay window
// — including the re-assertion path, which deliberately publishes with
// historical timestamps.
func recordMutation(seen map[string]time.Time, key string, issuedAt time.Time) map[string]time.Time {
	if seen == nil {
		seen = make(map[string]time.Time, 1)
	}
	if cur, ok := seen[key]; ok && !issuedAt.After(cur) {
		return seen
	}
	seen[key] = issuedAt.UTC()
	return seen
}

// assertionTime returns the timestamp a re-assertion of existing state
// should carry for a target: the moment we actually learned it, not now.
//
// This is what keeps BroadcastRoster honest. That function re-publishes
// "here is my current roster" on every session open, and if it stamped
// those leaves with the current time, a restarting admin would out-rank
// every other admin's genuine decisions — re-adding a member somebody
// else evicted a minute ago, and (once watermarks exist) pinning its own
// watermark high enough to reject that eviction when it finally arrives.
// A re-assertion is an echo of an old decision and must be dated like
// one.
//
// Falls back to the group's creation time for targets with no recorded
// mutation, which is when create-time members genuinely joined. Caller
// holds membersMu.
func (s *Session) assertionTimeLocked(family string, pk cipher.PubKey) time.Time {
	if at, ok := s.mutationSeen[watermarkKey(family, pk)]; ok && !at.IsZero() {
		return at
	}
	if !s.cfg.Record.CreatedAt.IsZero() {
		return s.cfg.Record.CreatedAt.UTC()
	}
	return time.Now().UTC()
}

// mergeWatermarks folds incoming into base, keeping the LATER timestamp
// for every key.
//
// Merging rather than replacing matters because the watermark is written
// from more than one path (each reconciler persists its own snapshot)
// and those snapshots interleave. A plain overwrite would let a
// slightly-stale snapshot move a watermark backwards, which is exactly
// the state a replay needs to succeed.
func mergeWatermarks(base, incoming map[string]time.Time) map[string]time.Time {
	if len(incoming) == 0 {
		return copyWatermarks(base)
	}
	out := copyWatermarks(base)
	if out == nil {
		out = make(map[string]time.Time, len(incoming))
	}
	for k, v := range incoming {
		if cur, ok := out[k]; !ok || v.After(cur) {
			out[k] = v.UTC()
		}
	}
	return out
}

// copyWatermarks returns an independent copy, or nil for an empty input
// so an untouched Record keeps marshalling without the field.
func copyWatermarks(in map[string]time.Time) map[string]time.Time {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]time.Time, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// SetMutationSeenHandler installs the callback that persists advanced
// watermarks. nil is fine — the guard still works for the life of the
// process, but a restart would forget the watermarks and re-open the
// replay window, so the Manager always installs one.
func (s *Session) SetMutationSeenHandler(f func(map[string]time.Time)) {
	s.membersMu.Lock()
	s.onMutationSeen = f
	s.membersMu.Unlock()
}

// noteLocalMutation advances the watermark for a mutation THIS visor
// just issued, and persists it.
//
// Without this the issuer is the one visor still vulnerable to its own
// leaf: a mutation we publish never comes back through applyRosterLeaf
// (we don't subscribe to ourselves), so our watermark for that target
// would stay unset. An attacker replaying our old `RosterOpAdd(X)` back
// at us would sail through the freshness gate on the very node that
// issued the eviction.
//
// Also the reason the guard converges regardless of arrival order:
// every participant, issuer included, ends up holding the newest
// timestamp per target, so a leaf that arrives late loses to the newer
// state whichever way round they turn up.
func (s *Session) noteLocalMutation(family string, peerPK cipher.PubKey, issuedAt time.Time) {
	key := watermarkKey(family, peerPK)
	s.membersMu.Lock()
	s.mutationSeen = recordMutation(s.mutationSeen, key, issuedAt)
	s.cfg.Record.MutationSeen = copyWatermarks(s.mutationSeen)
	snapshot := copyWatermarks(s.mutationSeen)
	s.membersMu.Unlock()
	s.persistWatermarks(snapshot)
}

// MutationSeen returns a copy of the live watermark set.
func (s *Session) MutationSeen() map[string]time.Time {
	s.membersMu.RLock()
	defer s.membersMu.RUnlock()
	return copyWatermarks(s.mutationSeen)
}

// persistWatermarks hands a snapshot to the store. Called WITHOUT
// membersMu held — the handler reaches into the store, and holding the
// session lock across that would put store I/O on the critical path of
// every inbound leaf.
func (s *Session) persistWatermarks(seen map[string]time.Time) {
	s.membersMu.RLock()
	f := s.onMutationSeen
	s.membersMu.RUnlock()
	if f != nil {
		f(seen)
	}
}

// Retention: watermarks are NEVER pruned, and that is deliberate.
//
// The obvious optimization — drop entries for PKs no longer in the
// roster — removes precisely the entries the guard depends on. A member
// who has been kicked is not a member, not an admin, not banned and not
// muted, so any "is this target still relevant" rule discards their
// watermark and the next replayed `RosterOpAdd` for them succeeds. The
// entries with no corresponding roster state are the load-bearing ones.
//
// So the map holds one entry per (family, target) ever acted on. For a
// chat group that is bounded by real membership churn — small. It is
// NOT bounded against an adversary minting fresh PKs to join an open
// group in a loop, since each join writes a new entry. That is the same
// Sybil exposure that already lets such an adversary flood the room
// itself, and it belongs to the join rate-limiting work rather than
// here; capping the map would trade a storage bound for a correctness
// hole, which is the wrong direction.
