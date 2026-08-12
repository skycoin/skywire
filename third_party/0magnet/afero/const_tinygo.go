//go:build tinygo

package afero

import "syscall"

// tinygo does not define EBADFD; EBADF is the closest errno.
const BADFD = syscall.EBADF
