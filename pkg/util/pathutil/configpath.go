// Package pathutil pkg/util/pathutil/configpath.go c0-com-util
package pathutil

import (
	"os"
)

// Exists checks if file or directory specified with `path` exists.
func Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
