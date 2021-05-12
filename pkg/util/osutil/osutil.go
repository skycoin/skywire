package osutil

import (
	"strings"
	"syscall"
)

// UnlinkSocketFiles removes unix socketFiles from file system
func UnlinkSocketFiles(socketFiles ...string) error {
	for _, f := range socketFiles {
		if err := syscall.Unlink(f); err != nil {
			// todo: check for specific unix error and use errors.Is instead of string contains
			if !strings.Contains(err.Error(), "no such file or directory") {
				return err
			}
		}
	}

	return nil
}
