// Package pairing — cmd/apps/skychat/pairing/integration_test.go:
// end-to-end coverage of the pair primitive over a real DMSG mesh.
//
// Spins up a 1-server / 2-client dmsg env via dmsgtest, wires a
// pairing.Manager on each side using shared PK/SK pairs (so the
// pair-feed PK matches the dmsg PK, mirroring the visor wiring),
// and exercises the full pipeline:
//
//   - Manager.Add bringing up publisher + subscriber
//   - allowlist enforcement (publisher accepts only the peer)
//   - deterministic port discovery (both sides hit the same port)
//   - CXO publish + subscribe round-trip in both directions
//
// Skipped under -short because the in-process DMSG mesh + CXO node
// startup add a few seconds of overhead.
package pairing

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	dmsg "github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgtest"
	"github.com/skycoin/skywire/pkg/logging"
)

func TestPairRoundTripOverDmsg(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test; skipped under -short")
	}

	// Generate keypairs first so dmsg client and pair manager share
	// the same PK/SK on each side. Mirrors the visor wiring where
	// v.dmsgC and the pair manager both use v.conf.PK / v.conf.SK.
	pkA, skA := cipher.GenerateKeyPair()
	pkB, skB := cipher.GenerateKeyPair()

	env := dmsgtest.NewEnv(t, dmsgtest.DefaultTimeout)
	env.Discovery() // ensure mock disc is ready (no-op if already)
	require.NoError(t, env.Startup(dmsgtest.DefaultTimeout, 1, 0, &dmsg.Config{MinSessions: 1}))
	t.Cleanup(env.Shutdown)

	// Use NewClientWithKeys so the dmsg client carries pkA/skA, etc.
	dmsgA, err := env.NewClientWithKeys(pkA, skA, &dmsg.Config{MinSessions: 1})
	require.NoError(t, err)
	dmsgB, err := env.NewClientWithKeys(pkB, skB, &dmsg.Config{MinSessions: 1})
	require.NoError(t, err)

	// Wait for both clients to be DMSG-ready before bringing up CXO
	// nodes — Listen otherwise races the session handshake.
	waitDmsgReady(t, dmsgA, 10*time.Second)
	waitDmsgReady(t, dmsgB, 10*time.Second)

	dirA := t.TempDir()
	dirB := t.TempDir()

	storeA, err := OpenStore(filepath.Join(dirA, "pairs.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = storeA.Close() }) //nolint:errcheck

	storeB, err := OpenStore(filepath.Join(dirB, "pairs.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = storeB.Close() }) //nolint:errcheck

	mgrA, err := NewManager(ManagerConfig{
		Store:   storeA,
		DmsgC:   dmsgA,
		MyPK:    pkA,
		MySK:    skA,
		DataDir: filepath.Join(dirA, "cxo"),
		Logger:  logging.MustGetLogger("pair-A"),
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = mgrA.Close() }) //nolint:errcheck

	mgrB, err := NewManager(ManagerConfig{
		Store:   storeB,
		DmsgC:   dmsgB,
		MyPK:    pkB,
		MySK:    skB,
		DataDir: filepath.Join(dirB, "cxo"),
		Logger:  logging.MustGetLogger("pair-B"),
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = mgrB.Close() }) //nolint:errcheck

	// Register handlers BEFORE Add so the very first Root the
	// subscriber sees gets surfaced. Manager.SetMessageHandler
	// propagates to any later-created Pair.
	inboxA := newTestInbox()
	inboxB := newTestInbox()
	mgrA.SetMessageHandler(inboxA.deliver)
	mgrB.SetMessageHandler(inboxB.deliver)

	pairA, err := mgrA.Add(pkB)
	require.NoError(t, err)
	pairB, err := mgrB.Add(pkA)
	require.NoError(t, err)

	// Retry Connect — on slow CI runners, concurrent dmsg
	// handshakes can transiently fail with "cannot connect to
	// delegated server" (dmsg error 202) when the server's
	// connection table is briefly saturated. Mirrors the retry
	// pattern in pkg/dmsg/dmsgtest/e2e_test.go:264.
	connectWithRetry := func(p *Pair) error {
		var lastErr error
		for attempt := 0; attempt < 10; attempt++ {
			if err := p.Connect(context.Background()); err == nil {
				return nil
			} else {
				lastErr = err
			}
			time.Sleep(time.Duration(200+attempt*100) * time.Millisecond)
		}
		return lastErr
	}
	require.NoError(t, connectWithRetry(pairA))
	require.NoError(t, connectWithRetry(pairB))

	// A → B
	const plaintextAB = "hello bob"
	require.NoError(t, pairA.Send(plaintextAB))

	// Snoop the publisher's local tree: the leaf bytes must be
	// ciphertext (not the plaintext). Catches a regression where
	// encryption gets accidentally bypassed and the leaf is the
	// raw JSON.
	pairA.pub.Walk(MessagePathPrefix, func(path string, value []byte) bool {
		require.NotContains(t, string(value), plaintextAB,
			"published leaf at %q must not contain plaintext", path)
		return true
	})

	require.NoError(t, inboxB.waitFor(20*time.Second, func(msgs []testReceived) bool {
		for _, m := range msgs {
			if m.peer == pkA && m.text == plaintextAB {
				return true
			}
		}
		return false
	}))

	// B → A
	require.NoError(t, pairB.Send("hi alice"))
	require.NoError(t, inboxA.waitFor(20*time.Second, func(msgs []testReceived) bool {
		for _, m := range msgs {
			if m.peer == pkB && m.text == "hi alice" {
				return true
			}
		}
		return false
	}))
}

// waitDmsgReady blocks until c.Ready() fires or the supplied
// timeout elapses (then t.Fatal). All current callers pass 10s;
// the parameter is retained so an outlier slower-network case can
// override without adding a second helper.
func waitDmsgReady(t *testing.T, c *dmsg.Client, timeout time.Duration) { //nolint:unparam
	t.Helper()
	select {
	case <-c.Ready():
	case <-time.After(timeout):
		t.Fatalf("dmsg client %s not ready after %v", c.LocalPK().Hex()[:8], timeout)
	}
}

// testInbox is a tiny test helper distinct from pkg/visor's pairInbox.
// We don't reuse pairInbox because it lives in pkg/visor and dragging
// it down here would force the test to depend on the visor's
// PairMessage envelope; the raw (peer, text) shape is enough.

type testReceived struct {
	peer cipher.PubKey
	text string
}

type testInbox struct {
	mu   sync.Mutex
	msgs []testReceived
}

func newTestInbox() *testInbox { return &testInbox{} }

func (i *testInbox) deliver(peer cipher.PubKey, msg Message) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.msgs = append(i.msgs, testReceived{peer: peer, text: msg.Text})
}

func (i *testInbox) snapshot() []testReceived {
	i.mu.Lock()
	defer i.mu.Unlock()
	out := make([]testReceived, len(i.msgs))
	copy(out, i.msgs)
	return out
}

var errInboxTimeout = errors.New("pairing: inbox wait timed out")

func (i *testInbox) waitFor(timeout time.Duration, pred func([]testReceived) bool) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if pred(i.snapshot()) {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return errInboxTimeout
}
