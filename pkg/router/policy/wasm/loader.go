// Package wasm pkg/router/policy/wasm/loader.go — file-backed
// loader for a WASM routing-policy module. Mirrors the shape of
// pkg/router/policy.Loader (Decide / OnLegChange / Source /
// IsActive / Close / Watch) so the router-side wiring code in
// pkg/visor can pick either backend by file extension.
//
// Hot-reload: same fsnotify-based watcher pattern as the
// Starlark loader. Reload failures keep the previous evaluator
// active; a transient editor (vi-style atomic-rename) doesn't
// break the running policy.
package wasm

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/skycoin/skywire/pkg/router/policy"
)

// Loader holds the currently-active WASM Evaluator and an
// optional file-watcher for hot-reload.
type Loader struct {
	source  string
	path    string // "" for inline (rare for WASM — but kept for symmetry)
	opts    []Option
	current atomic.Pointer[Evaluator]

	watcher   *fsnotify.Watcher
	stopWatch context.CancelFunc
	logger    func(format string, args ...interface{})
}

// NewLoader parses raw as either a "@/path/to/policy.wasm" file
// reference or an empty / "none" no-op signal. Inline byte-array
// loading is not exposed via this constructor; use NewLoaderBytes
// in tests if needed.
func NewLoader(raw string, opts ...Option) (*Loader, error) {
	if raw == "" || raw == "none" {
		return &Loader{source: "<noop>", opts: opts}, nil
	}
	if !strings.HasPrefix(raw, "@") {
		return nil, fmt.Errorf("wasm policy: inline modules not supported; use @/path/to/policy.wasm")
	}
	p, err := filepath.Abs(strings.TrimPrefix(raw, "@"))
	if err != nil {
		return nil, fmt.Errorf("wasm policy: invalid file path: %w", err)
	}
	l := &Loader{
		source: p,
		path:   p,
		opts:   opts,
		logger: func(string, ...interface{}) {},
	}
	if err := l.reload(); err != nil {
		return nil, err
	}
	return l, nil
}

// NewLoaderBytes constructs a Loader from in-memory bytes,
// bypassing the file path. Used by tests.
func NewLoaderBytes(name string, wasmBytes []byte, opts ...Option) (*Loader, error) {
	eval, err := NewEvaluator(name, wasmBytes, opts...)
	if err != nil {
		return nil, err
	}
	l := &Loader{source: name, opts: opts, logger: func(string, ...interface{}) {}}
	l.current.Store(eval)
	return l, nil
}

// reload reads the file and atomically swaps in a fresh
// Evaluator. On error the previous Evaluator stays active.
func (l *Loader) reload() error {
	if l.path == "" {
		return nil
	}
	data, err := os.ReadFile(l.path) //nolint:gosec
	if err != nil {
		return fmt.Errorf("wasm policy: read %s: %w", l.path, err)
	}
	eval, err := NewEvaluator(l.source, data, l.opts...)
	if err != nil {
		return fmt.Errorf("wasm policy: load %s: %w", l.path, err)
	}
	prev := l.current.Swap(eval)
	if prev != nil {
		_ = prev.Close() //nolint:errcheck
	}
	return nil
}

// Watch starts a goroutine that reloads the module when the
// file changes. No-op for in-memory loaders.
func (l *Loader) Watch(logger func(format string, args ...interface{})) error {
	if l.path == "" {
		return nil
	}
	if logger != nil {
		l.logger = logger
	}
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("wasm policy: watcher: %w", err)
	}
	dir := filepath.Dir(l.path)
	if err := w.Add(dir); err != nil {
		_ = w.Close() //nolint:errcheck
		return fmt.Errorf("wasm policy: watcher add %s: %w", dir, err)
	}
	l.watcher = w
	ctx, cancel := context.WithCancel(context.Background())
	l.stopWatch = cancel
	go l.watchLoop(ctx)
	return nil
}

func (l *Loader) watchLoop(ctx context.Context) {
	const debounce = 200 * time.Millisecond
	var timer *time.Timer
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-l.watcher.Events:
			if !ok {
				return
			}
			if filepath.Clean(ev.Name) != filepath.Clean(l.path) {
				continue
			}
			if ev.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename) == 0 {
				continue
			}
			if timer != nil {
				timer.Stop()
			}
			timer = time.AfterFunc(debounce, func() {
				if err := l.reload(); err != nil {
					l.logger("wasm policy %s: reload failed: %v", l.path, err)
					return
				}
				l.logger("wasm policy %s: reloaded", l.path)
			})
		case err, ok := <-l.watcher.Errors:
			if !ok {
				return
			}
			l.logger("wasm policy %s: watcher error: %v", l.path, err)
		}
	}
}

// Close stops the watcher and releases the active Evaluator.
func (l *Loader) Close() error {
	if l.stopWatch != nil {
		l.stopWatch()
	}
	if l.watcher != nil {
		_ = l.watcher.Close() //nolint:errcheck
	}
	if eval := l.current.Load(); eval != nil {
		_ = eval.Close() //nolint:errcheck
	}
	return nil
}

// Decide forwards to the active Evaluator's Decide. Returns the
// empty RouteSpec when no evaluator is loaded.
func (l *Loader) Decide(ctx context.Context, rctx policy.RoutingContext, candidates []policy.Candidate) (policy.RouteSpec, error) {
	eval := l.current.Load()
	if eval == nil {
		return policy.RouteSpec{}, nil
	}
	return eval.Decide(ctx, rctx, candidates)
}

// OnLegChange forwards to the active Evaluator's OnLegChange.
func (l *Loader) OnLegChange(ctx context.Context, rctx policy.RoutingContext, legs []policy.LegInfo, change policy.LegChange) (policy.RouteSpec, error) {
	eval := l.current.Load()
	if eval == nil {
		return policy.RouteSpec{}, nil
	}
	return eval.OnLegChange(ctx, rctx, legs, change)
}

// Source returns the human-readable source identifier (file
// path or "<noop>"). Used by callers for logging.
func (l *Loader) Source() string { return l.source }

// IsActive reports whether the loader has an evaluator loaded.
func (l *Loader) IsActive() bool { return l.current.Load() != nil }
