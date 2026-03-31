package idxdb

import (
	"os"
	"testing"

	"github.com/skycoin/skywire/pkg/cxo/data/tests"
)

func TestFeeds_Add(t *testing.T) {
	// Add(cipher.PubKey) error

	// Memory-backed variant not implemented.

	t.Run("drive", func(t *testing.T) {
		idx := testNewDriveIdxDB(t)
		defer os.Remove(testFileName) //nolint:errcheck,gosec
		defer idx.Close()             //nolint:errcheck,gosec
		tests.FeedsAdd(t, idx)
	})

}

func TestFeeds_Del(t *testing.T) {
	// Del(cipher.PubKey) error

	// Memory-backed variant not implemented.

	t.Run("drive", func(t *testing.T) {
		idx := testNewDriveIdxDB(t)
		defer os.Remove(testFileName) //nolint:errcheck,gosec
		defer idx.Close()             //nolint:errcheck,gosec
		tests.FeedsDel(t, idx)
	})

}

func TestFeeds_Iterate(t *testing.T) {
	// Iterate(IterateFeedsFunc) error

	// Memory-backed variant not implemented.

	t.Run("drive", func(t *testing.T) {
		idx := testNewDriveIdxDB(t)
		defer os.Remove(testFileName) //nolint:errcheck,gosec
		defer idx.Close()             //nolint:errcheck,gosec
		tests.FeedsIterate(t, idx)
	})

}

func TestFeeds_Has(t *testing.T) {
	// Has(cipher.PubKey) bool

	// Memory-backed variant not implemented.

	t.Run("drive", func(t *testing.T) {
		idx := testNewDriveIdxDB(t)
		defer os.Remove(testFileName) //nolint:errcheck,gosec
		defer idx.Close()             //nolint:errcheck,gosec
		tests.FeedsHas(t, idx)
	})

}

func TestFeeds_Heads(t *testing.T) {
	// Heads(cipher.PubKey) (Roots, error)

	// Memory-backed variant not implemented.

	t.Run("drive", func(t *testing.T) {
		idx := testNewDriveIdxDB(t)
		defer os.Remove(testFileName) //nolint:errcheck,gosec
		defer idx.Close()             //nolint:errcheck,gosec
		tests.FeedsHeads(t, idx)
	})

}
