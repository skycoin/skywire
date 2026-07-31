// Package pairing cmd/apps/skychat/pairing/manager_resume_test.go
//
// Covers the Manager/Pair surface integration_test.go left untouched: the
// constructor + Open validation arms, the small accessors, and — the one that
// matters — Manager.Resume.
//
// Resume is where a real, live-verified bug lived: it used to Open each stored
// pair (publisher + subscriber + onUpdate) but never Connect it. The symptom
// after any visor restart was silent and one-directional — outbound Sends kept
// working (the publisher's local Put succeeds with no peer attached) while
// inbound onUpdate never fired again, so `skychat pair poll` stayed empty
// forever. Nothing short of an actual restart-then-receive exercise catches
// that: a test that only checks Resume's return count, or that the Pair is in
// the map, passes on the broken version.
//
// So TestResumeReconnectsSubscriberAfterRestart tears the manager down and
// rebuilds it on the same store, then has the PEER send. The message can only
// arrive if Resume reconnected the subscriber.
//
// The dmsg-backed tests skip under -short, matching this package's existing
// convention (baseline_test.go, integration_test.go).
package pairing

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgtest"
	"github.com/skycoin/skywire/pkg/logging"
)

// --- no-dmsg: validation + accessors ----------------------------------------

func TestNewManager_Validation(t *testing.T) {
	dir := t.TempDir()
	st, err := OpenStore(filepath.Join(dir, "pairs.db"), testStoreSK())
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() }) //nolint:errcheck

	dmsgC := new(dmsg.Client) // never dialed; only the non-nil check reads it

	if _, err := NewManager(ManagerConfig{DmsgC: dmsgC, DataDir: dir}); err == nil {
		t.Error("a nil Store must be rejected")
	}
	if _, err := NewManager(ManagerConfig{Store: st, DataDir: dir}); err == nil {
		t.Error("a nil DmsgC must be rejected")
	}
	if _, err := NewManager(ManagerConfig{Store: st, DmsgC: dmsgC}); err == nil {
		t.Error("an empty DataDir must be rejected")
	}

	m, err := NewManager(ManagerConfig{Store: st, DmsgC: dmsgC, DataDir: dir})
	require.NoError(t, err)
	if m.log == nil {
		t.Error("a nil Logger should be defaulted, not left nil")
	}
	if m.pairs == nil {
		t.Error("the pairs map must be initialized or the first Add panics")
	}
	if len(m.pairs) != 0 {
		t.Errorf("NewManager opened %d pairs, want 0 — that is Resume's job", len(m.pairs))
	}
}

func TestManagerGet(t *testing.T) {
	m := &Manager{pairs: map[cipher.PubKey]*Pair{}}
	pk, _ := cipher.GenerateKeyPair()

	if p, ok := m.Get(pk); ok || p != nil {
		t.Errorf("Get on an empty manager = (%v, %v), want (nil, false)", p, ok)
	}

	want := &Pair{}
	m.pairs[pk] = want
	got, ok := m.Get(pk)
	if !ok || got != want {
		t.Errorf("Get = (%v, %v), want the stored pair and true", got, ok)
	}

	other, _ := cipher.GenerateKeyPair()
	if _, ok := m.Get(other); ok {
		t.Error("Get for an unpaired peer should report false")
	}
}

func TestManagerMarkActive(t *testing.T) {
	dir := t.TempDir()
	st, err := OpenStore(filepath.Join(dir, "pairs.db"), testStoreSK())
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() }) //nolint:errcheck

	m := &Manager{store: st, log: logging.MustGetLogger("pair-marktest")}
	pk, _ := cipher.GenerateKeyPair()

	// No record yet — the store surfaces that rather than silently creating one.
	if err := m.MarkActive(pk); err == nil {
		t.Error("MarkActive for an unknown peer should error")
	}

	require.NoError(t, st.Put(Record{PeerPK: pk, Status: StatusPending}))
	require.NoError(t, m.MarkActive(pk))

	recs, err := st.List()
	require.NoError(t, err)
	require.Len(t, recs, 1)
	if recs[0].Status != StatusActive {
		t.Errorf("status = %q, want %q", recs[0].Status, StatusActive)
	}
}

func TestPairPortAndPeerPK(t *testing.T) {
	peer, _ := cipher.GenerateKeyPair()
	p := &Pair{port: 12345, cfg: Config{PeerPK: peer}}

	if p.Port() != 12345 {
		t.Errorf("Port() = %d, want 12345", p.Port())
	}
	if p.PeerPK() != peer {
		t.Errorf("PeerPK() = %s, want %s", p.PeerPK().Hex()[:8], peer.Hex()[:8])
	}
}

func TestPairOpen_Validation(t *testing.T) {
	peer, _ := cipher.GenerateKeyPair()
	dir := t.TempDir()

	// Every arm below must fail BEFORE any CXO node is built, so these run
	// without a dmsg env.
	if _, err := Open(Config{PeerPK: peer, DataDir: dir}); err == nil {
		t.Error("a nil DmsgC must be rejected")
	}
	if _, err := Open(Config{DmsgC: new(dmsg.Client), DataDir: dir}); err == nil {
		t.Error("a zero PeerPK must be rejected")
	}
	if _, err := Open(Config{DmsgC: new(dmsg.Client), PeerPK: peer}); err == nil {
		t.Error("an empty DataDir must be rejected")
	}
}

// TestPairConnect_AfterCloseReturnsSentinel — Close nils the subscriber, and
// Connect used to nil-deref on it. A closed pair must report ErrPairClosed so
// the reconnect machinery can tell "torn down" from "peer unreachable".
func TestPairConnect_AfterCloseReturnsSentinel(t *testing.T) {
	p := &Pair{} // sub == nil, the post-Close shape
	err := p.Connect(context.Background())
	if err != ErrPairClosed { //nolint:errorlint // sentinel is returned verbatim
		t.Errorf("Connect on a closed pair = %v, want ErrPairClosed", err)
	}
}

// --- dmsg-backed: Resume ----------------------------------------------------

// resumeEnv brings up a 1-server dmsg env with two clients, ready to use.
func resumeEnv(t *testing.T) (dmsgA, dmsgB *dmsg.Client, pkA cipher.PubKey, skA cipher.SecKey, pkB cipher.PubKey, skB cipher.SecKey) {
	t.Helper()
	pkA, skA = cipher.GenerateKeyPair()
	pkB, skB = cipher.GenerateKeyPair()

	env := dmsgtest.NewEnv(t, dmsgtest.DefaultTimeout)
	env.Discovery()
	require.NoError(t, env.Startup(dmsgtest.DefaultTimeout, 1, 0, &dmsg.Config{MinSessions: 1}))
	t.Cleanup(env.Shutdown)

	var err error
	dmsgA, err = env.NewClientWithKeys(pkA, skA, &dmsg.Config{MinSessions: 1})
	require.NoError(t, err)
	dmsgB, err = env.NewClientWithKeys(pkB, skB, &dmsg.Config{MinSessions: 1})
	require.NoError(t, err)

	waitDmsgReady(t, dmsgA, 10*time.Second)
	waitDmsgReady(t, dmsgB, 10*time.Second)
	return dmsgA, dmsgB, pkA, skA, pkB, skB
}

// connectRetry mirrors integration_test.go: concurrent dmsg handshakes on a
// loaded runner can transiently fail with "cannot connect to delegated server".
func connectRetry(p *Pair) error {
	var lastErr error
	for attempt := 0; attempt < 10; attempt++ {
		if err := p.Connect(context.Background()); err == nil {
			return nil
		} else { //nolint:revive
			lastErr = err
		}
		time.Sleep(time.Duration(200+attempt*100) * time.Millisecond)
	}
	return lastErr
}

// TestResumeReconnectsSubscriberAfterRestart is the regression guard for the
// Open-without-Connect bug. The assertion is INBOUND delivery after a restart:
// a Resume that only opened the pair would leave the publisher live (so
// outbound still works) and the subscriber dead, exactly the reported symptom.
func TestResumeReconnectsSubscriberAfterRestart(t *testing.T) {
	if testing.Short() {
		t.Skip("dmsg integration test; skipped under -short")
	}
	dmsgA, dmsgB, pkA, skA, pkB, skB := resumeEnv(t)

	dirA, dirB := t.TempDir(), t.TempDir()
	storeA, err := OpenStore(filepath.Join(dirA, "pairs.db"), skA)
	require.NoError(t, err)
	t.Cleanup(func() { _ = storeA.Close() }) //nolint:errcheck
	storeB, err := OpenStore(filepath.Join(dirB, "pairs.db"), skB)
	require.NoError(t, err)
	t.Cleanup(func() { _ = storeB.Close() }) //nolint:errcheck

	newMgrA := func(tag string) *Manager {
		m, mErr := NewManager(ManagerConfig{
			Store: storeA, DmsgC: dmsgA, MyPK: pkA, MySK: skA,
			DataDir: filepath.Join(dirA, "cxo"),
			Logger:  logging.MustGetLogger("pair-A-" + tag),
		})
		require.NoError(t, mErr)
		return m
	}

	mgrB, err := NewManager(ManagerConfig{
		Store: storeB, DmsgC: dmsgB, MyPK: pkB, MySK: skB,
		DataDir: filepath.Join(dirB, "cxo"),
		Logger:  logging.MustGetLogger("pair-B"),
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = mgrB.Close() }) //nolint:errcheck

	inboxB := newTestInbox()
	mgrB.SetMessageHandler(inboxB.deliver)

	// --- first run: pair up and confirm both directions work -----------------
	mgrA1 := newMgrA("run1")
	inboxA1 := newTestInbox()
	mgrA1.SetMessageHandler(inboxA1.deliver)

	pairA, err := mgrA1.Add(pkB)
	require.NoError(t, err)
	pairB, err := mgrB.Add(pkA)
	require.NoError(t, err)
	require.NoError(t, connectRetry(pairA))
	require.NoError(t, connectRetry(pairB))

	require.NoError(t, pairB.Send("before restart"))
	require.NoError(t, inboxA1.waitFor(20*time.Second, func(msgs []testReceived) bool {
		for _, m := range msgs {
			if m.peer == pkB && m.text == "before restart" {
				return true
			}
		}
		return false
	}), "baseline inbound delivery failed — the restart assertion below would be meaningless")

	// --- restart A: same store + data dir, brand-new Manager -----------------
	require.NoError(t, mgrA1.Close())
	time.Sleep(500 * time.Millisecond) // let the CXO node release its dmsg port

	mgrA2 := newMgrA("run2")
	t.Cleanup(func() { _ = mgrA2.Close() }) //nolint:errcheck
	inboxA2 := newTestInbox()
	mgrA2.SetMessageHandler(inboxA2.deliver)

	resumed, err := mgrA2.Resume()
	require.NoError(t, err)
	if resumed != 1 {
		t.Fatalf("Resume restored %d pairs, want 1", resumed)
	}
	if _, ok := mgrA2.Get(pkB); !ok {
		t.Fatal("the resumed pair should be live in the manager's map")
	}

	// THE assertion: inbound still works after the restart. Only true if Resume
	// reconnected the subscriber rather than merely opening the pair.
	require.NoError(t, pairB.Send("after restart"))
	require.NoError(t, inboxA2.waitFor(30*time.Second, func(msgs []testReceived) bool {
		for _, m := range msgs {
			if m.peer == pkB && m.text == "after restart" {
				return true
			}
		}
		return false
	}), "no inbound after Resume — the subscriber was opened but never reconnected")

	// Outbound from the resumed pair also still works — but only once the PEER
	// re-subscribes. A's restart replaced its CXO node, so B's subscriber is
	// still attached to the old one; in production B's reconnect watchdog
	// closes that gap. Reconnecting explicitly keeps this assertion about A's
	// republished publisher rather than about B's watchdog timing.
	require.NoError(t, connectRetry(pairB))

	resumedPair, ok := mgrA2.Get(pkB)
	require.True(t, ok)
	require.NoError(t, resumedPair.Send("outbound after restart"))
	require.NoError(t, inboxB.waitFor(30*time.Second, func(msgs []testReceived) bool {
		for _, m := range msgs {
			if m.peer == pkA && m.text == "outbound after restart" {
				return true
			}
		}
		return false
	}), "the resumed publisher never reached the re-subscribed peer")
}

// TestResumeSkipsRevokedAndAlreadyLive covers Resume's two skip arms without
// needing a live peer: a revoked record must not be reopened (Remove is
// supposed to be final), and a second Resume must not double-open pairs that
// the first one already brought up.
func TestResumeSkipsRevokedAndAlreadyLive(t *testing.T) {
	if testing.Short() {
		t.Skip("dmsg integration test; skipped under -short")
	}
	dmsgA, _, pkA, skA, pkB, _ := resumeEnv(t)

	dir := t.TempDir()
	store, err := OpenStore(filepath.Join(dir, "pairs.db"), testStoreSK())
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() }) //nolint:errcheck

	revoked, _ := cipher.GenerateKeyPair()
	require.NoError(t, store.Put(Record{PeerPK: pkB, Status: StatusPending}))
	require.NoError(t, store.Put(Record{PeerPK: revoked, Status: StatusPending}))
	require.NoError(t, store.SetStatus(revoked, StatusRevoked))

	mgr, err := NewManager(ManagerConfig{
		Store: store, DmsgC: dmsgA, MyPK: pkA, MySK: skA,
		DataDir: filepath.Join(dir, "cxo"),
		Logger:  logging.MustGetLogger("pair-resume-skips"),
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = mgr.Close() }) //nolint:errcheck

	// A pending record IS resumed (a crash between invite and ack must not lose
	// state); the revoked one is not.
	resumed, err := mgr.Resume()
	require.NoError(t, err)
	if resumed != 1 {
		t.Errorf("Resume restored %d pairs, want 1 (pending yes, revoked no)", resumed)
	}
	if _, ok := mgr.Get(revoked); ok {
		t.Error("a revoked record must not be brought back up")
	}
	if _, ok := mgr.Get(pkB); !ok {
		t.Error("a pending record should be resumed")
	}

	// Idempotent: a second Resume adds nothing.
	again, err := mgr.Resume()
	require.NoError(t, err)
	if again != 0 {
		t.Errorf("second Resume restored %d pairs, want 0 — already-live pairs must be skipped", again)
	}
}
