// Package fsutil internal/fsutil/fsutil.go
package fsutil

import (
	"os"
)

// Exists checks if file exists at `path`.
func Exists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}
