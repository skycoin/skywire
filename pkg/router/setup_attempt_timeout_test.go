package router

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func within(t *testing.T, want, got time.Duration) {
	t.Helper()
	require.InDelta(t, float64(want), float64(got), float64(200*time.Millisecond), "want ~%v, got %v", want, got)
}

// TestSetupAttemptLeavesRoomToRetry: a dial with 30 s in all — the common
// caller budget — must not hand one attempt all 30 s.
func TestSetupAttemptLeavesRoomToRetry(t *testing.T) {
	at := func(budget time.Duration) time.Duration {
		ctx, cancel := context.WithTimeout(context.Background(), budget)
		defer cancel()
		return setupAttemptTimeout(ctx)
	}
	within(t, 15*time.Second, at(30*time.Second))
	within(t, routeSetupDialTimeout, at(5*time.Minute))
	within(t, routeSetupAttemptFloor, at(12*time.Second))
	within(t, 5*time.Second, at(5*time.Second))
	require.Equal(t, routeSetupDialTimeout, setupAttemptTimeout(context.Background()), "no deadline: the fixed bound")
}
