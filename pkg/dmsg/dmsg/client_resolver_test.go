package dmsg

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/logging"
)

// fakeResolver is an EntryResolver with scripted behavior.
type fakeResolver struct {
	name    string
	entry   *disc.Entry
	err     error
	delay   time.Duration
	calls   int
	lastCtx context.Context //nolint:containedctx // recorded to assert the timeout is applied
}

func (f *fakeResolver) Name() string { return f.name }

func (f *fakeResolver) Entry(ctx context.Context, _ cipher.PubKey) (*disc.Entry, error) {
	f.calls++
	f.lastCtx = ctx
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return f.entry, f.err
}

func clientEntry(pk cipher.PubKey, servers ...cipher.PubKey) *disc.Entry {
	return &disc.Entry{Static: pk, Client: &disc.Client{DelegatedServers: servers}}
}

func newResolverTestClient(t *testing.T) *Client {
	t.Helper()
	pk, sk := cipher.GenerateKeyPair()
	c := &Client{entryCache: make(map[cipher.PubKey]entryCacheEntry)}
	c.EntityCommon.init(pk, sk, nil, logging.MustGetLogger("resolver_test"), 0)
	return c
}

// TestEntryResolverAnswersBeforeDiscovery is the seam: an injected resolver
// that can answer must satisfy resolution with no discovery involvement. This
// is what lets a CXO-backed resolver live outside pkg/dmsg — pkg/cxo imports
// pkg/dmsg/dmsg, so the dependency can only run this way.
func TestEntryResolverAnswersBeforeDiscovery(t *testing.T) {
	c := newResolverTestClient(t)
	peer, _ := cipher.GenerateKeyPair()
	srv, _ := cipher.GenerateKeyPair()

	r := &fakeResolver{name: "test", entry: clientEntry(peer, srv)}
	c.AddEntryResolver(r)

	// dc is nil, so any fall-through to the discovery would panic — proving the
	// resolver alone satisfied the lookup.
	got, err := c.getClientEntryCached(context.Background(), peer)
	if err != nil {
		t.Fatalf("getClientEntryCached: %v", err)
	}
	if got == nil || got.Client == nil || len(got.Client.DelegatedServers) != 1 {
		t.Fatalf("resolver entry not returned: %+v", got)
	}
	if got.Client.DelegatedServers[0] != srv {
		t.Errorf("wrong server: got %v want %v", got.Client.DelegatedServers[0], srv)
	}
	if c.LookupResolverHits.Load() != 1 {
		t.Errorf("resolver hit counter = %d, want 1", c.LookupResolverHits.Load())
	}
}

// TestEntryResolverOrderAndFallThrough pins that resolvers are tried in the
// order added and that one which cannot answer is skipped rather than failing
// the resolution.
func TestEntryResolverOrderAndFallThrough(t *testing.T) {
	c := newResolverTestClient(t)
	peer, _ := cipher.GenerateKeyPair()
	srv, _ := cipher.GenerateKeyPair()

	first := &fakeResolver{name: "empty", err: errors.New("nothing here")}
	second := &fakeResolver{name: "has-it", entry: clientEntry(peer, srv)}
	c.AddEntryResolver(first)
	c.AddEntryResolver(second)

	if _, err := c.getClientEntryCached(context.Background(), peer); err != nil {
		t.Fatalf("getClientEntryCached: %v", err)
	}
	if first.calls != 1 || second.calls != 1 {
		t.Errorf("calls: first=%d second=%d, want 1 and 1", first.calls, second.calls)
	}
}

// TestEntryResolverServerOnlyEntryIsSkipped guards the shape check: an entry
// with no Client section names no delegated servers, so it cannot drive a dial
// and must not be treated as a hit.
func TestEntryResolverServerOnlyEntryIsSkipped(t *testing.T) {
	c := newResolverTestClient(t)
	peer, _ := cipher.GenerateKeyPair()

	r := &fakeResolver{name: "server-only", entry: &disc.Entry{Static: peer, Server: &disc.Server{}}}
	c.AddEntryResolver(r)

	if entry, _, ok := c.resolveViaResolvers(context.Background(), peer); ok {
		t.Errorf("a server-only entry was accepted as a client entry: %+v", entry)
	}
}

// TestEntryResolverTimeoutDoesNotStallDial is the safety property. A resolver
// is on the dial path; one that is cold, syncing or wedged must degrade to the
// discovery lookup rather than hold the dial open. Without the bound, adding a
// CXO-backed resolver would make every dial wait on a feed sync.
func TestEntryResolverTimeoutDoesNotStallDial(t *testing.T) {
	c := newResolverTestClient(t)
	peer, _ := cipher.GenerateKeyPair()

	wedged := &fakeResolver{name: "wedged", delay: time.Hour}
	c.AddEntryResolver(wedged)

	start := time.Now()
	_, _, ok := c.resolveViaResolvers(context.Background(), peer)
	elapsed := time.Since(start)

	if ok {
		t.Error("a wedged resolver must not report a hit")
	}
	if elapsed > 2*resolverTimeout {
		t.Errorf("a wedged resolver held the dial for %v; resolverTimeout is %v", elapsed, resolverTimeout)
	}
	if wedged.lastCtx == nil {
		t.Fatal("resolver was not called")
	}
	if _, hasDeadline := wedged.lastCtx.Deadline(); !hasDeadline {
		t.Error("resolver was called without a deadline")
	}
}

// TestNoResolversIsUnchanged pins the default: with none installed, nothing is
// consulted and no counter moves, so existing clients are unaffected.
func TestNoResolversIsUnchanged(t *testing.T) {
	c := newResolverTestClient(t)
	peer, _ := cipher.GenerateKeyPair()

	if _, _, ok := c.resolveViaResolvers(context.Background(), peer); ok {
		t.Error("resolution succeeded with no resolvers installed")
	}
	if c.LookupResolverHits.Load() != 0 {
		t.Error("resolver hit counter moved with no resolvers installed")
	}
}
