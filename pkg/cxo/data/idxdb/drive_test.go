package idxdb

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewDriveIdxDB(t *testing.T) {

	t.Run("garbage file is repaired and recreated", func(t *testing.T) {
		// Pre-existing behavior: NewDriveIdxDB returned an error on a
		// file that isn't a valid bbolt store. New behavior (after
		// integrity-check + auto-repair is wired into the open path):
		// the garbage file is moved aside as ".corrupt.<unix>" and a
		// fresh empty IdxDB is created. The trade — losing the bytes
		// in the garbage file — is acceptable for the IdxDB because
		// it's a cache rebuilt from CXO peer sync on next subscription
		// cycle.
		defer os.Remove(testFileName) //nolint:errcheck,gosec
		defer cleanupCorruptSiblings(testFileName)

		fl, err := os.Create(testFileName)
		if err != nil {
			t.Error(err)
			return
		}
		if _, err := fl.Write([]byte("Abra-Cadabra")); err != nil {
			fl.Close() //nolint:errcheck,gosec
			t.Error(err)
			return
		}
		if err := fl.Close(); err != nil {
			t.Error(err)
			return
		}

		idx, err := NewDriveIdxDB(testFileName)
		if err != nil {
			t.Fatalf("expected auto-repair to recover, got error: %v", err)
		}
		idx.Close() //nolint:errcheck,gosec

		// Confirm a .corrupt.<ts> sibling was created from the garbage
		// content — proves repair fired rather than the open
		// coincidentally accepting non-bbolt bytes.
		entries, err := os.ReadDir(filepath.Dir(absDir(testFileName)))
		if err != nil {
			t.Fatalf("ReadDir: %v", err)
		}
		var foundBak bool
		base := filepath.Base(testFileName) + ".corrupt."
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), base) {
				foundBak = true
				break
			}
		}
		if !foundBak {
			t.Errorf("expected %s.corrupt.* sibling after auto-repair", filepath.Base(testFileName))
		}
	})

	// It's impossible to test

	// t.Run("cant create bucket", func(t *testing.T) {
	// })

}

func absDir(name string) string {
	if filepath.IsAbs(name) {
		return name
	}
	cwd, err := os.Getwd()
	if err != nil {
		return name
	}
	return filepath.Join(cwd, name)
}

func cleanupCorruptSiblings(testFile string) {
	base := filepath.Base(testFile) + ".corrupt."
	dir := filepath.Dir(absDir(testFile))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), base) {
			os.Remove(filepath.Join(dir, e.Name())) //nolint:errcheck,gosec
		}
	}
}

func Test_incSlice(t *testing.T) {
	x := []byte{0, 0xff}
	incSlice(x)
	if x[0] != 0x01 || x[1] != 0x00 {
		t.Error("wrong")
	}
}
