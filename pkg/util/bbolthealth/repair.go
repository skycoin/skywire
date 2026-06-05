// Package bbolthealth — pkg/util/bbolthealth/repair.go: integrity-check
// helper for bbolt files. Used by visor subsystems whose bbolt stores
// hold rebuildable data (telemetry, content-addressed CXO leaves, etc.)
// where automatic recover-by-deletion is preferred over a panic at
// commit time.
//
// Motivation: bbolt's commit path runs in an internal batch goroutine
// triggered via time.AfterFunc, so an inconsistency assertion (e.g.
// "circular dependency occurred" from WriteInodeToPage) panics in a
// goroutine the caller can't recover. Trying to open a known-corrupt
// file just defers the crash to first write. The cheap defense is to
// integrity-check the file BEFORE the real open and move it aside if
// it fails.
package bbolthealth

import (
	"fmt"
	"os"
	"time"

	bolt "go.etcd.io/bbolt"
)

// RepairIfCorrupt opens the bbolt file at path (if it exists), runs a
// synchronous integrity walk of every reachable page, and — if any
// problem is found — moves the file aside with a timestamped
// ".corrupt.<unix>" suffix so the subsequent bolt.Open creates a
// fresh, empty file.
//
// Returns nil when:
//   - the file doesn't exist (nothing to check)
//   - the file passes the integrity check
//   - a corrupt file was successfully moved aside
//
// Returns an error only when:
//   - statting the path failed for a reason other than non-existence
//   - a corrupt file CANNOT be moved aside (permissions, ENOSPC)
//
// In the move-failed case, opening the existing file would just
// defer a process-killing panic, so callers should propagate.
//
// The integrity check uses a WRITABLE open (not ReadOnly): bbolt
// only initializes the freelist on a writable open, and the freelist
// is precisely where the kind of inconsistencies that trigger
// commit-time panics live. The handle is closed immediately so the
// caller's real open isn't blocked.
//
// Both bolt.Open and the synchronous integrity walk can panic on
// corruption (freelist inconsistencies like "invalid freelist page:
// N, page type is leaf" during Open; FastCheck page assertions during
// the walk). The probe wraps both in recover() so a panic during the
// integrity check is treated as proof of corruption and the file is
// moved aside, rather than propagating up to crash-loop the caller.
// (bbolt's own tx.Check() is unusable here: it scans in an internal
// goroutine whose panic recover() cannot reach — see probeIntegrity.)
//
// Callers that want operator visibility into the repair should
// snapshot the directory's ".corrupt.<unix>" entries before calling
// and re-scan after to detect a new entry — RepairIfCorrupt itself
// does no logging (no logger dependency).
func RepairIfCorrupt(path string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return fmt.Errorf("bbolthealth: stat %s: %w", path, err)
	}
	probeErr := probeIntegrity(path)
	if probeErr != nil {
		return moveCorruptAside(path, probeErr)
	}
	return nil
}

// probeIntegrity opens the file, walks every reachable page
// synchronously, and closes the handle. Returns nil on a clean file.
// Returns an error describing the failure on any of: open error, walk
// error, or panic from inside Open / the walk (which bbolt throws on
// freelist inconsistencies and on FastCheck page-header assertions).
// The recover() block is the load-bearing piece — and the walk MUST be
// synchronous (not tx.Check(), which panics in its own goroutine) so
// that recover() actually catches the corruption panic instead of the
// visor crash-looping.
func probeIntegrity(path string) (probeErr error) {
	defer func() {
		if r := recover(); r != nil {
			probeErr = fmt.Errorf("panic during integrity check: %v", r)
		}
	}()

	db, openErr := bolt.Open(path, 0600, &bolt.Options{
		Timeout: 5 * time.Second,
	})
	if openErr != nil {
		// File exists but bbolt won't open it — header corruption,
		// truncation, or unrelated I/O error.
		return fmt.Errorf("open: %w", openErr)
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil && probeErr == nil {
			// Close after a clean Check failing is unusual but not
			// itself proof of corruption — keep probeErr nil and let
			// the caller's real open surface any persistent close-
			// time issue.
			_ = closeErr //nolint:errcheck
		}
	}()

	// Integrity probe: a SYNCHRONOUS full walk of every reachable b-tree page.
	//
	// We deliberately do NOT use bbolt's tx.Check(): it runs its consistency
	// scan in an internal goroutine (`go tx.check(ch)`), so a page-assertion
	// panic — e.g. bbolt v1.4.x FastCheck "Page expected to be: N, but self
	// identifies as 0" on an SD-card-corrupted file — fires in THAT goroutine,
	// which no caller-side recover() can reach. That defeated probeIntegrity's
	// recover() entirely and crash-looped the visor (the very thing
	// bbolthealth exists to prevent).
	//
	// Instead we cursor-walk all buckets and keys ourselves. Every page access
	// goes through tx.page(), which runs the same FastCheck — but now
	// synchronously, in THIS goroutine, where the deferred recover() above
	// converts the panic into a corruption verdict and moves the file aside.
	// Combined with the writable Open above (which surfaces freelist
	// corruption synchronously), this covers the corruption modes that
	// actually crash a visor at access time.
	if viewErr := db.View(walkAllPages); viewErr != nil {
		return fmt.Errorf("integrity walk: %w", viewErr)
	}
	return nil
}

// walkAllPages forces synchronous access to every reachable b-tree page by
// cursoring over all buckets (recursing into nested buckets) and all keys. A
// corrupt page panics inside tx.page()'s FastCheck during this walk — in the
// caller's goroutine, so probeIntegrity's recover() catches it.
func walkAllPages(tx *bolt.Tx) error {
	return tx.ForEach(func(_ []byte, b *bolt.Bucket) error {
		return walkBucket(b)
	})
}

// walkBucket cursors over every key in b, recursing into nested buckets so the
// whole sub-tree's pages are touched.
func walkBucket(b *bolt.Bucket) error {
	return b.ForEach(func(k, v []byte) error {
		if v != nil {
			return nil // plain key — its leaf page was already read
		}
		// A nil value marks a nested bucket; descend to touch its pages.
		if sub := b.Bucket(k); sub != nil {
			return walkBucket(sub)
		}
		return nil
	})
}

// moveCorruptAside renames the bbolt file at path to
// "<path>.corrupt.<unix-ts>". Returns an error only when the rename
// itself fails. The reason argument is preserved in the error message
// so the operator can see why the file was set aside.
func moveCorruptAside(path string, reason error) error {
	bak := fmt.Sprintf("%s.corrupt.%d", path, time.Now().Unix())
	if err := os.Rename(path, bak); err != nil {
		return fmt.Errorf("bbolthealth: %s corrupt (%v); also failed to move aside to %s: %w",
			path, reason, bak, err)
	}
	return nil
}
