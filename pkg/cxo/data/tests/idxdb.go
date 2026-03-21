package tests

import (
	"testing"

	"github.com/skycoin/skywire/pkg/cxo/data"
)

// IdxDBClose is test case for IdxDB.Close
func IdxDBClose(t *testing.T, idx data.IdxDB) {
	if err := idx.Close(); err != nil {
		t.Error(err)
	}
	if err := idx.Close(); err != nil {
		t.Error(err)
	}
}
