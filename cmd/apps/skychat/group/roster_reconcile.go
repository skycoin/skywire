// Package group — receive-side roster/admin reconciler.
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

	"github.com/skycoin/skywire/pkg/cipher"
)

// subscribedPrefixes is the set of CXO path prefixes every group subscriber
// watches: msgs/ (chat) plus roster/ and admin/ (signed membership + admin
// gossip). Centralized so all SetPrefixes call sites stay in lockstep.
func subscribedPrefixes() []string {
	return []string{MessagePathPrefix, RosterPathPrefix, AdminPathPrefix}
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
	changed := false
	switch m.Op {
	case RosterOpAdd:
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
	if s.onRosterChange != nil {
		s.onRosterChange(members, admins)
	}
}

// applyAdminLeaf decodes, authenticates, and applies one admin/<seq> leaf
// (promote/demote). Same authority gate as applyRosterLeaf; the founder can
// never be demoted.
func (s *Session) applyAdminLeaf(body []byte) {
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
