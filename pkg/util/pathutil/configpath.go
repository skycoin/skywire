// Package pathutil installation and config file paths
package pathutil

import (
	"os"
)

// Exists checks if file or directory specified with `path` exists.
func Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
