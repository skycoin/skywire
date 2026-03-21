package idxdb

import (
	"os"
	"testing"

	"github.com/skycoin/skywire/pkg/cxo/data/tests"
)

func TestFeeds_Add(t *testing.T) {
	// Add(cipher.PubKey) error

	// TODO (kostyarin): memory

	t.Run("drive", func(t *testing.T) {
		idx := testNewDriveIdxDB(t)
		defer os.Remove(testFileName)
		defer idx.Close()
		tests.FeedsAdd(t, idx)
	})

}

func TestFeeds_Del(t *testing.T) {
	// Del(cipher.PubKey) error

	// TODO (kostyarin): memory

	t.Run("drive", func(t *testing.T) {
		idx := testNewDriveIdxDB(t)
		defer os.Remove(testFileName)
		defer idx.Close()
		tests.FeedsDel(t, idx)
	})

}

func TestFeeds_Iterate(t *testing.T) {
	// Iterate(IterateFeedsFunc) error

	// TODO (kostyarin): memory

	t.Run("drive", func(t *testing.T) {
		idx := testNewDriveIdxDB(t)
		defer os.Remove(testFileName)
		defer idx.Close()
		tests.FeedsIterate(t, idx)
	})

}

func TestFeeds_Has(t *testing.T) {
	// Has(cipher.PubKey) bool

	// TODO (kostyarin): memory

	t.Run("drive", func(t *testing.T) {
		idx := testNewDriveIdxDB(t)
		defer os.Remove(testFileName)
		defer idx.Close()
		tests.FeedsHas(t, idx)
	})

}

func TestFeeds_Heads(t *testing.T) {
	// Heads(cipher.PubKey) (Roots, error)

	// TODO (kostyarin): memory

	t.Run("drive", func(t *testing.T) {
		idx := testNewDriveIdxDB(t)
		defer os.Remove(testFileName)
		defer idx.Close()
		tests.FeedsHeads(t, idx)
	})

}
