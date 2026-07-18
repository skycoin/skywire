//go:build js

// Package cxds pkg/cxo/data/cxds/drive_js.go c2-net-cxo
// js/wasm stubs for the on-disk (bbolt) CXDS. bbolt does not build under js/wasm
// (arch-specific consts + mmap), so the disk implementation lives in drive.go
// (//go:build !js). A wasm visor uses the in-memory CXDS (NewMemoryCXDS) via
// skyobject's InMemoryDB path, so these constructors are never called on that
// path — they exist only so skyobject/container.go compiles for js/wasm. Calling
// one returns an error rather than silently falling back.
package cxds

import (
	"errors"

	"github.com/skycoin/skywire/pkg/cxo/data"
)

// errNoDiskDB is returned by the on-disk constructors on js/wasm.
var errNoDiskDB = errors.New("cxds: on-disk (bbolt) datastore is unavailable under js/wasm — use InMemoryDB")

// DriveOptions mirrors the non-js type so callers (skyobject/container.go) compile
// unchanged. Only NoSync is referenced; it has no effect here.
type DriveOptions struct {
	NoSync bool
}

// NewDriveCXDS is a js/wasm stub — always errors (no on-disk DB under js/wasm).
func NewDriveCXDS(string) (data.CXDS, error) { return nil, errNoDiskDB }

// NewDriveCXDSWithOptions is a js/wasm stub — always errors.
func NewDriveCXDSWithOptions(string, DriveOptions) (data.CXDS, error) { return nil, errNoDiskDB }
