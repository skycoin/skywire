// Package group pkg/skychat/group/joincost_integration_test.go
//
// The dmsg-backed lane for anti-flood: a real join round trip that must
// pay a proof of work, and a flood of fresh identities that must stop
// consuming the admin's queue.
//
// Reuses the harness in manager_integration_test.go; skipped under -short.
package group

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

// An ordinary join still works with the price on — the whole feature is
// worthless if it also keeps real people out. The default difficulty is
// what a normal joiner pays without noticing.
func TestJoinCost_HonestJoinerPaysAndGetsIn(t *testing.T) {
	a, b := newGroupEnv(t)

	rec, err := a.mgr.Create("priced room", KindPublic, nil)
	require.NoError(t, err, "Create")
	require.Equal(t, DefaultJoinPoWBits, rec.JoinPoWRequired(),
		"a new group should ask for the default price, not zero")

	// The invite states the price so the joiner pays before it dials.
	inv := inviteFor(t, a, rec.ID)
	require.Equal(t, DefaultJoinPoWBits, inv.PoWBits, "the link must carry the price")

	joined, err := b.mgr.Join(inv)
	require.NoError(t, err, "an honest joiner could not get in")
	require.Equal(t, StatusActive, joined.Status)

	owner, _, err := a.mgr.Get(rec.ID)
	require.NoError(t, err)
	require.Contains(t, owner.Members, b.pk)
}

// A requester whose invite understates the price is told the real one and
// pays it, inside the same call. That is what lets an admin raise the
// price under attack without invalidating every link in circulation.
func TestJoinCost_StaleInviteIsChallengedNotRefused(t *testing.T) {
	a, b := newGroupEnv(t)

	rec, err := a.mgr.Create("priced room", KindPublic, nil)
	require.NoError(t, err)

	// A link minted when the group was free.
	_, err = a.mgr.SetJoinPoW(rec.ID, 0)
	require.NoError(t, err, "SetJoinPoW(0)")
	cheapInv := inviteFor(t, a, rec.ID)
	require.Zero(t, cheapInv.PoWBits)

	// The admin raises the price afterwards.
	updated, err := a.mgr.SetJoinPoW(rec.ID, 14)
	require.NoError(t, err, "SetJoinPoW(14)")
	require.Equal(t, uint8(14), updated.JoinPoWRequired())

	// The stale link still works: the group challenges, the joiner pays.
	joined, err := b.mgr.Join(cheapInv)
	require.NoError(t, err, "a stale invite should be challenged, not refused")
	require.Equal(t, StatusActive, joined.Status)
}

// The headline case: unlimited free identities must stop being able to
// consume the approval queue. Each fake PK is a fresh identity, exactly
// what makes per-PK gates useless on their own.
func TestJoinCost_FloodIsThrottled(t *testing.T) {
	a, b := newGroupEnv(t)

	rec, err := a.mgr.Create("flooded room", KindPrivate, nil)
	require.NoError(t, err, "Create")
	// Price off for this test: it isolates the RATE limit, which is the
	// half that has to hold even when an attacker is willing to pay.
	_, err = a.mgr.SetJoinPoW(rec.ID, 0)
	require.NoError(t, err)

	// Drive the admission handler directly with distinct fresh PKs. Going
	// through the wire would test dmsg's throughput rather than the gate,
	// and the handler is exactly what a flood reaches after authentication.
	handler := a.mgr.admissionHandler(rec.ID)

	var admittedOrQueued, throttled int
	for range DefaultJoinBurst * 5 {
		pk, _ := cipher.GenerateKeyPair()
		resp := handler(JoinRequest{
			GroupID: rec.ID,
			PK:      pk,
			AskedAt: time.Now().UTC(),
			Status:  JoinStatusPending,
		})
		switch resp.Status {
		case JoinStatusThrottled:
			throttled++
		case JoinStatusPending, JoinStatusAdmitted:
			admittedOrQueued++
		default:
			t.Fatalf("unexpected status %q for a fresh requester: %s", resp.Status, resp.Reason)
		}
	}

	require.Positive(t, throttled, "a flood of fresh identities was never throttled")
	require.LessOrEqual(t, admittedOrQueued, DefaultJoinBurst,
		"more requests were absorbed than the burst allows")

	// And the throttled ones cost the admin nothing: they are not stored,
	// so the queue holds only what the bucket let through.
	pending, err := a.mgr.PendingJoins(rec.ID)
	require.NoError(t, err)
	n := 0
	for _, q := range pending {
		if q.IsPending() {
			n++
		}
	}
	require.LessOrEqual(t, n, DefaultJoinBurst,
		"throttled requests were queued anyway — the flood still reached the admin")

	// A real member is unaffected: B was admitted before the flood-facing
	// gates, and a member re-syncing never spends a token.
	_, err = a.mgr.AddMember(rec.ID, b.pk)
	require.NoError(t, err)
	resp := handler(JoinRequest{GroupID: rec.ID, PK: b.pk, AskedAt: time.Now().UTC(), Status: JoinStatusPending})
	require.Equal(t, JoinStatusAdmitted, resp.Status,
		"an existing member was throttled out of re-syncing")
}

// The queue cap is the second wall: even within the rate limit, an
// attacker must not be able to make the pending list unusable.
func TestJoinCost_QueueIsCapped(t *testing.T) {
	a, _ := newGroupEnv(t)

	rec, err := a.mgr.Create("capped room", KindPrivate, nil)
	require.NoError(t, err)
	_, err = a.mgr.SetJoinPoW(rec.ID, 0)
	require.NoError(t, err)

	// Fill the queue past the cap by writing straight to the store, which
	// is what a slow drip through the rate limit would eventually achieve.
	for i := range DefaultMaxPendingJoins + 5 {
		pk, _ := cipher.GenerateKeyPair()
		require.NoError(t, a.store.PutJoinRequest(JoinRequest{
			GroupID: rec.ID, PK: pk, Status: JoinStatusPending,
			AskedAt: time.Now().UTC().Add(-time.Duration(i) * time.Second),
		}))
	}

	handler := a.mgr.admissionHandler(rec.ID)
	pk, _ := cipher.GenerateKeyPair()
	resp := handler(JoinRequest{GroupID: rec.ID, PK: pk, AskedAt: time.Now().UTC(), Status: JoinStatusPending})
	require.Equal(t, JoinStatusThrottled, resp.Status,
		"a request past the queue cap was accepted")

	// Nothing already waiting was displaced: a PK that has been queued has
	// already been paid for, and evicting it for the newest arrival would
	// be the primitive an attacker wants.
	_, found, err := a.store.GetJoinRequest(rec.ID, pk)
	require.NoError(t, err)
	require.False(t, found, "the refused request was stored anyway")
}

// The price is admin-only and bounded — an operator cannot set a value
// that would burn a joiner's CPU for minutes.
func TestJoinCost_SetIsAdminOnlyAndBounded(t *testing.T) {
	a, b := newGroupEnv(t)

	rec, err := a.mgr.Create("priced room", KindPublic, nil)
	require.NoError(t, err)
	_, err = b.mgr.Join(inviteFor(t, a, rec.ID))
	require.NoError(t, err)

	_, err = b.mgr.SetJoinPoW(rec.ID, 20)
	require.Error(t, err, "a plain member set the group's join cost")
	require.Contains(t, err.Error(), "admin")

	_, err = a.mgr.SetJoinPoW(rec.ID, MaxJoinPoWBits+10)
	require.Error(t, err, "an absurd difficulty was accepted")

	off, err := a.mgr.SetJoinPoW(rec.ID, 0)
	require.NoError(t, err)
	require.Zero(t, off.JoinPoWRequired(), "an explicit zero must stick rather than falling back to the default")

	// And it reaches the invite, so joiners learn the new price.
	require.Zero(t, inviteFor(t, a, rec.ID).PoWBits)
}

// A group under attack can be closed to new identities entirely by
// pricing them out, and re-opened afterwards.
func TestJoinCost_RaisingThePriceRejectsUnpaidRequests(t *testing.T) {
	a, _ := newGroupEnv(t)

	rec, err := a.mgr.Create("priced room", KindPublic, nil)
	require.NoError(t, err)
	_, err = a.mgr.SetJoinPoW(rec.ID, 16)
	require.NoError(t, err)

	handler := a.mgr.admissionHandler(rec.ID)
	pk, _ := cipher.GenerateKeyPair()

	// No proof: challenged, and nothing is stored.
	resp := handler(JoinRequest{GroupID: rec.ID, PK: pk, AskedAt: time.Now().UTC(), Status: JoinStatusPending})
	require.Equal(t, JoinStatusChallenge, resp.Status)
	require.Equal(t, uint8(16), resp.PoWBits, "the challenge must name the price")
	owner, _, err := a.mgr.Get(rec.ID)
	require.NoError(t, err)
	require.NotContains(t, owner.Members, pk, "an unpaid requester was admitted")

	// With the proof: in.
	now := time.Now().UTC()
	proof, ok := SolveJoinPoW(rec.ID, pk, 16, now, now.Add(30*time.Second))
	require.True(t, ok, "SolveJoinPoW")
	resp = handler(JoinRequest{GroupID: rec.ID, PK: pk, PoW: proof, AskedAt: now, Status: JoinStatusPending})
	require.Equal(t, JoinStatusAdmitted, resp.Status, "a paid request was not admitted: %s", resp.Reason)
}
