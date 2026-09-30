package visor

import (
	"testing"
	"time"

	"github.com/ccding/go-stun/stun"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/deployment/ar/arfeed"
	tptypes "github.com/skycoin/skywire/pkg/transport/types"
)

func TestNatClass(t *testing.T) {
	require.Equal(t, arfeed.NATOpen, natClass(stun.NATNone))
	require.Equal(t, arfeed.NATPortRestricted, natClass(stun.NATPortRestricted))
	require.Equal(t, arfeed.NATSymmetric, natClass(stun.NATSymmetric))
	require.Equal(t, arfeed.NATUDPFirewall, natClass(stun.NATSymmetricUDPFirewall))
	// Unreachable STUN servers are not evidence that UDP is blocked.
	require.Equal(t, "", natClass(stun.NATBlocked))
	require.Equal(t, "", natClass(stun.NATError))
}

func TestReachTiersAndPeers(t *testing.T) {
	pk := func() cipher.PubKey { p, _ := cipher.GenerateKeyPair(); return p }
	conf, capa, old, stale, unknown := pk(), pk(), pk(), pk(), pk()
	now := time.Now()
	reach := map[cipher.PubKey]*arfeed.Reach{
		conf: {ReachDecl: arfeed.ReachDecl{NAT: arfeed.NATPortRestricted,
			Inbound: map[string]int64{arfeed.TypeSUDPH: now.Unix()}}, Bound: []string{arfeed.TypeSUDPH}, Live: true},
		capa:  {ReachDecl: arfeed.ReachDecl{NAT: arfeed.NATFullCone}, Bound: []string{arfeed.TypeSUDPH}, Live: true},
		old:   {Bound: []string{arfeed.TypeSUDPH}, Live: true},
		stale: {ReachDecl: arfeed.ReachDecl{NAT: arfeed.NATFullCone}, Bound: []string{arfeed.TypeSUDPH}},
	}

	peers := reachPeers(reach, arfeed.TypeSUDPH)
	require.Len(t, peers, 4, "every sudph binding is a candidate; the verdict sorts them")

	c, k, u, x := reachTiers(reach, []cipher.PubKey{conf, capa, old, stale, unknown}, tptypes.SUDPH, arfeed.NATFullCone, now)
	require.Equal(t, []cipher.PubKey{conf}, c)
	require.Equal(t, []cipher.PubKey{capa}, k)
	require.ElementsMatch(t, []cipher.PubKey{old, unknown}, u)
	require.Equal(t, []cipher.PubKey{stale}, x, "no live UDP control connection at the AR")

	// WS is judged by the stcpr probe, since it rides the stcpr port.
	pub := pk()
	reach[pub] = &arfeed.Reach{Bound: []string{arfeed.TypeSTCPR}, Closed: []string{arfeed.TypeSTCPR}}
	_, _, _, x = reachTiers(reach, []cipher.PubKey{pub}, tptypes.WS, "", now)
	require.Equal(t, []cipher.PubKey{pub}, x)
}
