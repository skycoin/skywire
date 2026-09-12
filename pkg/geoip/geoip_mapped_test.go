//go:build !mobile && !js

// Package geoip pkg/geoip/geoip_mapped_test.go c0-com-util
package geoip

import (
	"math"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

// The mapped reader must answer exactly like the in-memory one, and reuse the
// cache file it wrote instead of inflating again.
func TestOpenMapped_WritesCacheOnceAndMatchesEmbedded(t *testing.T) {
	if !Embedded() {
		t.Skip("no embedded database in this build")
	}
	dir := filepath.Join(t.TempDir(), "nested", "skywire")
	r, err := openMapped(dir)
	require.NoError(t, err)
	defer r.Close() //nolint:errcheck

	crc, size, ok := gzTrailer(embeddedGz)
	require.True(t, ok)
	fi, err := os.Stat(mappedPath(dir, crc))
	require.NoError(t, err)
	require.Equal(t, size, fi.Size(), "cache file is the inflated database")
	first := fi.ModTime()

	mem, err := OpenEmbedded()
	require.NoError(t, err)
	defer mem.Close() //nolint:errcheck
	ip := netip.MustParseAddr("8.8.8.8")
	got, err := r.City(ip)
	require.NoError(t, err)
	want, err := mem.City(ip)
	require.NoError(t, err)
	require.Equal(t, want.Country.ISOCode, got.Country.ISOCode)

	// A second open finds the file and does not rewrite it.
	r2, err := openMapped(dir)
	require.NoError(t, err)
	defer r2.Close() //nolint:errcheck
	fi2, err := os.Stat(mappedPath(dir, crc))
	require.NoError(t, err)
	require.Equal(t, first, fi2.ModTime())

	// A truncated cache file is rewritten.
	require.NoError(t, os.WriteFile(mappedPath(dir, crc), []byte("stale"), 0o600))
	r3, err := openMapped(dir)
	require.NoError(t, err)
	defer r3.Close() //nolint:errcheck
	fi3, err := os.Stat(mappedPath(dir, crc))
	require.NoError(t, err)
	require.Equal(t, size, fi3.Size())
}

func TestOpenMapped_UnwritableDirFails(t *testing.T) {
	if !Embedded() {
		t.Skip("no embedded database in this build")
	}
	if os.Geteuid() == 0 {
		t.Skip("root can write anywhere")
	}
	ro := t.TempDir()
	// 0o500 is the subject of the test: a directory the process may traverse but
	// not write. G302's 0600 ceiling is about files, not this.
	require.NoError(t, os.Chmod(ro, 0o500)) //nolint:gosec // G302: intentionally non-writable dir
	_, err := openMapped(filepath.Join(ro, "skywire"))
	require.Error(t, err, "Shared() falls back to the in-memory reader on this error")
}

// Not an assertion, evidence: how much heap each open path pins.
func TestOpenMapped_HeapFootprint(t *testing.T) {
	if !Embedded() {
		t.Skip("no embedded database in this build")
	}
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	r, err := openMapped(t.TempDir())
	require.NoError(t, err)
	defer r.Close() //nolint:errcheck
	runtime.GC()
	runtime.ReadMemStats(&after)
	// Heap sizes here are far below the int64 ceiling; the helper keeps the
	// signed subtraction (the delta can legitimately be negative) without an
	// unchecked uint64→int64 conversion at the call site.
	t.Logf("mapped: heap +%d KiB", heapDeltaKiB(before.HeapInuse, after.HeapInuse))

	t.Logf("in-memory: heap +%d KiB pinned by EmbeddedDB()", len(EmbeddedDB())/1024)
}

// heapDeltaKiB reports after-before in KiB. Both are runtime.MemStats byte
// counts, so the subtraction is done in the unsigned domain and only the
// (small) result is signed — a direct int64(x) on each operand is an
// unchecked narrowing that gosec flags, and rightly so as a habit.
func heapDeltaKiB(before, after uint64) int64 {
	d, neg := after-before, false
	if after < before {
		d, neg = before-after, true
	}
	d /= 1024
	if d > math.MaxInt64 {
		d = math.MaxInt64
	}
	if neg {
		return -int64(d)
	}
	return int64(d)
}
