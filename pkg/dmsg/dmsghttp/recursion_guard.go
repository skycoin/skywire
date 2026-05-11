// Package dmsghttp pkg/dmsg/dmsghttp/recursion_guard.go
//
// Recursion guard for dmsg-over-HTTP transports.
//
// HTTPTransport.RoundTrip dials a fresh dmsg stream via the caller's
// dmsg.Client whenever there's no idle pooled stream. When the dmsg
// client's own discovery is itself wrapped with dmsgfirst (DMSG-
// first, plain-HTTP fallback), the dmsg-side path of dmsgfirst goes
// over THIS transport. So a discovery lookup triggered from inside
// RoundTrip — to obtain entries needed for the very dial that's in
// progress — re-enters RoundTrip on the same goroutine and recurses
// indefinitely until the stack overflows.
//
// The cycle is structural to the dmsgfirst design (using dmsghttp
// for the primary path is what makes it dmsg-first), so the break
// has to happen at the inner layer: dmsgfirst notices it's been
// called from within a RoundTrip frame and routes the lookup through
// its plain-HTTP fallback for that one call only. The outer caller's
// flow continues on dmsg as normal.
//
// The marker is a context value because contexts already propagate
// through the entire dmsg DialStream / EnsureAndObtainSession path.
// No extra threading required at any of the intermediate layers.
package dmsghttp

import (
	"context"
)

// recursionGuardKey is the (unexported) context key marking that the
// current goroutine is executing inside HTTPTransport.RoundTrip.
type recursionGuardKey struct{}

// WithRecursionGuard returns ctx tagged so dmsg-discovery clients
// downstream of HTTPTransport.RoundTrip (notably dmsgfirst) can tell
// they're being called recursively from within a transport dial and
// avoid re-entering the same transport.
func WithRecursionGuard(ctx context.Context) context.Context {
	return context.WithValue(ctx, recursionGuardKey{}, true)
}

// IsInsideTransport reports whether ctx was tagged by
// WithRecursionGuard somewhere up the stack. dmsgfirst's
// fallbackClient checks this in each of its APIClient methods;
// when true, it short-circuits to the plain-HTTP fallback rather
// than calling its dmsghttp-backed primary.
func IsInsideTransport(ctx context.Context) bool {
	v, _ := ctx.Value(recursionGuardKey{}).(bool)
	return v
}
