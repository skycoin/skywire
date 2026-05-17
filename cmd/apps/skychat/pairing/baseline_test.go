// Package pairing — cmd/apps/skychat/pairing/baseline_test.go:
// baseline tests for 1:1 pair DMs (operator reset directive
// 2026-05-17T22:53Z, Phase A).
//
// TestPairRoundTripOverDmsg (integration_test.go) already proves the
// happy path: publisher + subscriber on dmsgtest, send-then-receive
// in both directions, ciphertext-at-rest, allowlist enforcement.
// This file adds the COVERAGE GAPS the operator's reset surfaced:
//
//   1. Reconnect after close. The production failure mode all three
//      agents hit on 2026-05-17 21:10Z: a subscriber closes (visor
//      restart, session reopen, network blip), reattaches via fresh
//      Connect, and silently misses messages published in the gap.
//      Pair-level test should be the first place this regression
//      shows up — pairs are 1:1, no admin/topology in the way.
//
//   2. CXO replay value-prop. The operator's "what does CXO solve
//      vs. what does it cost" question. CXO's claim is that a
//      re-subscribing peer catches up on the publisher's history
//      that landed during the gap, without needing per-message
//      retry logic in the chat-app. The test below proves that
//      claim or falsifies it.
//
// Built on integration_test.go's helpers (testInbox, waitDmsgReady,
// connectWithRetry pattern) — keeps this file focused on the new
// scenarios.
package pairing

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	dmsg "github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgtest"
	"github.com/skycoin/skywire/pkg/logging"
)

// pairTestRig collects the per-side state two-pair tests need. The
// methods on this type isolate the dmsgtest + Manager + Pair wiring
// so the actual test bodies stay focused on the scenario under test.
type pairTestRig struct {
	env *dmsgtest.Env

	mgrA, mgrB     *Manager
	pairA, pairB   *Pair
	inboxA, inboxB *testInbox
	pkA, pkB       cipher.PubKey
}

// newPairTestRig spins up a 1-server / 2-client dmsg env, brings up
// a pairing.Manager on each side with shared PK/SK pairs (mirroring
// the visor wiring), and registers handlers BEFORE Add so the very
// first Root the subscribers see gets surfaced. Returns a rig ready
// for the test to drive Connect / Send / Close lifecycle.
func newPairTestRig(t *testing.T) *pairTestRig {
	t.Helper()

	pkA, skA := cipher.GenerateKeyPair()
	pkB, skB := cipher.GenerateKeyPair()

	env := dmsgtest.NewEnv(t, dmsgtest.DefaultTimeout)
	env.Discovery()
	require.NoError(t, env.Startup(dmsgtest.DefaultTimeout, 1, 0, &dmsg.Config{MinSessions: 1}))
	t.Cleanup(env.Shutdown)

	dmsgA, err := env.NewClientWithKeys(pkA, skA, &dmsg.Config{MinSessions: 1})
	require.NoError(t, err)
	dmsgB, err := env.NewClientWithKeys(pkB, skB, &dmsg.Config{MinSessions: 1})
	require.NoError(t, err)
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

	inboxA := newTestInbox()
	inboxB := newTestInbox()
	mgrA.SetMessageHandler(inboxA.deliver)
	mgrB.SetMessageHandler(inboxB.deliver)

	pairA, err := mgrA.Add(pkB)
	require.NoError(t, err)
	pairB, err := mgrB.Add(pkA)
	require.NoError(t, err)

	connectWithRetry(t, pairA)
	connectWithRetry(t, pairB)

	return &pairTestRig{
		env:    env,
		mgrA:   mgrA,
		mgrB:   mgrB,
		pairA:  pairA,
		pairB:  pairB,
		inboxA: inboxA,
		inboxB: inboxB,
		pkA:    pkA,
		pkB:    pkB,
	}
}

// connectWithRetry mirrors integration_test.go's local helper: dmsg
// dials on a fresh server can transiently fail under CI load when
// the session table is briefly saturated. 10 attempts with linear
// backoff is plenty for the in-process env.
func connectWithRetry(t *testing.T, p *Pair) {
	t.Helper()
	var lastErr error
	for attempt := 0; attempt < 10; attempt++ {
		if err := p.Connect(context.Background()); err == nil {
			return
		} else {
			lastErr = err
		}
		time.Sleep(time.Duration(200+attempt*100) * time.Millisecond)
	}
	t.Fatalf("pairing connect retries exhausted: %v", lastErr)
}

// TestPairReceivesMessagesPublishedDuringSubscriberGap is the
// pair-level analog of the bug all three agents hit on 2026-05-17:
// a subscriber drops (visor restart / session reopen / dmsg blip),
// the publisher continues publishing while subscriber-less, then
// the subscriber comes back. The question this test answers: does
// CXO's publisher-side state plus re-subscribe handshake actually
// deliver the gap-window messages, or does the receive cascade
// silently lose them?
//
// If the test PASSES, the pair-level CXO replay is sound — any
// production loss must be at a higher layer (multi-publisher
// federation, reconnect bookkeeping, etc.). If the test FAILS,
// even the simplest CXO use case is unreliable on reconnect and
// every higher-layer fix is building on sand.
func TestPairReceivesMessagesPublishedDuringSubscriberGap(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test; skipped under -short")
	}
	rig := newPairTestRig(t)

	// Phase 1: prove the connected baseline. A sends, B receives.
	const helloAB = "phase-1-baseline"
	require.NoError(t, rig.pairA.Send(helloAB))
	require.NoError(t, rig.inboxB.waitFor(20*time.Second, func(msgs []testReceived) bool {
		for _, m := range msgs {
			if m.peer == rig.pkA && m.text == helloAB {
				return true
			}
		}
		return false
	}), "baseline send before gap must arrive")

	// Phase 2: B drops its pair entirely (the closest analog to a
	// visor restart at the pair level). Manager.Remove tears down
	// the subscriber + publisher and clears the persistent record;
	// the test then re-Adds. Anything A publishes between Remove
	// and the second Add lands in A's CXDS but has no subscriber to
	// fan out to.
	require.NoError(t, rig.mgrB.Remove(rig.pkA))

	const duringGapAB = "phase-2-during-gap"
	require.NoError(t, rig.pairA.Send(duringGapAB))

	// Brief grace so A's publisher actually commits the leaf to its
	// CXDS before B reconnects. Without this the test would
	// accidentally exercise the publish-and-subscribe-arrive
	// concurrently path, which is a different scenario.
	time.Sleep(500 * time.Millisecond)

	// Phase 3: B re-creates the pair from scratch. The new pair
	// holds a fresh subscriber to A's feed; CXO's claim is that the
	// initial sync delivers A's existing leaves (including the
	// during-gap one) to B's onUpdate.
	pairB2, err := rig.mgrB.Add(rig.pkA)
	require.NoError(t, err)
	connectWithRetry(t, pairB2)

	// The pre-restart message must arrive. If this assertion fails,
	// CXO replay-on-reconnect at the pair level is broken — a
	// foundational regression worth a stop-the-world fix before any
	// other comms work.
	require.NoError(t, rig.inboxB.waitFor(20*time.Second, func(msgs []testReceived) bool {
		for _, m := range msgs {
			if m.peer == rig.pkA && m.text == duringGapAB {
				return true
			}
		}
		return false
	}), "after re-Add, B must receive the during-gap message via CXO replay")

	// Phase 4: a fresh post-reconnect send still works. Distinguishes
	// "replay delivered the old message but the new subscriber is
	// dead going forward" from a healthy reconnect.
	const postReconnectAB = "phase-4-post-reconnect"
	require.NoError(t, rig.pairA.Send(postReconnectAB))
	require.NoError(t, rig.inboxB.waitFor(20*time.Second, func(msgs []testReceived) bool {
		for _, m := range msgs {
			if m.peer == rig.pkA && m.text == postReconnectAB {
				return true
			}
		}
		return false
	}), "post-reconnect live send must arrive on the re-Added pair")
}

// TestPairSendAfterCloseReturnsClosedError exercises an error
// surface gap surfaced by the operator's reset: pre-fix, calling
// Send after Close nil-deref'd on p.pub.Put because Close set p.pub
// = nil without guarding subsequent operations. The fix lifts the
// failure into a clean sentinel error (ErrPairClosed) callers can
// branch on via errors.Is — matches every other "this resource is
// gone" error pattern in skywire and gives a meaningful surface to
// the --verbose flag Alpha's #2693 wires up.
func TestPairSendAfterCloseReturnsClosedError(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test; skipped under -short")
	}
	rig := newPairTestRig(t)

	// Sanity: a healthy Send works before Close.
	require.NoError(t, rig.pairA.Send("pre-close"))

	// Close the pair. mgrA.Remove tears down both publisher and
	// subscriber and nils them out internally.
	require.NoError(t, rig.mgrA.Remove(rig.pkB))

	// The Pair pointer the test still holds is now closed. Send
	// MUST return ErrPairClosed, not panic.
	err := rig.pairA.Send("post-close")
	require.Error(t, err)
	require.ErrorIs(t, err, ErrPairClosed,
		"Send after Close must return ErrPairClosed, got: %v", err)
}

// TestPairConnectAfterCloseReturnsClosedError mirrors the Send case
// for the Connect side: Close nils p.sub; a later Connect would
// nil-deref. Same fix shape: explicit guard + ErrPairClosed.
func TestPairConnectAfterCloseReturnsClosedError(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test; skipped under -short")
	}
	rig := newPairTestRig(t)

	// Close pairA's underlying pair via the manager. The cached
	// rig.pairA pointer now references a closed Pair.
	require.NoError(t, rig.mgrA.Remove(rig.pkB))

	err := rig.pairA.Connect(context.Background())
	require.Error(t, err)
	require.ErrorIs(t, err, ErrPairClosed,
		"Connect after Close must return ErrPairClosed, got: %v", err)
}

// TestPairCloseIsIdempotent confirms the documented "Close may be
// called multiple times safely" contract still holds after the
// nil-guard additions. Defensive: a future refactor that adds state
// to the close path could break this without a test.
func TestPairCloseIsIdempotent(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test; skipped under -short")
	}
	rig := newPairTestRig(t)

	require.NoError(t, rig.pairA.Close())
	// Second Close must not panic and must return nil — there's
	// nothing left to tear down.
	require.NoError(t, rig.pairA.Close())
}

// errPairClosedSatisfiesErrorsIs is a compile-time check that the
// sentinel is reachable from this test package — guards against a
// future rename moving ErrPairClosed out of the public surface.
var errPairClosedSatisfiesErrorsIs = errors.Is(ErrPairClosed, ErrPairClosed)
