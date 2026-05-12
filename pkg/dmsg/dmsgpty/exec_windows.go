//go:build windows

// Package dmsgpty pkg/dmsgpty/exec_windows.go
//
// Windows-side platform helpers for the Exec gateway path. Windows
// has no Setsid analog in syscall.SysProcAttr that maps cleanly to
// the process-group-kill behavior we want; Exec's context cancel
// fires CancelCtx + Kill on the immediate process, which is the
// best the stdlib's exec.CommandContext offers without invoking
// jobobject APIs.

package dmsgpty

import (
	"os"
	"syscall"
)

// execSysProcAttr returns nil on Windows — no process-group setup
// is performed. Context cancellation still kills the immediate
// process; child processes spawned by the command are not reached.
func execSysProcAttr() *syscall.SysProcAttr {
	return nil
}

// defaultEnvSnapshot returns the host's current environment.
func defaultEnvSnapshot() []string {
	return os.Environ()
}
