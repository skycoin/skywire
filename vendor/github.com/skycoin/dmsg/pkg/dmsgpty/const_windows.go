//go:build windows
// +build windows

// Package dmsgpty pkg/dmsgpty/const_windows.go
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
