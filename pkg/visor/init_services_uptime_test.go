package visor

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// TestResolveUptimeTargets guards the reward-uptime regression: when the
// standalone uptime_tracker is decommissioned (nil/empty config), the
// TPD heartbeat — now the reward-critical presence signal — must still be
// targeted. A previous early-return tied the whole heartbeat loop to the
// standalone tracker, so emptying uptime_tracker fleet-wide silently disabled
// the TPD heartbeat and collapsed reward uptime for days.
func TestResolveUptimeTargets(t *testing.T) {
	tests := []struct {
		name          string
		ut            *visorconfig.UptimeTracker
		discovery     string
		discoveryDmsg string
		wantUT        string
		wantTPD       string
	}{
		{
			name:      "uptime_tracker nil still targets the TPD heartbeat (the regression)",
			ut:        nil,
			discovery: "dmsg://tpd:80",
			wantUT:    "",
			wantTPD:   "dmsg://tpd:80",
		},
		{
			name:          "TPD falls back to discovery_dmsg when discovery is empty",
			ut:            nil,
			discoveryDmsg: "dmsg://tpd:80",
			wantUT:        "",
			wantTPD:       "dmsg://tpd:80",
		},
		{
			name:      "standalone tracker Addr preferred, TPD still independent",
			ut:        &visorconfig.UptimeTracker{Addr: "http://ut", AddrDmsg: "dmsg://ut:80"},
			discovery: "dmsg://tpd:80",
			wantUT:    "http://ut",
			wantTPD:   "dmsg://tpd:80",
		},
		{
			name:    "standalone tracker AddrDmsg used when Addr empty",
			ut:      &visorconfig.UptimeTracker{AddrDmsg: "dmsg://ut:80"},
			wantUT:  "dmsg://ut:80",
			wantTPD: "",
		},
		{
			name:    "both unset yields nothing (caller skips the loop)",
			ut:      nil,
			wantUT:  "",
			wantTPD: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotUT, gotTPD := resolveUptimeTargets(tt.ut, tt.discovery, tt.discoveryDmsg)
			require.Equal(t, tt.wantUT, gotUT, "standalone uptime-tracker URL")
			require.Equal(t, tt.wantTPD, gotTPD, "TPD heartbeat URL")
		})
	}
}
