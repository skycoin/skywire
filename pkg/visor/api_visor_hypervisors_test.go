// Package visor pkg/visor/api_visor_hypervisors_test.go c3-vis-core
package visor

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

// TestConnectedHypervisorPKs_SortedAndComplete: Overview.Hypervisors and
// ConnectedHypervisor were declared but never assigned, so both reported null
// on every visor — including one with three hypervisors configured and
// connected. A state field that always reads empty is worse than an absent one,
// because callers trust it and conclude there are none.
//
// Sorted because a snapshot people diff between calls must not reorder just
// because Go randomizes map iteration.
func TestConnectedHypervisorPKs_SortedAndComplete(t *testing.T) {
	var a, b, c cipher.PubKey
	require.NoError(t, a.Set("0312c6fadc2432ca1fdb6ea9d85377e2ebfad38702a0d156047d756269d537ed89"))
	require.NoError(t, b.Set("025904a8cbe749823cf4b0cc5cfb0574ec3bd3dffa02b802bb644b2915cc7e573e"))
	require.NoError(t, c.Set("03c758a0daabab4b4bbd524cf46306212818b051b9d92949bd8acdf6226aad32e6"))

	v := &Visor{initLock: new(sync.RWMutex), connectedHypervisors: map[cipher.PubKey]bool{a: true, b: true, c: true}}

	got := v.connectedHypervisorPKs()
	require.Len(t, got, 3)
	require.Equal(t, []cipher.PubKey{b, a, c}, got, "must be sorted by hex, stable across calls")

	// Stable across repeated calls despite map iteration order.
	require.Equal(t, got, v.connectedHypervisorPKs())
}

// An empty map yields an empty slice, not nil-with-surprises: the caller is
// entitled to range over it either way, and "none connected" is a real answer
// distinct from "never populated".
func TestConnectedHypervisorPKs_EmptyIsNotNil(t *testing.T) {
	v := &Visor{initLock: new(sync.RWMutex), connectedHypervisors: map[cipher.PubKey]bool{}}
	got := v.connectedHypervisorPKs()
	require.NotNil(t, got)
	require.Empty(t, got)
}
