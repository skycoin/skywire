// Package skyobject pkg/cxo/skyobject/lastroot_missing_cxds_test.go:
// pins the behavior of Index.LastRoot and Container.rootByHash when
// CXDS no longer holds the bytes for a hash the index still
// references — the exact prod failure mode after #2676 + #2677 made
// the cleanup paths actually delete entries.
//
// Pre-fix Index.LastRoot crashed the entire visor on Publisher
// startup:
//
//	panic: runtime error: invalid memory address or nil pointer dereference
//	  pkg/cxo/skyobject/index.go:563
//	  pkg/cxo/treestore/(*Publisher).hydrateFromContainer  publisher.go:321
//	  pkg/cxo/treestore.New                                publisher.go:293
//	  pkg/cxo/treestore.NewWithDMSG                        publisher.go:219
//	  pkg/visor/visorinit.(*Module).InitConcurrent
//
// because the panic site `r.IsFull = true` dereferenced a nil r
// returned from rootByHash. Caller (Publisher.New) already had
// graceful error fallback to an empty in-memory tree — the panic
// was the only thing that turned the inconsistency into a crashloop.

package skyobject

import (
	"strings"
	"testing"

	"github.com/skycoin/skycoin/src/cipher"

	"github.com/skycoin/skywire/pkg/cxo/skyobject/registry"
)

func TestIndex_LastRoot_MissingCXDSBytes_ReturnsError(t *testing.T) {
	// Reproduce the prod crash shape: idx has a row pointing at a
	// root hash whose bytes are no longer in CXDS. Verify LastRoot
	// returns an error rather than panicking on nil dereference.
	c := getTestContainer()
	defer c.Close() //nolint:errcheck,gosec

	pk, sk := cipher.GenerateKeyPair() //nolint:errcheck,gosec
	if err := c.AddFeed(pk); err != nil {
		t.Fatalf("AddFeed: %v", err)
	}
	up, err := c.Unpack(sk, testRegistry)
	if err != nil {
		t.Fatalf("Unpack: %v", err)
	}
	r := &registry.Root{Pub: pk, Nonce: 7}
	if err := c.Save(up, r); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Sanity-check: LastRoot succeeds while CXDS still holds the bytes.
	if got, err := c.LastRoot(pk, r.Nonce); err != nil {
		t.Fatalf("pre-Del LastRoot: unexpected error %v", err)
	} else if got == nil {
		t.Fatalf("pre-Del LastRoot: returned nil with no error")
	} else if got.Hash != r.Hash {
		t.Fatalf("pre-Del LastRoot: got hash %s, want %s", got.Hash.Hex(), r.Hash.Hex())
	}

	// Simulate cleanup outrunning the idx update: yank the root's
	// bytes out of both the in-process Cache and the underlying CXDS
	// while leaving the idx row untouched. Touching only one layer
	// is invisible because Cache.Get short-circuits on its own map
	// before consulting the DB — but the production failure mode
	// reaches the DB layer (cleanup paths evict from both via Set/Inc
	// down to rc=0 + sweep), so the test mimics the same end-state.
	c.Cache.mx.Lock()
	delete(c.is, r.Hash)
	c.Cache.mx.Unlock()
	if err := c.DB().CXDS().Del(r.Hash); err != nil {
		t.Fatalf("CXDS.Del: %v", err)
	}

	// Post-fix: LastRoot returns an error (the data.ErrNotFound the
	// underlying Get surfaces). Pre-fix this panicked with nil deref.
	got, err := c.LastRoot(pk, r.Nonce)
	if err == nil {
		t.Errorf("LastRoot after CXDS.Del: want non-nil error, got nil")
	}
	if got != nil {
		t.Errorf("LastRoot after CXDS.Del: want nil root, got %+v", got)
	}
}

func TestContainer_RootByHash_MissingCXDSBytes_ReturnsError(t *testing.T) {
	// Companion test for the exported RootByHash → unexported
	// rootByHash path. Pre-fix the missing-bytes case would have
	// nil-derefed on `r.Hash = hash` after a failed DecodeRoot or
	// on Get-error after the assignment-before-return reorder. Post-
	// fix returns the error cleanly.
	c := getTestContainer()
	defer c.Close() //nolint:errcheck,gosec

	pk, sk := cipher.GenerateKeyPair() //nolint:errcheck,gosec
	if err := c.AddFeed(pk); err != nil {
		t.Fatalf("AddFeed: %v", err)
	}
	up, err := c.Unpack(sk, testRegistry)
	if err != nil {
		t.Fatalf("Unpack: %v", err)
	}
	r := &registry.Root{Pub: pk, Nonce: 11}
	if err := c.Save(up, r); err != nil {
		t.Fatalf("Save: %v", err)
	}

	c.Cache.mx.Lock()
	delete(c.is, r.Hash)
	c.Cache.mx.Unlock()
	if err := c.DB().CXDS().Del(r.Hash); err != nil {
		t.Fatalf("CXDS.Del: %v", err)
	}

	got, err := c.RootByHash(r.Hash)
	if err == nil {
		t.Errorf("RootByHash after CXDS.Del: want non-nil error, got nil")
	}
	if got != nil {
		t.Errorf("RootByHash after CXDS.Del: want nil root, got %+v", got)
	}

	// Sanity: error message is informative — the operator-facing
	// "what went wrong" should mention the missing hash so a future
	// log line is greppable. Loosely matched so the wording can
	// evolve without breaking the test.
	if err != nil && !strings.Contains(strings.ToLower(err.Error()), "not found") {
		// data.ErrNotFound's text is "not found" — relying on the
		// canonical error from the layer below.
		t.Logf("note: RootByHash err = %q (loose check for substring 'not found')", err.Error())
	}
}
