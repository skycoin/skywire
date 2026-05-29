// Package policy pkg/router/policy/loader.go — turn a config-
// field value (`"@/etc/skywire/policies/global.star"` or an
// inline string) into a live Evaluator, with file-watcher
// hot-reload so editing the file doesn't require a visor restart.
package policy

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Loader manages an Evaluator constructed from a config field.
// It encapsulates two patterns:
//
//  1. Inline string — config field starts with anything other
//     than "@". The string is the script. No file involved; no
//     watching.
//  2. File reference — config field starts with "@". The string
//     after "@" is a path. The Loader reads the file, constructs
//     the Evaluator, and (optionally) watches the file for
//     changes; on change it re-loads and atomic-swaps the
//     Evaluator.
//
// Engine is the surface the Hook needs from any policy backend.
// Both the Starlark Loader (this package) and the WASM Loader
// (pkg/router/policy/wasm) implement it, so the Hook can
// dispatch to either without caring which backend the operator
// chose.
type Engine interface {
	Decide(ctx context.Context, rctx RoutingContext, candidates []Candidate) (RouteSpec, error)
	OnLegChange(ctx context.Context, rctx RoutingContext, legs []LegInfo, change LegChange) (RouteSpec, error)
	IsActive() bool
	Source() string
	Close() error
}

// Callers go through Loader.Decide rather than holding an
// Evaluator directly so the hot-reload path is transparent.
type Loader struct {
	source  string
	path    string // "" for inline
	opts    []Option
	current atomic.Pointer[Evaluator]

	watcher   *fsnotify.Watcher
	stopWatch context.CancelFunc
	logger    func(format string, args ...interface{})
}

// NewLoader parses the supplied raw config-field value and
// returns a Loader. If raw is "" or "none", the Loader runs in
// no-op mode (every Decide returns the visor default). If raw
// starts with "@", the suffix is treated as a file path; the
// file's contents are loaded and watched. Otherwise raw is
// treated as an inline script.
//
// Returns an error only on a load-time failure (file unreadable,
// script unparseable). Runtime failures during Decide flow
// through the Evaluator's FailureMode.
func NewLoader(raw string, opts ...Option) (*Loader, error) {
	if raw == "" || raw == "none" {
		return &Loader{source: "<noop>", opts: opts}, nil
	}

	l := &Loader{opts: opts, logger: func(string, ...interface{}) {}}
	for _, o := range opts {
		// Capture logger if one was supplied (we'll reuse it for
		// reload events). This is a bit of a hack — we materialize
		// a temporary Evaluator to extract the logger config, but
		// since the Option pattern doesn't let us read fields back
		// out, simpler is to just rely on the user passing
		// WithLogger explicitly if they want reload events
		// logged.
		_ = o
	}

	if strings.HasPrefix(raw, "@") {
		p, err := filepath.Abs(strings.TrimPrefix(raw, "@"))
		if err != nil {
			return nil, fmt.Errorf("policy: invalid file path: %w", err)
		}
		l.source = p
		l.path = p
		if err := l.reload(); err != nil {
			return nil, err
		}
		return l, nil
	}

	// Inline script.
	l.source = "<inline>"
	eval, err := NewEvaluator(l.source, raw, opts...)
	if err != nil {
		return nil, err
	}
	l.current.Store(eval)
	return l, nil
}

// reload reads the file (if file-backed) and atomically swaps in
// a fresh Evaluator. On error the previous Evaluator stays
// active.
func (l *Loader) reload() error {
	if l.path == "" {
		return nil
	}
	data, err := os.ReadFile(l.path) //nolint:gosec
	if err != nil {
		return fmt.Errorf("policy: read %s: %w", l.path, err)
	}
	eval, err := NewEvaluator(l.source, string(data), l.opts...)
	if err != nil {
		return fmt.Errorf("policy: load %s: %w", l.path, err)
	}
	l.current.Store(eval)
	return nil
}

// Watch starts a goroutine that reloads the script when the file
// changes. No-op for inline scripts. Returns immediately; the
// goroutine runs until Close is called.
//
// On reload failure the watcher logs the error and keeps the
// previous Evaluator active. This way a transient editor
// (vi-style atomic-rename) doesn't break the running policy.
func (l *Loader) Watch(logger func(format string, args ...interface{})) error {
	if l.path == "" {
		return nil // inline — nothing to watch
	}
	if logger != nil {
		l.logger = logger
	}
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("policy: watcher: %w", err)
	}
	// Watch the parent directory, not the file itself — editors
	// often rename (vim's `*.swp` → `*` dance) which breaks
	// single-file watchers.
	dir := filepath.Dir(l.path)
	if err := w.Add(dir); err != nil {
		_ = w.Close() //nolint:errcheck
		return fmt.Errorf("policy: watch %s: %w", dir, err)
	}
	l.watcher = w
	ctx, cancel := context.WithCancel(context.Background())
	l.stopWatch = cancel
	go l.watchLoop(ctx)
	return nil
}

func (l *Loader) watchLoop(ctx context.Context) {
	// Debounce: editors save in bursts (write + chmod + rename).
	// We coalesce events within a short window so we don't
	// thrash on every byte.
	var pending bool
	debounce := time.NewTimer(0)
	if !debounce.Stop() {
		<-debounce.C
	}
	const debounceWindow = 200 * time.Millisecond

	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-l.watcher.Events:
			if !ok {
				return
			}
			// Filter to our specific file. Compare absolute paths
			// since events carry the path the watcher saw.
			abs, _ := filepath.Abs(ev.Name) //nolint:errcheck
			if abs != l.path {
				continue
			}
			if ev.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename) == 0 {
				continue
			}
			pending = true
			if !debounce.Stop() {
				select {
				case <-debounce.C:
				default:
				}
			}
			debounce.Reset(debounceWindow)
		case <-debounce.C:
			if !pending {
				continue
			}
			pending = false
			if err := l.reload(); err != nil {
				l.logger("policy %s: reload failed, keeping previous: %v", l.path, err)
				continue
			}
			l.logger("policy %s: reloaded", l.path)
		case err, ok := <-l.watcher.Errors:
			if !ok {
				return
			}
			l.logger("policy %s: watcher error: %v", l.path, err)
		}
	}
}

// Close stops the file-watcher goroutine and releases resources.
func (l *Loader) Close() error {
	if l.stopWatch != nil {
		l.stopWatch()
	}
	if l.watcher != nil {
		return l.watcher.Close()
	}
	return nil
}

// Decide is the loader's transparent entry point. Forwards to
// the currently-loaded Evaluator. If the loader is in noop mode
// (empty config field), returns an empty RouteSpec without
// invoking any script — the router treats this as "use built-in
// default."
func (l *Loader) Decide(ctx context.Context, rctx RoutingContext, candidates []Candidate) (RouteSpec, error) {
	eval := l.current.Load()
	if eval == nil {
		return RouteSpec{}, nil
	}
	return eval.Decide(ctx, rctx, candidates)
}

// OnLegChange forwards to the currently-loaded Evaluator's
// OnLegChange. Returns empty spec when no evaluator is loaded.
func (l *Loader) OnLegChange(ctx context.Context, rctx RoutingContext, legs []LegInfo, change LegChange) (RouteSpec, error) {
	eval := l.current.Load()
	if eval == nil {
		return RouteSpec{}, nil
	}
	return eval.OnLegChange(ctx, rctx, legs, change)
}

// Source returns the human-readable source identifier (file path
// or "<inline>" or "<noop>"). Used by callers for logging.
func (l *Loader) Source() string { return l.source }

// IsActive reports whether the loader has an evaluator loaded
// (i.e. not noop mode). Useful for the router to skip the call
// path entirely when no policy is configured.
func (l *Loader) IsActive() bool { return l.current.Load() != nil }
