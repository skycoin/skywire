//go:build !windows
// +build !windows

// Package pty pkg/pty/const_unix.go
package pty

import (
	"os"
	"path/filepath"
)

// DefaultCLIAddr gets the default cli address (temp address)
func DefaultCLIAddr() string {
	return filepath.Join(os.TempDir(), "pty.sock")
}
