package idxdb

import (
	"os"
	"testing"

	"github.com/skycoin/skywire/pkg/cxo/data/tests"
)

func TestRoots_Ascend(t *testing.T) {
	// Ascend(IterateRootsFunc) error

	// TODO (kostyarin): memeory

	t.Run("drive", func(t *testing.T) {
		idx := testNewDriveIdxDB(t)
		defer os.Remove(testFileName)
		defer idx.Close()

		tests.RootsAscend(t, idx)
	})

}

func TestRoots_Descend(t *testing.T) {
	// Descend(IterateRootsFunc) error

	// TODO (kostyarin): memeory

	t.Run("drive", func(t *testing.T) {
		idx := testNewDriveIdxDB(t)
		defer os.Remove(testFileName)
		defer idx.Close()

		tests.RootsDescend(t, idx)
	})

}

func TestRoots_Set(t *testing.T) {
	// Set(*Root) error

	// TODO (kostyarin): memeory

	t.Run("drive", func(t *testing.T) {
		idx := testNewDriveIdxDB(t)
		defer os.Remove(testFileName)
		defer idx.Close()

		tests.RootsSet(t, idx)
	})

}

func TestRoots_Del(t *testing.T) {
	// Del(uint64) error

	// TODO (kostyarin): memeory

	t.Run("drive", func(t *testing.T) {
		idx := testNewDriveIdxDB(t)
		defer os.Remove(testFileName)
		defer idx.Close()

		tests.RootsDel(t, idx)
	})

}

func TestRoots_Get(t *testing.T) {
	// Get(uint64) (*Root, error)

	// TODO (kostyarin): memeory

	t.Run("drive", func(t *testing.T) {
		idx := testNewDriveIdxDB(t)
		defer os.Remove(testFileName)
		defer idx.Close()

		tests.RootsGet(t, idx)
	})

}

func TestRoots_Has(t *testing.T) {
	// Has(uint64) bool

	// TODO (kostyarin): memeory

	t.Run("drive", func(t *testing.T) {
		idx := testNewDriveIdxDB(t)
		defer os.Remove(testFileName)
		defer idx.Close()

		tests.RootsHas(t, idx)
	})

}
