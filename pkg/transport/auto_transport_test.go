// Package transport pkg/transport/auto_transport_test.go
package transport

import (
	"testing"

	"github.com/stretchr/testify/require"

	types "github.com/skycoin/skywire/pkg/transport/types"
)

// TestPreferredDirectOrder verifies the "auto" transport-creation order used by
// EnsureBestTransport / the route-setup hooks: the global preference order,
// filtered to the DIRECT types this visor can create, with DMSG always excluded
// (it is the last-resort relay handled separately). This is the crux of keeping
// the data plane peer-to-peer instead of over-using dmsg.
func TestPreferredDirectOrder(t *testing.T) {
	tests := []struct {
		name string
		have []types.Type
		want []types.Type
	}{
		{
			name: "browser edge (webrtc/ws/wt + dmsg) → p2p types only, preference order",
			have: []types.Type{types.WT, types.DMSG, types.WEBRTC, types.WS},
			// global preference: … WT > WS > WEBRTC … (direct carriers before the
			// NAT-traversing one) so ordered thus, dmsg dropped.
			want: []types.Type{types.WT, types.WS, types.WEBRTC},
		},
		{
			name: "native visor (all types) → full preference order, dmsg dropped",
			have: []types.Type{types.DMSG, types.WT, types.SUDPH, types.STCPR, types.WEBRTC, types.QUIC, types.STCP, types.WS},
			want: []types.Type{types.STCPR, types.QUIC, types.SUDPH, types.STCP, types.WT, types.WS, types.WEBRTC},
		},
		{
			name: "only dmsg → empty (nothing direct to create)",
			have: []types.Type{types.DMSG},
			want: nil,
		},
		{
			name: "stcpr+dmsg → just stcpr",
			have: []types.Type{types.DMSG, types.STCPR},
			want: []types.Type{types.STCPR},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			have := make(map[types.Type]bool, len(tc.have))
			for _, ty := range tc.have {
				have[ty] = true
			}
			require.Equal(t, tc.want, preferredDirectOrder(have))
		})
	}
}
