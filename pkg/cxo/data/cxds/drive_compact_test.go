//go:build !js

// Package cxds pkg/cxo/data/cxds/drive_compact_test.go c2-net-cxo
package cxds

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/skycoin/skycoin/src/cipher"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cxo/data"
)

func mkVal(b byte, n int) []byte {
	d := make([]byte, n)
	for i := range d {
		d[i] = b
	}
	return d
}

// TestStartupGCCompact_DropsDeadKeepsLive verifies the startup GC reclaims dead
// (rc==0) objects and preserves live (rc>0) ones — the fix for cxds.db growing
// unbounded with orphaned leaves from superseded feed roots.
func TestStartupGCCompact_DropsDeadKeepsLive(t *testing.T) {
	fn := filepath.Join(t.TempDir(), "cxds.db")

	ds, err := NewDriveCXDS(fn)
	require.NoError(t, err)

	var live, dead [][]byte
	for i := 0; i < 3; i++ { // live: rc=1
		v := mkVal(byte('L'+i), 8192)
		_, err := ds.Set(cipher.SumSHA256(v), v, 1)
		require.NoError(t, err)
		live = append(live, v)
	}
	for i := 0; i < 3; i++ { // dead: Set rc=1 then Inc -1 -> rc=0 (retained by CXDS)
		v := mkVal(byte('D'+i), 8192)
		k := cipher.SumSHA256(v)
		_, err := ds.Set(k, v, 1)
		require.NoError(t, err)
		rc, err := ds.Inc(k, -1)
		require.NoError(t, err)
		require.Equal(t, uint32(0), rc)
		dead = append(dead, v)
	}
	// Pre-GC: dead objects are still present (CXDS keeps rc==0).
	for _, v := range dead {
		_, rc, err := ds.Get(cipher.SumSHA256(v), 0)
		require.NoError(t, err)
		require.Equal(t, uint32(0), rc)
	}
	require.NoError(t, ds.Close())

	// Lower the size gate so the small test store qualifies for compaction.
	old := compactMinFileBytes
	compactMinFileBytes = 4096
	defer func() { compactMinFileBytes = old }()

	// Reopen triggers startupGCCompact.
	ds2, err := NewDriveCXDS(fn)
	require.NoError(t, err)
	defer ds2.Close() //nolint:errcheck

	for _, v := range live { // live survive
		got, rc, err := ds2.Get(cipher.SumSHA256(v), 0)
		require.NoError(t, err, "live object must survive GC")
		require.Equal(t, v, got)
		require.Equal(t, uint32(1), rc)
	}
	for _, v := range dead { // dead reclaimed
		_, _, err := ds2.Get(cipher.SumSHA256(v), 0)
		require.ErrorIs(t, err, data.ErrNotFound, "dead (rc==0) object must be reclaimed")
	}
}

// TestStartupGCCompact_SkipsMostlyLive verifies a store that is mostly live is
// NOT rewritten (no live object dropped), so healthy stores are untouched.
func TestStartupGCCompact_SkipsMostlyLive(t *testing.T) {
	fn := filepath.Join(t.TempDir(), "cxds.db")
	ds, err := NewDriveCXDS(fn)
	require.NoError(t, err)
	var live [][]byte
	for i := 0; i < 6; i++ {
		v := mkVal(byte('a'+i), 8192)
		_, err := ds.Set(cipher.SumSHA256(v), v, 1)
		require.NoError(t, err)
		live = append(live, v)
	}
	require.NoError(t, ds.Close())

	old := compactMinFileBytes
	compactMinFileBytes = 4096
	defer func() { compactMinFileBytes = old }()

	ds2, err := NewDriveCXDS(fn)
	require.NoError(t, err)
	defer ds2.Close() //nolint:errcheck
	for _, v := range live {
		_, rc, err := ds2.Get(cipher.SumSHA256(v), 0)
		require.NoError(t, err, "all-live store must be untouched")
		require.Equal(t, uint32(1), rc)
	}
}

// A store whose dead objects were already deleted at runtime is "mostly live"
// to the object scan, yet the file keeps every freed page: the free-page
// criterion must still rewrite it and give the space back.
func TestStartupGCCompact_ReclaimsFreedPages(t *testing.T) {
	fn := filepath.Join(t.TempDir(), "cxds.db")
	ds, err := NewDriveCXDS(fn)
	require.NoError(t, err)
	// Phase 1: grow the file with 600 live objects.
	var keep, drop [][]byte
	for i := 0; i < 600; i++ {
		v := mkVal(byte(i%251), 8192)
		v[0], v[1] = byte(i>>8), byte(i) // distinct objects
		_, err := ds.Set(cipher.SumSHA256(v), v, 1)
		require.NoError(t, err)
		if i < 6 {
			keep = append(keep, v)
		} else {
			drop = append(drop, v)
		}
	}
	require.NoError(t, ds.Close())
	// Phase 2: delete nearly all of them in a fresh session — the pages they
	// used go to the freelist, the file keeps its high-water size.
	ds, err = NewDriveCXDS(fn)
	require.NoError(t, err)
	for _, v := range drop {
		require.NoError(t, ds.Del(cipher.SumSHA256(v)))
	}
	require.NoError(t, ds.Close())
	before, err := os.Stat(fn)
	require.NoError(t, err)

	old := compactMinFileBytes
	compactMinFileBytes = 4096
	defer func() { compactMinFileBytes = old }()

	reclaimed, err := startupGCCompact(fn)
	require.NoError(t, err)
	require.Greater(t, reclaimed, int64(0), "freed pages must be reclaimed by a rewrite")
	after, err := os.Stat(fn)
	require.NoError(t, err)
	require.Less(t, after.Size(), before.Size()/2, "file must shrink: %d -> %d", before.Size(), after.Size())

	ds2, err := NewDriveCXDS(fn)
	require.NoError(t, err)
	defer ds2.Close() //nolint:errcheck
	for _, v := range keep {
		got, rc, err := ds2.Get(cipher.SumSHA256(v), 0)
		require.NoError(t, err)
		require.Equal(t, v, got)
		require.Equal(t, uint32(1), rc)
	}
}
