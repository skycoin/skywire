// Package group cmd/apps/skychat/group/manager_integration_test.go
//
// The dmsg-backed integration lane for group.Manager — the half of manager.go
// that cannot be reached without a real transport: Create, Join, AddMember,
// PromoteAdmin, DemoteAdmin, SendToGroup, Resume, ReplayHistory and openLocked.
//
// Manager takes a *dmsg.Client and openLocked hands it straight to group.Open,
// so unlike Session (which also has a native-TCP mode) there is no transport
// seam to substitute. This spins up a 1-server / 2-client dmsgtest env and
// drives two managers against it, mirroring pairing/integration_test.go.
//
// Skipped under -short, matching this repo's convention for dmsg-backed tests
// (pairing/baseline_test.go, pairing/integration_test.go).
//
// Timing note: CXO subscription bring-up over dmsg is eventually consistent —
// see the scope note in session_lifecycle_test.go for the wait-for-root
// behavior that makes it so. Everything here therefore polls with a generous
// budget rather than asserting immediately after an operation returns.
package group

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgtest"
	"github.com/skycoin/skywire/pkg/logging"
)

// --- harness ----------------------------------------------------------------

// groupNode is one visor in the integration group.
type groupNode struct {
	pk    cipher.PubKey
	sk    cipher.SecKey
	dmsgC *dmsg.Client
	store *Store
	dir   string
	mgr   *Manager
	inbox *msgInbox // from session_lifecycle_test.go
}

// newGroupEnv brings up a dmsg env with two managers wired to it.
func newGroupEnv(t *testing.T) (a, b *groupNode) {
	t.Helper()
	nodes := newGroupEnvN(t, 2)
	return nodes[0], nodes[1]
}

// newGroupEnvN is the same harness for n visors, for the cases that need
// a third party — a founder, a second admin, and someone trying to join.
func newGroupEnvN(t *testing.T, n int) []*groupNode {
	t.Helper()
	if testing.Short() {
		t.Skip("dmsg integration test; skipped under -short")
	}

	env := dmsgtest.NewEnv(t, dmsgtest.DefaultTimeout)
	env.Discovery()
	require.NoError(t, env.Startup(dmsgtest.DefaultTimeout, 1, 0, &dmsg.Config{MinSessions: 1}))
	t.Cleanup(env.Shutdown)

	nodes := make([]*groupNode, 0, n)
	for i := 0; i < n; i++ {
		tag := string(rune('A' + i))
		pk, sk := cipher.GenerateKeyPair()
		c, err := env.NewClientWithKeys(pk, sk, &dmsg.Config{MinSessions: 1})
		require.NoError(t, err)
		waitDmsgClientReady(t, c)

		dir := t.TempDir()
		st, serr := OpenStore(filepath.Join(dir, "groups.db"), testStoreSK())
		require.NoError(t, serr)
		t.Cleanup(func() { _ = st.Close() }) //nolint:errcheck

		mgr, merr := NewManager(ManagerConfig{
			Store: st, DmsgC: c, MyPK: pk, MySK: sk,
			DataDir: filepath.Join(dir, "cxo"),
			Logger:  logging.MustGetLogger("group-mgr-" + tag),
		})
		require.NoError(t, merr)
		t.Cleanup(func() { _ = mgr.Close() }) //nolint:errcheck

		box := &msgInbox{}
		mgr.SetMessageHandler(box.deliver)
		nodes = append(nodes, &groupNode{pk: pk, sk: sk, dmsgC: c, store: st, dir: dir, mgr: mgr, inbox: box})
	}
	return nodes
}

func waitDmsgClientReady(t *testing.T, c *dmsg.Client) {
	t.Helper()
	select {
	case <-c.Ready():
	case <-time.After(20 * time.Second):
		t.Fatalf("dmsg client %s never became ready", c.LocalPK().Hex()[:8])
	}
}

// waitInbox polls an inbox until pred holds, or fails.
func waitInbox(t *testing.T, box *msgInbox, what string, pred func([]Message) bool) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if pred(box.snapshot()) {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s; inbox = %+v", what, box.snapshot())
}

func hasText(text string) func([]Message) bool {
	return func(msgs []Message) bool {
		for _, m := range msgs {
			if m.Text == text {
				return true
			}
		}
		return false
	}
}

// createAndJoin runs the real onboarding flow: A creates, admits B, issues an
// invite, B joins. Returns the group id.
func createAndJoin(t *testing.T, a, b *groupNode) string {
	t.Helper()

	rec, err := a.mgr.Create("integration room", KindPublic, nil)
	require.NoError(t, err, "Create")
	require.Equal(t, RoleOwner, rec.Role)
	require.Equal(t, StatusActive, rec.Status)
	require.Contains(t, rec.Members, a.pk, "the creator is always in its own allowlist")

	// Admit B BEFORE it subscribes, or the publisher's allowlist rejects it.
	updated, err := a.mgr.AddMember(rec.ID, b.pk)
	require.NoError(t, err, "AddMember")
	require.Contains(t, updated.Members, b.pk)

	link, err := a.mgr.BuildInvite(rec.ID)
	require.NoError(t, err, "BuildInvite")
	inv, err := DecodeInvite(link)
	require.NoError(t, err, "DecodeInvite")

	joined, err := b.mgr.Join(inv)
	require.NoError(t, err, "Join")
	require.Equal(t, rec.ID, joined.ID)
	require.Equal(t, RoleMember, joined.Role)
	require.Equal(t, StatusActive, joined.Status, "Join marks the record active even on an optimistic connect")

	return rec.ID
}

// --- onboarding + send ------------------------------------------------------

// TestManagerIntegration_CreateJoinAndSend walks the whole operator flow and
// asserts the owner's message actually reaches the joiner's handler.
func TestManagerIntegration_CreateJoinAndSend(t *testing.T) {
	a, b := newGroupEnv(t)
	id := createAndJoin(t, a, b)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Re-send periodically: a joiner's subscription attaches asynchronously, so
	// the first publish can precede it.
	const fromOwner = "hello from the owner"
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
				_ = a.mgr.SendToGroup(ctx, id, fromOwner) //nolint:errcheck
			}
		}
	}()
	require.NoError(t, a.mgr.SendToGroup(ctx, id, fromOwner), "SendToGroup")

	waitInbox(t, b.inbox, "the owner's message", hasText(fromOwner))

	// The owner self-echoes its own sends, so its inbox matches the members'.
	waitInbox(t, a.inbox, "the owner's self-echo", hasText(fromOwner))

	// last_message_at is refreshed by the send path.
	rec, ok, err := a.mgr.Get(id)
	require.NoError(t, err)
	require.True(t, ok)
	require.False(t, rec.LastMessageAt.IsZero(), "SendToGroup should refresh LastMessageAt")
}

// TestManagerIntegration_SendToGroupUnknownID — sending to a group with no live
// session must report it rather than silently dropping the message.
func TestManagerIntegration_SendToGroupUnknownID(t *testing.T) {
	a, _ := newGroupEnv(t)
	err := a.mgr.SendToGroup(context.Background(), "no-such-group", "into the void")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no live session")
}

// TestManagerIntegration_PrivateGroupRoundTrip — a private group generates an
// AES key at Create and the joiner decrypts with it.
//
// The key does NOT ride the invite link: it is handed over in the admission
// response, over the encrypted relay stream, to a PK the group has accepted.
// This test asserts both halves — that the link is keyless, and that the
// joiner nonetheless ends up able to read the feed.
func TestManagerIntegration_PrivateGroupRoundTrip(t *testing.T) {
	a, b := newGroupEnv(t)

	rec, err := a.mgr.Create("private room", KindPrivate, nil)
	require.NoError(t, err)
	require.Len(t, rec.AESKey, 32, "a private group gets a 32-byte AES key at create time")

	// Pre-admitting B means the join request is answered "admitted"
	// immediately, so this exercises key delivery without the approval
	// round trip (covered separately in the admission lane).
	_, err = a.mgr.AddMember(rec.ID, b.pk)
	require.NoError(t, err)
	link, err := a.mgr.BuildInvite(rec.ID)
	require.NoError(t, err)
	inv, err := DecodeInvite(link)
	require.NoError(t, err)
	require.Empty(t, inv.AESKey, "the invite link must not carry the group key")

	joined, err := b.mgr.Join(inv)
	require.NoError(t, err)
	require.Equal(t, rec.AESKey, joined.AESKey, "the key must arrive with the admission response")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	const secret = "sealed group traffic"
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
				_ = a.mgr.SendToGroup(ctx, rec.ID, secret) //nolint:errcheck
			}
		}
	}()
	require.NoError(t, a.mgr.SendToGroup(ctx, rec.ID, secret))

	waitInbox(t, b.inbox, "the decrypted private message", hasText(secret))
}

// --- roster operations ------------------------------------------------------

// TestManagerIntegration_RosterOps covers AddMember / PromoteAdmin /
// DemoteAdmin including their authority gates, which is the part a
// non-integration test cannot reach (they all mutate a live session).
func TestManagerIntegration_RosterOps(t *testing.T) {
	a, b := newGroupEnv(t)
	id := createAndJoin(t, a, b)

	// AddMember is idempotent.
	before, _, _ := a.mgr.Get(id) //nolint:errcheck
	again, err := a.mgr.AddMember(id, b.pk)
	require.NoError(t, err)
	require.Len(t, again.Members, len(before.Members), "re-adding an existing member is a no-op")

	// A non-admin cannot edit the roster.
	stranger, _ := cipher.GenerateKeyPair()
	_, err = b.mgr.AddMember(id, stranger)
	require.Error(t, err, "a plain member must not be able to add members")
	require.Contains(t, err.Error(), "admin")

	// Promote B, then it CAN.
	promoted, err := a.mgr.PromoteAdmin(id, b.pk)
	require.NoError(t, err, "PromoteAdmin")
	require.True(t, promoted.IsAdmin(b.pk), "B should be an admin after promotion")

	// Demote B again.
	demoted, err := a.mgr.DemoteAdmin(id, b.pk)
	require.NoError(t, err, "DemoteAdmin")
	require.False(t, demoted.IsAdmin(b.pk), "B should not be an admin after demotion")

	// The founder cannot be demoted — it is the recovery anchor.
	_, err = a.mgr.DemoteAdmin(id, a.pk)
	require.Error(t, err, "demoting the founder must be refused")

	// Roster ops on an unknown group report it.
	_, err = a.mgr.AddMember("no-such-group", stranger)
	require.Error(t, err)
	_, err = a.mgr.PromoteAdmin("no-such-group", stranger)
	require.Error(t, err)
}

// TestManagerIntegration_JoinRejectsOwnGroup — Join is the joiner side; using
// it on your own invite would subscribe a visor to itself.
func TestManagerIntegration_JoinRejectsOwnGroup(t *testing.T) {
	a, _ := newGroupEnv(t)
	rec, err := a.mgr.Create("mine", KindPublic, nil)
	require.NoError(t, err)

	link, err := a.mgr.BuildInvite(rec.ID)
	require.NoError(t, err)
	inv, err := DecodeInvite(link)
	require.NoError(t, err)

	_, err = a.mgr.Join(inv)
	require.Error(t, err, "joining your own group must be refused")
	require.Contains(t, err.Error(), "own group")
}

// --- Resume -----------------------------------------------------------------

// TestManagerIntegration_ResumeReopensStoredGroups is the restart path: a fresh
// Manager on the same store must bring every non-revoked group back up, and the
// reopened session must still send.
func TestManagerIntegration_ResumeReopensStoredGroups(t *testing.T) {
	a, b := newGroupEnv(t)
	id := createAndJoin(t, a, b)

	// A revoked group must NOT come back.
	revoked, err := a.mgr.Create("deleted room", KindPublic, nil)
	require.NoError(t, err)
	require.NoError(t, a.mgr.Delete(revoked.ID))

	// Restart A's manager against the same store + data dir.
	require.NoError(t, a.mgr.Close())
	time.Sleep(500 * time.Millisecond)

	mgr2, err := NewManager(ManagerConfig{
		Store: a.store, DmsgC: a.dmsgC, MyPK: a.pk, MySK: a.sk,
		DataDir: filepath.Join(a.dir, "cxo"),
		Logger:  logging.MustGetLogger("group-mgr-A2"),
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = mgr2.Close() }) //nolint:errcheck
	inbox2 := &msgInbox{}
	mgr2.SetMessageHandler(inbox2.deliver)

	require.NoError(t, mgr2.Resume(), "Resume")

	if _, ok := mgr2.sessions[id]; !ok {
		t.Fatal("Resume should have reopened the active group")
	}
	if _, ok := mgr2.sessions[revoked.ID]; ok {
		t.Error("Resume must not reopen a revoked group")
	}

	// The resumed session still publishes, and ReplayHistory re-pumps what it
	// already holds through the handler.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	require.NoError(t, mgr2.SendToGroup(ctx, id, "after the restart"))
	waitInbox(t, inbox2, "the post-restart self-echo", hasText("after the restart"))

	replay := &msgInbox{}
	mgr2.SetMessageHandler(replay.deliver)
	mgr2.ReplayHistory(id)
	waitInbox(t, replay, "the replayed history", hasText("after the restart"))

	// ReplayHistory for an unknown group is a no-op, not a panic.
	mgr2.ReplayHistory("no-such-group")
}

// TestManagerIntegration_SubmitToOwnerRelay drives the member's relay fallback
// over dmsg: B submits to A's relay listener, A's handleRelay verifies the
// sender against the allowlist, republishes, and acks.
//
// The relay is vestigial for steady-state groups (every member publishes to its
// own feed now), but SendToGroup still falls back to it when the local
// publisher is unhealthy — so it has to keep working.
func TestManagerIntegration_SubmitToOwnerRelay(t *testing.T) {
	a, b := newGroupEnv(t)
	id := createAndJoin(t, a, b)

	b.mgr.mu.RLock()
	sess := b.mgr.sessions[id]
	b.mgr.mu.RUnlock()
	require.NotNil(t, sess, "the joiner should hold a live session")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// The owner's relay listener binds during openOwner; give dmsg a moment.
	const viaRelay = "submitted through the owner relay"
	var lastErr error
	for attempt := 0; attempt < 10; attempt++ {
		lastErr = sess.SubmitToOwner(ctx, viaRelay)
		// ErrRelayNoAck means the bytes landed but the ack didn't come back —
		// still a successful submit as far as the owner's feed is concerned.
		if lastErr == nil || errors.Is(lastErr, ErrRelayNoAck) {
			break
		}
		time.Sleep(time.Duration(500+attempt*500) * time.Millisecond)
	}
	require.True(t, lastErr == nil || errors.Is(lastErr, ErrRelayNoAck),
		"SubmitToOwner over dmsg: %v", lastErr)

	// The owner republished it under the MEMBER's PK — sender attribution
	// survives the relay hop, which is the whole point of the path.
	waitInbox(t, a.inbox, "the relayed message", func(msgs []Message) bool {
		for _, m := range msgs {
			if m.Text == viaRelay && m.SenderPK == b.pk {
				return true
			}
		}
		return false
	})
}
