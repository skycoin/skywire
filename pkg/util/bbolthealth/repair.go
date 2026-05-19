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

// RepairIfCorrupt opens the bbolt file at path (if it exists), runs
// bbolt's built-in tx.Check() consistency walk, and — if any problem
// is found — moves the file aside with a timestamped
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

	db, openErr := bolt.Open(path, 0600, &bolt.Options{
		Timeout: 5 * time.Second,
	})
	if openErr != nil {
		// File exists but bbolt won't open it — header corruption,
		// truncation, or unrelated I/O error. Move aside and let the
		// caller retry; if the move itself fails, surface so the
		// caller can fail closed.
		return moveCorruptAside(path, fmt.Errorf("open: %w", openErr))
	}

	// Collect up to a few problems for diagnostic context. Don't
	// keep all of them — bbolt's Check can emit hundreds on a badly
	// damaged file, and the reason field only carries the first one
	// to the rename-aside message anyway.
	var problems []error
	const maxProblemsKept = 5
	viewErr := db.View(func(tx *bolt.Tx) error {
		for e := range tx.Check() {
			if len(problems) < maxProblemsKept {
				problems = append(problems, e)
			}
		}
		return nil
	})
	closeErr := db.Close()
	if viewErr != nil {
		return moveCorruptAside(path, fmt.Errorf("check view: %w", viewErr))
	}
	if len(problems) > 0 {
		return moveCorruptAside(path, fmt.Errorf("bbolt check found %d problems (first: %v)", len(problems), problems[0]))
	}
	if closeErr != nil {
		// Close after a clean Check failing is unusual but not
		// itself proof of corruption — surface as a soft warning by
		// returning nil (we already verified contents). The caller's
		// real open will surface any persistent close-time issue.
		return nil
	}
	return nil
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
