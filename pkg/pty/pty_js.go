//go:build js && wasm

// Package pty pkg/pty/pty_js.go c2-app-pty
//
// js/wasm stand-ins: a browser cannot allocate a pseudo-terminal or exec a
// process, so hosting a pty is impossible there. These keep the package — and
// the pty command tree with its real help text — compiling in the wasm build
// of the full skywire binary. The CLIENT side (dialing a remote visor's pty)
// is platform-independent and untouched; only local pty allocation errors.
package pty

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

var errNoPtyOnJS = errors.New("pty: no pseudo-terminal on js/wasm — cannot host a pty in a browser")

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
	return &WinSize{Rows: 30, Cols: 100}, []string{"TERM=xterm-256color"}, nil
}

// Pty is the local pseudo-terminal host. Unavailable on js/wasm.
type Pty struct{}

// NewPty constructs the (non-functional) js Pty host.
func NewPty() *Pty { return &Pty{} }

// Stop is a no-op.
func (s *Pty) Stop() error { return nil }

func (s *Pty) Read(_ []byte) (int, error)  { return 0, errNoPtyOnJS }
func (s *Pty) Write(_ []byte) (int, error) { return 0, errNoPtyOnJS }

// Start always fails: no exec, no pty device.
func (s *Pty) Start(_ string, _ []string, _ *WinSize, _ []string) error {
	return errNoPtyOnJS
}

// SetPtySize always fails on js/wasm.
func (s *Pty) SetPtySize(_ *WinSize) error { return errNoPtyOnJS }

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
