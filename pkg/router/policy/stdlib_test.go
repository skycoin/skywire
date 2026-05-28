// Package policy pkg/router/policy/stdlib_test.go — tests for
// the geo / transports / peers / logging stdlib modules.
package policy

import (
	"context"
	"strings"
	"testing"
)

func TestGeo_CountryFromProvider(t *testing.T) {
	prov := NewFakeProvider().
		SetGeo("pk_indonesia", "ID").
		SetGeo("pk_germany", "DE")
	src := `
def decide_route(ctx, candidates):
    return RouteSpec(fallback = geo.country(ctx.peer_pk))
`
	eval, err := NewEvaluator("geo.star", src, WithProvider(prov))
	if err != nil {
		t.Fatalf("NewEvaluator: %v", err)
	}
	tests := []struct{ pk, want string }{
		{"pk_indonesia", "ID"},
		{"pk_germany", "DE"},
		{"pk_unknown", "??"},
	}
	for _, tc := range tests {
		spec, err := eval.Decide(context.Background(), RoutingContext{PeerPK: tc.pk}, nil)
		if err != nil {
			t.Errorf("Decide(%s): %v", tc.pk, err)
			continue
		}
		if spec.Fallback != tc.want {
			t.Errorf("geo.country(%q) = %q, want %q", tc.pk, spec.Fallback, tc.want)
		}
	}
}

func TestTransports_LatencyAndKind(t *testing.T) {
	prov := NewFakeProvider().
		SetLatency("pk_fast", 20).
		SetKind("pk_fast", "stcpr").
		SetKind("pk_slow", "dmsg")
	// A policy that bins peers by latency tier into different
	// mux strategies. The "unknown latency" branch is the third
	// case so the test pins all three explicit paths.
	src := `
def decide_route(ctx, candidates):
    ms = transports.latency(ctx.peer_pk)
    kind = transports.kind(ctx.peer_pk)
    if ms == 0:
        return RouteSpec(fallback = "unknown:" + kind)
    if ms < 50:
        return RouteSpec(fallback = "fast:" + kind)
    return RouteSpec(fallback = "slow:" + kind)
`
	eval, err := NewEvaluator("tp.star", src, WithProvider(prov))
	if err != nil {
		t.Fatalf("NewEvaluator: %v", err)
	}
	cases := []struct{ pk, want string }{
		{"pk_fast", "fast:stcpr"},
		{"pk_slow", "unknown:dmsg"}, // SetLatency wasn't called for pk_slow
		{"pk_unknown", "unknown:"},
	}
	for _, tc := range cases {
		spec, err := eval.Decide(context.Background(), RoutingContext{PeerPK: tc.pk}, nil)
		if err != nil {
			t.Errorf("Decide(%s): %v", tc.pk, err)
			continue
		}
		if spec.Fallback != tc.want {
			t.Errorf("latency/kind for %s: got %q, want %q", tc.pk, spec.Fallback, tc.want)
		}
	}
}

func TestPeers_IsTrustedAndIsHypervisor(t *testing.T) {
	prov := NewFakeProvider().
		SetTrusted("pk_trusted").
		SetHypervisor("pk_hv")
	src := `
def decide_route(ctx, candidates):
    if peers.is_hypervisor(ctx.peer_pk):
        return RouteSpec(fallback = "hv", mux = 4)
    if peers.is_trusted(ctx.peer_pk):
        return RouteSpec(fallback = "trusted", mux = 2)
    return RouteSpec(fallback = "other", mux = 1)
`
	eval, err := NewEvaluator("peers.star", src, WithProvider(prov))
	if err != nil {
		t.Fatalf("NewEvaluator: %v", err)
	}
	cases := []struct {
		pk      string
		want    string
		wantMux int
	}{
		{"pk_hv", "hv", 4},
		{"pk_trusted", "trusted", 2},
		{"pk_other", "other", 1},
	}
	for _, tc := range cases {
		spec, err := eval.Decide(context.Background(), RoutingContext{PeerPK: tc.pk}, nil)
		if err != nil {
			t.Errorf("Decide(%s): %v", tc.pk, err)
			continue
		}
		if spec.Fallback != tc.want {
			t.Errorf("peers branch for %s: fallback=%q, want %q", tc.pk, spec.Fallback, tc.want)
		}
		if spec.Mux != tc.wantMux {
			t.Errorf("peers branch for %s: mux=%d, want %d", tc.pk, spec.Mux, tc.wantMux)
		}
	}
}

func TestLogging_InfoAndWarn(t *testing.T) {
	// Capture the messages the policy emits via logging.info /
	// logging.warn and assert both reach the logger.
	var captured []string
	src := `
def decide_route(ctx, candidates):
    logging.info("considering dial to " + ctx.peer_pk)
    if ctx.peer_pk == "blocked":
        logging.warn("blocked peer attempted: " + ctx.peer_pk)
        return RouteSpec(fallback = "drop")
    return None
`
	eval, err := NewEvaluator("log.star", src,
		WithLogger(func(format string, args ...interface{}) {
			captured = append(captured, format)
		}))
	if err != nil {
		t.Fatalf("NewEvaluator: %v", err)
	}
	_, _ = eval.Decide(context.Background(), RoutingContext{PeerPK: "blocked"}, nil) //nolint:errcheck
	if len(captured) < 2 {
		t.Fatalf("expected 2 log messages, got %d: %v", len(captured), captured)
	}
	// Two log entries: info + warn. Both go through logger with
	// the source name and message embedded; we just check we
	// captured both kinds.
	hasInfo, hasWarn := false, false
	for _, m := range captured {
		if strings.Contains(m, "info") {
			hasInfo = true
		}
		if strings.Contains(m, "warn") {
			hasWarn = true
		}
	}
	if !hasInfo {
		t.Error("expected an info log entry")
	}
	if !hasWarn {
		t.Error("expected a warn log entry")
	}
}

func TestNopProvider_GeoReturnsUnknown(t *testing.T) {
	// Sanity: without a Provider wired in, the stdlib functions
	// still resolve and return sensible "unknown" defaults
	// rather than panicking. Operators who write a policy that
	// uses geo.country() on a freshly-installed visor (no geoip
	// data yet) should see "??" and branch on that, not crash.
	src := `
def decide_route(ctx, candidates):
    return RouteSpec(fallback = geo.country(ctx.peer_pk))
`
	eval, err := NewEvaluator("nop.star", src)
	if err != nil {
		t.Fatalf("NewEvaluator: %v", err)
	}
	spec, err := eval.Decide(context.Background(), RoutingContext{PeerPK: "anything"}, nil)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if spec.Fallback != "??" {
		t.Errorf("Fallback=%q, want %q", spec.Fallback, "??")
	}
}
