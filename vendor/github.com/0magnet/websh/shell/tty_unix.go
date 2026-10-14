//go:build unix

package shell

import (
	"os"

	"golang.org/x/sys/unix"
)

// blockingDup is a second descriptor for the terminal f, with the terminal
// set blocking: the flag belongs to the terminal's open file, which every
// descriptor for it shares.
func blockingDup(f *os.File) (*os.File, error) {
	sc, err := f.SyscallConn()
	if err != nil {
		return nil, err
	}
	var fd int
	var derr error
	if err := sc.Control(func(raw uintptr) {
		fd, derr = unix.Dup(int(raw))
		if derr == nil {
			derr = unix.SetNonblock(fd, false)
		}
	}); err != nil {
		return nil, err
	}
	if derr != nil {
		return nil, derr
	}
	return os.NewFile(uintptr(fd), f.Name()), nil
}

// restoreNonblock sets the terminal non-blocking again, through d, one of
// blockingDup's.
func restoreNonblock(d *os.File) {
	unix.SetNonblock(int(d.Fd()), true) //nolint:errcheck,gosec // reads of the shell's own wait in the poller either way
}
