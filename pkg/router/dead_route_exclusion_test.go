//go:build !tinygo || (js && wasm)

package router

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/routing"
)

// deadRoutePath builds a 2-hop forward path leaving over tpID through mid.
func deadRoutePath(tpID uuid.UUID, mid cipher.PubKey) []routing.Hop {
	src, _ := cipher.GenerateKeyPair()
	dst, _ := cipher.GenerateKeyPair()
	return []routing.Hop{
		{TpID: tpID, From: src, To: mid},
		{TpID: uuid.New(), From: mid, To: dst},
	}
}

// The live regression (bench/2026-09-16/c302e1746-smoke/mux-standby-3): three
// consecutive pool dials picked the SAME first hop + intermediate and each group
// closed at exactly handshakeAwaitTimeout. The diversify filter must not hand
// that route back on the next dial.
func TestDeadRouteExcludedFromDiversifyPick(t *testing.T) {
	r := &router{deadRoutes: newDeadRouteCache(deadRouteTTLDefault, deadRouteMaxTTLDefault)}
	mid, _ := cipher.GenerateKeyPair()
	deadTp, liveTp := uuid.New(), uuid.New()
	dead := deadRoutePath(deadTp, mid)
	live := deadRoutePath(liveTp, mid)

	opts := &DialOptions{DiversifyTransports: true}
	require.Len(t, r.freeFirstHops([][]routing.Hop{dead, live}, opts), 2,
		"nothing is excluded before a death is recorded")

	require.True(t, r.deadRoutes.mark(dead, handshakeAwaitTimeout, time.Now()),
		"a group that died at the handshake timeout is young enough to remember")

	opts = &DialOptions{DiversifyTransports: true}
	keep := r.freeFirstHops([][]routing.Hop{dead, live}, opts)
	require.Len(t, keep, 1)
	require.Equal(t, liveTp, keep[0][0].TpID)
	trail := strings.Join(opts.dialNotes, "; ")
	require.Contains(t, trail, "died 10.0s after dial at")
	require.Contains(t, trail, deadTp.String()[:8])
}

// A route that carried payload, or that lived a normal life before closing, is
// not remembered — only a young death with no bytes is evidence.
func TestDeadRouteOnlyRemembersYoungDeaths(t *testing.T) {
	c := newDeadRouteCache(deadRouteTTLDefault, deadRouteMaxTTLDefault)
	mid, _ := cipher.GenerateKeyPair()
	p := deadRoutePath(uuid.New(), mid)

	require.False(t, c.mark(p, deadRouteYoungAge+time.Second, time.Now()),
		"a group that lived past the young window is a normal teardown")
	_, bad := c.excluded(p, time.Now())
	require.False(t, bad)

	require.True(t, c.mark(p, 5*time.Second, time.Now()))
	_, bad = c.excluded(p, time.Now())
	require.True(t, bad)

	// Proving itself on a later dial clears the memory outright.
	c.clear(p)
	_, bad = c.excluded(p, time.Now())
	require.False(t, bad)
}

// A repeat death backs the exclusion off; the window expires on its own.
func TestDeadRouteBackoffAndExpiry(t *testing.T) {
	c := newDeadRouteCache(time.Minute, 4*time.Minute)
	mid, _ := cipher.GenerateKeyPair()
	p := deadRoutePath(uuid.New(), mid)
	now := time.Now()

	c.mark(p, time.Second, now)
	e, bad := c.excluded(p, now)
	require.True(t, bad)
	require.Equal(t, time.Minute, e.ttl)
	require.Equal(t, 1, e.strikes)

	c.mark(p, time.Second, now)
	e, _ = c.excluded(p, now)
	require.Equal(t, 2*time.Minute, e.ttl)
	require.Equal(t, 2, e.strikes)

	_, bad = c.excluded(p, now.Add(2*time.Minute+time.Second))
	require.False(t, bad, "the exclusion is short-lived, not permanent")
}

// The key is first hop AND the rest of the path: a different route over the
// same transport is a different experiment and stays dialable.
func TestDeadRouteKeyedOnWholeRouteNotJustFirstHop(t *testing.T) {
	c := newDeadRouteCache(deadRouteTTLDefault, deadRouteMaxTTLDefault)
	midA, _ := cipher.GenerateKeyPair()
	midB, _ := cipher.GenerateKeyPair()
	tp := uuid.New()
	viaA, viaB := deadRoutePath(tp, midA), deadRoutePath(tp, midB)

	c.mark(viaA, time.Second, time.Now())
	_, bad := c.excluded(viaA, time.Now())
	require.True(t, bad)
	_, bad = c.excluded(viaB, time.Now())
	require.False(t, bad, "same first hop, different intermediate: still worth a dial")
}

// The filter never fails a dial outright: if every candidate is excluded the
// set passes through so the caller retries the least-bad route.
func TestDeadRouteFilterNeverEmptiesTheCandidateSet(t *testing.T) {
	r := &router{deadRoutes: newDeadRouteCache(deadRouteTTLDefault, deadRouteMaxTTLDefault)}
	mid, _ := cipher.GenerateKeyPair()
	a := deadRoutePath(uuid.New(), mid)
	b := deadRoutePath(uuid.New(), mid)
	r.deadRoutes.mark(a, time.Second, time.Now())
	r.deadRoutes.mark(b, time.Second, time.Now())

	opts := &DialOptions{DiversifyTransports: true}
	require.Len(t, r.freeFirstHops([][]routing.Hop{a, b}, opts), 2)
	require.Contains(t, strings.Join(opts.dialNotes, "; "), "keeping them rather than failing the dial")
}
