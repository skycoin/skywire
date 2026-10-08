package dmsg

import (
	"context"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// A local relay socket with nothing behind it, as while its visor restarts,
// earns the short retry and not the refusal backoff.
func TestRelayBackoffForDialFailure(t *testing.T) {
	_, err := (&net.Dialer{}).DialContext(context.Background(), "unix", filepath.Join(t.TempDir(), "missing.sock"))
	require.Error(t, err)
	require.Equal(t, relayTimeoutRetry, relayBackoffFor(fmt.Errorf("failed to dial skynet://x:70: %w", err)))
	require.Equal(t, relayTimeoutRetry, relayBackoffFor(context.DeadlineExceeded))
	require.Equal(t, relayFailureBackoff, relayBackoffFor(errors.New("refused after handshake")))
}
