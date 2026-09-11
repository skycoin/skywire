// Package appnet pkg/app/appnet/direct_mux_precedence_test.go c3-app-net
package appnet

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/router"
)

// applyGlobalMuxDefault mirrors the precedence rule in DialContext: the
// visor-global mux_routes seeds an unset per-dial value, EXCEPT on a --direct
// dial, which asked for the direct leg explicitly.
func applyGlobalMuxDefault(globalMuxRoutes int, opts *router.DialOptions) {
	directDial := opts.EnsureDirectTransport || opts.UseExistingTpOnly
	if globalMuxRoutes >= 1 && opts.MuxRoutes == 0 && !directDial {
		opts.MuxRoutes = globalMuxRoutes
	}
}

// With mux_routes=1 in the visor config, every --direct dial was pushed into
// route-group setup — which reserves route IDs on the destination through the
// setup node. The setup node is only needed to route THROUGH a third party;
// routing TO a peer we already hold a transport to needs no route at all.
func TestGlobalMuxRoutesDoesNotOverrideDirectDial(t *testing.T) {
	const globalMux = 1

	t.Run("plain dial inherits the global default", func(t *testing.T) {
		opts := &router.DialOptions{}
		applyGlobalMuxDefault(globalMux, opts)
		require.Equal(t, globalMux, opts.MuxRoutes,
			"a dial with no preference should still form a route group")
	})

	t.Run("--direct is exempt (EnsureDirectTransport)", func(t *testing.T) {
		opts := &router.DialOptions{EnsureDirectTransport: true}
		applyGlobalMuxDefault(globalMux, opts)
		require.Zero(t, opts.MuxRoutes,
			"MuxRoutes must stay unset so the app-direct shortcut is eligible")
	})

	t.Run("--direct is exempt (UseExistingTpOnly)", func(t *testing.T) {
		opts := &router.DialOptions{UseExistingTpOnly: true}
		applyGlobalMuxDefault(globalMux, opts)
		require.Zero(t, opts.MuxRoutes)
	})

	// #2751: an explicit per-dial mux request is the caller asking for a route
	// group in the same breath, and still wins over the shortcut.
	t.Run("an explicit per-dial --mux still wins, even with --direct", func(t *testing.T) {
		opts := &router.DialOptions{EnsureDirectTransport: true, MuxRoutes: 2}
		applyGlobalMuxDefault(globalMux, opts)
		require.Equal(t, 2, opts.MuxRoutes)
	})

	t.Run("no global default set leaves opts alone", func(t *testing.T) {
		opts := &router.DialOptions{}
		applyGlobalMuxDefault(0, opts)
		require.Zero(t, opts.MuxRoutes)
	})
}
