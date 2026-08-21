//go:build !tinygo || (js && wasm)

// route_setup_fallback_test.go — covers the cascade -> classic (legacy)
// setup-node fallback plumbing. When a cascade-installed route fails its
// data-plane handshake (the fingerprint of a destination that does not trust
// cascade route setup), DialRoutes flips WithForceLegacyRouteSetup on the dial
// context for the remaining attempts so the RouteGroupDialer uses the classic
// trusted-setup-node path the destination accepts. These tests lock the
// context plumbing and the dialer's cascade-vs-classic decision.
package router

import (
	"context"
	"testing"

	"github.com/skycoin/skywire/pkg/routing"
)

func TestForceLegacyRouteSetupContext(t *testing.T) {
	base := context.Background()
	if forceLegacyRouteSetup(base) {
		t.Fatal("a plain context must NOT force the legacy setup path")
	}
	forced := WithForceLegacyRouteSetup(base)
	if !forceLegacyRouteSetup(forced) {
		t.Fatal("WithForceLegacyRouteSetup(ctx) must force the legacy setup path")
	}
	// The flag must not leak back to the parent context.
	if forceLegacyRouteSetup(base) {
		t.Fatal("force-legacy leaked onto the parent context")
	}
	// A child of the forced context inherits the flag.
	child := context.WithValue(forced, struct{ k int }{1}, "v")
	if !forceLegacyRouteSetup(child) {
		t.Fatal("child of a force-legacy context must inherit the flag")
	}
}

// stubOrigin is a no-op cascadeOriginProcessor; cascadeEnabled only checks it
// for nil-ness, so it is never invoked here.
type stubOrigin struct{}

func (stubOrigin) ProcessLocalOrigin(_ []byte) (*routing.CascadeAck, error) { return nil, nil }

func TestSetupNodeDialer_CascadeEnabled(t *testing.T) {
	cascade := &CascadeBuilder{}
	origin := stubOrigin{}

	tests := []struct {
		name       string
		srcCascade *CascadeBuilder
		origin     cascadeOriginProcessor
		forceLeg   bool
		want       bool
	}{
		{"fully wired, not forced", cascade, origin, false, true},
		{"fully wired but forced legacy", cascade, origin, true, false},
		{"no source cascade builder", nil, origin, false, false},
		{"no origin processor", cascade, nil, false, false},
		{"nothing wired", nil, nil, false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := &setupNodeDialer{srcCascade: tc.srcCascade, cascadeOrigin: tc.origin}
			ctx := context.Background()
			if tc.forceLeg {
				ctx = WithForceLegacyRouteSetup(ctx)
			}
			if got := d.cascadeEnabled(ctx); got != tc.want {
				t.Fatalf("cascadeEnabled() = %v, want %v", got, tc.want)
			}
		})
	}
}
