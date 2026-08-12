// Package router pkg/router/router_mux_seed_test.go — Config.MuxRoutes
// seeds the router's runtime mux value at construction.
//
// The runtime value is what GetRouterSettings reports, and the settings
// surface is a read-modify-write for its callers (the PUT applies every
// field). Before the seed, a visor whose config carried mux_routes booted
// with the router reporting 0, so the first settings write-back — the
// mobile app saving its min_hops control — echoed that 0 into
// SetMuxRoutes and silently turned the configured default off. The
// networker applied the config value to dials all along; it was only the
// reported value that lied, which is exactly the kind of half-truth a
// read-modify-write caller amplifies into behavior.
package router

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
)

func TestNew_SeedsMuxRoutesFromConfig(t *testing.T) {
	for _, mux := range []int{0, 2} {
		conf := &Config{
			Logger:       logging.MustGetLogger("router_mux_seed_test"),
			MasterLogger: logging.NewMasterLogger(),
			MuxRoutes:    mux,
			// Pre-reserved listener: New must not try to open a real
			// dmsg listener for this construction-only test.
			AwaitSetupListener: &dmsg.Listener{},
		}
		r, err := New(nil, conf, nil)
		require.NoError(t, err)
		require.Equal(t, mux, r.GetMuxRoutes())
	}
}
