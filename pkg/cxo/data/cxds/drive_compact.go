//go:build !js

// Package cxds pkg/cxo/data/cxds/drive_compact.go c2-net-cxo
// Startup garbage-collection + compaction for the on-disk CXDS. bbolt never
// shrinks its file on delete, and the store retains dead (rc==0) objects
// indefinitely — orphaned leaves from superseded feed roots (classically the
// stats/telemetry feed on a public visor) that are never referenced again. Left
// alone, cxds.db grows without bound (observed at 17 GB on a plain public
// visor). This rewrites the store keeping ONLY live (rc>0) objects, so the file
// self-corrects on the next process start. Safe: CXDS is content-addressed, so
// any dropped leaf is re-fetched from peers on the next subscription cycle.
package cxds

import (
	"encoding/binary"
	"fmt"
	"os"
	"time"

	bolt "go.etcd.io/bbolt"
)

// compactMinFileBytes: only stores at least this large on disk are considered —
// small stores are not worth scanning on every start. A var (not const) so tests
// can lower it.
var compactMinFileBytes = int64(1) << 30 // 1 GiB

const (
	// compactMinDeadFrac: compact only when at least this fraction of the stored
	// object volume is dead (rc==0); otherwise the store is mostly live and a
	// rewrite would reclaim little.
	compactMinDeadFrac = 0.40
	// compactRescanGrowth: after a scan that decided NOT to compact, skip
	// re-scanning until the file has grown by this factor, so a legitimately
	// large mostly-live store is not rescanned on every start.
	compactRescanGrowth = 1.25
)

// gcCheckedSizeKey records (in the meta bucket) the file size at the last scan
// that decided the store was mostly live, so we don't rescan a large healthy
// store on every start.
var gcCheckedSizeKey = []byte("gc_checked_size")

func putUint64Meta(b *bolt.Bucket, key []byte, v uint64) error {
	var ub [8]byte
	binary.BigEndian.PutUint64(ub[:], v)
	return b.Put(key, ub[:])
}

func getUint64Meta(b *bolt.Bucket, key []byte) uint64 {
	v := b.Get(key)
	if len(v) != 8 {
		return 0
	}
	return binary.BigEndian.Uint64(v)
}

func putUint32Meta(b *bolt.Bucket, key []byte, v uint32) error {
	var ub [4]byte
	binary.BigEndian.PutUint32(ub[:], v)
	return b.Put(key, ub[:])
}

// startupGCCompact GCs dead (rc==0) objects and shrinks the file when the store
// has accumulated significant dead volume. Best-effort: on any error the
// original file is left untouched (the rewrite goes to a temp file swapped in
// atomically only on success). Returns bytes reclaimed (0 when it did nothing).
// A separate function (not a method) so it runs before the main handle opens.
func startupGCCompact(fileName string) (reclaimed int64, err error) {
	fi, sErr := os.Stat(fileName)
	if sErr != nil || fi.Size() < compactMinFileBytes {
		return 0, nil
	}
	origSize := fi.Size()

	src, oErr := bolt.Open(fileName, 0644, &bolt.Options{Timeout: 500 * time.Millisecond})
	if oErr != nil {
		return 0, oErr
	}
	srcOpen := true
	closeSrc := func() {
		if srcOpen {
			_ = src.Close()
			srcOpen = false
		}
	}
	defer closeSrc()

	// Skip re-scanning a store recently checked that has not grown enough.
	var checked uint64
	_ = src.View(func(tx *bolt.Tx) error {
		if m := tx.Bucket(metaBucket); m != nil {
			checked = getUint64Meta(m, gcCheckedSizeKey)
		}
		return nil
	})
	if checked > 0 && uint64(origSize) <= uint64(float64(checked)*compactRescanGrowth) {
		return 0, nil
	}

	// Scan: live vs dead volume (the on-disk volume_* stats are unreliable, so we
	// measure directly).
	var liveVol, deadVol, liveCount uint64
	if sErr = src.View(func(tx *bolt.Tx) error {
		o := tx.Bucket(objsBucket)
		if o == nil {
			return nil
		}
		return o.ForEach(func(_, v []byte) error {
			if len(v) < 4 {
				return nil
			}
			dv := uint64(len(v) - 4)
			if getRefsCount(v) > 0 {
				liveVol += dv
				liveCount++
			} else {
				deadVol += dv
			}
			return nil
		})
	}); sErr != nil {
		return 0, sErr
	}

	totalVol := liveVol + deadVol
	if totalVol == 0 || float64(deadVol) < compactMinDeadFrac*float64(totalVol) {
		// Mostly live — remember this size so we don't rescan until it grows.
		_ = src.Update(func(tx *bolt.Tx) error {
			if m := tx.Bucket(metaBucket); m != nil {
				return putUint64Meta(m, gcCheckedSizeKey, uint64(origSize))
			}
			return nil
		})
		return 0, nil
	}

	// Compact: copy version + live objects into a temp file, then swap.
	tmp := fileName + ".compact"
	_ = os.Remove(tmp)
	dst, dErr := bolt.Open(tmp, 0644, &bolt.Options{Timeout: 500 * time.Millisecond})
	if dErr != nil {
		return 0, dErr
	}
	cErr := dst.Update(func(dtx *bolt.Tx) error {
		mo, e := dtx.CreateBucketIfNotExists(metaBucket)
		if e != nil {
			return e
		}
		if e = mo.Put(versionKey, versionBytes()); e != nil {
			return e
		}
		// Correct stats for the compacted (live-only) store. Volume is stored as
		// uint32 by the existing schema; the live set fits after dropping the dead
		// bulk (a large store's bloat is the dead objects).
		if e = putUint32Meta(mo, amountAllKey, uint32(liveCount)); e != nil { //nolint:gosec
			return e
		}
		if e = putUint32Meta(mo, amountUsedKey, uint32(liveCount)); e != nil { //nolint:gosec
			return e
		}
		if e = putUint32Meta(mo, volumeAllKey, uint32(liveVol)); e != nil { //nolint:gosec
			return e
		}
		if e = putUint32Meta(mo, volumeUsedKey, uint32(liveVol)); e != nil { //nolint:gosec
			return e
		}
		ob, e := dtx.CreateBucketIfNotExists(objsBucket)
		if e != nil {
			return e
		}
		return src.View(func(stx *bolt.Tx) error {
			so := stx.Bucket(objsBucket)
			if so == nil {
				return nil
			}
			return so.ForEach(func(k, v []byte) error {
				if len(v) >= 4 && getRefsCount(v) > 0 {
					return ob.Put(k, v)
				}
				return nil
			})
		})
	})
	_ = dst.Close()
	if cErr != nil {
		_ = os.Remove(tmp)
		return 0, cErr
	}

	closeSrc() // release the lock before swapping the file
	if rErr := os.Rename(tmp, fileName); rErr != nil {
		_ = os.Remove(tmp)
		return 0, rErr
	}
	if nfi, e := os.Stat(fileName); e == nil {
		reclaimed = origSize - nfi.Size()
	}
	return reclaimed, nil
}

// maybeStartupGCCompact runs startupGCCompact and reports the outcome to stderr
// (the visor's log stream) when it reclaims space. Best-effort: a compaction
// failure is logged and swallowed so it can never block opening the store.
func maybeStartupGCCompact(fileName string) {
	reclaimed, err := startupGCCompact(fileName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cxds: startup GC/compaction skipped for %s: %v\n", fileName, err)
		return
	}
	if reclaimed > 0 {
		fmt.Fprintf(os.Stderr, "cxds: startup GC/compaction reclaimed %d bytes (%.1f MiB) from %s\n",
			reclaimed, float64(reclaimed)/(1<<20), fileName)
	}
}
