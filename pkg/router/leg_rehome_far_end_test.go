// leg_rehome_far_end_test.go: farEndPK must name the PEER whichever way round
// the group's descriptor was built. The setup node hands each edge the
// descriptor that points AT that edge (setupnode.go initEdge/respEdge), so a
// dialed group carries (peer -> local) and Dst is this visor — naming Dst on
// the initiator printed the local visor's own key as the far end of every
// re-home and split that went unanswered.
package router

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/routing"
)

func TestFarEndPKNamesThePeerInEitherOrientation(t *testing.T) {
	localPK, _ := cipher.GenerateKeyPair()
	peerPK, _ := cipher.GenerateKeyPair()

	tests := []struct {
		name      string
		desc      routing.RouteDescriptor
		localPK   cipher.PubKey
		initiator bool
		want      cipher.PubKey
	}{
		{
			// What a DIALED group actually carries: initEdge is the reversed
			// descriptor, so Src is the peer and Dst is this visor.
			name:      "dialed group, descriptor points at this visor",
			desc:      routing.NewRouteDescriptor(peerPK, localPK, 3, 49153),
			localPK:   localPK,
			initiator: true,
			want:      peerPK,
		},
		{
			name:      "accepted group, descriptor points at this visor",
			desc:      routing.NewRouteDescriptor(peerPK, localPK, 49153, 3),
			localPK:   localPK,
			initiator: false,
			want:      peerPK,
		},
		{
			// The other orientation, for a descriptor built by hand or by a
			// future setup path: the local key still decides.
			name:      "descriptor points at the peer, initiator",
			desc:      routing.NewRouteDescriptor(localPK, peerPK, 49153, 3),
			localPK:   localPK,
			initiator: true,
			want:      peerPK,
		},
		{
			name:      "descriptor points at the peer, acceptor",
			desc:      routing.NewRouteDescriptor(localPK, peerPK, 3, 49153),
			localPK:   localPK,
			initiator: false,
			want:      peerPK,
		},
		{
			// No noise config (the emulated testbed builds groups directly):
			// fall back to the descriptor's Src, which is the far end on every
			// group the setup node built.
			name:      "no local key known falls back to the descriptor source",
			desc:      routing.NewRouteDescriptor(peerPK, localPK, 3, 49153),
			localPK:   cipher.PubKey{},
			initiator: true,
			want:      peerPK,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rg := &RouteGroup{desc: tc.desc, localPK: tc.localPK, initiator: tc.initiator}
			got := rg.farEndPK()
			require.Equal(t, tc.want.String(), got.String())
			require.NotEqual(t, localPK.String(), got.String(),
				"farEndPK must never name this visor")
		})
	}
}
