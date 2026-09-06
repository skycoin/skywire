// Package skyobject pkg/cxo/skyobject/preview_local_hit_test.go:
// pins (*Preview).Get preferring a locally-held object over the remote
// getter. The pre-fix guard was
//
//	if val, err = p.Pack.Get(key); err != nil && err != data.ErrNotFound {
//	    return // db failure
//	}
//
// which does not fire when err == nil, so a local HIT fell through to
// p.g.Get, discarded the local value and paid a network round-trip for
// an object already held. Objects are content-addressed, so the local
// copy is always the same bytes the peer would return.

package skyobject

import (
	"errors"
	"testing"

	"github.com/skycoin/skycoin/src/cipher"

	"github.com/skycoin/skywire/pkg/cxo/skyobject/registry"
)

// countingGetter records how many times the remote getter was asked,
// and for which keys.
type countingGetter struct {
	calls int
	keys  []cipher.SHA256
	vals  map[cipher.SHA256][]byte
}

func (g *countingGetter) Get(key cipher.SHA256) (val []byte, err error) {
	g.calls++
	g.keys = append(g.keys, key)
	if val, ok := g.vals[key]; ok {
		return val, nil
	}
	return nil, errors.New("countingGetter: no such object")
}

// newTestPreview builds a Preview over a fresh in-memory container whose
// registry is already cached, so (*Container).Preview never has to reach
// for the getter to resolve the Registry.
func newTestPreview(t *testing.T, g Getter) (c *Container, p *Preview) {
	t.Helper()

	c = getTestContainer()

	pk, sk := cipher.GenerateKeyPair() //nolint:errcheck,gosec
	if err := c.AddFeed(pk); err != nil {
		t.Fatalf("AddFeed: %v", err)
	}
	if _, err := c.Unpack(sk, testRegistry); err != nil {
		t.Fatalf("Unpack: %v", err)
	}
	// Store the Registry in the container so (*Container).Preview resolves
	// it locally (registry caching is config-gated and off by default).
	regRef := testRegistry.Reference()
	if _, err := c.Set(cipher.SHA256(regRef), testRegistry.Encode(), 1); err != nil {
		t.Fatalf("Set registry: %v", err)
	}

	r := &registry.Root{Pub: pk, Nonce: 1, Reg: regRef}

	var err error
	if p, err = c.Preview(r, g); err != nil {
		t.Fatalf("Preview: %v", err)
	}
	return c, p
}

// TestPreviewGet_LocalHitDoesNotCallRemote is the regression test: an
// object present in the container must be served from the container,
// with the remote getter left untouched.
func TestPreviewGet_LocalHitDoesNotCallRemote(t *testing.T) {
	local := []byte("locally held object")
	key := cipher.SumSHA256(local)

	g := &countingGetter{vals: map[cipher.SHA256][]byte{
		// Same key, different bytes: if the fall-through fires we not
		// only see calls > 0, we get the wrong value back too.
		key: []byte("remote copy that must never be fetched"),
	}}

	c, p := newTestPreview(t, g)
	defer c.Close() //nolint:errcheck,gosec

	if _, err := c.Set(key, local, 1); err != nil {
		t.Fatalf("Set: %v", err)
	}

	val, err := p.Get(key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(val) != string(local) {
		t.Fatalf("Get returned %q, want the local copy %q", val, local)
	}
	if g.calls != 0 {
		t.Fatalf("remote getter called %d time(s) for a locally-held object (keys %v); "+
			"a local hit must not cause a network round-trip", g.calls, g.keys)
	}

	// Repeat reads stay local too.
	if _, err = p.Get(key); err != nil {
		t.Fatalf("second Get: %v", err)
	}
	if g.calls != 0 {
		t.Fatalf("remote getter called %d time(s) across two local hits", g.calls)
	}
}

// TestPreviewGet_MissFallsThroughToRemoteAndCaches keeps the other half
// of the contract honest: what the container does not have is still
// requested from the peer exactly once, then served from the map.
func TestPreviewGet_MissFallsThroughToRemoteAndCaches(t *testing.T) {
	remote := []byte("object only the peer has")
	key := cipher.SumSHA256(remote)

	g := &countingGetter{vals: map[cipher.SHA256][]byte{key: remote}}

	c, p := newTestPreview(t, g)
	defer c.Close() //nolint:errcheck,gosec

	val, err := p.Get(key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(val) != string(remote) {
		t.Fatalf("Get returned %q, want %q", val, remote)
	}
	if g.calls != 1 {
		t.Fatalf("remote getter called %d time(s), want 1", g.calls)
	}

	if val, err = p.Get(key); err != nil { // served from p.m now
		t.Fatalf("second Get: %v", err)
	}
	if string(val) != string(remote) {
		t.Fatalf("second Get returned %q, want %q", val, remote)
	}
	if g.calls != 1 {
		t.Fatalf("remote getter called %d time(s) after caching, want 1", g.calls)
	}
}

// TestPreviewGet_RemoteMissReturnsError: absent both locally and
// remotely, the getter's error reaches the caller.
func TestPreviewGet_RemoteMissReturnsError(t *testing.T) {
	g := &countingGetter{}

	c, p := newTestPreview(t, g)
	defer c.Close() //nolint:errcheck,gosec

	if _, err := p.Get(cipher.SumSHA256([]byte("nowhere"))); err == nil {
		t.Fatal("Get: expected an error for an object held neither locally nor remotely")
	}
	if g.calls != 1 {
		t.Fatalf("remote getter called %d time(s), want 1", g.calls)
	}
}
