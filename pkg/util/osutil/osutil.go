package osutil

import (
	"errors"
	"syscall"
)

// UnlinkSocketFiles removes unix socketFiles from file system
func UnlinkSocketFiles(socketFiles ...string) error {
	for _, f := range socketFiles {
		if err := syscall.Unlink(f); err != nil {
			// todo: check for specific unix error and use errors.Is instead of string contains
			if !errors.Is(err, syscall.ERROR_FILE_NOT_FOUND) {
				return err
			}
		}
	}

	return nil
}