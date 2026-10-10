package visor

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestHeartbeatURL guards the reward-uptime heartbeat target: TPD's clearnet
// address, else its dmsg address. Once the target hung off the retired uptime
// tracker's config, and emptying that config silently stopped the heartbeat.
func TestHeartbeatURL(t *testing.T) {
	require.Equal(t, "http://tpd", heartbeatURL("http://tpd", "dmsg://tpd:80"))
	require.Equal(t, "dmsg://tpd:80", heartbeatURL("", "dmsg://tpd:80"))
	require.Equal(t, "", heartbeatURL("", ""))
}
