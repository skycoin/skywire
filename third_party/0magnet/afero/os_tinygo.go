//go:build tinygo

package afero

import (
	"os"
	"syscall"
)

// tinygo's os package lacks Chmod, Chown and Link; OsFs is not usable
// on the wasm targets this fork exists for, so report ENOSYS.

func osChmod(name string, mode os.FileMode) error { return syscall.ENOSYS }
func osChown(name string, uid, gid int) error     { return syscall.ENOSYS }

func (OsFs) Link(oldname, newname string) error { return syscall.ENOSYS }
