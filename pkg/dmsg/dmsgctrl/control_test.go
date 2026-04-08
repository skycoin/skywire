// Package dmsgctrl pkg/dmsgctrl/control_test.go
package dmsgctrl

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestControl_Ping(t *testing.T) {
	const times = 10

	// arrange
	connA, connB := net.Pipe()
	ctrlA := ControlStream(connA)
	ctrlB := ControlStream(connB)

	defer func() {
		// Close in order: B first (the responder side), then A.
		// This avoids EOF races where A's close kills the pipe
		// while B's serve goroutine is mid-read.
		_ = ctrlB.Close() //nolint:errcheck
		_ = ctrlA.Close() //nolint:errcheck
	}()

	for i := 0; i < times; i++ {
		// Ping A → B (ctrlA sends ping, ctrlB's serve goroutine responds with pong).
		durA, errA := ctrlA.Ping(context.TODO())
		require.NoError(t, errA, "ping A failed on iteration %d", i)
		t.Logf("A: %v", durA)

		// Ping B → A.
		durB, errB := ctrlB.Ping(context.TODO())
		require.NoError(t, errB, "ping B failed on iteration %d", i)
		t.Logf("B: %v", durB)
	}
}

func TestControl_Done(t *testing.T) {
	// arrange
	connA, connB := net.Pipe()
	ctrlA := ControlStream(connA)
	ctrlB := ControlStream(connB)

	// act
	require.NoError(t, ctrlA.Close())
	time.Sleep(time.Millisecond * 200)

	// assert
	assert.True(t, isDone(ctrlA.Done()))
	assert.True(t, isDone(ctrlB.Done()))
}
