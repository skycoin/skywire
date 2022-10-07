//go:build !windows
// +build !windows

package dmsgpty

import (
	"os"
	"path/filepath"
)

// DefaultCLIAddr gets the default cli address (temp address)
func DefaultCLIAddr() string {
	return filepath.Join(os.TempDir(), "dmsgpty.sock")
}
