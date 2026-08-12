// Copyright (c) 2026, 0magnet fork authors.
// See LICENSE for licensing information.

//go:build !tinygo

package interp

import "os"

func sameFile(fi1, fi2 os.FileInfo) bool { return os.SameFile(fi1, fi2) }
