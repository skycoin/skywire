// Package visor — pkg/visor/init_transport_ipv6_test.go
//
// Tests for the Phase 2b v6 helper that init_transport uses to build
// the AR client's secondary v6-forced HTTP transport. We don't have
// a v6-only test fixture in this unit test, so the assertions focus
// on the constructor shape — the actual v6 dial is integration-
// tested live against an AR with an AAAA record.
package visor

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestNewV6ForcedHTTPClient_BuildsNonNil verifies the helper returns
// a well-formed *http.Client with a non-nil Transport. The
// Transport's DialContext is the v6-forcing one; we don't invoke it
// here (no v6 fixture), but the constructor shape contract is what a
// future refactor would silently break.
func TestNewV6ForcedHTTPClient_BuildsNonNil(t *testing.T) {
	c := newV6ForcedHTTPClient()
	if c == nil {
		t.Fatal("newV6ForcedHTTPClient returned nil")
	}
	assert.NotNil(t, c.Transport, "Transport must be set so DialContext is honored")
	assert.NotZero(t, c.Timeout, "Timeout must be set so a hung v6 dial doesn't pin the bind goroutine forever")
}

// TestNewV6ForcedHTTPClient_DialContextWired verifies the Transport's
// DialContext is wired through (not nil — Go's http.Transport defaults
// to a generic family-agnostic dialer when DialContext is nil, which
// would silently bypass the v6 force). Pre-cancelled ctx ensures we
// don't touch the network — we only check the dialer's plumbed in.
func TestNewV6ForcedHTTPClient_DialContextWired(t *testing.T) {
	c := newV6ForcedHTTPClient()
	tr, ok := c.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("expected *http.Transport, got %T", c.Transport)
	}
	assert.NotNil(t, tr.DialContext, "DialContext must be set so 'tcp6' force is honored on every dial")

	// Sanity: pre-cancelled ctx returns immediately. Confirms the
	// dialer call path itself doesn't panic on the v6 force, and
	// respects caller-supplied ctx. We deliberately don't make a
	// real network call (no portable v6 fixture in this unit test).
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := tr.DialContext(ctx, "tcp", "127.0.0.1:1")
	if err == nil {
		t.Fatal("expected pre-cancelled DialContext to error, got nil")
	}
}
