// Package clidmsg cmd/skywire-cli/commands/dmsg/scp_fallback_test.go c4-vis-cli
package clidmsg

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

// Auto falls back to the standalone path only when no visor answered.
func TestVisorUnreachable(t *testing.T) {
	refused := fmt.Errorf("VisorSCP RPC client: %w: %w", errVisorUnreachable, errors.New("connection refused"))
	require.True(t, visorUnreachable(refused))
	require.False(t, visorUnreachable(errors.New("VisorSCP (dmsg): i/o deadline reached")))
	require.False(t, visorUnreachable(errors.New("VisorSCP (dmsg): rejected by whitelist")))
	require.False(t, visorUnreachable(nil))
}
