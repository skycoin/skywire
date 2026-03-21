package idxdb

import (
	"io/ioutil" //nolint:staticcheck
	"os"

	"github.com/skycoin/skywire/pkg/cxo/data"
)

type memoryDB struct {
	*driveDB
	name string
}

// NewMemeoryDB returns stub for memory DB.
// The memeory-db is not implemened yet
// and the function returns on-drive-db that
// uses temporary file that deleted on Close
func NewMemeoryDB() (idx data.IdxDB) {
	fl, err := ioutil.TempFile("", "cxds")
	if err != nil {
		panic(err)
	}
	fl.Close()           //nolint:errcheck,gosec,gofmt
	os.Remove(fl.Name()) //nolint:errcheck,gosec
	// the NewDriveIdxDB uses os.Stat for internals
	// the removing is not as safe, but any problem
	// can occurs in < 1% of cases
	if idx, err = NewDriveIdxDB(fl.Name()); err != nil {
		panic(err)
	}
	idx = &memoryDB{idx.(*driveDB), fl.Name()} // wrap
	return
}

func (m *memoryDB) Close() (err error) {
	err = m.driveDB.Close() //nolint:errcheck,gosec
	os.Remove(m.name)       //nolint:errcheck,gosec,gofmt
	return
}
