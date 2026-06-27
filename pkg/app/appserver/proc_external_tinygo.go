//go:build tinygo || js

// Package appserver pkg/app/appserver/proc_external_tinygo.go
//
// TinyGo (browser/wasm) stubs for the external (OS-subprocess) app launch path.
// A browser visor runs apps in-process (appcommon.RunModeInternal) — it cannot
// fork processes — so os/exec and the golang-ipc Windows IPC server (both of
// which fail to compile here) are absent and every external operation is a
// fail-closed no-op. See proc_external_native.go for the real implementations.
package appserver

import (
	"errors"
	"io"

	"github.com/skycoin/skywire/pkg/app/appcommon"
	"github.com/skycoin/skywire/pkg/logging"
)

// errExternalLaunchUnsupported is returned by startExternal on TinyGo builds.
var errExternalLaunchUnsupported = errors.New("appserver: external (OS-subprocess) app launch is not supported on this build (TinyGo); use RunModeInternal")

// newExternalCmd has no external command on TinyGo (in-process apps only).
func newExternalCmd(_ appcommon.ProcConfig, _ *logging.MasterLogger, _ string) any { return nil }

// attachCmdLogs is a no-op on TinyGo (no external cmd to wire).
func attachCmdLogs(_ any, _ *logging.MasterLogger, _ string) io.ReadCloser { return nil }

// Cmd returns the (always nil) external command on TinyGo builds.
func (p *Proc) Cmd() any { return p.cmd }

// pid is 0 on TinyGo (in-process apps have no OS process).
func (p *Proc) pid() int { return 0 }

// isExitError is always false on TinyGo (no os/exec process exits).
func isExitError(_ error) bool { return false }

// startWindowsIPCServer is a no-op on TinyGo (no golang-ipc).
func (p *Proc) startWindowsIPCServer() error { return nil }

// signalStop is a no-op on TinyGo (no external process to signal).
func (p *Proc) signalStop() error { return nil }

// closeIPCServer is a no-op on TinyGo.
func (p *Proc) closeIPCServer() {}

// startExternal fails closed on TinyGo — a browser visor cannot fork.
func (p *Proc) startExternal() error { return errExternalLaunchUnsupported }
