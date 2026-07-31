// Package group pkg/skychat/group/manager_admission.go c4-app-chat
// the Manager half of admission control: asking to join, ruling on
// requests, and the moderation commands (kick / ban / mute / read-only).
//
// Split from manager.go, which is already large and is about session
// lifecycle. The division: manager.go owns "bring sessions up and keep
// them up", this file owns "who is allowed in and who may speak".
//
// Every command here follows the same three-beat shape, and it matters
// that they stay in that order:
//
//  1. Authorize + mutate the local Record, and persist it. Local state
//     is the source of truth for this visor even if the network half
//     fails.
//  2. Apply to the live Session so enforcement takes effect now rather
//     than after the next restart.
//  3. Publish the signed gossip so other visors converge.
//
// Steps 2 and 3 are best-effort: a publisher that is mid-bootstrap
// shouldn't roll back an admin's decision. Every command is idempotent,
// so re-issuing it once the session is healthy re-emits the gossip.
package group

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
)

// pendingJoinRetryInterval is how often a requester re-asks a group
// that has queued it for approval.
//
// Deliberately unhurried. The other side of this poll is a human
// deciding, so a tighter loop buys nothing and a request that sits for
// an hour costs 120 dials. It is also the reason a denial is terminal:
// re-asking forever after an explicit no would make the poll
// indistinguishable from pestering.
const pendingJoinRetryInterval = 30 * time.Second

// joinAttemptBudget is how long one join attempt may take for n
// candidates: the last candidate starts after (n-1) staggers and then
// needs a full response window of its own, plus the slack the
// single-target path always had for the dial itself.
//
// It grows with n rather than being a flat constant so that adding an
// admin to an invite cannot silently cut the last candidate's read short
// — a joiner that timed out mid-answer would leave the admin with a
// queued request and the joiner with an error, the one outcome worse
// than a slow join.
func joinAttemptBudget(n int) time.Duration {
	if n < 1 {
		n = 1
	}
	return joinResponseReadTimeout + 5*time.Second +
		time.Duration(n-1)*joinCandidateStagger
}

// SetJoinRequestNotifier installs a callback fired when a NEW join
// request lands on this visor's approval queue. Used by the app layer
// to raise an SSE event and an OS notification. Not fired for a re-ask
// of a request already queued — the requester polls, and an admin
// doesn't need a notification every 30s for the same person.
func (m *Manager) SetJoinRequestNotifier(f func(JoinRequest)) {
	m.onJoinRequestMu.Lock()
	m.onJoinRequestNotify = f
	m.onJoinRequestMu.Unlock()
}

// persistModeration returns the mod-change callback installed on every
// session, writing the reconciler's converged moderation state back to
// the store. Best-effort, mirroring persistRoster.
func (m *Manager) persistModeration(id string) func(ModState) {
	return func(st ModState) {
		if err := m.store.SetModeration(id, st); err != nil {
			m.log.WithError(err).WithField("id", id).
				Debug("group: persistModeration: store put failed")
		}
	}
}

// persistKeys returns the key-change callback installed on every session.
//
// Unlike persistRoster and persistModeration this is NOT best-effort
// decoration: roster and moderation state re-converge from the next
// gossip round, but a rotation is a one-shot delivery. A visor that
// applied a new key and failed to save it comes back after a restart
// holding a retired key — unable to read anything published since, and
// sealing its own messages into a key the group has moved off. Hence the
// warning rather than a debug line.
func (m *Manager) persistKeys(id string) func(KeyState) {
	return func(st KeyState) {
		if err := m.store.SetKeyState(id, st); err != nil {
			m.log.WithError(err).WithField("id", id).WithField("epoch", st.Epoch).
				Warn("group: persisting the rotated group key failed; a restart would leave this visor unable to read the group")
		}
	}
}

// RotateKey mints a new group key and distributes it to every current
// member except this visor, each copy sealed to that member's PK.
//
// Admin-only and encrypted-groups-only. Exposed for the operator-driven
// case — a key believed leaked, an operator tidying up after an
// out-of-band departure — while eviction rotates automatically through
// rotateAfterEviction.
func (m *Manager) RotateKey(id string) (Record, error) {
	r, ok, err := m.store.Get(id)
	if err != nil {
		return Record{}, fmt.Errorf("group: RotateKey: get: %w", err)
	}
	if !ok {
		return Record{}, fmt.Errorf("group: RotateKey: no record for %s", id)
	}
	if !r.IsAdmin(m.myPK) {
		return Record{}, errors.New("group: RotateKey: only admins can rotate the group key")
	}
	if !r.Encrypted() {
		return Record{}, ErrKeyRotationNotEncrypted
	}
	m.mu.RLock()
	sess, live := m.sessions[id]
	m.mu.RUnlock()
	if !live {
		// Rotating without a publisher would mint a key we cannot
		// distribute — every member would keep the old one and we would
		// be the only visor unable to read the group.
		return Record{}, fmt.Errorf("group: RotateKey: no live session for %s", id)
	}
	if _, err := sess.RotateKeyFor(m.keyRecipients(r)); err != nil {
		return Record{}, err
	}
	// The session's key-change handler has already persisted the new
	// state; re-read so the caller sees it.
	updated, _, err := m.store.Get(id)
	if err != nil {
		return Record{}, fmt.Errorf("group: RotateKey: re-read: %w", err)
	}
	return updated, nil
}

// rotateAfterEviction rotates the group key after a peer loses its seat,
// so the eviction takes away the ability to READ and not merely the
// ability to connect.
//
// Called after the roster has already shrunk and been persisted — the new
// key is sealed to the members that remain, and the evicted PK is not
// among them. Best-effort by design: a rotation that cannot be published
// (no live session, publisher mid-bootstrap) must not roll back the
// eviction itself, which is already correct locally and already gossiped.
// It is logged loudly because the gap it leaves is exactly the gap this
// feature closes, and re-running the moderation command rotates again.
//
// No-op for plaintext groups, where there is no key and the CXO allowlist
// is the whole of the access control.
func (m *Manager) rotateAfterEviction(id string, r Record, evicted cipher.PubKey, why string) {
	if !r.Encrypted() {
		return
	}
	m.mu.RLock()
	sess, live := m.sessions[id]
	m.mu.RUnlock()
	if !live {
		m.log.WithField("id", id).WithField("peer", evicted.Hex()).WithField("op", why).
			Warn("group: no live session to rotate the group key on; the evicted peer still holds the current key — re-run the command once the group is connected")
		return
	}
	epoch, err := sess.RotateKeyFor(m.keyRecipients(r))
	if err != nil {
		m.log.WithError(err).WithField("id", id).WithField("peer", evicted.Hex()).WithField("op", why).
			Warn("group: key rotation after eviction failed; the evicted peer still holds the current key")
		return
	}
	m.log.WithField("id", id).WithField("peer", evicted.Hex()).WithField("op", why).
		WithField("epoch", epoch).Info("group: rotated the group key after eviction")
}

// pendingJoinCount returns how many requests are awaiting a decision.
func (m *Manager) pendingJoinCount(id string) (int, error) {
	reqs, err := m.store.ListJoinRequests(id)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, q := range reqs {
		if q.IsPending() {
			n++
		}
	}
	return n, nil
}

// maxPendingJoins is this visor's cap on undecided requests per group.
func (m *Manager) maxPendingJoins() int {
	if m.maxPending > 0 {
		return m.maxPending
	}
	return DefaultMaxPendingJoins
}

// keyRecipients is who a rotation on record r must be sealed to: every
// member except this visor (which mints the key) and except anyone
// banned.
//
// Taken from the persisted record rather than from the live session,
// because the session's copy of Members is the snapshot it opened with —
// roster changes update the CXO allowlist and the store, not that field.
// Deriving recipients session-side would seal the key to whoever was in
// the room when the session started and silently skip everyone admitted
// since.
func (m *Manager) keyRecipients(r Record) []cipher.PubKey {
	out := make([]cipher.PubKey, 0, len(r.Members))
	for _, pk := range r.Members {
		if pk == m.myPK || pk == (cipher.PubKey{}) || r.IsBanned(pk) {
			continue
		}
		out = append(out, pk)
	}
	return out
}

// admissionHandler returns the JoinRequestHandler installed on sessions
// for group id. Runs on the relay listener goroutine with the requester
// already authenticated against the transport.
func (m *Manager) admissionHandler(id string) JoinRequestHandler {
	return func(req JoinRequest) JoinResponseMsg {
		// The three "not my call" answers below are deliberately
		// JoinStatusUnavailable rather than denied. A requester now asks
		// several admins, and a refusal it believes is terminal would let
		// one stale name in an invite — a visor that left the group, or
		// deleted it, or was demoted — sink a join every other admin
		// would have approved. Unavailable moves it to the next name.
		r, ok, err := m.store.Get(id)
		if err != nil || !ok {
			return JoinResponseMsg{Status: JoinStatusUnavailable, Reason: "this visor does not hold that group"}
		}
		if r.Status.IsTerminal() {
			return JoinResponseMsg{Status: JoinStatusUnavailable, Reason: "this visor has left or deleted the group"}
		}
		if r.IsBanned(req.PK) {
			return JoinResponseMsg{Status: JoinStatusBanned, Reason: "banned from this group"}
		}
		// Only a visor with roster authority can admit. Reachable now that
		// invites name several admins: any of them may have been demoted
		// since the link was minted, and a member session answers join
		// requests too so it can act the moment it IS promoted.
		if !r.IsAdmin(m.myPK) {
			return JoinResponseMsg{Status: JoinStatusUnavailable, Reason: "this visor holds no roster authority on the group"}
		}
		// Already in. A re-ask from a member is how a joiner recovers
		// after being admitted but losing the response frame, so it must
		// be idempotent and must re-deliver the key.
		//
		// Answered BEFORE the cost gates on purpose: a member re-syncing
		// is not a new identity, and charging it would mean an honest
		// member could be throttled out of recovering.
		if containsPK(r.Members, req.PK) {
			return m.admittedResponse(r)
		}

		// Cost gate. From here down the request is from a PK the group has
		// never admitted — the only shape a Sybil flood can take — so it
		// pays before it consumes anything that costs us.
		if bits := r.JoinPoWRequired(); bits > 0 {
			if ok, why := VerifyJoinPoW(id, req.PK, req.PoW, bits, time.Now().UTC()); !ok {
				// A challenge, not a refusal: the requester solves and comes
				// straight back. Nothing is stored, so an attacker that
				// never solves costs us one hash per attempt.
				return JoinResponseMsg{
					Status:  JoinStatusChallenge,
					PoWBits: bits,
					Reason:  why,
				}
			}
		}
		// Rate gate. Bounds what a flood can consume per unit time even
		// when every identity paid: proof of work prices an identity, the
		// bucket prices the group's attention.
		if ok, wait := m.joinBucket.allow(id, time.Now()); !ok {
			m.log.WithField("id", id).WithField("peer", req.PK.Hex()).
				WithField("retry_in", wait.Round(time.Second).String()).
				Info("group: admission: join rate limit reached; refusing without queueing")
			return JoinResponseMsg{
				Status:        JoinStatusThrottled,
				RetryAfterSec: int(wait.Round(time.Second).Seconds()),
				Reason:        "this group is accepting new members slowly right now; try again later",
			}
		}

		switch r.JoinPolicy() {
		case JoinOpen:
			updated, err := m.AddMember(id, req.PK)
			if err != nil {
				m.log.WithError(err).WithField("peer", req.PK.Hex()).
					Warn("group: admission: auto-admit failed")
				return JoinResponseMsg{Status: JoinStatusDenied, Reason: "could not add member: " + err.Error()}
			}
			decided := req
			decided.Status = JoinStatusAdmitted
			decided.DecidedAt = time.Now().UTC()
			decided.DecidedBy = m.myPK
			if err := m.store.PutJoinRequest(decided); err != nil {
				// Audit-only; the member is already added.
				m.log.WithError(err).Debug("group: admission: recording auto-admit failed")
			}
			return m.admittedResponse(updated)

		case JoinApproval:
			existing, found, err := m.store.GetJoinRequest(id, req.PK)
			if !found {
				// Queue depth is an admin's attention, and attention is what
				// a flood actually attacks: bury the real requests and the
				// group is unusable even though every gate "worked". At the
				// cap we refuse rather than evict, because a PK that has
				// been waiting already paid for its slot and dropping it for
				// the newest arrival is precisely the primitive an attacker
				// wants.
				if n, cErr := m.pendingJoinCount(id); cErr == nil && n >= m.maxPendingJoins() {
					m.log.WithField("id", id).WithField("pending", n).
						Info("group: admission: approval queue is full; refusing without queueing")
					return JoinResponseMsg{
						Status:        JoinStatusThrottled,
						RetryAfterSec: int(DefaultJoinRefill.Seconds()),
						Reason:        "this group's approval queue is full; try again later",
					}
				}
			}
			if err == nil && found {
				switch existing.Status {
				case JoinStatusDenied:
					// Terminal for the requester. An admin can still
					// flip this from their queue via ApproveJoin; the
					// requester picks it up when they next ask.
					return JoinResponseMsg{Status: JoinStatusDenied, Reason: "request was declined"}
				case JoinStatusAdmitted:
					// Decided yes but the roster says otherwise (kicked
					// after approval). Treat the roster as truth and
					// re-queue rather than admitting on a stale record.
				case JoinStatusPending, JoinStatusBanned:
				}
				if existing.IsPending() {
					return JoinResponseMsg{Status: JoinStatusPending, Reason: "awaiting admin approval"}
				}
			}
			queued := req
			queued.Status = JoinStatusPending
			if err := m.store.PutJoinRequest(queued); err != nil {
				m.log.WithError(err).Warn("group: admission: queueing request failed")
				return JoinResponseMsg{Status: JoinStatusDenied, Reason: "could not queue request"}
			}
			m.notifyJoinRequest(queued)
			return JoinResponseMsg{Status: JoinStatusPending, Reason: "awaiting admin approval"}
		}
		return JoinResponseMsg{Status: JoinStatusDenied, Reason: "unknown admission policy"}
	}
}

// admittedResponse builds the success payload: roster, moderation
// state, and — for an encrypted group — the AES key.
//
// This is the delivery point that replaced putting the key in the
// invite link. It rides the relay stream, which is Noise-encrypted and
// mutually authenticated, so the key reaches exactly the PK that was
// approved and nobody else.
func (m *Manager) admittedResponse(r Record) JoinResponseMsg {
	resp := JoinResponseMsg{
		Status:               JoinStatusAdmitted,
		GroupID:              r.ID,
		Name:                 r.Name,
		Members:              append([]cipher.PubKey(nil), r.Members...),
		Admins:               append([]cipher.PubKey(nil), r.Admins...),
		Muted:                append([]cipher.PubKey(nil), r.Muted...),
		ReadOnly:             r.ReadOnly,
		PeerBackfillDisabled: r.PeerBackfillDisabled,
	}
	if r.Encrypted() {
		resp.AESKey = append([]byte(nil), r.AESKey...)
		resp.KeyEpoch = r.KeyEpoch
	}
	return resp
}

func (m *Manager) notifyJoinRequest(req JoinRequest) {
	m.onJoinRequestMu.RLock()
	f := m.onJoinRequestNotify
	m.onJoinRequestMu.RUnlock()
	if f != nil {
		f(req)
	}
}

// PendingJoins returns the join-request queue for a group, newest
// first. Includes decided entries so the UI can show history; callers
// wanting only actionable items filter on IsPending.
func (m *Manager) PendingJoins(id string) ([]JoinRequest, error) {
	return m.store.ListJoinRequests(id)
}

// ApproveJoin admits a queued requester. Idempotent: approving an
// already-admitted PK returns the current record.
//
// The requester is NOT notified — they have no allowlist seat yet, so
// there is no channel to reach them on. They learn on their next poll,
// within pendingJoinRetryInterval. Approving a previously-denied
// request works, but that requester has stopped polling, so they see it
// only when they ask again.
func (m *Manager) ApproveJoin(id string, pk cipher.PubKey) (Record, error) {
	r, ok, err := m.store.Get(id)
	if err != nil {
		return Record{}, fmt.Errorf("group: ApproveJoin: get: %w", err)
	}
	if !ok {
		return Record{}, fmt.Errorf("group: ApproveJoin: no record for %s", id)
	}
	if !r.IsAdmin(m.myPK) {
		return Record{}, errors.New("group: ApproveJoin: only admins can approve join requests")
	}
	if r.IsBanned(pk) {
		return Record{}, errors.New("group: ApproveJoin: peer is banned; unban before approving")
	}
	updated, err := m.AddMember(id, pk)
	if err != nil {
		return Record{}, fmt.Errorf("group: ApproveJoin: add member: %w", err)
	}
	req, found, err := m.store.GetJoinRequest(id, pk)
	if err != nil || !found {
		// Approving a PK that never asked is a legitimate admin action
		// (an out-of-band invite); synthesize the audit entry.
		req = JoinRequest{GroupID: id, PK: pk, AskedAt: time.Now().UTC()}
	}
	req.Status = JoinStatusAdmitted
	req.DecidedAt = time.Now().UTC()
	req.DecidedBy = m.myPK
	if err := m.store.PutJoinRequest(req); err != nil {
		m.log.WithError(err).Debug("group: ApproveJoin: recording decision failed")
	}
	return updated, nil
}

// DenyJoin declines a queued request. The requester is told on its next
// poll and then stops asking.
func (m *Manager) DenyJoin(id string, pk cipher.PubKey) error {
	r, ok, err := m.store.Get(id)
	if err != nil {
		return fmt.Errorf("group: DenyJoin: get: %w", err)
	}
	if !ok {
		return fmt.Errorf("group: DenyJoin: no record for %s", id)
	}
	if !r.IsAdmin(m.myPK) {
		return errors.New("group: DenyJoin: only admins can decline join requests")
	}
	req, found, err := m.store.GetJoinRequest(id, pk)
	if err != nil || !found {
		req = JoinRequest{GroupID: id, PK: pk, AskedAt: time.Now().UTC()}
	}
	req.Status = JoinStatusDenied
	req.DecidedAt = time.Now().UTC()
	req.DecidedBy = m.myPK
	return m.store.PutJoinRequest(req)
}

// RemoveMember evicts a peer from the roster — the kick the gossip
// protocol has always defined (RosterOpRemove) but that nothing called.
//
// A kick is not a ban: the peer may ask to rejoin, and in an open group
// will be readmitted immediately. Use BanMember when the intent is that
// they stay out.
func (m *Manager) RemoveMember(id string, pk cipher.PubKey) (Record, error) {
	r, ok, err := m.store.Get(id)
	if err != nil {
		return Record{}, fmt.Errorf("group: RemoveMember: get: %w", err)
	}
	if !ok {
		return Record{}, fmt.Errorf("group: RemoveMember: no record for %s", id)
	}
	if !r.IsAdmin(m.myPK) {
		return Record{}, errors.New("group: RemoveMember: only admins can edit roster")
	}
	if r.IsFounder(pk) {
		return Record{}, errors.New("group: RemoveMember: cannot remove the founder")
	}
	if !containsPK(r.Members, pk) {
		return r, nil // already gone — idempotent
	}
	r.Members = removePK(r.Members, pk)
	// A removed peer keeping admin authority could re-add itself on the
	// next gossip round, so the eviction has to take the authority too.
	r.Admins = removePK(r.Admins, pk)
	r.EnsureFounderInAdmins()
	if err := m.store.Put(r); err != nil {
		return Record{}, fmt.Errorf("group: RemoveMember: store: %w", err)
	}
	m.applyRosterToSession(id, r, pk)
	// A kick is weaker than a ban — the peer may ask to rejoin — but it
	// still ends their read access now, and the key they hold would
	// otherwise keep working until they did. Rotate for the same reason a
	// ban does; if they come back, admission hands them the new key.
	m.rotateAfterEviction(id, r, pk, "kick")
	return r, nil
}

// applyRosterToSession pushes a roster change onto the live session and
// emits the gossip. evicted may be the zero PK when nothing was removed.
func (m *Manager) applyRosterToSession(id string, r Record, evicted cipher.PubKey) {
	m.mu.RLock()
	sess, live := m.sessions[id]
	m.mu.RUnlock()
	if !live {
		return
	}
	dropped, err := sess.SetAllowlist(r.Members)
	if err != nil {
		m.log.WithError(err).WithField("id", id).Debug("group: SetAllowlist after roster change failed")
	}
	for _, pk := range dropped {
		m.peerReconnectClear(id, pk)
	}
	if _, err := sess.SetAdminRoster(r.Admins); err != nil {
		m.log.WithError(err).WithField("id", id).Debug("group: SetAdminRoster after roster change failed")
	}
	if evicted != (cipher.PubKey{}) {
		m.peerReconnectClear(id, evicted)
		if _, err := sess.PublishRosterMutation(RosterOpRemove, evicted, 0); err != nil {
			m.log.WithError(err).WithField("id", id).WithField("peer", evicted.String()).
				Debug("group: roster-remove gossip emit failed; local state still consistent")
		}
	}
}

// BanMember bars a peer: removes them from the roster (taking their
// allowlist seat, which is what makes a ban structural) and records the
// ban so they cannot walk back in through the join gate.
func (m *Manager) BanMember(id string, pk cipher.PubKey) (Record, error) {
	return m.moderate(id, pk, ModOpBan, "BanMember", func(r *Record) bool {
		changed := false
		if !containsPK(r.Banned, pk) {
			r.Banned = append(r.Banned, pk)
			changed = true
		}
		if containsPK(r.Members, pk) {
			r.Members = removePK(r.Members, pk)
			changed = true
		}
		if containsPK(r.Admins, pk) {
			r.Admins = removePK(r.Admins, pk)
			changed = true
		}
		r.EnsureFounderInAdmins()
		return changed
	})
}

// UnbanMember lifts a ban. Does not re-admit: the peer must ask to join
// again, so the group's admission policy applies to their return the
// same way it applied to their arrival.
func (m *Manager) UnbanMember(id string, pk cipher.PubKey) (Record, error) {
	return m.moderate(id, pk, ModOpUnban, "UnbanMember", func(r *Record) bool {
		if !containsPK(r.Banned, pk) {
			return false
		}
		r.Banned = removePK(r.Banned, pk)
		return true
	})
}

// MuteMember restricts a peer from posting while leaving their read
// access intact. Forward-only: their existing messages stay visible.
func (m *Manager) MuteMember(id string, pk cipher.PubKey) (Record, error) {
	return m.moderate(id, pk, ModOpMute, "MuteMember", func(r *Record) bool {
		if containsPK(r.Muted, pk) {
			return false
		}
		r.Muted = append(r.Muted, pk)
		if r.MutedSince == nil {
			r.MutedSince = make(map[string]time.Time, 1)
		}
		r.MutedSince[pk.Hex()] = time.Now().UTC()
		return true
	})
}

// UnmuteMember lifts an individual posting restriction.
func (m *Manager) UnmuteMember(id string, pk cipher.PubKey) (Record, error) {
	return m.moderate(id, pk, ModOpUnmute, "UnmuteMember", func(r *Record) bool {
		if !containsPK(r.Muted, pk) {
			return false
		}
		r.Muted = removePK(r.Muted, pk)
		delete(r.MutedSince, pk.Hex())
		return true
	})
}

// SetReadOnly suspends (or resumes) posting for every non-admin — the
// reversible "quiet the room" control.
func (m *Manager) SetReadOnly(id string, readOnly bool) (Record, error) {
	op := ModOpReadWrite
	if readOnly {
		op = ModOpReadOnly
	}
	return m.moderate(id, cipher.PubKey{}, op, "SetReadOnly", func(r *Record) bool {
		if r.ReadOnly == readOnly {
			return false
		}
		r.ReadOnly = readOnly
		if readOnly {
			r.ReadOnlySince = time.Now().UTC()
		} else {
			r.ReadOnlySince = time.Time{}
		}
		return true
	})
}

// SetJoinPoW sets how much proof of work this group asks of a join
// request, in leading zero bits. Zero turns the price off entirely.
//
// Admin-only. Local policy rather than gossiped state — see
// Record.JoinPoWBits for why converging it would buy nothing — so it
// takes effect on requests THIS visor answers, and on invites this visor
// mints. Raising it is the lever for a group under active flood; every
// link already in circulation understates the price, and those joiners
// get one extra round trip (a challenge) rather than a refusal.
func (m *Manager) SetJoinPoW(id string, bits uint8) (Record, error) {
	r, ok, err := m.store.Get(id)
	if err != nil {
		return Record{}, fmt.Errorf("group: SetJoinPoW: get: %w", err)
	}
	if !ok {
		return Record{}, fmt.Errorf("group: SetJoinPoW: no record for %s", id)
	}
	if !r.IsAdmin(m.myPK) {
		return Record{}, errors.New("group: SetJoinPoW: only admins can set the join cost")
	}
	if bits > MaxJoinPoWBits {
		return Record{}, fmt.Errorf("group: SetJoinPoW: %d bits exceeds the %d-bit cap; a higher price would burn a joiner's CPU for minutes", bits, MaxJoinPoWBits)
	}
	r.JoinPoWBits = bits
	r.JoinPoWConfigured = true
	if err := m.store.Put(r); err != nil {
		return Record{}, fmt.Errorf("group: SetJoinPoW: store: %w", err)
	}
	m.log.WithField("id", id).WithField("bits", bits).Info("group: join proof-of-work difficulty set")
	return r, nil
}

// SetPeerBackfill decides whether any online member may serve this
// group's history and messages to a joiner, or only admins.
//
// Admin-only and group-wide, converging to every member through the same
// signed gossip as the read-only toggle. Turning it ON is what makes a
// group readable while its admins are offline; turning it OFF restores the
// admins-only topology, at the cost of the group going dark whenever no
// admin is up. See Record.PeerBackfillDisabled for the full trade.
//
// The live session's subscription set is re-evaluated either way, so the
// change takes effect now rather than at the next restart.
func (m *Manager) SetPeerBackfill(id string, enabled bool) (Record, error) {
	op := ModOpPeerBackfillOff
	if enabled {
		op = ModOpPeerBackfillOn
	}
	r, err := m.moderate(id, cipher.PubKey{}, op, "SetPeerBackfill", func(r *Record) bool {
		if r.PeerBackfillEnabled() == enabled {
			return false
		}
		r.PeerBackfillDisabled = !enabled
		return true
	})
	if err != nil {
		return Record{}, err
	}
	// moderate() pushed the new ModState onto the session, which is what
	// the subscription rule reads; ask it to re-apply that rule now.
	m.mu.RLock()
	sess, live := m.sessions[id]
	m.mu.RUnlock()
	if live {
		// Async: this opens or closes peer subscriptions and dials each
		// new one, which would otherwise hold the admin's RPC call open
		// for the dial timeout per peer.
		admins := append([]cipher.PubKey(nil), r.Admins...)
		go func() {
			if _, sErr := sess.SetAdminRoster(admins); sErr != nil {
				m.log.WithError(sErr).WithField("id", id).
					Debug("group: SetPeerBackfill: peer-sub re-evaluation failed; takes effect on next roster change or restart")
			}
		}()
	}
	return r, nil
}

// moderate is the shared body of every moderation command: authorize,
// apply the caller's mutation, persist, push to the live session, emit
// the signed gossip.
//
// mutate reports whether it changed anything; a no-op short-circuits
// before the store write so repeated commands don't churn the feed with
// redundant mutations.
func (m *Manager) moderate(id string, pk cipher.PubKey, op ModOp, what string, mutate func(*Record) bool) (Record, error) {
	r, ok, err := m.store.Get(id)
	if err != nil {
		return Record{}, fmt.Errorf("group: %s: get: %w", what, err)
	}
	if !ok {
		return Record{}, fmt.Errorf("group: %s: no record for %s", what, id)
	}
	if !r.IsAdmin(m.myPK) {
		return Record{}, fmt.Errorf("group: %s: only admins can moderate", what)
	}
	// Founder immunity, matching applyModLeaf's receive-side rule. A
	// promoted admin must not be able to silence the founder — that
	// would be a group-takeover primitive rather than moderation.
	if (op == ModOpBan || op == ModOpMute) && r.IsFounder(pk) {
		return Record{}, fmt.Errorf("group: %s: %w", what, ErrGossipBanFounder)
	}
	if !op.isGroupScoped() && pk == (cipher.PubKey{}) {
		return Record{}, fmt.Errorf("group: %s: peer pk required", what)
	}
	if !mutate(&r) {
		return r, nil // idempotent no-op
	}
	if err := m.store.Put(r); err != nil {
		return Record{}, fmt.Errorf("group: %s: store: %w", what, err)
	}

	m.mu.RLock()
	sess, live := m.sessions[id]
	m.mu.RUnlock()
	if !live {
		return r, nil
	}
	sess.SetModeration(modStateOf(r))
	// A ban shrank the roster, so the allowlist has to follow or the
	// banned PK keeps the CXO seat that the ban is meant to revoke.
	if op == ModOpBan {
		m.applyRosterToSession(id, r, pk)
	}
	if _, err := sess.PublishModMutation(op, pk, 0); err != nil {
		m.log.WithError(err).WithField("id", id).WithField("op", int(op)).
			Debug("group: moderation gossip emit failed; local state still consistent")
	}
	// Losing the seat is not the same as losing the ability to read: the
	// banned PK has held the group key since the day it was admitted, and
	// a copy of the feed keeps opening with it. Rotate so "banned" means
	// cut off. Last, after the roster shrank and the ban gossiped, so the
	// new key is sealed only to the members that remain.
	if op == ModOpBan {
		m.rotateAfterEviction(id, r, pk, "ban")
	}
	return r, nil
}

// RequestJoin asks a group for admission and acts on the answer. The
// member-side entry point that Join delegates to.
//
// Outcomes:
//   - admitted → record persisted active, session opened and connected
//   - pending  → record persisted awaiting-approval, NO session (we have
//     no allowlist seat, so a subscribe would just be refused); the
//     pending-join loop re-asks until the answer changes
//   - denied / banned → terminal error, nothing persisted
//   - no response → the group runs a build without admission control;
//     fall back to the legacy "persist and try to subscribe" path so
//     existing groups keep working
//
// The request goes to every admin the invite names, not just the founder
// — see Invite.Admins for why that stopped being good enough.
func (m *Manager) RequestJoin(inv Invite, note string) (Record, error) {
	if inv.OwnerPK == m.myPK {
		return Record{}, errors.New("group: RequestJoin: refusing to join own group")
	}
	if existing, ok, err := m.store.Get(inv.ID); err == nil && ok && existing.Status == StatusActive {
		return existing, nil // already in — idempotent
	}
	targets := inv.AdmissionTargets(m.myPK)
	if len(targets) == 0 {
		return Record{}, errors.New("group: RequestJoin: invite names nobody who can admit us")
	}

	ctx, cancel := context.WithTimeout(context.Background(), joinAttemptBudget(len(targets)))
	defer cancel()
	resp, err := SendJoinRequestAny(ctx, m.dmsgC, inv.ID, targets, inv.Port, m.myPK, note, inv.PoWBits)
	if err != nil {
		if errors.Is(err, ErrJoinNoResponse) {
			m.log.WithField("id", inv.ID).
				Info("group: join: no admission response; falling back to legacy direct-subscribe join")
			return m.legacyJoin(inv)
		}
		return Record{}, err
	}

	switch resp.Status {
	case JoinStatusDenied, JoinStatusBanned:
		return Record{}, errForStatus(resp.Status, resp.Reason)

	case JoinStatusUnavailable:
		// Only reachable on the single-candidate path (the fan-out folds
		// this into ErrJoinNoAdmin itself): the one PK we could ask holds
		// no authority over the group, or no longer holds it at all.
		// Retryable, not a refusal.
		return Record{}, fmt.Errorf("%w: %s", ErrJoinNoAdmin, resp.Reason)

	case JoinStatusPending:
		r := m.recordFromInvite(inv, resp)
		r.Status = StatusAwaitingApproval
		if err := m.store.Put(r); err != nil {
			return Record{}, fmt.Errorf("group: RequestJoin: store: %w", err)
		}
		return r, nil

	case JoinStatusAdmitted:
		r := m.recordFromInvite(inv, resp)
		r.Status = StatusPending
		if r.Encrypted() && len(r.AESKey) != 32 {
			return Record{}, fmt.Errorf("group: RequestJoin: admitted to an encrypted group without a key")
		}
		if err := m.store.Put(r); err != nil {
			return Record{}, fmt.Errorf("group: RequestJoin: store: %w", err)
		}
		return m.openAdmitted(r)
	}
	return Record{}, fmt.Errorf("group: RequestJoin: unexpected status %q", resp.Status)
}

// recordFromInvite builds the member-side Record, preferring the
// response's authoritative values over the invite's possibly-stale ones.
func (m *Manager) recordFromInvite(inv Invite, resp JoinResponseMsg) Record {
	name := inv.Name
	if resp.Name != "" {
		name = resp.Name
	}
	members := resp.Members
	if len(members) == 0 {
		members = []cipher.PubKey{inv.OwnerPK, m.myPK}
	} else if !containsPK(members, m.myPK) {
		members = append(append([]cipher.PubKey(nil), members...), m.myPK)
	}
	// The key comes from the response for groups using key-on-approval,
	// and from the link for legacy invites that still carry one.
	key := resp.AESKey
	if len(key) == 0 {
		key = inv.AESKey
	}
	r := Record{
		ID:      inv.ID,
		Name:    name,
		OwnerPK: inv.OwnerPK,
		Admins:  resp.Admins,
		// Remember who the link said could let us in, so a queued
		// request keeps re-asking all of them and not just the founder.
		// Kept out of Admins on purpose — see Record.AdmissionPKs.
		AdmissionPKs:         append([]cipher.PubKey(nil), inv.Admins...),
		Port:                 inv.Port,
		Mode:                 inv.Mode,
		Kind:                 kindForMode(inv.Mode),
		AESKey:               key,
		KeyEpoch:             resp.KeyEpoch,
		Members:              members,
		Muted:                resp.Muted,
		ReadOnly:             resp.ReadOnly,
		PeerBackfillDisabled: resp.PeerBackfillDisabled,
		Role:                 RoleMember,
		CreatedAt:            time.Now().UTC(),
		JoinedAt:             time.Now().UTC(),
	}
	r.EnsureFounderInAdmins()
	return r
}

// openAdmitted brings up the session for a freshly-admitted member and
// flips the record active. Mirrors the tail of the legacy Join: a
// connect that doesn't complete in the join budget is NOT fatal, because
// the reconnect watchdog attaches the feed in the background and the
// room should appear immediately.
func (m *Manager) openAdmitted(r Record) (Record, error) {
	sess, err := m.openLocked(r)
	if err != nil {
		_ = m.store.Delete(r.ID) //nolint:errcheck
		return Record{}, err
	}
	joinCtx, cancel := context.WithTimeout(context.Background(), reconnectAttemptTimeout)
	defer cancel()
	if err := sess.Connect(joinCtx); err != nil {
		if isSubscribeRejected(err) {
			// Admitted by the relay but refused by the publisher: the
			// allowlist hasn't picked up the roster change yet. Keep the
			// membership and let the watchdog retry — tearing down here
			// would strand a member the group has already accepted.
			m.log.WithError(err).WithField("id", r.ID).
				Info("group: join: admitted but subscribe not yet authorized; watchdog will retry")
		} else {
			m.log.WithError(err).WithField("id", r.ID).
				Info("group: join: initial connect not ready; joined optimistically")
		}
	}
	_ = m.store.SetStatus(r.ID, StatusActive) //nolint:errcheck
	r.Status = StatusActive
	return r, nil
}

// legacyJoin is the pre-admission-control path, kept for groups whose
// owner runs an older build: persist the invite as a member record and
// try to subscribe directly. Works only when an admin already ran
// AddMember for this PK out of band, which was the sole way to join
// before this feature existed.
func (m *Manager) legacyJoin(inv Invite) (Record, error) {
	if inv.Mode == ModePrivate && len(inv.AESKey) != 32 {
		return Record{}, errors.New("group: join: private group invite carries no key and the group did not answer the join request")
	}
	r := Record{
		ID:        inv.ID,
		Name:      inv.Name,
		OwnerPK:   inv.OwnerPK,
		Port:      inv.Port,
		Mode:      inv.Mode,
		Kind:      kindForMode(inv.Mode),
		AESKey:    inv.AESKey,
		Members:   []cipher.PubKey{inv.OwnerPK, m.myPK},
		Role:      RoleMember,
		Status:    StatusPending,
		CreatedAt: time.Now().UTC(),
		JoinedAt:  time.Now().UTC(),
	}
	if err := m.store.Put(r); err != nil {
		return Record{}, fmt.Errorf("group: Join: store: %w", err)
	}
	sess, err := m.openLocked(r)
	if err != nil {
		_ = m.store.Delete(r.ID) //nolint:errcheck
		return Record{}, err
	}
	joinCtx, cancel := context.WithTimeout(context.Background(), reconnectAttemptTimeout)
	defer cancel()
	if err := sess.Connect(joinCtx); err != nil {
		if isSubscribeRejected(err) {
			_ = sess.Close()         //nolint:errcheck
			_ = m.store.Delete(r.ID) //nolint:errcheck
			return Record{}, fmt.Errorf("group: Join: connect: %w", err)
		}
		m.log.WithError(err).WithField("id", r.ID).
			Info("group: Join: initial connect not ready; joined optimistically — reconnect watchdog will attach the feed")
	}
	_ = m.store.SetStatus(r.ID, StatusActive) //nolint:errcheck
	r.Status = StatusActive
	return r, nil
}

// retryPendingJoins re-asks every group that has us queued for approval.
// Driven by the reconnect loop, which already ticks on a timer and
// already owns the "keep trying in the background" responsibility.
//
// Records move out of awaiting-approval on their own: an admitted answer
// opens the session and flips to active, a refusal flips to a terminal
// status and stops the retries.
func (m *Manager) retryPendingJoins(ctx context.Context) {
	records, err := m.store.List()
	if err != nil {
		return
	}
	for _, r := range records {
		if r.Status != StatusAwaitingApproval {
			continue
		}
		if ctx.Err() != nil {
			return
		}
		if !m.pendingJoinShouldAttempt(r.ID) {
			continue
		}
		targets := r.AdmissionTargets(m.myPK)
		if len(targets) == 0 {
			continue
		}
		resp, err := SendJoinRequestAny(ctx, m.dmsgC, r.ID, targets, r.Port, m.myPK, "", r.JoinPoWBits)
		if err != nil {
			if terminal := errIsTerminalJoin(err); terminal {
				_ = m.store.SetStatus(r.ID, StatusDenied) //nolint:errcheck
			}
			// Deliberately NOT terminal: every admin we know of is either
			// unreachable or says the group isn't theirs to rule on, and
			// any of those can change. Logged because the record otherwise
			// sits in awaiting-approval indefinitely with nothing to
			// distinguish "nobody has decided yet" from "there is nobody
			// left to decide" — the group's last admin may be gone for
			// good, in which case the operator wants to Leave it.
			if errors.Is(err, ErrJoinNoAdmin) {
				m.log.WithField("id", r.ID).WithField("asked", len(targets)).
					Info("group: join: no reachable admin can rule on our request; still waiting")
			}
			continue
		}
		switch resp.Status {
		case JoinStatusAdmitted:
			admitted := r
			if len(resp.Members) > 0 {
				admitted.Members = resp.Members
			}
			if len(resp.Admins) > 0 {
				admitted.Admins = resp.Admins
			}
			if len(resp.AESKey) > 0 {
				admitted.AESKey = resp.AESKey
				admitted.KeyEpoch = resp.KeyEpoch
			}
			admitted.Muted = resp.Muted
			admitted.ReadOnly = resp.ReadOnly
			admitted.PeerBackfillDisabled = resp.PeerBackfillDisabled
			admitted.EnsureFounderInAdmins()
			admitted.Status = StatusPending
			if admitted.Encrypted() && len(admitted.AESKey) != 32 {
				m.log.WithField("id", r.ID).
					Warn("group: join: admitted to an encrypted group without a key; staying pending")
				continue
			}
			if err := m.store.Put(admitted); err != nil {
				continue
			}
			if _, err := m.openAdmitted(admitted); err != nil {
				m.log.WithError(err).WithField("id", r.ID).
					Warn("group: join: approved but session open failed")
			} else {
				m.log.WithField("id", r.ID).Info("group: join: approved by admin; group is now active")
			}
		case JoinStatusDenied:
			_ = m.store.SetStatus(r.ID, StatusDenied) //nolint:errcheck
			m.log.WithField("id", r.ID).Info("group: join: request declined by admin")
		case JoinStatusBanned:
			_ = m.store.SetStatus(r.ID, StatusBanned) //nolint:errcheck
			m.log.WithField("id", r.ID).Info("group: join: banned from group")
		case JoinStatusPending:
			// Still waiting; ask again next tick.
		}
	}
}

// pendingJoinShouldAttempt rate-limits re-asks to
// pendingJoinRetryInterval regardless of how fast the reconnect loop
// ticks, so shortening that loop for connection health doesn't turn
// into hammering an admin's approval queue.
func (m *Manager) pendingJoinShouldAttempt(id string) bool {
	m.reconnectMu.Lock()
	defer m.reconnectMu.Unlock()
	if m.pendingJoinLast == nil {
		m.pendingJoinLast = make(map[string]time.Time)
	}
	now := time.Now()
	if last, ok := m.pendingJoinLast[id]; ok && now.Sub(last) < pendingJoinRetryInterval {
		return false
	}
	m.pendingJoinLast[id] = now
	return true
}
