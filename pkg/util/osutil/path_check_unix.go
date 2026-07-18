//go:build !windows
// +build !windows

// Package osutil pkg/util/osutil/path_check_unix.go c0-com-util
package osutil

import (
	"errors"
	"io/fs"
)

func pathErrCheck(err error) bool {
	return !errors.Is(err, fs.ErrNotExist)
}
