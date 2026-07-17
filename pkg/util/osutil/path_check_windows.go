//go:build windows
// +build windows

// Package osutil pkg/util/osutil/path_check_windows.go c0-com-util
package osutil

import (
	"errors"
	"syscall"
)

func pathErrCheck(err error) bool {
	return !errors.Is(err, syscall.ERROR_NOT_FOUND) && !errors.Is(err, syscall.ERROR_PATH_NOT_FOUND) && !errors.Is(err, syscall.ERROR_FILE_NOT_FOUND)
}
