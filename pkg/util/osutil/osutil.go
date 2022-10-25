// Package osutil pkg/util/osutil/osutil.go
package osutil

import (
	"syscall"
)

// UnlinkSocketFiles removes unix socketFiles from file system
func UnlinkSocketFiles(socketFiles ...string) error {
	for _, f := range socketFiles {
		if err := syscall.Unlink(f); err != nil {
			if pathErrCheck(err) {
				return err
			}
		}
	}

	return nil
}
