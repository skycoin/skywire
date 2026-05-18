// Package treestore — federated_full_mesh_baseline_test.go:
// Phase 1d of the operator's 2026-05-17 22:50Z reset (per Alpha's
// 23:02Z work-split V2: "Gamma owns 1d — full-mesh CXO group test:
// N members each publish to own feed + subscribe to ALL other
// members' feeds. Validates that CXO actually converges in a
// multi-subscriber group without the federated layer.").
//
// Builds on the existing federatedBurstRig (N-visor in-process
// shape from #2673) with two new tests that strain CXO multi-
// subscriber convergence beyond what TestFederatedThreeVisorBurst
// covers:
//
//   - N=4 burst (12 directed (sender, receiver) pairs vs N=3's 6)
//     with full content set-equality — catches both count drops
//     AND content corruption that would slip past N=3's count-only
//     assertion.
//
//   - N=4 concurrent burst (all 4 publishers fire from goroutines
//     simultaneously) — stresses the convergence under publisher
//     contention, mirroring what real groups look like when 4
//     members chat at once.
//
// Both pin the "CXO converges in full-mesh pre-admin-aggregator
// shape" contract. Phase 2 (admin-aggregator federated group, the
// merged #2685/#2688/#2689 stack) can be re-validated against this
// baseline only after both green.

package treestore

import (
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

// TestFullMeshFourVisor_AllPairsDeliverByContent extends the
// federated convergence guarantee to N=4. With 4 visors we have
// 12 directed (sender, receiver) pairs vs N=3's 6 — a regression
// that scaled with the count of subscribers per node would only
// surface when N crosses some threshold; N=4 is the minimum
// non-trivial scale-up.
//
// Asserts set-equality per (sender, receiver) pair: every body the
// sender published must appear in the receiver's tally, exactly
// once. No ordering assertion — the existing N=2 test's docstring
// documents that map-iteration order in applySnapshot breaks per-
// Root ordering, and Phase 1's pairing-layer #2697 confirmed the
// same ordering quirk surfaces all the way up to the user handler.
func TestFullMeshFourVisor_AllPairsDeliverByContent(t *testing.T) {
	const (
		n        = 4
		perVisor = 8
		deadline = 20 * time.Second
	)
	rig := newFederatedBurstRig(t, n)

	// Brief settle window matches the N=2/N=3 tests — the Subscribed
	// sentinel arrival on the live channel is async from Connect's
	// return.
	time.Sleep(200 * time.Millisecond)

	// Each visor publishes a deterministic body set so we can
	// assert content set-equality, not just counts.
	want := make(map[cipher.PubKey][][]byte, n)
	for _, vm := range rig.visors {
		for seq := 1; seq <= perVisor; seq++ {
			body := []byte("msg-" + strconv.Itoa(seq) + "-from-" + vm.pk.Hex()[:8])
			vm.publish(t, seq, body)
			want[vm.pk] = append(want[vm.pk], body)
		}
	}

	require.NoError(t, rig.waitForRcv(t, perVisor, deadline))

	// Set-equality assert per (receiver, sender) pair. 4 visors,
	// 12 directed pairs, perVisor messages each = 96 message
	// observations total. Failure mode names the offending pair
	// so a regression points at the right edge of the mesh.
	for i, vm := range rig.visors {
		vm.rcvMu.Lock()
		for j, peer := range rig.visors {
			if peer.pk == vm.pk {
				continue
			}
			got := vm.rcv[peer.pk]
			require.Lenf(t, got, perVisor,
				"visor[%d] received from visor[%d]: count drift (got %d, want %d)",
				i, j, len(got), perVisor)

			wantSet := make(map[string]struct{}, perVisor)
			for _, body := range want[peer.pk] {
				wantSet[string(body)] = struct{}{}
			}
			gotSet := make(map[string]struct{}, len(got))
			for _, body := range got {
				gotSet[string(body)] = struct{}{}
			}
			require.Equalf(t, wantSet, gotSet,
				"visor[%d] received from visor[%d]: content set mismatch", i, j)
		}
		vm.rcvMu.Unlock()
	}
}

// TestFullMeshFourVisor_ConcurrentBurstConverges fires N
// publishers in parallel from goroutines. This is the realistic
// "4 chat-app members typing at once" shape — the existing burst
// tests serialize publishes per visor, which leaves the
// publisher's batch-window coalescing path under-exercised. A
// regression that silently drops on concurrent publisher
// contention (e.g. a publisher-local lock that doesn't fan out
// properly) would surface here but not in the serialized tests.
//
// Same set-equality assertion as the serialized N=4 test — but the
// publish-side path is the contention point under test, not the
// receive side.
func TestFullMeshFourVisor_ConcurrentBurstConverges(t *testing.T) {
	const (
		n        = 4
		perVisor = 8
		deadline = 25 * time.Second
	)
	rig := newFederatedBurstRig(t, n)
	time.Sleep(200 * time.Millisecond)

	want := make(map[cipher.PubKey][][]byte, n)
	// Pre-compute the bodies so the inner goroutines have nothing
	// to allocate while contending on the publisher.
	for _, vm := range rig.visors {
		for seq := 1; seq <= perVisor; seq++ {
			body := []byte("conc-" + strconv.Itoa(seq) + "-from-" + vm.pk.Hex()[:8])
			want[vm.pk] = append(want[vm.pk], body)
		}
	}

	var wg sync.WaitGroup
	wg.Add(n)
	for _, vm := range rig.visors {
		vm := vm
		go func() {
			defer wg.Done()
			for seq := 1; seq <= perVisor; seq++ {
				vm.publish(t, seq, want[vm.pk][seq-1])
			}
		}()
	}
	wg.Wait()

	require.NoError(t, rig.waitForRcv(t, perVisor, deadline))

	for i, vm := range rig.visors {
		vm.rcvMu.Lock()
		for j, peer := range rig.visors {
			if peer.pk == vm.pk {
				continue
			}
			got := vm.rcv[peer.pk]
			require.Lenf(t, got, perVisor,
				"concurrent burst visor[%d] from visor[%d]: count drift (got %d, want %d)",
				i, j, len(got), perVisor)

			wantSet := make(map[string]struct{}, perVisor)
			for _, body := range want[peer.pk] {
				wantSet[string(body)] = struct{}{}
			}
			gotSet := make(map[string]struct{}, len(got))
			for _, body := range got {
				gotSet[string(body)] = struct{}{}
			}
			require.Equalf(t, wantSet, gotSet,
				"concurrent burst visor[%d] from visor[%d]: content set mismatch under contention", i, j)
		}
		vm.rcvMu.Unlock()
	}
}

// TestFullMeshFourVisor_NoCrossContamination verifies the receiver
// invariant that vm.rcv[peerPK] only contains leaves PUBLISHED by
// peerPK — never self-echoes, never accidental fan-in from a third
// visor's feed. A regression that wired up the OnUpdate handler to
// the wrong feed (e.g. by capturing a loop variable wrong, which
// the rig's closure carefully avoids) would surface as the
// "wrong-sender" body appearing in a tally.
//
// Distinct from the count/content tests because it's a NEGATIVE
// assertion: the receiver MUST NOT see content it didn't subscribe
// to. Catches a class of routing bug the positive tests trivially
// pass on (they only check "did the right content arrive", not
// "did wrong content not arrive").
func TestFullMeshFourVisor_NoCrossContamination(t *testing.T) {
	const (
		n        = 4
		perVisor = 4
		deadline = 15 * time.Second
	)
	rig := newFederatedBurstRig(t, n)
	time.Sleep(200 * time.Millisecond)

	// Each visor's bodies carry the visor's PK-prefix so we can
	// detect cross-contamination unambiguously: a body labeled
	// "from-<senderPK>" must ONLY appear in vm.rcv[senderPK], for
	// every receiver vm != sender.
	for _, vm := range rig.visors {
		for seq := 1; seq <= perVisor; seq++ {
			body := []byte("isol-" + strconv.Itoa(seq) + "-from-" + vm.pk.Hex()[:8])
			vm.publish(t, seq, body)
		}
	}
	require.NoError(t, rig.waitForRcv(t, perVisor, deadline))

	for i, vm := range rig.visors {
		vm.rcvMu.Lock()
		for j, peer := range rig.visors {
			if peer.pk == vm.pk {
				// Self-receive must be empty: no entry exists in the
				// rcv map for own PK because the rig skips self
				// subscriptions. Sanity-pin it so a future refactor
				// that accidentally enabled self-subscription would
				// fail loudly here.
				require.Empty(t, vm.rcv[vm.pk],
					"visor[%d] self-receive should be empty (full-mesh excludes self)", i)
				continue
			}
			wantPrefix := "isol-"
			wantFromTag := "-from-" + peer.pk.Hex()[:8]
			for _, body := range vm.rcv[peer.pk] {
				s := string(body)
				require.Containsf(t, s, wantPrefix,
					"visor[%d] from visor[%d]: body not isolation-tagged: %q", i, j, s)
				require.Containsf(t, s, wantFromTag,
					"visor[%d] from visor[%d]: cross-contamination — body %q lacks expected sender tag", i, j, s)
			}
		}
		vm.rcvMu.Unlock()
	}
}
