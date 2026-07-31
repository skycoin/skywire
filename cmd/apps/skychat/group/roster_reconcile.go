// Package group cmd/apps/skychat/group/roster_reconcile.go c4-app-chat
//
// PublishRosterMutation/PublishAdminMutation write signed roster/<seq> and
// admin/<seq> leaves onto the issuer's feed, but before this file NOTHING read
// them: every subscriber subscribed to msgs/ only, and onUpdate had no branch
// for roster/admin leaves. So a late joiner (which bootstraps its Record as
// [owner, self] from the invite) never converged to the owner's real member
// list — the "reconciler (step 3 of the gossip impl)" the comments referenced
// was never implemented. See skycoin/skywire#3426.
//
// This wires the receive side: subscribers now include the roster/+admin/
// prefixes (see subscribedPrefixes), and onUpdate hands each such leaf to
// applyRosterLeaf / applyAdminLeaf here. The security-critical rule is that a
// mutation is applied ONLY if it is (a) signature-valid and (b) issued by a
// CURRENT admin of the group — otherwise any member could forge the roster.
package group

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
)

// rosterBroadcastDelay is how long the manager waits after opening a session
// before re-broadcasting the roster, giving the publisher + dmsg time to settle
// so the roster/admin Puts actually land.
const rosterBroadcastDelay = 3 * time.Second

// subscribedPrefixes is the set of CXO path prefixes every group subscriber
// watches: msgs/ (chat) plus roster/, admin/, mod/ and keys/ (signed
// membership, admin, moderation and key-rotation gossip). Centralized so
// all SetPrefixes call sites stay in lockstep.
func subscribedPrefixes() []string {
	return []string{MessagePathPrefix, RosterPathPrefix, AdminPathPrefix, ModerationPathPrefix, KeyPathPrefix}
}

// isGossipPath reports whether a leaf path belongs to one of the signed
// control subtrees rather than to chat. The inbox loop skips these — they
// were already consumed by the reconciler pre-pass.
func isGossipPath(path string) bool {
	return strings.HasPrefix(path, RosterPathPrefix+"/") ||
		strings.HasPrefix(path, AdminPathPrefix+"/") ||
		strings.HasPrefix(path, ModerationPathPrefix+"/") ||
		strings.HasPrefix(path, KeyPathPrefix+"/")
}

// SetRosterChangeHandler installs a callback invoked (with a snapshot of the
// reconciled members + admins) whenever the reconciler mutates this session's
// roster. The manager uses it to persist the updated Record to its store so
// group info/list reflect the converged roster and it survives restart. nil is
// fine — the in-memory session state + peer-subscriptions still converge; only
// on-disk persistence is skipped.
func (s *Session) SetRosterChangeHandler(f func(members, admins []cipher.PubKey)) {
	s.onRosterChange = f
}

func containsPK(list []cipher.PubKey, pk cipher.PubKey) bool {
	for _, x := range list {
		if x == pk {
			return true
		}
	}
	return false
}

func removePK(list []cipher.PubKey, pk cipher.PubKey) []cipher.PubKey {
	out := make([]cipher.PubKey, 0, len(list))
	for _, x := range list {
		if x != pk {
			out = append(out, x)
		}
	}
	return out
}

// applyRosterLeaf decodes, authenticates, and applies one roster/<seq> leaf.
// Rejected silently (debug-logged) if it doesn't decode, the signature is
// invalid, it targets another group, or — the security gate — the issuer is not
// a current admin. Idempotent: an Add of an existing member (or Remove of an
// absent one) is a no-op.
func (s *Session) applyRosterLeaf(body []byte) {
	// Unseal first: on an encrypted group the envelope arrives sealed
	// under the group key. A leaf sealed under a key we do not hold yet
	// is parked rather than dropped — see gossip_seal.go.
	body, ok := s.openOrPark(familyRoster, body)
	if !ok {
		return
	}
	var m RosterMutation
	if err := json.Unmarshal(body, &m); err != nil {
		return
	}
	if m.GroupID.String() != s.cfg.Record.ID {
		return
	}
	if err := VerifyRoster(m); err != nil {
		s.log.WithError(err).Debug("group: reconcile: rejecting roster mutation with bad signature")
		return
	}

	s.membersMu.Lock()
	// AUTHORITY GATE: only a current admin may mutate the roster. Without this
	// any member could forge roster/<seq> leaves and add/remove arbitrary PKs.
	if !s.cfg.Record.IsAdmin(m.IssuerPK) {
		s.membersMu.Unlock()
		s.log.WithField("issuer", m.IssuerPK.String()).
			Debug("group: reconcile: rejecting roster mutation from non-admin issuer")
		return
	}
	// FRESHNESS GATE: a signature stays valid forever, so authority alone
	// does not make a leaf current. Without this, any member could replay
	// an admin's old RosterOpAdd verbatim and undo a later eviction. See
	// replay_guard.go.
	wmKey := watermarkKey(familyRoster, m.PeerPK)
	if ok, why := mutationFresh(s.mutationSeen, wmKey, m.IssuedAt, time.Now().UTC()); !ok {
		s.membersMu.Unlock()
		s.log.WithField("issuer", m.IssuerPK.String()).WithField("peer", m.PeerPK.String()).
			WithField("reason", why).
			Debug("group: reconcile: rejecting stale roster mutation")
		return
	}
	s.mutationSeen = recordMutation(s.mutationSeen, wmKey, m.IssuedAt)
	s.cfg.Record.MutationSeen = copyWatermarks(s.mutationSeen)
	seenSnapshot := copyWatermarks(s.mutationSeen)
	changed := false
	switch m.Op {
	case RosterOpAdd:
		// Never let an add re-admit a banned PK. Independent of the
		// freshness gate on purpose: ban is the strongest revocation the
		// group has, and it should not depend on watermark bookkeeping
		// being intact to hold.
		if s.cfg.Record.IsBanned(m.PeerPK) {
			s.membersMu.Unlock()
			s.log.WithField("peer", m.PeerPK.String()).
				Warn("group: reconcile: refusing to add a banned peer")
			s.persistWatermarks(seenSnapshot)
			return
		}
		if m.PeerPK != (cipher.PubKey{}) && !containsPK(s.cfg.Record.Members, m.PeerPK) {
			s.cfg.Record.Members = append(s.cfg.Record.Members, m.PeerPK)
			changed = true
		}
	case RosterOpRemove:
		// Never let a remove strip the founder — matches DemoteAdmin's
		// founder-immutability and keeps the group recoverable.
		if m.PeerPK != s.cfg.Record.OwnerPK && containsPK(s.cfg.Record.Members, m.PeerPK) {
			s.cfg.Record.Members = removePK(s.cfg.Record.Members, m.PeerPK)
			changed = true
		}
	}
	members := append([]cipher.PubKey(nil), s.cfg.Record.Members...)
	admins := append([]cipher.PubKey(nil), s.cfg.Record.Admins...)
	s.membersMu.Unlock()

	// The watermark advanced even when the mutation was a no-op (an add
	// of an existing member), so persist it regardless of `changed` —
	// otherwise a restart would forget the advance and re-open the
	// replay window for that target.
	s.persistWatermarks(seenSnapshot)

	if !changed {
		return
	}
	s.log.WithField("op", int(m.Op)).WithField("peer", m.PeerPK.String()).
		Debug("group: reconcile: applied roster mutation")
	// Reconcile peer-subscriptions so this visor actually subscribes to the
	// new member's feed where its role calls for it (SetAllowlist is the
	// owner-side reconciler; it recomputes the desired peer-sub set).
	if _, err := s.SetAllowlist(members); err != nil {
		s.log.WithError(err).Debug("group: reconcile: SetAllowlist after roster change failed")
	}
	// SetAllowlist is owner-role only, so on a MEMBER session the call
	// above did nothing and the subscription set was left untouched by a
	// roster change. That was invisible while non-admins followed admins
	// only — the set never depended on the plain-member roster — but it
	// means a member-role admin never picked up a newly added member
	// either, and with peer backfill it would leave a joiner unfollowed by
	// everyone except the admins. SetAdminRoster is the role-agnostic
	// re-evaluation of the same rule; it is idempotent, so running it on
	// both paths is free.
	// Async for the same reason as the backfill toggle: this opens peer
	// subscriptions, and dialing a peer that is offline would otherwise
	// block this subscriber's receive pump for the dial timeout.
	go func() {
		if _, err := s.SetAdminRoster(admins); err != nil {
			s.log.WithError(err).Debug("group: reconcile: peer-sub re-evaluation after roster change failed")
		}
	}()
	if s.onRosterChange != nil {
		s.onRosterChange(members, admins)
	}
}

// applyAdminLeaf decodes, authenticates, and applies one admin/<seq> leaf
// (promote/demote). Same authority gate as applyRosterLeaf; the founder can
// never be demoted.
func (s *Session) applyAdminLeaf(body []byte) {
	body, ok := s.openOrPark(familyAdmin, body)
	if !ok {
		return
	}
	var m AdminMutation
	if err := json.Unmarshal(body, &m); err != nil {
		return
	}
	if m.GroupID.String() != s.cfg.Record.ID {
		return
	}
	if err := VerifyAdmin(m); err != nil {
		s.log.WithError(err).Debug("group: reconcile: rejecting admin mutation with bad signature")
		return
	}

	s.membersMu.Lock()
	if !s.cfg.Record.IsAdmin(m.IssuerPK) {
		s.membersMu.Unlock()
		s.log.WithField("issuer", m.IssuerPK.String()).
			Debug("group: reconcile: rejecting admin mutation from non-admin issuer")
		return
	}
	// Freshness gate — without it, a replayed AdminOpPromote re-grants
	// authority to an admin who was later demoted.
	wmKey := watermarkKey(familyAdmin, m.PeerPK)
	if ok, why := mutationFresh(s.mutationSeen, wmKey, m.IssuedAt, time.Now().UTC()); !ok {
		s.membersMu.Unlock()
		s.log.WithField("issuer", m.IssuerPK.String()).WithField("peer", m.PeerPK.String()).
			WithField("reason", why).
			Debug("group: reconcile: rejecting stale admin mutation")
		return
	}
	s.mutationSeen = recordMutation(s.mutationSeen, wmKey, m.IssuedAt)
	s.cfg.Record.MutationSeen = copyWatermarks(s.mutationSeen)
	seenSnapshot := copyWatermarks(s.mutationSeen)
	changed := false
	switch m.Op {
	case AdminOpPromote:
		if m.PeerPK != (cipher.PubKey{}) && !s.cfg.Record.IsAdmin(m.PeerPK) {
			s.cfg.Record.Admins = append(s.cfg.Record.Admins, m.PeerPK)
			changed = true
		}
	case AdminOpDemote:
		if m.PeerPK != s.cfg.Record.OwnerPK && containsPK(s.cfg.Record.Admins, m.PeerPK) {
			s.cfg.Record.Admins = removePK(s.cfg.Record.Admins, m.PeerPK)
			changed = true
		}
	}
	s.cfg.Record.EnsureFounderInAdmins()
	members := append([]cipher.PubKey(nil), s.cfg.Record.Members...)
	admins := append([]cipher.PubKey(nil), s.cfg.Record.Admins...)
	s.membersMu.Unlock()

	s.persistWatermarks(seenSnapshot)

	if !changed {
		return
	}
	s.log.WithField("op", int(m.Op)).WithField("peer", m.PeerPK.String()).
		Debug("group: reconcile: applied admin mutation")
	// Admin-set changes shift who non-admins subscribe to (admins) and who
	// admins aggregate — reconcile both sides.
	if _, err := s.SetAdminRoster(admins); err != nil {
		s.log.WithError(err).Debug("group: reconcile: SetAdminRoster after admin change failed")
	}
	if _, err := s.SetAllowlist(members); err != nil {
		s.log.WithError(err).Debug("group: reconcile: SetAllowlist after admin change failed")
	}
	if s.onRosterChange != nil {
		s.onRosterChange(members, admins)
	}
}

// BroadcastRoster re-publishes the current member + admin set as signed roster/
// admin gossip on this session's own feed. Joiners bootstrap their Record from
// the invite as just [owner, self] and converge by hydrating these leaves via
// the reconciler — so WITHOUT this, a member added at CREATE time (--member /
// Create's initialMembers) leaves no roster/ leaf and is never learned by a
// late joiner (the reconciler only applies leaves that exist on the feed).
//
// Only an admin's mutations are authoritative (the reconciler rejects non-admin
// issuers), so only admins broadcast; a non-admin or publisher-less session is a
// no-op. Idempotent on the receive side (Add of an existing member is a no-op),
// so it's safe to call on every owner-session open. Best-effort: publish errors
// are debug-logged and retried on the next open.
func (s *Session) BroadcastRoster() {
	// openLocked calls this from a goroutine after rosterBroadcastDelay, so the
	// session can already be torn down by the time it fires (create-then-delete,
	// or app shutdown right after a join). Bail rather than publishing N leaves
	// into a closed publisher.
	if s == nil || s.pub == nil || s.closed.Load() {
		return
	}
	s.membersMu.RLock()
	self := s.cfg.MyPK
	owner := s.cfg.Record.OwnerPK
	if !s.cfg.Record.IsAdmin(self) {
		s.membersMu.RUnlock()
		return
	}
	members := append([]cipher.PubKey(nil), s.cfg.Record.Members...)
	admins := append([]cipher.PubKey(nil), s.cfg.Record.Admins...)
	// Date each re-assertion by when we actually learned it, NOT by now.
	// Stamping these with the current time would make a restarting admin
	// out-rank every other admin's live decisions under the freshness
	// rule — silently re-adding a member somebody else just evicted.
	memberAt := make(map[cipher.PubKey]time.Time, len(members))
	for _, pk := range members {
		memberAt[pk] = s.assertionTimeLocked(familyRoster, pk)
	}
	adminAt := make(map[cipher.PubKey]time.Time, len(admins))
	for _, pk := range admins {
		adminAt[pk] = s.assertionTimeLocked(familyAdmin, pk)
	}
	s.membersMu.RUnlock()

	for _, pk := range members {
		if pk == self {
			continue
		}
		if _, err := s.publishRosterMutationAt(RosterOpAdd, pk, 0, memberAt[pk]); err != nil {
			s.log.WithError(err).WithField("peer", pk.String()).
				Debug("group: BroadcastRoster: roster add publish failed")
		}
	}
	for _, pk := range admins {
		if pk == self || pk == owner {
			continue // owner is an implicit admin; self needs no promotion
		}
		if _, err := s.publishAdminMutationAt(AdminOpPromote, pk, 0, adminAt[pk]); err != nil {
			s.log.WithError(err).WithField("peer", pk.String()).
				Debug("group: BroadcastRoster: admin promote publish failed")
		}
	}
}
