package idxdb

import (
	"errors"
	"os"
	"testing"

	"github.com/skycoin/skywire/pkg/cxo/data"
	"github.com/skycoin/skywire/pkg/cxo/data/tests"
)

const testFileName string = "test.db.goignore"

var errTestError = errors.New("test error") //nolint:unused

func testNewDriveIdxDB(t *testing.T) (idx data.IdxDB) {
	var err error
	if idx, err = NewDriveIdxDB(testFileName); err != nil {
		t.Fatal(err)
	}
	return
}

func TestIdxDB_Tx(t *testing.T) {
	// Tx(func(Tx) error) error

	// TODO (kostyarin):
}

func TestIdxDB_Close(t *testing.T) {
	// Close() error

	// TODO (kostyarin): memory

	t.Run("drive", func(t *testing.T) {
		idx := testNewDriveIdxDB(t)
		defer os.Remove(testFileName) //nolint:errcheck,gosec
		defer idx.Close()             //nolint:errcheck,gosec

		tests.IdxDBClose(t, idx)
	})

}
