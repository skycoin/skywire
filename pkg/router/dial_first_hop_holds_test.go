// dial_first_hop_holds_test.go: two standby-pool fills dialing at the same
// instant must not land on the same first-hop transport (#5125). The exclusion
// scan only sees route groups that already exist, so a dial still in flight is
// invisible to it — the claim registry is what makes it visible.
package router

import (
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/routing"
)

// The defect, in one assertion: two concurrent dials pick the same first hop
// (both read an empty exclusion set), and exactly one of them may have it.
func TestClaimFirstHop_ParallelDialsCannotShareAFirstHop(t *testing.T) {
	exit := mustPK(t)
	const rPort = routing.Port(3)
	src := mustPK(t)
	shared := uuid.New()

	r := &router{}
	path := directCand(src, exit, shared, 40)

	const dials = 8
	var (
		wg  sync.WaitGroup
		mu  sync.Mutex
		won int
	)
	start := make(chan struct{})
	for i := 0; i < dials; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if _, ok := r.claimFirstHop(exit, rPort, path); ok {
				mu.Lock()
				won++
				mu.Unlock()
			}
		}()
	}
	close(start)
	wg.Wait()

	require.Equal(t, 1, won,
		"eight simultaneous pool fills, one first-hop transport: exactly one tunnel may take it")
}

// A claim is reported as an exclusion, so a sibling dial's first-hop admission
// test refuses the claimed hop exactly as if a live route group held it — and
// stops refusing it once the dial that claimed it returns.
func TestInFlightFirstHopExclusions_FeedTheAdmissionTest(t *testing.T) {
	exit := mustPK(t)
	const rPort = routing.Port(3)
	src := mustPK(t)
	claimed, other := uuid.New(), uuid.New()

	r := &router{}
	inFlight := directCand(src, exit, claimed, 40)

	release, ok := r.claimFirstHop(exit, rPort, inFlight)
	require.True(t, ok)

	ids, peers, ips := r.inFlightFirstHopExclusions(exit, rPort)
	require.Equal(t, []uuid.UUID{claimed}, ids)
	require.Equal(t, 1, len(peers), "the hop's PEER is claimed too, not just its transport")
	require.Empty(t, ips, "no transport manager in this router, so no remote IP to claim")

	opts := &DialOptions{
		DiversifyTransports:     true,
		RequireDisjointFirstHop: true,
		ExcludeTransportIDs:     ids,
		ExcludeFirstHopPeers:    peers,
	}
	require.Empty(t, r.freeFirstHops([][]routing.Hop{inFlight}, opts),
		"a first hop an in-flight dial claimed is not free")
	require.True(t, r.firstHopExcluded(inFlight, opts))

	// The same peer over a SECOND transport rides the same link, and the peer
	// exclusion catches it.
	twin := directCand(src, exit, other, 41)
	require.Empty(t, r.freeFirstHops([][]routing.Hop{twin}, opts),
		"a twin transport to the claimed peer is the claimed link again")

	release()
	ids, peers, _ = r.inFlightFirstHopExclusions(exit, rPort)
	require.Empty(t, ids, "the claim is dropped when the dial returns")
	require.Empty(t, peers)
	require.Len(t, r.freeFirstHops([][]routing.Hop{inFlight}, &DialOptions{DiversifyTransports: true}), 1)
}

// Claims are per destination: a tunnel to one exit never blocks a tunnel to
// another from using the same first hop.
func TestClaimFirstHop_ScopedToTheDestination(t *testing.T) {
	src, exitA, exitB := mustPK(t), mustPK(t), mustPK(t)
	shared := uuid.New()
	r := &router{}

	_, ok := r.claimFirstHop(exitA, 3, directCand(src, exitA, shared, 40))
	require.True(t, ok)
	_, ok = r.claimFirstHop(exitB, 3, directCand(src, exitB, shared, 40))
	require.True(t, ok, "a different exit is a different pool")

	ids, _, _ := r.inFlightFirstHopExclusions(exitA, 3)
	require.Len(t, ids, 1)
}

// An empty path claims nothing, so callers need not special-case it.
func TestClaimFirstHop_EmptyPath(t *testing.T) {
	r := &router{}
	release, ok := r.claimFirstHop(mustPK(t), 3, nil)
	require.True(t, ok)
	release()
}
