//go:build windows
// +build windows

// Package pty pkg/pty/const_windows.go
package pty

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
	return filepath.Join(homedir, "pty.sock")
}
