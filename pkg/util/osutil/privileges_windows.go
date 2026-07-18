//go:build windows
// +build windows

// Package osutil pkg/util/osutil/privileges_windows.go c0-com-util
package osutil

import (
	"syscall"
)

// GainRoot escalates privileges to gain root access, it's not needed on windows
func GainRoot() (int, error) {
	return syscall.Getuid(), nil
}

// ReleaseRoot releases root privileges, not needed on windows
func ReleaseRoot(_ int) error {
	return nil
}
