// Package treestore — relay_presence_test.go
//
// Characterization test: publisher-offline presence is NOT transitive
// through a CXO store-and-forward relay.
//
// A CXO node forwards a feed it relays (broadcastRoot re-sends a received
// Root to every OTHER subscriber of that feed), but liveness is a property
// of a direct connection — it does not propagate past the relay. This test
// pins that behavior so a future change to relay/close semantics is a
// deliberate, visible decision.
//
// Topology:
//
//	V (publisher) ── R (relay: subscribes V, shares feedV) ── A (leaf, dials R only)
//	V ─────────────────────────────────────────────────────── A2 (direct control, dials V)
//
// Both A and A2 subscribe to feedV and receive V's first Put. Then V is
// killed and we record, on each downstream node, OnDisconnect (a conn to
// this node dropped) and OnUnsubscribeRemote (a peer stopped serving a feed).
//
// Observed result (see the design RFC docs/design/dmsg-bootstrap-floor.md):
//   - A2 (direct)      → disconnect fires: it learns V is gone.
//   - R  (relay's edge) → disconnect fires: R learns V is gone.
//   - A  (behind relay) → NOTHING: no disconnect, no unsub. A holds V's
//     last Root indefinitely and can only infer absence from silence.
//
// Design consequence: uptime-through-a-relay-tree cannot be a passive
// connection-lifetime read; it needs V's periodically-republished signed,
// timestamped Root (positive liveness, verifiable through untrusted relays).
package treestore

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	skycipher "github.com/skycoin/skycoin/src/cipher"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/node"
	cxotransport "github.com/skycoin/skywire/pkg/cxo/node/transport"
	"github.com/skycoin/skywire/pkg/cxo/skyobject"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgtest"
)

type presenceProbe struct {
	name           string
	disconnects    int64
	unsubs         int64
	firstDisconnAt atomic.Value // time.Time
}

func (p *presenceProbe) onDisconnect(_ *node.Conn, _ error) {
	if atomic.AddInt64(&p.disconnects, 1) == 1 {
		p.firstDisconnAt.Store(time.Now())
	}
}

func (p *presenceProbe) onUnsub(_ *node.Conn, _ skycipher.PubKey) {
	atomic.AddInt64(&p.unsubs, 1)
}

// newInstrumentedNode builds a dmsg-enabled CXO node bound to sk, with
// OnDisconnect / OnUnsubscribeRemote wired into probe.
func newInstrumentedNode(t *testing.T, dmsgC *dmsg.Client, sk cipher.SecKey, probe *presenceProbe) *node.Node {
	t.Helper()
	cfg := node.NewConfig()
	cfg.SecKey = skycipher.SecKey(sk)
	cfg.Config = skyobject.NewConfig()
	cfg.Config.InMemoryDB = true
	cfg.TCP.Listen = ""
	cfg.UDP.Listen = ""
	cfg.RPC = ""
	cfg.OnDisconnect = probe.onDisconnect
	cfg.OnUnsubscribeRemote = probe.onUnsub
	n, err := node.NewNode(cfg)
	require.NoError(t, err)
	require.NoError(t, n.EnableDMSG(cxotransport.NewDMSGFactory(dmsgC, cxotransport.DefaultCXOPort)))
	t.Cleanup(func() { _ = n.Close() })
	return n
}

func TestRelayPresenceNotTransitive(t *testing.T) {
	const timeout = 40 * time.Second
	env := dmsgtest.NewEnv(t, timeout)
	require.NoError(t, env.Startup(0, 1, 0, &dmsg.Config{MinSessions: 1}))
	t.Cleanup(env.Shutdown)

	// Four keypairs / dmsg clients: V (publisher), R (relay), A (leaf via R), A2 (direct).
	vpk, vsk := cipher.GenerateKeyPair()
	rpk, rsk := cipher.GenerateKeyPair()
	apk, ask := cipher.GenerateKeyPair()
	a2pk, a2sk := cipher.GenerateKeyPair()
	vClient, err := env.NewClientWithKeys(vpk, vsk, &dmsg.Config{MinSessions: 1})
	require.NoError(t, err)
	rClient, err := env.NewClientWithKeys(rpk, rsk, &dmsg.Config{MinSessions: 1})
	require.NoError(t, err)
	aClient, err := env.NewClientWithKeys(apk, ask, &dmsg.Config{MinSessions: 1})
	require.NoError(t, err)
	a2Client, err := env.NewClientWithKeys(a2pk, a2sk, &dmsg.Config{MinSessions: 1})
	require.NoError(t, err)

	// V: publisher on feedV = vpk.
	pub, err := NewWithDMSG(vClient, vsk, PubConfig{InMemoryDB: true, BatchWindow: 5 * time.Millisecond})
	require.NoError(t, err)
	feedV := pub.Feed()

	// R: relay. Shares feedV (serves it inbound) and subscribes it from V.
	rProbe := &presenceProbe{name: "R"}
	rNode := newInstrumentedNode(t, rClient, rsk, rProbe)
	require.NoError(t, rNode.Share(skycipher.PubKey(feedV)))
	rSub, err := NewSubscriberOnNode(rNode, feedV, SubConfig{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = rSub.Close() })

	// A: leaf, subscribes feedV but dials ONLY R.
	aProbe := &presenceProbe{name: "A"}
	aNode := newInstrumentedNode(t, aClient, ask, aProbe)
	aSub, err := NewSubscriberOnNode(aNode, feedV, SubConfig{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = aSub.Close() })

	// A2: direct control, subscribes feedV and dials V.
	a2Probe := &presenceProbe{name: "A2"}
	a2Node := newInstrumentedNode(t, a2Client, a2sk, a2Probe)
	a2Sub, err := NewSubscriberOnNode(a2Node, feedV, SubConfig{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = a2Sub.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Wire the chain: R←V, A←R, A2←V.
	require.Eventually(t, func() bool { return rSub.Connect(ctx, vpk) == nil }, 15*time.Second, 200*time.Millisecond, "R could not connect to V")
	require.Eventually(t, func() bool { return aSub.Connect(ctx, rpk) == nil }, 15*time.Second, 200*time.Millisecond, "A could not connect to R")
	require.Eventually(t, func() bool { return a2Sub.Connect(ctx, vpk) == nil }, 15*time.Second, 200*time.Millisecond, "A2 could not connect to V")

	// V publishes; both A (via relay) and A2 (direct) must receive it — this
	// proves the relay forwards data, so any presence difference below is
	// about liveness propagation, not a broken relay.
	require.NoError(t, pub.Put("k", []byte("v1")))
	require.True(t, waitVal(a2Sub, "k", "v1", 15*time.Second), "control: direct subscriber must receive V's root")
	require.True(t, waitVal(aSub, "k", "v1", 15*time.Second), "relay must forward V's root to A")

	require.Equal(t, int64(0), atomic.LoadInt64(&aProbe.disconnects))
	require.Equal(t, int64(0), atomic.LoadInt64(&a2Probe.disconnects))

	// ---- Kill V (publisher offline). ----
	killAt := time.Now()
	require.NoError(t, pub.Close())
	require.NoError(t, vClient.Close())

	// The direct edge MUST notice V's disappearance (its conn drops). This is
	// the load-bearing control: if it ever fails, close detection itself broke.
	require.Eventually(t, func() bool { return atomic.LoadInt64(&a2Probe.disconnects) > 0 },
		15*time.Second, 200*time.Millisecond,
		"direct subscriber A2 never observed V's disconnect")
	// The relay's own edge to V also drops — R learns V is gone.
	require.Eventually(t, func() bool { return atomic.LoadInt64(&rProbe.disconnects) > 0 },
		15*time.Second, 200*time.Millisecond,
		"relay R never observed V's disconnect")

	// The characterized behavior: the leaf behind the relay gets NO signal.
	// Give it a generous window; if a future change starts propagating
	// presence, this assertion flips and the RFC's uptime design must be
	// revisited.
	require.Never(t, func() bool {
		return atomic.LoadInt64(&aProbe.disconnects) > 0 || atomic.LoadInt64(&aProbe.unsubs) > 0
	}, 8*time.Second, 500*time.Millisecond,
		"presence propagated through the relay to A — CXO relay semantics changed; revisit the uptime design")

	when := "-"
	if v, ok := rProbe.firstDisconnAt.Load().(time.Time); ok {
		when = v.Sub(killAt).Round(time.Millisecond).String()
	}
	t.Logf("presence after V killed: A2(direct) disc=%d | R(relay edge) disc=%d (+%s) | A(behind relay) disc=%d unsub=%d",
		atomic.LoadInt64(&a2Probe.disconnects), atomic.LoadInt64(&rProbe.disconnects), when,
		atomic.LoadInt64(&aProbe.disconnects), atomic.LoadInt64(&aProbe.unsubs))
}

func waitVal(s *Subscriber, path, want string, d time.Duration) bool {
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		if v, ok := s.Get(path); ok && string(v) == want {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}
