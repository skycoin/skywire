// Package porter pkg/transport/network/porter/porter_test.go: unit tests for
// the port-reservation helper.
package porter

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewReservesPortZero verifies port 0 is reserved up-front so it can never
// be handed out.
func TestNewReservesPortZero(t *testing.T) {
	p := New(MinEphemeral)

	ok, free := p.Reserve(0)
	assert.False(t, ok)
	assert.Nil(t, free)
}

// TestReserve verifies a port can be reserved once, refuses a second
// reservation, and becomes available again after its freer runs.
func TestReserve(t *testing.T) {
	p := New(MinEphemeral)

	ok, free := p.Reserve(8080)
	require.True(t, ok)
	require.NotNil(t, free)

	// Re-reserving the same port fails while held.
	ok2, free2 := p.Reserve(8080)
	assert.False(t, ok2)
	assert.Nil(t, free2)

	// Releasing makes it reservable again.
	free()
	ok3, free3 := p.Reserve(8080)
	assert.True(t, ok3)
	assert.NotNil(t, free3)
}

// TestReserveFreerIdempotent verifies calling the freer more than once is safe
// and does not free a port reserved by a later caller.
func TestReserveFreerIdempotent(t *testing.T) {
	p := New(MinEphemeral)

	ok, free := p.Reserve(9090)
	require.True(t, ok)

	free()
	free() // second call is a no-op thanks to sync.Once

	// A fresh reservation of the same port should survive the duplicate free.
	ok2, _ := p.Reserve(9090)
	require.True(t, ok2)

	free() // freeing the original handle must not release the new reservation
	ok3, _ := p.Reserve(9090)
	assert.False(t, ok3)
}

// TestReserveEphemeral verifies ephemeral reservations are unique and at or
// above the configured minimum.
func TestReserveEphemeral(t *testing.T) {
	p := New(MinEphemeral)
	ctx := context.Background()

	seen := make(map[uint16]struct{})
	for range 5 {
		port, free, err := p.ReserveEphemeral(ctx)
		require.NoError(t, err)
		require.NotNil(t, free)
		assert.GreaterOrEqual(t, port, MinEphemeral)

		_, dup := seen[port]
		assert.False(t, dup, "port %d handed out twice", port)
		seen[port] = struct{}{}
	}
}

// TestReserveEphemeralSkipsReserved verifies ReserveEphemeral skips a port that
// is already taken and returns the next free one.
func TestReserveEphemeralSkipsReserved(t *testing.T) {
	p := New(MinEphemeral)

	// The first ephemeral candidate is minEph+1; reserve it manually so the
	// allocator has to skip past it.
	ok, _ := p.Reserve(MinEphemeral + 1)
	require.True(t, ok)

	port, free, err := p.ReserveEphemeral(context.Background())
	require.NoError(t, err)
	require.NotNil(t, free)
	assert.NotEqual(t, MinEphemeral+1, port)
	assert.GreaterOrEqual(t, port, MinEphemeral)
}

// TestReserveEphemeralContextCancelled verifies that when no ephemeral port is
// available, a canceled context aborts the search with its error.
func TestReserveEphemeralContextCancelled(t *testing.T) {
	// minEph at the top of the uint16 range gives exactly one ephemeral slot
	// (65535); reserving it forces the allocator to loop and hit the ctx check.
	const minEph = ^uint16(0) // 65535
	p := New(minEph)

	ok, _ := p.Reserve(minEph)
	require.True(t, ok)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	port, free, err := p.ReserveEphemeral(ctx)
	assert.Equal(t, context.Canceled, err)
	assert.Zero(t, port)
	assert.Nil(t, free)
}
