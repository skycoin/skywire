package policy

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/router/policy/presets"
)

// TestPresetLoader covers the "preset:<name>" config form: an embedded,
// no-compile policy selectable by name (the mechanism behind the spread-bw /
// benevolent-browsing preset).
func TestPresetLoader(t *testing.T) {
	t.Run("registry exposes spread-bw", func(t *testing.T) {
		assert.Contains(t, presets.Names(), "spread-bw")
		src, ok := presets.Source("spread-bw")
		require.True(t, ok)
		assert.Contains(t, src, "decide_route")
	})

	t.Run("spread-bw loads and forces parallel multihop", func(t *testing.T) {
		l, err := NewLoader("preset:spread-bw")
		require.NoError(t, err)
		spec, err := l.Decide(context.Background(), RoutingContext{App: "skysocks-client"}, nil)
		require.NoError(t, err)
		assert.Equal(t, 4, spec.Mux)
		assert.Equal(t, 2, spec.MinHops)
		assert.Greater(t, spec.RotationIntervalSeconds, 0)
	})

	t.Run("unknown preset errors with available list", func(t *testing.T) {
		_, err := NewLoader("preset:does-not-exist")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "spread-bw")
	})

	t.Run("every preset loads and evaluates decide_route", func(t *testing.T) {
		for _, name := range presets.Names() {
			l, err := NewLoader("preset:" + name)
			require.NoError(t, err, name)
			_, err = l.Decide(context.Background(), RoutingContext{App: "skysocks-client"}, nil)
			require.NoError(t, err, name)
		}
	})

	t.Run("asymmetric-fanout sets per-direction mux", func(t *testing.T) {
		l, err := NewLoader("preset:asymmetric-fanout")
		require.NoError(t, err)
		spec, err := l.Decide(context.Background(), RoutingContext{App: "skysocks-client"}, nil)
		require.NoError(t, err)
		assert.Equal(t, 1, spec.ForwardMux)
		assert.Equal(t, 4, spec.ReverseMux)
	})

	t.Run("privacy-max forces >=3 hops", func(t *testing.T) {
		l, err := NewLoader("preset:privacy-max")
		require.NoError(t, err)
		spec, err := l.Decide(context.Background(), RoutingContext{App: "x"}, nil)
		require.NoError(t, err)
		assert.Equal(t, 3, spec.MinHops)
		assert.Equal(t, 2, spec.Mux)
	})

	t.Run("low-latency-direct is a single low-hop path", func(t *testing.T) {
		l, err := NewLoader("preset:low-latency-direct")
		require.NoError(t, err)
		spec, err := l.Decide(context.Background(), RoutingContext{App: "x"}, nil)
		require.NoError(t, err)
		assert.Equal(t, 1, spec.Mux)
		assert.Equal(t, 1, spec.MinHops)
	})

	t.Run("prefer-connected: direct when a transport already exists", func(t *testing.T) {
		prov := NewFakeProvider().SetHasTransport("pk_connected")
		l, err := NewLoader("preset:prefer-connected", WithProvider(prov))
		require.NoError(t, err)
		spec, err := l.Decide(context.Background(), RoutingContext{App: "x", PeerPK: "pk_connected"}, nil)
		require.NoError(t, err)
		assert.Equal(t, 1, spec.Mux, "connected peer → single direct leg")
		assert.Equal(t, 1, spec.MinHops)
		assert.Equal(t, 0, spec.RotationIntervalSeconds, "connected path is stable")
	})

	t.Run("prefer-connected: multihop fan-out when not connected", func(t *testing.T) {
		// No provider (Nop → has_transport=false), or a provider that
		// doesn't know this PK: fall back to the cold multihop path.
		l, err := NewLoader("preset:prefer-connected")
		require.NoError(t, err)
		spec, err := l.Decide(context.Background(), RoutingContext{App: "x", PeerPK: "pk_stranger"}, nil)
		require.NoError(t, err)
		assert.Equal(t, 2, spec.Mux, "cold peer → modest fan-out")
		assert.Equal(t, 2, spec.MinHops)
		assert.Greater(t, spec.RotationIntervalSeconds, 0, "cold path rotates to spread relays")
	})

	t.Run("balanced is a two-leg min_hops=1 default", func(t *testing.T) {
		l, err := NewLoader("preset:balanced")
		require.NoError(t, err)
		spec, err := l.Decide(context.Background(), RoutingContext{App: "x"}, nil)
		require.NoError(t, err)
		assert.Equal(t, 2, spec.Mux)
		assert.Equal(t, 1, spec.MinHops)
	})

	t.Run("hypervisor-priority fans out to the control plane", func(t *testing.T) {
		prov := NewFakeProvider().SetHypervisor("pk_hv").SetTrusted("pk_ok")
		l, err := NewLoader("preset:hypervisor-priority", WithProvider(prov))
		require.NoError(t, err)

		hv, err := l.Decide(context.Background(), RoutingContext{PeerPK: "pk_hv"}, nil)
		require.NoError(t, err)
		assert.Equal(t, 4, hv.Mux, "hypervisor → wide fan-out")
		assert.Equal(t, 1, hv.MinHops)

		other, err := l.Decide(context.Background(), RoutingContext{PeerPK: "pk_stranger"}, nil)
		require.NoError(t, err)
		assert.Equal(t, 2, other.MinHops, "untrusted peer → real intermediate")
	})
}
