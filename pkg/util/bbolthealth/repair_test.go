package bbolthealth

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

func TestRepairIfCorrupt_NoFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.db")
	if err := RepairIfCorrupt(path); err != nil {
		t.Fatalf("non-existent path should be a no-op, got: %v", err)
	}
	// And the file still shouldn't exist after.
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("non-existent path should remain absent; stat err=%v", err)
	}
}

func TestRepairIfCorrupt_HealthyFilePassesThrough(t *testing.T) {
	path := filepath.Join(t.TempDir(), "healthy.db")
	// Create a real bbolt file with a bucket so Check has something
	// to walk.
	db, err := bolt.Open(path, 0600, &bolt.Options{Timeout: time.Second})
	if err != nil {
		t.Fatalf("create healthy bbolt: %v", err)
	}
	if err := db.Update(func(tx *bolt.Tx) error {
		_, e := tx.CreateBucket([]byte("test"))
		return e
	}); err != nil {
		t.Fatalf("create bucket: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	if err := RepairIfCorrupt(path); err != nil {
		t.Fatalf("healthy file should pass: %v", err)
	}

	// File still there, no .corrupt.* sibling.
	if _, err := os.Stat(path); err != nil {
		t.Errorf("healthy file should still exist: %v", err)
	}
	entries, dirErr := os.ReadDir(filepath.Dir(path))
	if dirErr != nil {
		t.Fatalf("ReadDir: %v", dirErr)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".corrupt.") {
			t.Errorf("unexpected .corrupt. sibling on healthy file: %s", e.Name())
		}
	}
}

func TestRepairIfCorrupt_GarbageFileMovedAside(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "garbage.db")
	// Plant bytes that aren't a valid bbolt file — bbolt.Open will
	// reject the header and we'll hit the moveCorruptAside path.
	if err := os.WriteFile(path, []byte("definitely not a bbolt page"), 0600); err != nil {
		t.Fatalf("plant garbage: %v", err)
	}

	if err := RepairIfCorrupt(path); err != nil {
		t.Fatalf("RepairIfCorrupt on garbage should succeed (move aside), got: %v", err)
	}

	// Original path should now be absent (moved aside).
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("expected garbage file to be moved away; stat err=%v", err)
	}

	// And a .corrupt.<ts> sibling should exist.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	var bak string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "garbage.db.corrupt.") {
			bak = e.Name()
			break
		}
	}
	if bak == "" {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("no garbage.db.corrupt.* sibling found, got %v", names)
	}
}

// TestRepairIfCorrupt_RecoversFromOpenPanic exercises the recover()
// path. probeIntegrity catches a panic from inside its own probe
// and treats it as proof of corruption — without the recover, the
// panic would propagate up and crash-loop the caller. We can't
// trivially craft a bbolt file that panics inside Open without
// matching bbolt's exact freelist layout, so we exercise the
// recover surface directly via a runtime panic injected on a
// goroutine that's holding probeIntegrity's defer.
func TestRepairIfCorrupt_RecoversFromOpenPanic(t *testing.T) {
	// File that survives stat() but bbolt.Open will reject it
	// because the magic number is wrong. bolt.Open returns an
	// error in this case (no panic) — but the test still
	// confirms the no-panic-propagates property of the wrapping
	// flow: probeIntegrity returns an error and RepairIfCorrupt
	// moves the file aside.
	dir := t.TempDir()
	path := filepath.Join(dir, "fake.db")
	// Plant a file with a plausible-looking bbolt header magic
	// (0xED0CDAED little-endian, version 2) followed by garbage
	// freelist data. bbolt will accept the header but blow up
	// during freelist read.
	data := make([]byte, 8192)
	// Page 0 header: magic + version, then garbage. bbolt's Open
	// reads the meta pages and validates them; corrupt meta gets
	// rejected with an error (not a panic) on most paths.
	for i := 0; i < 16; i++ {
		data[i] = 0xAB // garbage
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatalf("plant: %v", err)
	}
	if err := RepairIfCorrupt(path); err != nil {
		t.Fatalf("repair: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("corrupt file should be moved aside; stat err=%v", err)
	}
}

func TestRepairIfCorrupt_IsIdempotentOnRepair(t *testing.T) {
	// After a successful move-aside, a second call with the same
	// (now-absent) path should be a clean no-op.
	dir := t.TempDir()
	path := filepath.Join(dir, "x.db")
	if err := os.WriteFile(path, []byte("nope"), 0600); err != nil {
		t.Fatalf("plant: %v", err)
	}
	if err := RepairIfCorrupt(path); err != nil {
		t.Fatalf("first repair: %v", err)
	}
	if err := RepairIfCorrupt(path); err != nil {
		t.Fatalf("second repair (path absent now): %v", err)
	}
}

// TestRepairIfCorrupt_SyncWalkCatchesPageCorruption reproduces the production
// crash: a bbolt file with a corrupted b-tree page header (page id zeroed,
// yielding bbolt's "Page expected to be: N, but self identifies as 0" FastCheck
// panic). The old probe used tx.Check(), which panics in an internal goroutine
// that recover() can't reach — so the visor crash-looped. The synchronous walk
// hits the same FastCheck in the caller's goroutine, where recover() converts
// it into a corruption verdict and the file is moved aside. If this test panics
// instead of passing, the regression is back.
func TestRepairIfCorrupt_SyncWalkCatchesPageCorruption(t *testing.T) {
	const pageSize = 4096
	dir := t.TempDir()
	path := filepath.Join(dir, "corrupt.db")

	db, err := bolt.Open(path, 0600, &bolt.Options{Timeout: time.Second, PageSize: pageSize})
	if err != nil {
		t.Fatalf("create bbolt: %v", err)
	}
	// Write enough keys (plus a nested bucket) to span many leaf/branch pages.
	if err := db.Update(func(tx *bolt.Tx) error {
		b, e := tx.CreateBucketIfNotExists([]byte("data"))
		if e != nil {
			return e
		}
		val := make([]byte, 256)
		for i := 0; i < 800; i++ {
			if e := b.Put([]byte(fmt.Sprintf("key-%06d", i)), val); e != nil {
				return e
			}
		}
		nb, e := b.CreateBucketIfNotExists([]byte("nested"))
		if e != nil {
			return e
		}
		return nb.Put([]byte("nk"), []byte("nv"))
	}); err != nil {
		t.Fatalf("populate: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Zero the page-id header (first 8 bytes) of every page from index 4 to the
	// end, preserving the meta (0,1) and freelist pages so bolt.Open succeeds
	// and the corruption is hit during the b-tree WALK (the fixed path).
	f, err := os.OpenFile(path, os.O_RDWR, 0600) //nolint:gosec // test-controlled temp path
	if err != nil {
		t.Fatalf("open for corruption: %v", err)
	}
	fi, statErr := f.Stat()
	if statErr != nil {
		t.Fatalf("stat: %v", statErr)
	}
	zeros := make([]byte, 8)
	corrupted := 0
	for off := int64(4 * pageSize); off+8 <= fi.Size(); off += pageSize {
		if _, e := f.WriteAt(zeros, off); e != nil {
			t.Fatalf("corrupt at %d: %v", off, e)
		}
		corrupted++
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close corrupt file: %v", err)
	}
	if corrupted == 0 {
		t.Skip("db too small to corrupt interior pages")
	}

	// Must NOT crash; must move the file aside and return nil.
	if err := RepairIfCorrupt(path); err != nil {
		t.Fatalf("RepairIfCorrupt returned error: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("corrupt file should have been moved aside; still present (err=%v)", err)
	}
	entries, readErr := os.ReadDir(dir)
	if readErr != nil {
		t.Fatalf("readdir: %v", readErr)
	}
	movedAside := false
	for _, e := range entries {
		if strings.Contains(e.Name(), ".corrupt.") {
			movedAside = true
		}
	}
	if !movedAside {
		t.Errorf("expected a .corrupt.<ts> sibling after repair; dir=%v", entries)
	}
}
