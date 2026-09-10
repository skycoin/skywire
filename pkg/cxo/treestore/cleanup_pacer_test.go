// Package treestore pkg/cxo/treestore/cleanup_pacer_test.go c2-net-cxo
package treestore

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCleanupPacer(t *testing.T) {
	var p cleanupPacer
	t0 := time.Date(2026, 9, 10, 19, 0, 0, 0, time.UTC)
	require.Zero(t, p.holdFor(t0), "first sweep runs at once")

	// A 10 s sweep holds the next nudge for 200 s from its start.
	p.ran(t0, 10*time.Second)
	require.Equal(t, 190*time.Second, p.holdFor(t0.Add(10*time.Second)))
	require.Equal(t, 100*time.Second, p.holdFor(t0.Add(100*time.Second)))
	require.Zero(t, p.holdFor(t0.Add(200*time.Second)))
	require.Zero(t, p.holdFor(t0.Add(time.Hour)))

	// A millisecond sweep is effectively unpaced.
	p.ran(t0, time.Millisecond)
	require.Zero(t, p.holdFor(t0.Add(50*time.Millisecond)))
}
