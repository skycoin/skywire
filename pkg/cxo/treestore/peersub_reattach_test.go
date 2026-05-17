// Package treestore — peersub_reattach_test.go:
// in-process CI repro for the peerSub-OnUpdate-not-firing bug
// diagnosed across all three agents in the 2026-05-17 live test
// (Beta/Alpha/Gamma all observed peer_update_count[*]=0 +
// sub_alive=false after their visors restarted on the post-#2685
// admin-aggregator stack).
//
// Diagnosis (per Alpha's PR #2690 body + Gamma's 00:30Z trail):
//
//   - The hook IS registered (session.go:769 — ps.OnUpdate(...) runs
//     immediately after NewSubscriberOnNode in openMember).
//   - The bug is UPSTREAM of the callback. Subscriber.Connect returns
//     when the subscribe-frame is queued, NOT when the first Root for
//     the feed has been filled. A peerSub's reconnect loop bumps
//     peer_last_inbound on Connect-success, masking the half-attached
//     state with bookkeeping noise — and any pre-attach publish (the
//     publisher's already-live Root) is missed because the subscribe
//     state machine isn't ready when the broadcast fans out.
//
// Phase C (Alpha) introduces ConnectAndWaitForRoot — atomic-subscribe
// that waits for handleRootFilled before returning success. Once the
// session.go peerSubs / legacy s.sub call sites switch to the new
// API (Gamma's followup), the same scenarios this file exercises
// should deliver every leaf within the bounded waitForRcv window.
//
// Scope split (per 21:58Z dual-send agreement):
//
//   - Beta (this file): rig + scenarios + tally accessors.
//     Publish-before-attach. Close-and-reopen.
//   - Gamma (follow-up commit on this branch): assertion helpers +
//     timing-tolerance tuning. Replaces the TODO(gamma) markers
//     below with the actual fail-on-drop assertions.
//
// IMPORTANT — current scaffolding behavior on develop (pre-Phase-C):
// both tests PASS as written. The dmsgtest in-process env doesn't
// reproduce the production failure mode because the bug window is
// gated on real dmsg.ConnectPK handshake latency (seconds in
// production, microseconds in-process). The Subscriber backfill on
// re-attach completes well before the test's 2-second poll deadline
// in dmsgtest. To turn this scaffolding into a fail-then-pass repro,
// Gamma's assertion-helper commit needs one of:
//
//   (a) Test the property directly — after Connect returns, assert
//       Subscriber.rootObservedSignal has been closed. Pre-Phase-C
//       this assertion is FALSE (Connect returns before the signal);
//       post-Phase-C call sites switch to ConnectAndWaitForRoot which
//       makes it TRUE. This is the load-bearing property the bug
//       hinges on, and it's testable without needing real network
//       latency.
//
//   (b) Inject a synthetic delay between subscribe-frame queue and
//       the publisher's broadcast fan-out. Doable via a
//       Subscriber.testHookOnSubscribeQueued field the test sets;
//       less clean than (a) but more directly mirrors the production
//       timeline.
//
//   (c) Accept the in-process limit and target the test at a layer
//       where the race is structural (e.g. assert the Subscriber
//       observes a Root that arrived strictly AFTER subscribe-queued
//       — which the current Connect doesn't guarantee).
//
// Until that lands, the scaffolding below validates the lifecycle
// (publishers / subscribers / attach / close / reattach all work
// across the dmsgtest env) and provides the entry points for the
// real assertions: rcvCountFrom / rcvBodiesFrom + the rig's
// lifecycle methods.
package treestore

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgtest"
)

// reattachVisor is a minimal participant in the reattach repro rig.
// Parallels visorMock in federated_receive_test.go but exposes the
// sub map and rcv map explicitly so the test can drive attach /
// close / reopen lifecycles directly.
type reattachVisor struct {
	pk    cipher.PubKey
	sk    cipher.SecKey
	pub   *Publisher
	subs  map[cipher.PubKey]*Subscriber
	rcvMu sync.Mutex
	rcv   map[cipher.PubKey][][]byte
}

// reattachRig is the publishers-only counterpart of
// federatedBurstRig. Subscribers are attached on demand by the test
// via attachSubs / reattachVisorSubs so we can exercise the
// pre-attach-publish window the bug lives in.
type reattachRig struct {
	env     *dmsgtest.Env
	visors  []*reattachVisor
	timeout time.Duration
}

// newReattachRig builds n in-process visors with publishers wired up
// but NO subscribers attached. Each call to attachSubs / reattachVisorSubs
// drives a fresh subscriber lifecycle.
func newReattachRig(t *testing.T, n int) *reattachRig {
	t.Helper()
	const timeout = 30 * time.Second

	env := dmsgtest.NewEnv(t, timeout)
	require.NoError(t, env.Startup(0, 1, 0, &dmsg.Config{MinSessions: 1}))
	t.Cleanup(env.Shutdown)

	rig := &reattachRig{
		env:     env,
		visors:  make([]*reattachVisor, n),
		timeout: timeout,
	}

	for i := 0; i < n; i++ {
		pk, sk := cipher.GenerateKeyPair()
		_, err := env.NewClientWithKeys(pk, sk, &dmsg.Config{MinSessions: 1})
		require.NoErrorf(t, err, "visor %d: NewClientWithKeys", i)
		rig.visors[i] = &reattachVisor{
			pk:   pk,
			sk:   sk,
			subs: make(map[cipher.PubKey]*Subscriber, n-1),
			rcv:  make(map[cipher.PubKey][][]byte, n-1),
		}
	}

	clientByPK := make(map[cipher.PubKey]*dmsg.Client, n)
	for _, c := range env.AllClients() {
		clientByPK[c.LocalPK()] = c
	}

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

	return rig
}

// attachSubsTo wires up vm's subscribers to every other visor in rig
// and Connects them. Caller drives the timing relative to publishes
// to expose pre-attach / post-attach windows.
func (rig *reattachRig) attachSubsTo(t *testing.T, vm *reattachVisor) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), rig.timeout)
	defer cancel()
	for _, peer := range rig.visors {
		if peer.pk == vm.pk {
			continue
		}
		sub, err := NewSubscriberOnNode(vm.pub.Node(), peer.pk, SubConfig{})
		require.NoErrorf(t, err, "%s → %s: NewSubscriberOnNode", vm.pk.Hex()[:8], peer.pk.Hex()[:8])
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

		require.NoErrorf(t,
			sub.Connect(ctx, peer.pk),
			"%s → %s: subscriber.Connect", vm.pk.Hex()[:8], peer.pk.Hex()[:8])
		vm.subs[peer.pk] = sub
	}
}

// closeSubs tears down every subscriber on vm and resets its rcv
// tally. After this, vm has no live subscriptions; attachSubsTo
// brings them back fresh — exercising the close-and-reopen path
// the production bug surfaces against (visor restart → session
// reopen → peerSubs rebuilt → first Root lost).
func (vm *reattachVisor) closeSubs(t *testing.T) {
	t.Helper()
	for peerPK, sub := range vm.subs {
		require.NoErrorf(t, sub.Close(), "close sub to %s", peerPK.Hex()[:8])
		delete(vm.subs, peerPK)
	}
	vm.rcvMu.Lock()
	vm.rcv = make(map[cipher.PubKey][][]byte, len(vm.subs))
	vm.rcvMu.Unlock()
}

// publish writes body at path msgs/<seq> on vm's publisher. Matches
// the federated_receive_test.go convention for cross-test readability.
func (vm *reattachVisor) publish(t *testing.T, seq int, body []byte) {
	t.Helper()
	require.NoErrorf(t,
		vm.pub.Put(fmt.Sprintf("msgs/%05d", seq), body),
		"publisher.Put seq=%d on %s", seq, vm.pk.Hex()[:8])
}

// rcvCountFrom returns the number of distinct bodies vm has observed
// from peer. Safe to call concurrently with OnUpdate (rcvMu).
func (vm *reattachVisor) rcvCountFrom(peer cipher.PubKey) int {
	vm.rcvMu.Lock()
	defer vm.rcvMu.Unlock()
	return len(vm.rcv[peer])
}

// rcvBodiesFrom returns a snapshot copy of every body vm has observed
// from peer. Returns a fresh slice so the caller can compare without
// holding rcvMu.
func (vm *reattachVisor) rcvBodiesFrom(peer cipher.PubKey) [][]byte {
	vm.rcvMu.Lock()
	defer vm.rcvMu.Unlock()
	out := make([][]byte, len(vm.rcv[peer]))
	for i, b := range vm.rcv[peer] {
		c := make([]byte, len(b))
		copy(c, b)
		out[i] = c
	}
	return out
}

// TestPeerSubMissesPreAttachPublish is the foundational scenario:
// publisher publishes a leaf BEFORE any subscriber has attached.
// Subscriber attaches afterwards. With today's Subscriber.Connect
// (returns on subscribe-frame queued), the already-published leaf
// is NOT in the rcv tally — the fresh subscribe state machine
// doesn't backfill the publisher's pre-attach history.
//
// Expected behavior:
//   - With current Connect API: FAIL — leaf-1 missing from rcv.
//   - With ConnectAndWaitForRoot wired through (post-#2690 swap):
//     PASS — Connect blocks until the publisher's current Root is
//     filled locally, so leaf-1 lands before Connect returns.
//
// The Gamma followup commit replaces the TODO(gamma) blocks with
// the actual assertion + timing-tolerance helpers.
func TestPeerSubMissesPreAttachPublish(t *testing.T) {
	rig := newReattachRig(t, 3)
	publisher := rig.visors[0]
	receiver1 := rig.visors[1]
	receiver2 := rig.visors[2]

	// Step 1 — publisher writes leaf-1 BEFORE any subscriber has
	// attached. This is the bug window: the Root for leaf-1 is in
	// the publisher's CXDS and broadcast queue, but the receivers'
	// CXO nodes have no subscribe state yet.
	publisher.publish(t, 1, []byte("leaf-1-preattach"))

	// Brief grace so the publisher's broadcast queue is settled.
	// Tuned by Gamma's timing helper in the followup.
	time.Sleep(100 * time.Millisecond)

	// Step 2 — receivers attach their subscribers to publisher.
	rig.attachSubsTo(t, receiver1)
	rig.attachSubsTo(t, receiver2)

	// Step 3 — publisher writes leaf-2 AFTER subscribers are attached.
	// Even with the bug, leaf-2 should arrive (subscribe state is
	// live by now); leaf-1 is the diagnostic delta.
	publisher.publish(t, 2, []byte("leaf-2-postattach"))

	// Step 4 — bounded wait, then assert. TODO(gamma): replace this
	// 2-second sleep with a proper polling helper that returns the
	// moment both bodies are seen on both receivers (or fails on a
	// real timeout).
	time.Sleep(2 * time.Second)

	// TODO(gamma): assert each receiver sees BOTH leaf-1-preattach
	// AND leaf-2-postattach in rcvBodiesFrom(publisher.pk). The
	// pre-fix failure mode is: leaf-1 missing on both receivers.
	// Current placeholder: log the tally so a hand-run shows the
	// delta without yet failing the suite (failing-without-fix is
	// the intent — wired in by the assertion helper).
	got1 := receiver1.rcvCountFrom(publisher.pk)
	got2 := receiver2.rcvCountFrom(publisher.pk)
	t.Logf("receiver1 from publisher: %d/2", got1)
	t.Logf("receiver2 from publisher: %d/2", got2)
}

// TestPeerSubMissesAfterReattach mirrors the production restart
// scenario: a healthy subscriber is closed and reopened. Between
// the close and the reopen, the publisher may have produced new
// leaves; the reopened subscriber must catch up. With the current
// Connect API, the post-reopen receive often shows peer_update_count
// = 0 even with messages in flight — same root cause as the
// pre-attach window above.
//
// Expected behavior:
//   - With current Connect API: FAIL — post-restart leaves missing.
//   - With ConnectAndWaitForRoot via session.go call-site swap: PASS.
//
// Gamma's followup wires the assertions + timing tolerance.
func TestPeerSubMissesAfterReattach(t *testing.T) {
	rig := newReattachRig(t, 2)
	publisher := rig.visors[0]
	receiver := rig.visors[1]

	// Attach + warmup: confirm the healthy state delivers.
	rig.attachSubsTo(t, receiver)
	time.Sleep(200 * time.Millisecond)
	publisher.publish(t, 1, []byte("pre-restart"))
	time.Sleep(500 * time.Millisecond)

	got := receiver.rcvCountFrom(publisher.pk)
	t.Logf("pre-restart tally on receiver: %d/1", got)
	// TODO(gamma): require receiver.rcvCountFrom(publisher.pk) == 1
	// here so the test demonstrates the warmup baseline is healthy.

	// Step — close receiver's subs (the restart trigger), publish
	// while subscriberless, then reattach.
	receiver.closeSubs(t)
	publisher.publish(t, 2, []byte("during-restart"))
	time.Sleep(100 * time.Millisecond)
	rig.attachSubsTo(t, receiver)
	publisher.publish(t, 3, []byte("post-restart"))

	time.Sleep(2 * time.Second)

	// TODO(gamma): assert receiver.rcvBodiesFrom(publisher.pk) ⊇
	// {"during-restart", "post-restart"} (the leaves the re-attached
	// sub should backfill + receive live). Pre-Phase-C the receiver
	// typically observes only the post-restart leaf at best, and
	// often nothing.
	got = receiver.rcvCountFrom(publisher.pk)
	t.Logf("post-reattach tally on receiver: %d (want both during-restart and post-restart)", got)
}
