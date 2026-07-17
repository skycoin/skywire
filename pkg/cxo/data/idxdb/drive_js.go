//go:build js

// Package idxdb pkg/cxo/data/idxdb/drive_js.go c2-net-cxo
// js/wasm stubs for the on-disk (bbolt) index DB. bbolt does not build under
// js/wasm, so the disk implementation lives in drive.go (//go:build !js). A wasm
// visor uses the in-memory index (NewMemeoryDB) via skyobject's InMemoryDB path,
// so these constructors are never called there — they exist only so
// skyobject/container.go compiles for js/wasm.
package idxdb

import (
	"errors"

	"github.com/skycoin/skywire/pkg/cxo/data"
)

// errNoDiskDB is returned by the on-disk constructors on js/wasm.
var errNoDiskDB = errors.New("idxdb: on-disk (bbolt) index is unavailable under js/wasm — use InMemoryDB")

// DriveOptions mirrors the non-js type so callers compile unchanged.
type DriveOptions struct {
	NoSync bool
}

// NewDriveIdxDB is a js/wasm stub — always errors (no on-disk DB under js/wasm).
func NewDriveIdxDB(string) (data.IdxDB, error) { return nil, errNoDiskDB }

// NewDriveIdxDBWithOptions is a js/wasm stub — always errors.
func NewDriveIdxDBWithOptions(string, DriveOptions) (data.IdxDB, error) { return nil, errNoDiskDB }
