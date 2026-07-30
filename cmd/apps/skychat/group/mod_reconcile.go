// Package group cmd/apps/skychat/group/mod_reconcile.go c4-app-chat
// the emit + apply + enforce sides of moderation state.
//
// Mirrors roster_reconcile.go: PublishModMutation writes a signed
// mod/<seq> leaf onto the issuer's feed, applyModLeaf consumes such
// leaves from every subscribed feed, and the same authority gate
// applies — a mutation counts only if it is signature-valid AND issued
// by a CURRENT admin. Without that gate any member could forge a ban.
//
// The enforcement predicates (senderAllowedToPost, CanPostLocally)
// live here too, so the "what does muted actually do" answer is in one
// file rather than spread across the inbox and send paths.
package group

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
)

// SetModChangeHandler installs a callback invoked with a snapshot of the
// converged moderation state whenever the reconciler mutates it. The
// manager uses it to persist the updated Record. nil is fine — in-memory
// state still converges, only durability is skipped.
func (s *Session) SetModChangeHandler(f func(ModState)) {
	s.onModChange = f
}

// SetJoinRequestHandler installs the admission decision callback. See
// the onJoinRequest field comment: the session authenticates the
// requester, the handler decides.
func (s *Session) SetJoinRequestHandler(h JoinRequestHandler) {
	s.membersMu.Lock()
	s.onJoinRequest = h
	s.membersMu.Unlock()
}

// ModState is a transferable snapshot of a group's moderation state.
// Bundled rather than passed as four loose arguments because every
// producer and consumer needs all of it, and the two "since" halves are
// meaningless apart from the flags they qualify.
type ModState struct {
	Banned        []cipher.PubKey
	Muted         []cipher.PubKey
	MutedSince    map[string]time.Time
	ReadOnly      bool
	ReadOnlySince time.Time
	// PeerBackfillDisabled mirrors Record.PeerBackfillDisabled — a
	// group-wide admin toggle that converges through this same envelope.
	PeerBackfillDisabled bool
}

// modStateOf builds a snapshot from a Record.
func modStateOf(r Record) ModState {
	return ModState{
		Banned:               append([]cipher.PubKey(nil), r.Banned...),
		Muted:                append([]cipher.PubKey(nil), r.Muted...),
		MutedSince:           copyMuteTimes(r.MutedSince),
		ReadOnly:             r.ReadOnly,
		ReadOnlySince:        r.ReadOnlySince,
		PeerBackfillDisabled: r.PeerBackfillDisabled,
	}
}

// applyTo writes the snapshot onto a Record.
func (m ModState) applyTo(r *Record) {
	r.Banned = append([]cipher.PubKey(nil), m.Banned...)
	r.Muted = append([]cipher.PubKey(nil), m.Muted...)
	r.MutedSince = copyMuteTimes(m.MutedSince)
	r.ReadOnly = m.ReadOnly
	r.ReadOnlySince = m.ReadOnlySince
	r.PeerBackfillDisabled = m.PeerBackfillDisabled
}

func copyMuteTimes(in map[string]time.Time) map[string]time.Time {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]time.Time, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// Moderation returns a snapshot of this session's live moderation
// state. Used by the manager to answer info queries without reaching
// into session internals.
func (s *Session) Moderation() ModState {
	s.membersMu.RLock()
	defer s.membersMu.RUnlock()
	return s.moderationLocked()
}

func (s *Session) moderationLocked() ModState {
	return ModState{
		Banned:        append([]cipher.PubKey(nil), s.banned...),
		Muted:         append([]cipher.PubKey(nil), s.muted...),
		MutedSince:    copyMuteTimes(s.mutedSince),
		ReadOnly:      s.readOnly,
		ReadOnlySince: s.readOnlySince,
		// Unlike the lists above there is no mirrored session field for
		// this one — the record IS the live state. It still has to be
		// carried here, because applyModLeaf round-trips the snapshot back
		// onto the record and a field missing from the snapshot gets
		// zeroed: the toggle would appear to apply and then immediately
		// revert.
		PeerBackfillDisabled: s.cfg.Record.PeerBackfillDisabled,
	}
}

// SetModeration replaces the live moderation state (the local-command
// path, parallel to SetAllowlist for the roster). Returns the PKs newly
// banned by this call so the caller can tear down their subscriptions.
//
// Owner-side the caller follows up with SetAllowlist over the post-ban
// member list: a banned PK must lose its CXO seat, which is what makes
// a ban structural rather than merely cooperative.
func (s *Session) SetModeration(st ModState) []cipher.PubKey {
	s.membersMu.Lock()
	prev := make(map[cipher.PubKey]bool, len(s.banned))
	for _, pk := range s.banned {
		prev[pk] = true
	}
	var newlyBanned []cipher.PubKey
	for _, pk := range st.Banned {
		if !prev[pk] {
			newlyBanned = append(newlyBanned, pk)
		}
	}
	s.banned = append([]cipher.PubKey(nil), st.Banned...)
	s.muted = append([]cipher.PubKey(nil), st.Muted...)
	s.mutedSince = copyMuteTimes(st.MutedSince)
	s.readOnly = st.ReadOnly
	s.readOnlySince = st.ReadOnlySince
	st.applyTo(&s.cfg.Record)
	s.membersMu.Unlock()
	return newlyBanned
}

// isBannedLocked / isMutedLocked assume membersMu is held.
func (s *Session) isBannedLocked(pk cipher.PubKey) bool { return containsPK(s.banned, pk) }
func (s *Session) isMutedLocked(pk cipher.PubKey) bool  { return containsPK(s.muted, pk) }

// IsBanned reports whether pk is barred from this group.
func (s *Session) IsBanned(pk cipher.PubKey) bool {
	s.membersMu.RLock()
	defer s.membersMu.RUnlock()
	return s.isBannedLocked(pk)
}

// senderAllowedToPost is the READER-side gate: given an inbound leaf
// authored by senderPK at time ts, may this visor render it?
//
// This is where mute and read-only actually take effect. A muted member
// still owns their feed and can publish to it; every honest subscriber
// drops the leaf here, before it reaches the inbox. See the enforcement
// table in moderation.go for why this is the right layer.
//
// Forward-only for mute and read-only: a leaf predating the restriction
// is rendered normally. History is not rewritten by a moderation
// action — flipping a busy group to read-only must not blank every
// message anyone has ever posted, and restricting a member is about
// what they say next. Bans are the deliberate exception: banning means
// gone, so their leaves drop regardless of age.
//
// Timestamps come from different machines, so clock skew can misplace a
// leaf sent within a second or two of the restriction. That is
// acceptable for the same reason the whole gate is: an adversary can
// backdate ts anyway, so this layer targets honest clients that simply
// have not converged yet, not attackers.
//
// Admins are exempt from read-only (they set it) but not from an
// explicit mute, matching Record.CanPost so the local composer state
// and the remote drop decision can never disagree.
func (s *Session) senderAllowedToPost(senderPK cipher.PubKey, ts time.Time) (bool, string) {
	s.membersMu.RLock()
	defer s.membersMu.RUnlock()
	switch {
	case s.isBannedLocked(senderPK):
		return false, "banned"
	case s.isMutedLocked(senderPK) && afterOrEqual(ts, s.mutedSince[senderPK.Hex()]):
		return false, "muted"
	case s.readOnly && !s.cfg.Record.IsAdmin(senderPK) && afterOrEqual(ts, s.readOnlySince):
		return false, "group is read-only"
	default:
		return true, ""
	}
}

// afterOrEqual reports whether ts falls within a restriction that began
// at since. A zero since means "always in effect" (no recorded start),
// and a zero ts — an undated leaf — is treated as covered so an
// unstamped message can't slip past the gate.
func afterOrEqual(ts, since time.Time) bool {
	if since.IsZero() || ts.IsZero() {
		return true
	}
	return !ts.Before(since)
}

// ErrPostNotPermitted is returned by Send when local moderation state
// forbids this visor from posting. Wrapped with the operator-facing
// reason so the UI can show it verbatim.
var ErrPostNotPermitted = errors.New("group: posting not permitted")

// CanPostLocally reports whether THIS visor may currently post, and why
// not. The send-path counterpart of senderAllowedToPost: checking
// locally means a muted operator gets an immediate, explanatory error
// instead of publishing into a feed where every reader silently drops
// it.
func (s *Session) CanPostLocally() (bool, string) {
	return s.senderAllowedToPost(s.cfg.MyPK, time.Now().UTC())
}

// PublishModMutation writes a signed moderation mutation onto this
// session's own feed. Peer-scoped ops take a peerPK; group-scoped ops
// (read-only toggles) must pass the zero value.
//
// Same shape as PublishRosterMutation / PublishAdminMutation.
func (s *Session) PublishModMutation(op ModOp, peerPK cipher.PubKey, parentSeq uint64) (uint64, error) {
	return s.publishModMutationAt(op, peerPK, parentSeq, time.Now().UTC())
}

// publishModMutationAt is PublishModMutation with an explicit issue
// time, for the re-assertion path (BroadcastModeration).
func (s *Session) publishModMutationAt(op ModOp, peerPK cipher.PubKey, parentSeq uint64, at time.Time) (uint64, error) {
	if s == nil || s.pub == nil {
		return 0, errors.New("group: PublishModMutation: no live publisher")
	}
	gid, err := uuid.Parse(s.cfg.Record.ID)
	if err != nil {
		return 0, fmt.Errorf("group: PublishModMutation: group id %q: %w", s.cfg.Record.ID, err)
	}
	seq := s.modSeq.Add(1)
	m := ModerationMutation{
		GroupID:   gid,
		Op:        op,
		PeerPK:    peerPK,
		ParentSeq: parentSeq,
		IssuedAt:  at.UTC(),
	}
	if err := SignMod(&m, s.cfg.MySK); err != nil {
		return 0, fmt.Errorf("group: PublishModMutation: sign: %w", err)
	}
	body, err := MarshalMod(m)
	if err != nil {
		return 0, fmt.Errorf("group: PublishModMutation: marshal: %w", err)
	}
	path := fmt.Sprintf("%s/%05d", ModerationPathPrefix, seq)
	if err := s.pub.Put(path, body); err != nil {
		return 0, fmt.Errorf("group: PublishModMutation: Put %s: %w", path, err)
	}
	s.noteLocalMutation(familyMod, peerPK, m.IssuedAt)
	return seq, nil
}

// applyModLeaf decodes, authenticates, and applies one mod/<seq> leaf.
// Dropped silently (debug-logged) on decode failure, bad signature,
// wrong group, or — the security gate — a non-admin issuer.
//
// Founder immunity: a ban or mute targeting the founder is refused,
// mirroring the founder-immutability rule in applyAdminLeaf. The
// founder is the record's recovery anchor and the invite trust root;
// letting a promoted admin silence them would be a takeover primitive
// (promote self, ban founder, own the group).
func (s *Session) applyModLeaf(body []byte) {
	var m ModerationMutation
	if err := json.Unmarshal(body, &m); err != nil {
		return
	}
	if m.GroupID.String() != s.cfg.Record.ID {
		return
	}
	if err := VerifyMod(m); err != nil {
		s.log.WithError(err).Debug("group: reconcile: rejecting moderation mutation with bad signature")
		return
	}

	s.membersMu.Lock()
	if !s.cfg.Record.IsAdmin(m.IssuerPK) {
		s.membersMu.Unlock()
		s.log.WithField("issuer", m.IssuerPK.String()).
			Debug("group: reconcile: rejecting moderation mutation from non-admin issuer")
		return
	}
	if (m.Op == ModOpBan || m.Op == ModOpMute) && m.PeerPK == s.cfg.Record.OwnerPK {
		s.membersMu.Unlock()
		s.log.WithField("issuer", m.IssuerPK.String()).
			Warn("group: reconcile: refusing moderation mutation against founder")
		return
	}
	// Freshness gate — without it, a replayed ModOpUnban lifts a standing
	// ban and a replayed ModOpUnmute lifts a restriction, using nothing
	// but a leaf the attacker legitimately observed. One watermark covers
	// the whole moderation family per target: ban and mute are different
	// dimensions, but ordering them together means the newest admin
	// decision about a PK always wins, which is the behavior an operator
	// expects from a moderation queue.
	wmKey := watermarkKey(familyMod, m.PeerPK)
	if ok, why := mutationFresh(s.mutationSeen, wmKey, m.IssuedAt, time.Now().UTC()); !ok {
		s.membersMu.Unlock()
		s.log.WithField("issuer", m.IssuerPK.String()).WithField("peer", m.PeerPK.String()).
			WithField("reason", why).WithField("op", int(m.Op)).
			WithField("issued_at", m.IssuedAt.UnixNano()).
			WithField("watermark", s.mutationSeen[wmKey].UnixNano()).
			Debug("XXDEBUG group: reconcile: rejecting stale moderation mutation")
		return
	}
	s.mutationSeen = recordMutation(s.mutationSeen, wmKey, m.IssuedAt)
	s.cfg.Record.MutationSeen = copyWatermarks(s.mutationSeen)
	seenSnapshot := copyWatermarks(s.mutationSeen)

	changed := false
	// topologyChanged flags the peer-backfill toggle: unlike the other
	// moderation ops it changes WHO this visor subscribes to, so the
	// subscription set has to be re-evaluated once the lock is released.
	topologyChanged := false
	switch m.Op {
	case ModOpBan:
		if !containsPK(s.banned, m.PeerPK) {
			s.banned = append(s.banned, m.PeerPK)
			changed = true
		}
		// A ban implies removal from the roster: keeping a banned PK in
		// Members would leave it holding an allowlist seat, which is
		// exactly the capability a ban is supposed to revoke.
		if containsPK(s.cfg.Record.Members, m.PeerPK) {
			s.cfg.Record.Members = removePK(s.cfg.Record.Members, m.PeerPK)
			changed = true
		}
		// A banned PK must not retain admin authority — otherwise its
		// own forged mutations would still pass the authority gate on
		// visors that applied the ban.
		if containsPK(s.cfg.Record.Admins, m.PeerPK) {
			s.cfg.Record.Admins = removePK(s.cfg.Record.Admins, m.PeerPK)
			changed = true
		}
	case ModOpUnban:
		if containsPK(s.banned, m.PeerPK) {
			s.banned = removePK(s.banned, m.PeerPK)
			changed = true
		}
		// Deliberately does NOT re-add to Members: unban means "may ask
		// again", not "is back in". Re-entry goes through the normal
		// join-request path so the admission policy applies.
	case ModOpMute:
		if !containsPK(s.muted, m.PeerPK) {
			s.muted = append(s.muted, m.PeerPK)
			// Stamp the mute with the ISSUER's time, not ours: every
			// visor must agree on the cutoff or the same leaf would be
			// dropped on one member's screen and rendered on another's.
			if s.mutedSince == nil {
				s.mutedSince = make(map[string]time.Time, 1)
			}
			s.mutedSince[m.PeerPK.Hex()] = m.IssuedAt.UTC()
			changed = true
		}
	case ModOpUnmute:
		if containsPK(s.muted, m.PeerPK) {
			s.muted = removePK(s.muted, m.PeerPK)
			delete(s.mutedSince, m.PeerPK.Hex())
			changed = true
		}
	case ModOpReadOnly:
		if !s.readOnly {
			s.readOnly = true
			s.readOnlySince = m.IssuedAt.UTC()
			changed = true
		}
	case ModOpReadWrite:
		if s.readOnly {
			s.readOnly = false
			s.readOnlySince = time.Time{}
			changed = true
		}
	case ModOpPeerBackfillOn:
		if s.cfg.Record.PeerBackfillDisabled {
			s.cfg.Record.PeerBackfillDisabled = false
			changed = true
			topologyChanged = true
		}
	case ModOpPeerBackfillOff:
		if !s.cfg.Record.PeerBackfillDisabled {
			s.cfg.Record.PeerBackfillDisabled = true
			changed = true
			topologyChanged = true
		}
	}

	s.moderationLocked().applyTo(&s.cfg.Record)
	s.cfg.Record.EnsureFounderInAdmins()
	modSnapshot := s.moderationLocked()
	members := append([]cipher.PubKey(nil), s.cfg.Record.Members...)
	admins := append([]cipher.PubKey(nil), s.cfg.Record.Admins...)
	wasBan := m.Op == ModOpBan
	s.membersMu.Unlock()

	s.persistWatermarks(seenSnapshot)

	if !changed {
		return
	}
	s.log.WithField("op", int(m.Op)).WithField("peer", m.PeerPK.String()).
		Debug("group: reconcile: applied moderation mutation")

	// A ban shrank the roster, so reconcile subscriptions: owner-side
	// this drops the banned PK's allowlist seat, and every side closes
	// the peer-sub that was following their feed.
	if wasBan {
		if _, err := s.SetAllowlist(members); err != nil {
			s.log.WithError(err).Debug("group: reconcile: SetAllowlist after ban failed")
		}
		if _, err := s.SetAdminRoster(admins); err != nil {
			s.log.WithError(err).Debug("group: reconcile: SetAdminRoster after ban failed")
		}
		if s.onRosterChange != nil {
			s.onRosterChange(members, admins)
		}
	}
	// The backfill toggle decides whether non-admins follow each other, so
	// applying it means re-running the subscription rule: switching it on
	// has to actually open those subs (otherwise the setting is inert until
	// the next restart), and switching it off has to close them.
	if topologyChanged {
		// Off the update-callback goroutine: SetAdminRoster opens peer
		// subscriptions, and each one dials with a 15s timeout. Running
		// that inline would stall this subscriber's whole receive pump —
		// every other leaf in the batch, and every batch behind it —
		// while a peer that may well be offline times out.
		go func() {
			if _, err := s.SetAdminRoster(admins); err != nil {
				s.log.WithError(err).Debug("group: reconcile: peer-sub re-evaluation after backfill toggle failed")
			}
		}()
	}
	if s.onModChange != nil {
		s.onModChange(modSnapshot)
	}
}

// BroadcastModeration re-publishes the current moderation state as
// signed mod/ gossip on this session's own feed, so a late joiner
// converges to it the same way BroadcastRoster handles membership.
//
// Only admins' mutations are authoritative, so non-admin sessions are
// a no-op. Idempotent on the receive side.
func (s *Session) BroadcastModeration() {
	if s == nil || s.pub == nil || s.closed.Load() {
		return
	}
	s.membersMu.RLock()
	if !s.cfg.Record.IsAdmin(s.cfg.MyPK) {
		s.membersMu.RUnlock()
		return
	}
	st := s.moderationLocked()
	// Historical timestamps, same reason as BroadcastRoster: an echo of
	// an old decision must not out-rank another admin's newer one.
	modAt := make(map[cipher.PubKey]time.Time, len(st.Banned)+len(st.Muted))
	for _, pk := range st.Banned {
		modAt[pk] = s.assertionTimeLocked(familyMod, pk)
	}
	for _, pk := range st.Muted {
		modAt[pk] = s.assertionTimeLocked(familyMod, pk)
	}
	groupAt := s.assertionTimeLocked(familyMod, cipher.PubKey{})
	s.membersMu.RUnlock()

	for _, pk := range st.Banned {
		if _, err := s.publishModMutationAt(ModOpBan, pk, 0, modAt[pk]); err != nil {
			s.log.WithError(err).WithField("peer", pk.String()).
				Debug("group: BroadcastModeration: ban publish failed")
		}
	}
	for _, pk := range st.Muted {
		if _, err := s.publishModMutationAt(ModOpMute, pk, 0, modAt[pk]); err != nil {
			s.log.WithError(err).WithField("peer", pk.String()).
				Debug("group: BroadcastModeration: mute publish failed")
		}
	}
	// Only emit the read-only state when it's ON. Broadcasting
	// ModOpReadWrite unconditionally would be a no-op for fresh
	// joiners but would race a concurrent admin's ModOpReadOnly on
	// resume, flipping a deliberately quieted room back open.
	if st.ReadOnly {
		if _, err := s.publishModMutationAt(ModOpReadOnly, cipher.PubKey{}, 0, groupAt); err != nil {
			s.log.WithError(err).Debug("group: BroadcastModeration: read-only publish failed")
		}
	}
	// Same asymmetry, same reason: only the non-default state is worth
	// re-asserting. Backfill is enabled unless an admin turned it off, so
	// broadcasting the "on" op would be a no-op for fresh joiners while
	// racing another admin's genuine "off" on resume.
	if st.PeerBackfillDisabled {
		if _, err := s.publishModMutationAt(ModOpPeerBackfillOff, cipher.PubKey{}, 0, groupAt); err != nil {
			s.log.WithError(err).Debug("group: BroadcastModeration: peer-backfill publish failed")
		}
	}
}
