// Package network pkg/transport/network/sockets_test.go c2-net-transport
package network

import (
	"errors"
	"fmt"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNextListenPort(t *testing.T) {
	require.Equal(t, 8001, nextListenPort(fmt.Errorf("listen: %w", syscall.EADDRINUSE), 8000))
	require.Equal(t, 8001, nextListenPort(errors.New("listen tcp :8000: bind: address already in use"), 8000))
	require.Equal(t, 81, nextListenPort(errors.New("listen tcp :80: bind: permission denied"), 80))

	start := time.Now()
	require.Equal(t, 0, nextListenPort(errors.New("not connected to a host bridge"), 0))
	require.GreaterOrEqual(t, time.Since(start), listenRetryWait)
}
