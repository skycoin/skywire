//go:build !windows
// +build !windows

package osutil

import (
	"errors"
	"io/fs"
)

func pathErrCheck(err error) bool {
	return !errors.Is(err, fs.ErrNotExist)
}
