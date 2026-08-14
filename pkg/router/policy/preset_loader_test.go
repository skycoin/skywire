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
}
