package policy

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSpreadBWLegHealth exercises the spread-bw preset's on_tick resilience:
// it sheds the single worst (lossy/slow) leg per tick and replaces it, never
// drops the last live leg, and surfaces the new per-leg retransmit signal.
func TestSpreadBWLegHealth(t *testing.T) {
	load := func(t *testing.T) *Loader {
		l, err := NewLoader("preset:spread-bw")
		require.NoError(t, err)
		return l
	}
	tick := func(t *testing.T, l *Loader, legs []LegInfo) RotationAction {
		act, err := l.OnTick(context.Background(), RoutingContext{App: "skysocks-client"}, legs)
		require.NoError(t, err)
		return act
	}

	t.Run("sheds the single lossy leg and replaces it", func(t *testing.T) {
		// leg 2 has heavy retransmits → highest badness; others healthy.
		legs := []LegInfo{
			{Index: 0, Alive: true, LatencyMs: 50, Hops: []string{"a"}},
			{Index: 1, Alive: true, LatencyMs: 60, Hops: []string{"b"}},
			{Index: 2, Alive: true, LatencyMs: 55, Retransmits: 20, Hops: []string{"c"}},
			{Index: 3, Alive: true, LatencyMs: 70, Hops: []string{"d"}},
		}
		act := tick(t, load(t), legs)
		assert.Equal(t, []int{2}, act.DropLegs, "only the lossy leg")
		assert.True(t, act.AddLeg, "replace it to keep the mux degree")
		assert.Equal(t, []string{"c"}, act.ExcludeHops, "steer the replacement off its intermediate")
	})

	t.Run("sheds a clearly-slow leg", func(t *testing.T) {
		legs := []LegInfo{
			{Index: 0, Alive: true, LatencyMs: 40},
			{Index: 1, Alive: true, LatencyMs: 900}, // ~22x the best
			{Index: 2, Alive: true, LatencyMs: 50},
		}
		act := tick(t, load(t), legs)
		assert.Equal(t, []int{1}, act.DropLegs)
	})

	t.Run("never drops the last live leg", func(t *testing.T) {
		legs := []LegInfo{
			{Index: 0, Alive: true, LatencyMs: 5000, Retransmits: 999},
			{Index: 1, Alive: false},
		}
		act := tick(t, load(t), legs)
		assert.Empty(t, act.DropLegs)
	})

	t.Run("healthy legs under budget → no rotation", func(t *testing.T) {
		legs := []LegInfo{
			{Index: 0, Alive: true, LatencyMs: 50, SentBytes: 1000, RecvBytes: 1000},
			{Index: 1, Alive: true, LatencyMs: 55, SentBytes: 1000, RecvBytes: 1000},
		}
		act := tick(t, load(t), legs)
		assert.Empty(t, act.DropLegs)
		assert.False(t, act.AddLeg)
	})
}
