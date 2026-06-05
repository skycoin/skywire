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
}
