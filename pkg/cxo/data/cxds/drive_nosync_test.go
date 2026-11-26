//go:build !js

package cxds

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/skycoin/skycoin/src/cipher"
)

// A NoSync store runs a serial refcount change at once. Through Batch with
// bbolt's default delay each one waited 10 ms for company.
func TestDriveCXDS_NoSyncSerialIncDoesNotWaitForABatch(t *testing.T) {
	ds, err := NewDriveCXDSWithOptions(filepath.Join(t.TempDir(), "db.cxds"), DriveOptions{NoSync: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ds.Close() }) //nolint:errcheck

	val := []byte("value")
	key := cipher.SumSHA256(val)
	if _, err := ds.Set(key, val, 1); err != nil {
		t.Fatal(err)
	}
	const n = 300
	start := time.Now()
	for range n {
		if _, err := ds.Inc(key, 1); err != nil {
			t.Fatal(err)
		}
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("%d serial Inc took %s", n, d)
	}
	if _, rc, err := ds.Get(key, 0); err != nil || rc != n+1 {
		t.Fatalf("rc = %d, %v; want %d", rc, err, n+1)
	}
}
