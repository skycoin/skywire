// Copyright (c) 2026, 0magnet fork authors.
// See LICENSE for licensing information.

//go:build tinygo

package interp

import "os"

// tinygo has no os.SameFile; approximate with name/size/modtime, which
// is exact for the virtual filesystems used on wasm targets.
func sameFile(fi1, fi2 os.FileInfo) bool {
	return fi1.Name() == fi2.Name() && fi1.Size() == fi2.Size() &&
		fi1.ModTime().Equal(fi2.ModTime()) && fi1.Mode() == fi2.Mode()
}
