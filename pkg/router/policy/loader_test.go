// Package policy pkg/router/policy/loader_test.go — tests for
// the @filepath / inline / noop loader paths and the file-watcher
// hot-reload semantics.
package policy

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestLoader_NoopMode(t *testing.T) {
	l, err := NewLoader("")
	if err != nil {
		t.Fatalf("NewLoader(\"\"): %v", err)
	}
	defer l.Close() //nolint:errcheck
	if l.IsActive() {
		t.Error("expected IsActive() = false for empty source")
	}
	spec, err := l.Decide(context.Background(), RoutingContext{App: "x"}, nil)
	if err != nil {
		t.Errorf("Decide on noop loader: %v", err)
	}
	if spec.Chosen != nil || spec.Fallback != "" {
		t.Errorf("noop loader Decide returned non-empty %+v", spec)
	}
}

func TestLoader_InlineScript(t *testing.T) {
	src := `def decide_route(ctx, candidates): return RouteSpec(fallback="ok")`
	l, err := NewLoader(src)
	if err != nil {
		t.Fatalf("NewLoader inline: %v", err)
	}
	defer l.Close() //nolint:errcheck
	if !l.IsActive() {
		t.Fatal("expected IsActive() = true for inline script")
	}
	spec, err := l.Decide(context.Background(), RoutingContext{}, nil)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if spec.Fallback != "ok" {
		t.Errorf("inline script: Fallback=%q, want %q", spec.Fallback, "ok")
	}
}

func TestLoader_FileScript(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "policy.star")
	src := `def decide_route(ctx, candidates): return RouteSpec(fallback="from-file")`
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	l, err := NewLoader("@" + path)
	if err != nil {
		t.Fatalf("NewLoader @file: %v", err)
	}
	defer l.Close() //nolint:errcheck
	if !l.IsActive() {
		t.Fatal("expected IsActive() = true for @file")
	}
	spec, err := l.Decide(context.Background(), RoutingContext{}, nil)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if spec.Fallback != "from-file" {
		t.Errorf("@file script: Fallback=%q, want %q", spec.Fallback, "from-file")
	}
}

func TestLoader_FileNotFound(t *testing.T) {
	_, err := NewLoader("@/nonexistent/path/policy.star")
	if err == nil {
		t.Fatal("expected an error for missing file, got nil")
	}
}

func TestLoader_InlineParseError(t *testing.T) {
	_, err := NewLoader("def decide_route(") // unterminated
	if err == nil {
		t.Fatal("expected a parse error, got nil")
	}
}

// TestLoader_Watch_HotReload writes a file, points the loader at
// it, starts the watcher, then rewrites the file with new
// content. After a debounce window the loader must serve the new
// content's RouteSpec without anyone calling reload() manually.
func TestLoader_Watch_HotReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "policy.star")
	v1 := `def decide_route(ctx, candidates): return RouteSpec(fallback="v1")`
	v2 := `def decide_route(ctx, candidates): return RouteSpec(fallback="v2")`
	if err := os.WriteFile(path, []byte(v1), 0o600); err != nil {
		t.Fatalf("write v1: %v", err)
	}

	var reloadCount atomic.Int32
	l, err := NewLoader("@" + path)
	if err != nil {
		t.Fatalf("NewLoader: %v", err)
	}
	defer l.Close() //nolint:errcheck

	if err := l.Watch(func(format string, args ...interface{}) {
		// Count "reloaded" log entries — they indicate a
		// successful reload completed.
		for _, s := range []string{format} {
			if contains([]string{s}, format) && len(format) > 0 && format[len(format)-len("reloaded"):] == "reloaded" {
				reloadCount.Add(1)
			}
		}
	}); err != nil {
		t.Fatalf("Watch: %v", err)
	}

	// Sanity: v1 served.
	spec, err := l.Decide(context.Background(), RoutingContext{}, nil)
	if err != nil {
		t.Fatalf("Decide v1: %v", err)
	}
	if spec.Fallback != "v1" {
		t.Errorf("pre-reload: Fallback=%q, want %q", spec.Fallback, "v1")
	}

	// Overwrite with v2 — the file-watcher should see this and
	// reload after the debounce window.
	if err := os.WriteFile(path, []byte(v2), 0o600); err != nil {
		t.Fatalf("write v2: %v", err)
	}

	// Poll for up to 2s (debounce is 200ms; give ARM CPUs some
	// margin).
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		spec, err := l.Decide(context.Background(), RoutingContext{}, nil)
		if err == nil && spec.Fallback == "v2" {
			return // reloaded successfully
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("loader did not reload within 2s of file change")
}

// TestLoader_Watch_BadReloadKeepsPrevious verifies that a syntax
// error in a reloaded file doesn't take down the running policy.
// The previous Evaluator stays active until the operator fixes
// the file.
func TestLoader_Watch_BadReloadKeepsPrevious(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "policy.star")
	good := `def decide_route(ctx, candidates): return RouteSpec(fallback="good")`
	bad := `def decide_route(` // unterminated
	if err := os.WriteFile(path, []byte(good), 0o600); err != nil {
		t.Fatalf("write good: %v", err)
	}
	l, err := NewLoader("@" + path)
	if err != nil {
		t.Fatalf("NewLoader: %v", err)
	}
	defer l.Close() //nolint:errcheck

	var logErrors atomic.Int32
	if err := l.Watch(func(format string, args ...interface{}) {
		if len(format) >= 13 && format[:13] == "policy %s: re" {
			logErrors.Add(1)
		}
	}); err != nil {
		t.Fatalf("Watch: %v", err)
	}

	// Write the bad version — reload should FAIL and the
	// previous evaluator should remain active.
	if err := os.WriteFile(path, []byte(bad), 0o600); err != nil {
		t.Fatalf("write bad: %v", err)
	}

	// Wait long enough for the debounce + reload attempt.
	time.Sleep(500 * time.Millisecond)

	spec, err := l.Decide(context.Background(), RoutingContext{}, nil)
	if err != nil {
		t.Fatalf("Decide after bad-reload: %v", err)
	}
	if spec.Fallback != "good" {
		t.Errorf("expected previous Evaluator to stay active; Fallback=%q, want %q", spec.Fallback, "good")
	}
}
