package visor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/deployment/ar/arfeed"
	tptypes "github.com/skycoin/skywire/pkg/transport/types"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

func TestPlanDmsgServerDials(t *testing.T) {
	pk := func() cipher.PubKey { p, _ := cipher.GenerateKeyPair(); return p }
	self, open, quicOnly, closed, reached, notVisor, oldVisor := pk(), pk(), pk(), pk(), pk(), pk(), pk()
	now := time.Now()
	local := []tptypes.Type{tptypes.STCPR, tptypes.QUIC, tptypes.SUDPH}
	reach := map[cipher.PubKey]*arfeed.Reach{
		open: {Bound: []string{arfeed.TypeSTCPR, arfeed.TypeQUIC}, Open: []string{arfeed.TypeSTCPR}},
		// stcpr is known to fail, squicr was probed open.
		quicOnly: {Bound: []string{arfeed.TypeSTCPR, arfeed.TypeQUIC}, Closed: []string{arfeed.TypeSTCPR},
			Open: []string{arfeed.TypeQUIC}},
		closed:  {Bound: []string{arfeed.TypeSTCPR}, Closed: []string{arfeed.TypeSTCPR}},
		reached: {Bound: []string{arfeed.TypeSTCPR}},
		// A record that states nothing: every type is untested.
		oldVisor: {},
	}
	servers := []cipher.PubKey{self, open, quicOnly, closed, reached, notVisor, oldVisor, open}
	has := map[cipher.PubKey]bool{reached: true}

	got := map[cipher.PubKey][]tptypes.Type{}
	for _, d := range planDmsgServerDials(self, servers, has, reach, local, "", now) {
		_, dup := got[d.pk]
		require.False(t, dup, "one dial per server")
		got[d.pk] = d.types
	}
	require.Equal(t, map[cipher.PubKey][]tptypes.Type{
		open:     {tptypes.STCPR, tptypes.QUIC},
		quicOnly: {tptypes.QUIC},
		oldVisor: {tptypes.STCPR, tptypes.QUIC},
	}, got, "self, reached, known failures, sudph without a binding and non-visors are skipped")

	// Before the reach feed syncs every server is tried in preference order.
	plan := planDmsgServerDials(self, []cipher.PubKey{notVisor}, nil, nil, local, "", now)
	require.Equal(t, []dmsgServerDial{{pk: notVisor, types: local}}, plan)

	// Only locally dialable types are planned.
	plan = planDmsgServerDials(self, []cipher.PubKey{open}, nil, reach, []tptypes.Type{tptypes.QUIC}, "", now)
	require.Equal(t, []dmsgServerDial{{pk: open, types: []tptypes.Type{tptypes.QUIC}}}, plan)
	require.Empty(t, planDmsgServerDials(self, []cipher.PubKey{open}, nil, reach, nil, "", now))
}

func TestDmsgServerTransportsKnob(t *testing.T) {
	off, on := false, true
	require.True(t, (&visorconfig.V1{}).DmsgServerTransportsOn(), "no transport section")
	require.True(t, (&visorconfig.V1{Transport: &visorconfig.Transport{}}).DmsgServerTransportsOn(), "unset is on")
	require.True(t, (&visorconfig.V1{Transport: &visorconfig.Transport{DmsgServerTransports: &on}}).DmsgServerTransportsOn())
	require.False(t, (&visorconfig.V1{Transport: &visorconfig.Transport{DmsgServerTransports: &off}}).DmsgServerTransportsOn())
}
