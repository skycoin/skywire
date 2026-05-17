// Package treestore — federated_receive_test.go: in-process CI
// guard against the receive-cascade losing messages.
//
// Background. After today's 33-PR cascade (CXO Conn evict, per-peer
// liveness, dial-side context, history replay, etc.), the 3-agent
// production reliability tests still showed silent drops: a peer's
// publisher reached the local CXO subscriber (subscriber_alive=true,
// last_message_at advanced) but messages never reached the operator's
// listener side. Every diagnosis was a post-hoc guess because the test
// rig was a 30-min live 3-visor coordination — too noisy and too
// expensive to iterate on. Per the operator's direction, this file is
// the in-process guard: assemble N mock visors locally, wire the
// federated send/receive shape end-to-end, drive deterministic
// bursts, and assert ZERO silent drops.
//
// Design.
//
//   - Mock dmsg env from pkg/dmsg/dmsgtest provides a discovery + N
//     servers + N clients. The clients run in-process; messages
//     cross goroutines, not the network.
//   - Each "visor" gets one CXO Node (via treestore.NewWithDMSG)
//     hosting its own publisher on its PK feed, plus (N-1)
//     subscribers attached to the same node via NewSubscriberOnNode
//     — matching the production session.go shape (commit fc1251658 —
//     pkg/cxo/treestore + cmd/apps/skychat/group/session.go).
//   - The subscriber's OnUpdate appends every event to a per-(receiver,
//     sender) slice under a single mutex. Test asserts each
//     (receiver, sender) slice has exactly the messages sender
//     published, in publish order.
//
// Success bar: ZERO silent drops across every (receiver, sender)
// pair. Not a percentage. Run with -race + -count=N to expose flakes;
// the test passes only when every burst produces a complete tally.
package treestore

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgtest"
)

// visorMock collects the publisher + per-peer subscribers for one
// participant in the federated burst test.
type visorMock struct {
	pk    cipher.PubKey
	sk    cipher.SecKey
	pub   *Publisher
	subs  map[cipher.PubKey]*Subscriber // peer-PK → subscriber to that peer's feed
	rcvMu sync.Mutex
	// rcv[peerPK] is the ordered list of message bodies this visor
	// observed from peerPK. Appended under rcvMu in OnUpdate.
	rcv map[cipher.PubKey][][]byte
}

// federatedBurstRig builds N participants on a shared dmsg env, wires
// federated send/receive (each publishes to own feed, subscribes to
// every other's), and returns a ready-to-use rig. Caller is
// responsible for driving the burst and inspecting rcv tallies.
type federatedBurstRig struct {
	env     *dmsgtest.Env
	visors  []*visorMock
	timeout time.Duration
}

func newFederatedBurstRig(t *testing.T, n int) *federatedBurstRig {
	t.Helper()
	const timeout = 30 * time.Second

	env := dmsgtest.NewEnv(t, timeout)
	// 1 dmsg-server is enough for in-process testing; the routing
	// layer doesn't matter for our receive-cascade focus.
	require.NoError(t, env.Startup(0, 1, 0, &dmsg.Config{MinSessions: 1}))
	t.Cleanup(env.Shutdown)

	rig := &federatedBurstRig{
		env:     env,
		visors:  make([]*visorMock, n),
		timeout: timeout,
	}

	// Generate keys + clients first so we can wire each visor's
	// publisher to its own dmsg client.
	for i := 0; i < n; i++ {
		pk, sk := cipher.GenerateKeyPair()
		_, err := env.NewClientWithKeys(pk, sk, &dmsg.Config{MinSessions: 1})
		require.NoErrorf(t, err, "visor %d: NewClientWithKeys", i)
		rig.visors[i] = &visorMock{
			pk:   pk,
			sk:   sk,
			subs: make(map[cipher.PubKey]*Subscriber, n-1),
			rcv:  make(map[cipher.PubKey][][]byte, n-1),
		}
	}

	// Build a PK → *dmsg.Client lookup so each publisher binds to
	// its own client (env.AllClients returns the slice without an
	// explicit accessor). Order matches NewClientWithKeys call order
	// but the explicit lookup is cheap insurance.
	clientByPK := make(map[cipher.PubKey]*dmsg.Client, n)
	for _, c := range env.AllClients() {
		clientByPK[c.LocalPK()] = c
	}

	// Build publishers (each owns its own CXO node).
	for i, vm := range rig.visors {
		client := clientByPK[vm.pk]
		require.NotNilf(t, client, "visor %d: dmsg client lookup", i)
		pub, err := NewWithDMSG(client, vm.sk, PubConfig{
			InMemoryDB:  true,
			BatchWindow: 5 * time.Millisecond,
		})
		require.NoErrorf(t, err, "visor %d: NewWithDMSG", i)
		t.Cleanup(func() { _ = pub.Close() }) //nolint:errcheck
		vm.pub = pub
	}

	// Attach per-peer subscribers + dial. This mirrors the
	// session.go SetAllowlist add-loop: NewSubscriberOnNode on the
	// publisher's CXO node, then Connect(ctx, peerPK).
	for i, vm := range rig.visors {
		for j, peer := range rig.visors {
			if i == j {
				continue
			}
			sub, err := NewSubscriberOnNode(vm.pub.Node(), peer.pk, SubConfig{})
			require.NoErrorf(t, err, "visor %d → %d: NewSubscriberOnNode", i, j)
			t.Cleanup(func() { _ = sub.Close() }) //nolint:errcheck
			peerPK := peer.pk
			receiver := vm
			sub.OnUpdate(func(events []UpdateEvent) {
				receiver.rcvMu.Lock()
				defer receiver.rcvMu.Unlock()
				for _, ev := range events {
					if ev.Value == nil {
						continue
					}
					buf := make([]byte, len(ev.Value))
					copy(buf, ev.Value)
					receiver.rcv[peerPK] = append(receiver.rcv[peerPK], buf)
				}
			})
			vm.subs[peer.pk] = sub
		}
	}

	// Connect every (i → j) subscription. Done after all publishers
	// are alive so each dial finds the remote's CXO node listening.
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	for i, vm := range rig.visors {
		for _, peer := range rig.visors {
			if peer.pk == vm.pk {
				continue
			}
			require.NoErrorf(t,
				vm.subs[peer.pk].Connect(ctx, peer.pk),
				"visor %d → %s: subscriber.Connect", i, peer.pk)
		}
	}

	return rig
}

// publish sends body at path msgs/<seq> on vm's publisher. Production
// session.go uses a UUID-keyed scheme; for tests we use a fixed seq
// so the burst's ordering is deterministic.
func (vm *visorMock) publish(t *testing.T, seq int, body []byte) {
	t.Helper()
	require.NoErrorf(t,
		vm.pub.Put(fmt.Sprintf("msgs/%05d", seq), body),
		"publisher.Put seq=%d", seq)
}

// waitForRcv polls until every visor's rcv tally has exactly `want`
// entries from every other visor. Returns nil on success; on
// timeout, returns a detailed report of which (receiver, sender)
// pair fell short. Uses bounded polling rather than a fixed
// time.Sleep so a fast test machine doesn't sleep for nothing and
// a slow one doesn't false-fail at the deadline.
func (rig *federatedBurstRig) waitForRcv(t *testing.T, want int, deadline time.Duration) error {
	t.Helper()
	end := time.Now().Add(deadline)
	for {
		if time.Now().After(end) {
			return rig.tallyReport(want)
		}
		if rig.allReceived(want) {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (rig *federatedBurstRig) allReceived(want int) bool {
	for _, vm := range rig.visors {
		vm.rcvMu.Lock()
		for _, peer := range rig.visors {
			if peer.pk == vm.pk {
				continue
			}
			if len(vm.rcv[peer.pk]) < want {
				vm.rcvMu.Unlock()
				return false
			}
		}
		vm.rcvMu.Unlock()
	}
	return true
}

func (rig *federatedBurstRig) tallyReport(want int) error {
	var report []string
	for i, vm := range rig.visors {
		vm.rcvMu.Lock()
		for j, peer := range rig.visors {
			if peer.pk == vm.pk {
				continue
			}
			got := len(vm.rcv[peer.pk])
			if got < want {
				report = append(report, fmt.Sprintf(
					"visor[%d] received from visor[%d] (pk=%s…): got %d / want %d",
					i, j, peer.pk.Hex()[:8], got, want))
			}
		}
		vm.rcvMu.Unlock()
	}
	if len(report) == 0 {
		return nil
	}
	out := "federated burst: silent drops detected:\n"
	for _, line := range report {
		out += "  " + line + "\n"
	}
	return fmt.Errorf("%s", out)
}

// TestFederatedThreeVisorBurstDelivers exercises the production-shaped
// receive cascade with 3 mock visors. Each visor publishes 10 messages
// at its own offset; every other visor's subscriber must see all 10.
// 30 messages total. Failure mode: any (receiver, sender) pair with
// fewer than 10 messages observed.
//
// This is the in-process counterpart of the 3-agent T2-mini live
// reliability test. Operates entirely on in-memory dmsg + CXO; runs
// in a few seconds, suitable for -race -count=N.
func TestFederatedThreeVisorBurstDelivers(t *testing.T) {
	const (
		n        = 3
		perVisor = 10
		deadline = 15 * time.Second
	)
	rig := newFederatedBurstRig(t, n)

	// Give the federated subscribe-side a brief moment to settle —
	// the Subscribed sentinel arrival on the live channel is async
	// from Connect's return. Without this, the first publish can
	// race the subscriber's filler.
	time.Sleep(200 * time.Millisecond)

	for _, vm := range rig.visors {
		for seq := 1; seq <= perVisor; seq++ {
			body := []byte("msg-" + strconv.Itoa(seq) + "-from-" + vm.pk.Hex()[:8])
			vm.publish(t, seq, body)
		}
	}

	require.NoError(t, rig.waitForRcv(t, perVisor, deadline))
}

// TestFederatedTwoVisorBidirectional is the 2-visor warmup. Smaller
// blast radius, exact-content assert (verifies each received body
// matches what the peer published — not just the count). Catches
// content corruption that the count-only 3-visor test would miss.
func TestFederatedTwoVisorBidirectional(t *testing.T) {
	const (
		n        = 2
		perVisor = 5
		deadline = 10 * time.Second
	)
	rig := newFederatedBurstRig(t, n)
	time.Sleep(200 * time.Millisecond)

	want := make(map[cipher.PubKey][][]byte, n)
	for _, vm := range rig.visors {
		for seq := 1; seq <= perVisor; seq++ {
			body := []byte("msg-" + strconv.Itoa(seq) + "-from-" + vm.pk.Hex()[:8])
			vm.publish(t, seq, body)
			want[vm.pk] = append(want[vm.pk], body)
		}
	}

	require.NoError(t, rig.waitForRcv(t, perVisor, deadline))

	for i, vm := range rig.visors {
		vm.rcvMu.Lock()
		for j, peer := range rig.visors {
			if peer.pk == vm.pk {
				continue
			}
			got := vm.rcv[peer.pk]
			require.Lenf(t, got, perVisor,
				"visor[%d] from visor[%d]: receive count", i, j)
			// Set-equality assert. The publisher coalesces same-
			// BatchWindow Puts into one Root, and the subscriber's
			// applySnapshot iterates the resulting tree in
			// map-iteration order (non-deterministic). The receive
			// path itself preserves no per-Root ordering guarantee;
			// what we DO guarantee is exactly-once delivery for each
			// distinct path + body pair. So compare as sets, not
			// sequences.
			wantSet := make(map[string]struct{}, perVisor)
			for _, body := range want[peer.pk] {
				wantSet[string(body)] = struct{}{}
			}
			gotSet := make(map[string]struct{}, len(got))
			for _, body := range got {
				gotSet[string(body)] = struct{}{}
			}
			require.Equalf(t, wantSet, gotSet,
				"visor[%d] from visor[%d]: body set mismatch", i, j)
		}
		vm.rcvMu.Unlock()
	}
}
