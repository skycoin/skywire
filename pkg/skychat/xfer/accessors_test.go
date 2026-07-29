// Package xfer pkg/skychat/xfer/accessors_test.go
//
// AddListener — the late-binding hook. Serve takes the listeners available at
// startup; the skynet one only exists once the visor's networker registers, so
// it joins afterwards through here. A nil listener is a no-op so the caller
// doesn't have to branch on whether skynet came up.
package xfer

import (
	"context"
	"testing"
	"time"
)

func TestAddListener_LateBinding(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m := NewManager(Config{})

	m.AddListener(ctx, nil) // must not panic or register anything
	m.mu.Lock()
	n := len(m.serving)
	m.mu.Unlock()
	if n != 0 {
		t.Errorf("a nil listener registered %d entries, want 0", n)
	}

	lis := newMemListener()
	m.AddListener(ctx, lis)
	m.mu.Lock()
	n = len(m.serving)
	m.mu.Unlock()
	if n != 1 {
		t.Errorf("serving = %d after AddListener, want 1", n)
	}

	// Canceling unwinds the accept loop it spawned.
	cancel()
	_ = lis.Close() //nolint:errcheck
	time.Sleep(50 * time.Millisecond)
}
