//go:build !arm64
// +build !arm64

package stdio

import "syscall"

// DupTo duplicates old fd into the new fd
// see dup2 and dup3 system calls
func DupTo(oldfd, newfd int) error {
	return syscall.Dup2(oldfd, newfd)
}
