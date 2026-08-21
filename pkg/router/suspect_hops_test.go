// suspect_hops_test.go — unit tests for the self-heal failed-path exclusion
// (dead-edge route-setup fix, the route-level-failover follow-up to #4057).
// Covers the suspect-hop TTL cache in isolation plus the fetchBestRoutes
// candidate-count widening that lets the exclusion actually fail over when the
// finder's default top-3 all funnel through one dead intermediary.
package router

import (
	"context"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/rfclient"
	"github.com/skycoin/skywire/pkg/routing"
)

func TestResolveFailedHopExclusionTTL(t *testing.T) {
	if got := resolveFailedHopExclusionTTL(0); got != defaultSuspectHopTTL {
		t.Errorf("0 → %v, want default %v", got, defaultSuspectHopTTL)
	}
	if got := resolveFailedHopExclusionTTL(-1); got != -1 {
		t.Errorf("negative → %v, want -1 (disabled passthrough)", got)
	}
	if got := resolveFailedHopExclusionTTL(42 * time.Second); got != 42*time.Second {
		t.Errorf("positive → %v, want 42s", got)
	}
}

func TestSuspectHopCache_MarkAndSuspects(t *testing.T) {
	dst := mustPK(t)
	a, b := mustPK(t), mustPK(t)
	c := newSuspectHopCache(time.Minute)
	c.mark(dst, []cipher.PubKey{a, b})

	got := c.suspects(dst)
	if len(got) != 2 {
		t.Fatalf("suspects=%d, want 2", len(got))
	}
	set := map[cipher.PubKey]struct{}{got[0]: {}, got[1]: {}}
	if _, ok := set[a]; !ok {
		t.Errorf("suspect a missing")
	}
	if _, ok := set[b]; !ok {
		t.Errorf("suspect b missing")
	}
}

func TestSuspectHopCache_PerDestinationIsolation(t *testing.T) {
	dstA, dstB := mustPK(t), mustPK(t)
	hop := mustPK(t)
	c := newSuspectHopCache(time.Minute)
	c.mark(dstA, []cipher.PubKey{hop})

	if got := c.suspects(dstB); len(got) != 0 {
		t.Errorf("dstB leaked %d suspects from dstA", len(got))
	}
	if got := c.suspects(dstA); len(got) != 1 {
		t.Errorf("dstA suspects=%d, want 1", len(got))
	}
}

func TestSuspectHopCache_Disabled(t *testing.T) {
	dst := mustPK(t)
	hop := mustPK(t)
	for _, ttl := range []time.Duration{-time.Second, -1} {
		c := newSuspectHopCache(ttl)
		c.mark(dst, []cipher.PubKey{hop})
		if got := c.suspects(dst); got != nil {
			t.Errorf("ttl=%v: suspects=%v, want nil (disabled)", ttl, got)
		}
	}
}

func TestSuspectHopCache_Expiry(t *testing.T) {
	dst := mustPK(t)
	a, b := mustPK(t), mustPK(t)
	c := newSuspectHopCache(time.Minute)
	c.mark(dst, []cipher.PubKey{a, b})

	// Force a to be expired; b stays live. suspects must prune a and return b.
	c.mu.Lock()
	c.m[dst][a] = time.Now().Add(-time.Second)
	c.mu.Unlock()

	got := c.suspects(dst)
	if len(got) != 1 || got[0] != b {
		t.Fatalf("after expiry: got=%v, want [b]", got)
	}

	// Expiring the last entry should drop the destination key entirely.
	c.mu.Lock()
	c.m[dst][b] = time.Now().Add(-time.Second)
	c.mu.Unlock()
	if got := c.suspects(dst); got != nil {
		t.Errorf("after full expiry: got=%v, want nil", got)
	}
	c.mu.Lock()
	_, present := c.m[dst]
	c.mu.Unlock()
	if present {
		t.Errorf("destination key not pruned after full expiry")
	}
}

func TestSuspectHopCache_SkipsNull(t *testing.T) {
	dst := mustPK(t)
	c := newSuspectHopCache(time.Minute)
	c.mark(dst, []cipher.PubKey{{}}) // null PK only
	if got := c.suspects(dst); len(got) != 0 {
		t.Errorf("null PK stored: got=%v", got)
	}
}

func TestSuspectHopCache_NilReceiver(t *testing.T) {
	var c *suspectHopCache
	// Must not panic on a nil cache (routers built without one in narrow tests).
	c.mark(mustPK(t), []cipher.PubKey{mustPK(t)})
	if got := c.suspects(mustPK(t)); got != nil {
		t.Errorf("nil cache suspects=%v, want nil", got)
	}
}

// recordingFinder is a minimal rfclient.Client that records the NumRoutes it
// was last asked for and returns a fixed candidate set.
type recordingFinder struct {
	lastNumRoutes uint16
	paths         map[routing.PathEdges][][]routing.Hop
}

func (f *recordingFinder) FindRoutes(_ context.Context, _ []routing.PathEdges, opts *rfclient.RouteOptions) (map[routing.PathEdges][][]routing.Hop, error) {
	if opts != nil {
		f.lastNumRoutes = opts.NumRoutes
	}
	return f.paths, nil
}

// TestFetchBestRoutes_WidensCandidatesWhenExcluding verifies the Part-A half of
// the fix: with an intermediate excluded, fetchBestRoutes asks the finder for a
// wider candidate set (so an alternate avoiding the dead hop can be surfaced),
// whereas a plain single-route dial keeps the finder's default.
func TestFetchBestRoutes_WidensCandidatesWhenExcluding(t *testing.T) {
	src, dst := mustPK(t), mustPK(t)
	inter := mustPK(t)

	forward := routing.PathEdges{src, dst}
	backward := routing.PathEdges{dst, src}
	finder := &recordingFinder{
		paths: map[routing.PathEdges][][]routing.Hop{
			forward:  {{hop(src, inter), hop(inter, dst)}},
			backward: {{hop(dst, inter), hop(inter, src)}},
		},
	}
	r := &router{
		conf:   &Config{RouteFinder: finder, MaxHops: 5},
		logger: logging.MustGetLogger("test"),
	}

	// No exclusions: single-route dial → finder default (0 = use its own).
	// Retries>0 is required for fetchBestRoutes to consume the finder result
	// (retries==0 short-circuits to the local-calc fallback).
	if _, _, err := r.fetchBestRoutes(context.Background(), nil, src, dst, &DialOptions{Retries: 3}, 2); err != nil {
		t.Fatalf("fetch (no exclude) err: %v", err)
	}
	if finder.lastNumRoutes != 0 {
		t.Errorf("no-exclude NumRoutes=%d, want 0 (finder default)", finder.lastNumRoutes)
	}

	// Exclude an unrelated PK (so the candidate through `inter` survives the
	// filter): fetch must widen the requested candidate count.
	excludeOnly := mustPK(t)
	opts := &DialOptions{Retries: 3, ExcludeIntermediatePKs: []cipher.PubKey{excludeOnly}}
	if _, _, err := r.fetchBestRoutes(context.Background(), nil, src, dst, opts, 2); err != nil {
		t.Fatalf("fetch (with exclude) err: %v", err)
	}
	if finder.lastNumRoutes != widenedRouteCandidates {
		t.Errorf("with-exclude NumRoutes=%d, want %d (widened)", finder.lastNumRoutes, widenedRouteCandidates)
	}
}
