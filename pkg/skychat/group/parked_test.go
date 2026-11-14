// Package group pkg/skychat/group/parked_test.go c3-app-chat
package group

import (
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
)

// parkBox stands in for the dmsg-async inboxes.
type parkBox struct {
	mu     sync.Mutex
	frames map[cipher.PubKey][]parkedFrame
}

type parkedFrame struct {
	from    cipher.PubKey
	payload []byte
}

func (b *parkBox) parker(from cipher.PubKey) Parker {
	return func(to cipher.PubKey, payload []byte) error {
		b.mu.Lock()
		defer b.mu.Unlock()
		if b.frames == nil {
			b.frames = map[cipher.PubKey][]parkedFrame{}
		}
		b.frames[to] = append(b.frames[to], parkedFrame{from, payload})
		return nil
	}
}

func (b *parkBox) take(t *testing.T, to cipher.PubKey) []parkedFrame {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		b.mu.Lock()
		got := b.frames[to]
		delete(b.frames, to)
		b.mu.Unlock()
		if len(got) > 0 {
			return got
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("nothing parked for %s", to.Hex())
	return nil
}

// A join to a private group whose only admin is offline waits on the admin's
// inbox, and the approval reaches the requester through its own inbox.
func TestAdmission_ParkedWhileFounderIsOffline(t *testing.T) {
	a, b := newGroupEnv(t)
	box := &parkBox{}

	rec, err := a.mgr.Create("parked room", KindPrivate, nil)
	require.NoError(t, err)
	inv := inviteFor(t, a, rec.ID)
	require.NoError(t, a.mgr.Close())
	time.Sleep(500 * time.Millisecond)

	b.mgr.SetParker(box.parker(b.pk))
	joined, err := b.mgr.RequestJoin(inv, "hello")
	require.NoError(t, err, "the join should park instead of failing")
	require.Equal(t, StatusAwaitingApproval, joined.Status)

	mgr2, err := NewManager(ManagerConfig{
		Store: a.store, DmsgC: a.dmsgC, MyPK: a.pk, MySK: a.sk,
		DataDir: filepath.Join(a.dir, "cxo"),
		Logger:  logging.MustGetLogger("group-mgr-A2"),
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = mgr2.Close() }) //nolint:errcheck
	mgr2.SetParker(box.parker(a.pk))
	require.NoError(t, mgr2.Resume())

	for _, f := range box.take(t, a.pk) {
		require.NoError(t, mgr2.HandleParked(f.from, f.payload, time.Now()))
	}
	reqs, err := mgr2.PendingJoins(rec.ID)
	require.NoError(t, err)
	require.Len(t, reqs, 1)
	require.Equal(t, b.pk, reqs[0].PK)
	require.Equal(t, "hello", reqs[0].Note)

	_, err = mgr2.ApproveJoin(rec.ID, b.pk)
	require.NoError(t, err)
	decision := box.take(t, b.pk)
	require.Len(t, decision, 1)

	// Only an admin named by the invite may decide.
	stranger, _ := cipher.GenerateKeyPair()
	require.NoError(t, b.mgr.HandleParked(stranger, decision[0].payload, time.Now()))
	got, _, err := b.mgr.Get(rec.ID)
	require.NoError(t, err)
	require.Equal(t, StatusAwaitingApproval, got.Status, "a decision from a stranger must not apply")

	require.NoError(t, b.mgr.HandleParked(decision[0].from, decision[0].payload, time.Now()))
	got, _, err = b.mgr.Get(rec.ID)
	require.NoError(t, err)
	require.Equal(t, StatusActive, got.Status)
	require.Equal(t, rec.AESKey, got.AESKey, "the approval must carry the group key")
}

// Without a Parker an unreachable admin still fails the join.
func TestAdmission_NoParkerStillFails(t *testing.T) {
	a, b := newGroupEnv(t)
	rec, err := a.mgr.Create("closed room", KindPrivate, nil)
	require.NoError(t, err)
	inv := inviteFor(t, a, rec.ID)
	require.NoError(t, a.mgr.Close())
	time.Sleep(500 * time.Millisecond)
	_, err = b.mgr.RequestJoin(inv, "")
	require.Error(t, err)
}

// A request's proof of work is checked at the time it was asked, so one that
// waited on the inbox for longer than the window still counts.
func TestAdmission_ParkedRequestPoWAtDepositTime(t *testing.T) {
	a, _ := newGroupEnv(t)
	rec, err := a.mgr.Create("pow room", KindPrivate, nil)
	require.NoError(t, err)
	bits := rec.JoinPoWRequired()
	require.NotZero(t, bits)
	pk, _ := cipher.GenerateKeyPair()
	then := time.Now().UTC().Add(-time.Hour)
	pow, ok := SolveJoinPoW(rec.ID, pk, bits, then, time.Now().Add(time.Minute))
	require.True(t, ok)

	h := a.mgr.admissionHandler(rec.ID)
	resp := h(JoinRequest{GroupID: rec.ID, PK: pk, PoW: pow, Status: JoinStatusPending})
	require.Equal(t, JoinStatusChallenge, resp.Status, "an hour old proof checked now")
	resp = h(JoinRequest{GroupID: rec.ID, PK: pk, PoW: pow, AskedAt: then, Status: JoinStatusPending})
	require.Equal(t, JoinStatusPending, resp.Status, resp.Reason)
}
