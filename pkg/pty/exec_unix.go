//go:build !windows

// Package pty pkg/pty/exec_unix.go
//
// Unix-side platform helpers for the Exec gateway path. Splits out
// the SysProcAttr setup (Setsid for process-group kill) and the
// env-snapshot helper so the cross-platform exec_gateway.go stays
// clean of build tags.

package pty

import (
	"os"
	"syscall"
)

// execSysProcAttr returns the SysProcAttr to apply to commands
// spawned by Exec. Setsid puts the child in a new process group so
// that context-cancellation SIGKILL reaches descendants too — without
// it, a shell child that forks (e.g. `sh -c 'sleep 9999'`) would
// orphan on timeout instead of dying.
func execSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}

// defaultEnvSnapshot returns the host's current environment as a
// freshly-allocated slice. Used as the base for Exec's env merge.
func defaultEnvSnapshot() []string {
	return os.Environ()
}
