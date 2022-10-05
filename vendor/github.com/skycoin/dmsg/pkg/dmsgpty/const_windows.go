//go:build windows
// +build windows

package dmsgpty

import (
	"os"
	"path/filepath"
)

// DefaultCLIAddr gets the default cli address
func DefaultCLIAddr() string {
	homedir, err := os.UserHomeDir()
	if err != nil {
		homedir = os.TempDir()
	}
	return filepath.Join(homedir, "dmsgpty.sock")
}
