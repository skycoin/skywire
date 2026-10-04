//go:build js && wasm

// Package pty pkg/pty/pty_js.go c2-app-pty
//
// js/wasm helpers. A browser has no pseudo-terminal or processes, so the pty
// host runs a websh session instead (pty_websh_js.go).
package pty

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

// Errors mirrored from the platform pty hosts.
var (
	ErrPtyAlreadyRunning = errors.New("a pty session is already running")
	ErrPtyNotRunning     = errors.New("no active pty session")
)

// DefaultCLIAddr gets the default cli address (temp address).
func DefaultCLIAddr() string {
	return filepath.Join(os.TempDir(), "pty.sock")
}

// uiWinSize returns the initial pty window size and environment for a web
// terminal (mirrors the platform variants; no host terminal to measure).
func (ui *UI) uiWinSize() (*WinSize, []string, error) {
	return &WinSize{Rows: wsRows, Cols: wsCols}, []string{"TERM=xterm-256color"}, nil
}

// mergeEnv mirrors the native helper: override wins per KEY=VALUE key.
func mergeEnv(base, override []string) []string {
	if len(override) == 0 {
		return base
	}
	out := make([]string, 0, len(base)+len(override))
	seen := make(map[string]struct{}, len(override))
	key := func(kv string) string {
		for i := 0; i < len(kv); i++ {
			if kv[i] == '=' {
				return kv[:i]
			}
		}
		return kv
	}
	for _, kv := range override {
		seen[key(kv)] = struct{}{}
	}
	for _, kv := range base {
		if _, ok := seen[key(kv)]; !ok {
			out = append(out, kv)
		}
	}
	return append(out, override...)
}

// ptyResizeLoop has no local terminal to track; block until canceled.
func ptyResizeLoop(ctx context.Context, _ *PtyClient) error {
	<-ctx.Done()
	return ctx.Err()
}

// prepareStdin: stdin is not a terminal in wasm; nothing to set raw.
func (cli *CLI) prepareStdin() (restore func(), err error) {
	return func() {}, nil
}

// execSysProcAttr: no processes to configure on js/wasm.
func execSysProcAttr() *syscall.SysProcAttr { return nil }

// defaultEnvSnapshot mirrors the native helper.
func defaultEnvSnapshot() []string { return os.Environ() }
