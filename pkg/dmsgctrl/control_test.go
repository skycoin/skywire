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

	t.Cleanup(func() {
		assert.NoError(t, ctrlA.Close())
		assert.NoError(t, ctrlB.Close())
	})

	for i := 0; i < times; i++ {
		// act
		durA, errA := ctrlA.Ping(context.TODO())
		durB, errB := ctrlB.Ping(context.TODO())
		t.Log(durA)
		t.Log(durB)

		// assert
		assert.NoError(t, errA)
		assert.NoError(t, errB)
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
