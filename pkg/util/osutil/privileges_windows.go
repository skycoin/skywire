//go:build windows
// +build windows

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
