//go:build !windows
// +build !windows

// Package dmsgpty pkg/dmsgpty/const_unix.go
package dmsgpty

import (
	"os"
	"path/filepath"
)

// DefaultCLIAddr gets the default cli address (temp address)
func DefaultCLIAddr() string {
	return filepath.Join(os.TempDir(), "dmsgpty.sock")
}
