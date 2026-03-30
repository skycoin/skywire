package idxdb

import (
	"os"
	"testing"

	"github.com/skycoin/skywire/pkg/cxo/data/tests"
)

func TestRoots_Ascend(t *testing.T) {
	// Ascend(IterateRootsFunc) error

	// Memory-backed variant not implemented.

	t.Run("drive", func(t *testing.T) {
		idx := testNewDriveIdxDB(t)
		defer os.Remove(testFileName) //nolint:errcheck,gosec
		defer idx.Close()             //nolint:errcheck,gosec

		tests.RootsAscend(t, idx)
	})

}

func TestRoots_Descend(t *testing.T) {
	// Descend(IterateRootsFunc) error

	// Memory-backed variant not implemented.

	t.Run("drive", func(t *testing.T) {
		idx := testNewDriveIdxDB(t)
		defer os.Remove(testFileName) //nolint:errcheck,gosec
		defer idx.Close()             //nolint:errcheck,gosec

		tests.RootsDescend(t, idx)
	})

}

func TestRoots_Set(t *testing.T) {
	// Set(*Root) error

	// Memory-backed variant not implemented.

	t.Run("drive", func(t *testing.T) {
		idx := testNewDriveIdxDB(t)
		defer os.Remove(testFileName) //nolint:errcheck,gosec
		defer idx.Close()             //nolint:errcheck,gosec

		tests.RootsSet(t, idx)
	})

}

func TestRoots_Del(t *testing.T) {
	// Del(uint64) error

	// Memory-backed variant not implemented.

	t.Run("drive", func(t *testing.T) {
		idx := testNewDriveIdxDB(t)
		defer os.Remove(testFileName) //nolint:errcheck,gosec
		defer idx.Close()             //nolint:errcheck,gosec

		tests.RootsDel(t, idx)
	})

}

func TestRoots_Get(t *testing.T) {
	// Get(uint64) (*Root, error)

	// Memory-backed variant not implemented.

	t.Run("drive", func(t *testing.T) {
		idx := testNewDriveIdxDB(t)
		defer os.Remove(testFileName) //nolint:errcheck,gosec
		defer idx.Close()             //nolint:errcheck,gosec

		tests.RootsGet(t, idx)
	})

}

func TestRoots_Has(t *testing.T) {
	// Has(uint64) bool

	// Memory-backed variant not implemented.

	t.Run("drive", func(t *testing.T) {
		idx := testNewDriveIdxDB(t)
		defer os.Remove(testFileName) //nolint:errcheck,gosec
		defer idx.Close()             //nolint:errcheck,gosec

		tests.RootsHas(t, idx)
	})

}
