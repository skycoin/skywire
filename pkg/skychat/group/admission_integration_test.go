// Package group pkg/skychat/group/admission_integration_test.go
//
// The dmsg-backed lane for admission control: the join round trip that
// makes an invite link sufficient on its own, the approval queue, and
// the ban that closes the door again.
//
// These are the cases that cannot be reached without a real transport,
// because the requester's identity is taken from the authenticated dmsg
// stream rather than from anything in the payload.
//
// Reuses the harness in manager_integration_test.go; skipped under
// -short like every other dmsg-backed test in this package.
package group

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A public group's invite link must be sufficient on its own. Before
// admission control the joiner had to be added out of band first — the
// link alone always failed at the publisher's allowlist — so this is
// the headline behavior of the feature.
func TestAdmission_PublicGroupJoinsWithLinkAlone(t *testing.T) {
	a, b := newGroupEnv(t)

	rec, err := a.mgr.Create("open room", KindPublic, nil)
	require.NoError(t, err, "Create")
	require.NotContains(t, rec.Members, b.pk, "B must NOT be pre-admitted for this test to mean anything")

	link, err := a.mgr.BuildInvite(rec.ID)
	require.NoError(t, err, "BuildInvite")
	inv, err := DecodeInvite(link)
	require.NoError(t, err, "DecodeInvite")

	joined, err := b.mgr.Join(inv)
	require.NoError(t, err, "Join with only the invite link")
	require.Equal(t, StatusActive, joined.Status)
	require.Equal(t, RoleMember, joined.Role)

	// The admission must have written B into the owner's roster —
	// otherwise nobody subscribes to B's feed and B is mute in practice.
	owner, ok, err := a.mgr.Get(rec.ID)
	require.NoError(t, err)
	require.True(t, ok)
	require.Contains(t, owner.Members, b.pk, "auto-admit did not add the joiner to the roster")

	// And the joiner learned the real roster from the response rather
	// than the invite's two-PK guess.
	require.Contains(t, joined.Members, a.pk)
	require.Contains(t, joined.Members, b.pk)
}

// A private group queues the request instead of admitting, and hands
// over the AES key only once an admin approves. The key must NOT be in
// the invite link — that is what makes a forwarded link inert.
func TestAdmission_PrivateGroupRequiresApproval(t *testing.T) {
	a, b := newGroupEnv(t)

	rec, err := a.mgr.Create("private room", KindPrivate, nil)
	require.NoError(t, err, "Create")
	require.Len(t, rec.AESKey, 32, "a private group must have a key")

	link, err := a.mgr.BuildInvite(rec.ID)
	require.NoError(t, err, "BuildInvite")
	inv, err := DecodeInvite(link)
	require.NoError(t, err, "DecodeInvite")

	joined, err := b.mgr.Join(inv)
	require.NoError(t, err, "Join should queue, not fail")
	require.Equal(t, StatusAwaitingApproval, joined.Status,
		"a private group must not admit on the strength of a link")

	// The request is on A's queue.
	reqs, err := a.mgr.PendingJoins(rec.ID)
	require.NoError(t, err, "PendingJoins")
	require.Len(t, reqs, 1, "expected exactly one queued request")
	require.Equal(t, b.pk, reqs[0].PK, "the queue must name the AUTHENTICATED requester")
	require.True(t, reqs[0].IsPending())

	// B is not in the roster and holds no key while it waits.
	pending, ok, err := b.mgr.Get(rec.ID)
	require.NoError(t, err)
	require.True(t, ok)
	require.Empty(t, pending.AESKey, "an unapproved requester must not hold the group key")

	// Approve, then let B's retry loop pick it up.
	updated, err := a.mgr.ApproveJoin(rec.ID, b.pk)
	require.NoError(t, err, "ApproveJoin")
	require.Contains(t, updated.Members, b.pk)

	waitStatus(t, b, rec.ID, StatusActive, "B never became active after approval")

	admitted, ok, err := b.mgr.Get(rec.ID)
	require.NoError(t, err)
	require.True(t, ok)
	require.Len(t, admitted.AESKey, 32, "the group key must arrive with the approval")
	require.Equal(t, rec.AESKey, admitted.AESKey, "the delivered key must be the group's key")
}

// A declined request is terminal for the requester: it stops asking
// rather than re-queuing every 30s, which would be indistinguishable
// from harassment on the admin's list.
func TestAdmission_DeniedRequestIsTerminal(t *testing.T) {
	a, b := newGroupEnv(t)

	rec, err := a.mgr.Create("private room", KindPrivate, nil)
	require.NoError(t, err)
	inv := inviteFor(t, a, rec.ID)

	joined, err := b.mgr.Join(inv)
	require.NoError(t, err)
	require.Equal(t, StatusAwaitingApproval, joined.Status)

	require.NoError(t, a.mgr.DenyJoin(rec.ID, b.pk), "DenyJoin")

	waitStatus(t, b, rec.ID, StatusDenied, "B never observed the denial")

	// Asking again gets the same answer rather than re-queueing.
	_, err = b.mgr.RequestJoin(inv, "please?")
	require.Error(t, err, "a declined requester must not be able to re-queue")

	reqs, err := a.mgr.PendingJoins(rec.ID)
	require.NoError(t, err)
	for _, q := range reqs {
		require.False(t, q.IsPending(), "the declined request came back as pending")
	}
}

// A ban must close the door even on an OPEN group, where the join gate
// would otherwise admit anyone who asks.
func TestAdmission_BannedPeerCannotRejoinOpenGroup(t *testing.T) {
	a, b := newGroupEnv(t)

	rec, err := a.mgr.Create("open room", KindPublic, nil)
	require.NoError(t, err)
	inv := inviteFor(t, a, rec.ID)

	_, err = b.mgr.Join(inv)
	require.NoError(t, err, "first join")

	banned, err := a.mgr.BanMember(rec.ID, b.pk)
	require.NoError(t, err, "BanMember")
	require.True(t, banned.IsBanned(b.pk))
	require.NotContains(t, banned.Members, b.pk, "a ban must take the roster seat too")

	// Clear B's local record so it genuinely re-asks rather than
	// short-circuiting on "already active".
	require.NoError(t, b.mgr.Leave(rec.ID))
	require.NoError(t, b.store.Delete(rec.ID))

	_, err = b.mgr.RequestJoin(inv, "")
	require.Error(t, err, "a banned PK walked back into an open group")
	require.ErrorIs(t, err, ErrJoinBanned)
}

// Kicking is not banning: a removed peer may come back through the
// normal door, which in an open group means immediately.
func TestAdmission_RemovedPeerMayRejoin(t *testing.T) {
	a, b := newGroupEnv(t)

	rec, err := a.mgr.Create("open room", KindPublic, nil)
	require.NoError(t, err)
	inv := inviteFor(t, a, rec.ID)

	_, err = b.mgr.Join(inv)
	require.NoError(t, err)

	removed, err := a.mgr.RemoveMember(rec.ID, b.pk)
	require.NoError(t, err, "RemoveMember")
	require.NotContains(t, removed.Members, b.pk)
	require.False(t, removed.IsBanned(b.pk), "a kick must not imply a ban")

	require.NoError(t, b.mgr.Leave(rec.ID))
	require.NoError(t, b.store.Delete(rec.ID))

	rejoined, err := b.mgr.RequestJoin(inv, "")
	require.NoError(t, err, "a merely-removed peer should be able to rejoin an open group")
	require.Equal(t, StatusActive, rejoined.Status)
}

// The founder is the record's recovery anchor: a second admin must not
// be able to ban, mute or remove them and take the group.
func TestAdmission_FounderIsProtected(t *testing.T) {
	a, b := newGroupEnv(t)

	rec, err := a.mgr.Create("open room", KindPublic, nil)
	require.NoError(t, err)
	inv := inviteFor(t, a, rec.ID)
	_, err = b.mgr.Join(inv)
	require.NoError(t, err)

	// A promotes B, then B tries to take over.
	_, err = a.mgr.PromoteAdmin(rec.ID, b.pk)
	require.NoError(t, err, "PromoteAdmin")
	waitMember(t, b, rec.ID, func(r Record) bool { return r.IsAdmin(b.pk) }, "B never saw its own promotion")

	_, err = b.mgr.BanMember(rec.ID, a.pk)
	require.Error(t, err, "an admin banned the founder")
	require.ErrorIs(t, err, ErrGossipBanFounder)

	_, err = b.mgr.MuteMember(rec.ID, a.pk)
	require.Error(t, err, "an admin muted the founder")

	_, err = b.mgr.RemoveMember(rec.ID, a.pk)
	require.Error(t, err, "an admin removed the founder")
}

// The founder must not be a single point of failure in admission. Every
// invite used to point join requests at OwnerPK alone: founder offline
// meant nobody could join, and a founder whose key was lost meant the
// group could never admit anyone again — with other admins sitting there
// authorized and unreachable.
//
// A (founder) creates and promotes B, then goes away for good. C, holding
// nothing but the link, has to get in through B.
func TestAdmission_SecondAdminAdmitsWhileFounderIsOffline(t *testing.T) {
	nodes := newGroupEnvN(t, 3)
	a, b, c := nodes[0], nodes[1], nodes[2]

	rec, err := a.mgr.Create("resilient room", KindPublic, nil)
	require.NoError(t, err, "Create")

	// B joins and is promoted, so the group has two admins.
	_, err = b.mgr.Join(inviteFor(t, a, rec.ID))
	require.NoError(t, err, "B joins")
	_, err = a.mgr.PromoteAdmin(rec.ID, b.pk)
	require.NoError(t, err, "PromoteAdmin")
	waitMember(t, b, rec.ID, func(r Record) bool { return r.IsAdmin(b.pk) }, "B never saw its own promotion")

	// The link B hands out names B as well as the founder.
	inv := inviteFor(t, b, rec.ID)
	require.Contains(t, inv.Admins, b.pk, "an admin's invite must name that admin")
	require.Equal(t, a.pk, inv.OwnerPK)

	// The founder goes dark: sessions closed, relay listener gone.
	require.NoError(t, a.mgr.Close(), "closing the founder's manager")

	// The pre-fix shape of the same link — founder only — is exactly the
	// failure this change is about, so pin it: C cannot get in.
	founderOnly := inv
	founderOnly.Admins = nil
	_, err = c.mgr.RequestJoin(founderOnly, "")
	require.Error(t, err, "a founder-only invite must fail while the founder is offline")

	// Naming B makes the same group joinable.
	joined, err := c.mgr.RequestJoin(inv, "let me in")
	require.NoError(t, err, "C could not join through the second admin")
	require.Equal(t, StatusActive, joined.Status)
	require.Equal(t, RoleMember, joined.Role)

	// B did the admitting: its roster carries C, which is what makes the
	// membership real rather than local to C.
	onB, ok, err := b.mgr.Get(rec.ID)
	require.NoError(t, err)
	require.True(t, ok)
	require.Contains(t, onB.Members, c.pk, "the admitting admin did not add C to the roster")

	// And C learned the group's real admin set from the response — the
	// founder included, so a returning founder is still the anchor.
	require.Contains(t, joined.Admins, a.pk)
	require.Contains(t, joined.Admins, b.pk)
}

// A private group's approval queue has to be reachable through a second
// admin too, or "founder offline" merely moves from "cannot join" to
// "cannot be approved".
func TestAdmission_SecondAdminApprovesWhileFounderIsOffline(t *testing.T) {
	nodes := newGroupEnvN(t, 3)
	a, b, c := nodes[0], nodes[1], nodes[2]

	rec, err := a.mgr.Create("private resilient room", KindPrivate, nil)
	require.NoError(t, err, "Create")

	_, err = b.mgr.Join(inviteFor(t, a, rec.ID))
	require.NoError(t, err, "B joins")
	waitStatus(t, b, rec.ID, StatusAwaitingApproval, "B never queued")
	_, err = a.mgr.ApproveJoin(rec.ID, b.pk)
	require.NoError(t, err, "ApproveJoin B")
	waitStatus(t, b, rec.ID, StatusActive, "B never became active")

	_, err = a.mgr.PromoteAdmin(rec.ID, b.pk)
	require.NoError(t, err, "PromoteAdmin")
	waitMember(t, b, rec.ID, func(r Record) bool { return r.IsAdmin(b.pk) }, "B never saw its own promotion")

	inv := inviteFor(t, b, rec.ID)
	require.Empty(t, inv.AESKey, "the link must stay keyless")
	require.NoError(t, a.mgr.Close(), "closing the founder's manager")

	// C's request lands on B's queue rather than nowhere.
	queued, err := c.mgr.RequestJoin(inv, "hi")
	require.NoError(t, err, "RequestJoin should queue, not fail")
	require.Equal(t, StatusAwaitingApproval, queued.Status)
	require.Equal(t, inv.Admins, queued.AdmissionPKs,
		"the record must remember who else can be asked, or the re-ask goes back to dialing only the founder")

	reqs, err := b.mgr.PendingJoins(rec.ID)
	require.NoError(t, err, "PendingJoins on the second admin")
	require.Len(t, reqs, 1, "the request did not reach the second admin's queue")
	require.Equal(t, c.pk, reqs[0].PK)

	// B approves, and C's retry loop — which must be re-asking B, not the
	// offline founder — picks it up along with the group key.
	_, err = b.mgr.ApproveJoin(rec.ID, c.pk)
	require.NoError(t, err, "ApproveJoin C")
	waitStatus(t, c, rec.ID, StatusActive, "C never became active after the second admin approved")

	admitted, ok, err := c.mgr.Get(rec.ID)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, rec.AESKey, admitted.AESKey, "the second admin must hand over the group key too")
}

// A visor that holds no authority answers "ask someone else" rather than
// "no", so one stale name in a link can't sink a join the group's real
// admins would have approved.
func TestAdmission_NonAdminAnswersUnavailable(t *testing.T) {
	a, b := newGroupEnv(t)

	rec, err := a.mgr.Create("open room", KindPublic, nil)
	require.NoError(t, err, "Create")
	_, err = b.mgr.Join(inviteFor(t, a, rec.ID))
	require.NoError(t, err, "B joins as a plain member")

	// Ask the plain member directly — the fan-out would fold this into
	// ErrJoinNoAdmin, so go one level down to see the actual answer. The
	// requester PK has to be A's own: the responder takes the identity
	// from the authenticated stream and refuses a mismatched claim.
	ctx, cancel := context.WithTimeout(t.Context(), joinResponseReadTimeout+5*time.Second)
	defer cancel()
	resp, err := SendJoinRequest(ctx, a.dmsgC, rec.ID, b.pk, rec.Port, a.pk, "")
	require.NoError(t, err, "a member session must answer join requests at all")
	require.Equal(t, JoinStatusUnavailable, resp.Status,
		"a non-admin must not answer a join request with a terminal refusal")
	require.False(t, resp.Status.IsTerminal())
}

// --- helpers ---------------------------------------------------------------

func inviteFor(t *testing.T, n *groupNode, id string) Invite {
	t.Helper()
	link, err := n.mgr.BuildInvite(id)
	require.NoError(t, err, "BuildInvite")
	inv, err := DecodeInvite(link)
	require.NoError(t, err, "DecodeInvite")
	return inv
}

// waitStatus drives the manager's pending-join retry loop until the
// record reaches want. The loop is time-based, so poke it directly
// rather than waiting on the background ticker's cadence.
func waitStatus(t *testing.T, n *groupNode, id string, want Status, msg string) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		r, ok, err := n.mgr.Get(id)
		require.NoError(t, err)
		if ok && r.Status == want {
			return
		}
		// Clear the rate-limiter so each poll actually re-asks.
		n.mgr.reconnectMu.Lock()
		delete(n.mgr.pendingJoinLast, id)
		n.mgr.reconnectMu.Unlock()
		n.mgr.retryPendingJoins(t.Context())
		time.Sleep(200 * time.Millisecond)
	}
	r, _, _ := n.mgr.Get(id) //nolint
	t.Fatalf("%s (status = %q, want %q)", msg, r.Status, want)
}

func waitMember(t *testing.T, n *groupNode, id string, pred func(Record) bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		r, ok, err := n.mgr.Get(id)
		require.NoError(t, err)
		if ok && pred(r) {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal(msg)
}
