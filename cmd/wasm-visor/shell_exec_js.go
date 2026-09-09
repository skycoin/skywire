// Package main cmd/wasm-visor/shell_exec_js.go c3-wasm-visor
//
// A headless websh: one command line run in a shell with no terminal
// attached, its output returned as text. This is what a CDP-driven harness
// calls (globalThis.__skywireDesk.exec) to drive the tab's visor —
// `skywire cli …` — and read the result back, where a console tab could
// only be screenshotted.
package main

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"time"

	"github.com/0magnet/websh/shell"
	"github.com/0magnet/websh/shell/browser"
)

// execDefaultTimeout bounds a headless command that gives no timeoutMs.
const execDefaultTimeout = 60 * time.Second

// lockedBuffer is a bytes.Buffer safe for the shell's concurrent stdout and
// stderr writers.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// runHeadless runs cmd in a fresh shell over the shared page filesystem with
// the same applets a console has, and returns everything it wrote to stdout
// and stderr. A non-zero exit is reported in the error, as "exit status N".
func runHeadless(ctx context.Context, cmd string) (string, error) {
	registerVisorApplets()
	browser.Register()
	registerMeshApplets()
	registerCurl()
	out := &lockedBuffer{}
	sh, err := shell.New(sharedShellFS(), strings.NewReader(""), out, out)
	if err != nil {
		return "", err
	}
	_, runErr := sh.Run(ctx, cmd)
	return out.String(), runErr
}
