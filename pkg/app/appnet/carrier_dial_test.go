// Package appnet pkg/app/appnet/carrier_dial_test.go c3-app-net
package appnet

import (
	"context"
	"github.com/skycoin/skywire/pkg/router"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCarrierDialContext(t *testing.T) {
	require.False(t, IsCarrierDial(context.Background()), "an ordinary dial is not a carrier dial")
	require.False(t, IsCarrierDial(nil), "a nil context must not panic") //nolint:staticcheck // SA1012: nil-safety is the assertion
	require.True(t, IsCarrierDial(WithCarrierDial(context.Background())))

	// The marker survives derivation, which is how it reaches the networker:
	// the visor wraps the dial ctx and then applies a timeout to it.
	ctx, cancel := context.WithCancel(WithCarrierDial(context.Background()))
	defer cancel()
	require.True(t, IsCarrierDial(ctx))
}

// min_hops is a property of TRAFFIC — it keeps an intermediate from learning
// the true source and destination. A carrier dial's destination IS the peer
// that will carry the session, which terminates it and therefore knows whose
// it is, so hops hide nothing from the only party positioned to learn
// anything. What they cost is the bootstrap: an ineligible carrier dial falls
// through to route setup, reached over the dmsg it is replacing.
//
// A per-dial value cannot express the exemption — EffectiveMinHops takes the
// MAX of the global floor and any per-dial number, so a lower one is ignored.
// This pins that the carrier flag bypasses the test outright, and that it does
// NOT override an explicit per-dial mux request.
func TestCarrierDialExemptFromMinHops(t *testing.T) {
	// stubRouter reports 1; wrap it so this one reports a privacy-configured 3.
	r := &SkywireNetworker{r: minHopsThree{}}

	require.False(t, r.directShortcutEligible(&router.DialOptions{}, false),
		"min_hops=3 keeps an ordinary dial out of the 0-hop shortcut")
	require.True(t, r.directShortcutEligible(&router.DialOptions{}, true),
		"a carrier dial takes the shortcut despite min_hops=3")
	require.False(t, r.directShortcutEligible(&router.DialOptions{MuxRoutes: 2}, true),
		"an explicit per-dial mux still forms a route group, carrier or not")
}

// minHopsThree is stubRouter with the one answer this test cares about
// changed: a visor configured for sender privacy.
type minHopsThree struct{ stubRouter }

func (minHopsThree) EffectiveMinHops(*router.DialOptions) uint16 { return 3 }
