//go:build windows
// +build windows

package skyenv

import (
	"os"
	"path/filepath"
)

// CLIAddr gets the default cli address
func CLIAddr() string {
	return filepath.Join(os.TempDir(), "dmsgpty.sock")
}
