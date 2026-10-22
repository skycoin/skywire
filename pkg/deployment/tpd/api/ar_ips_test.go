package api

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/deployment/ar/arfeed"
	"github.com/skycoin/skywire/pkg/transport/network/addrresolver"
)

func TestBindingIPs(t *testing.T) {
	b := &arfeed.PeerBindings{
		STCPR: &addrresolver.VisorData{RemoteAddr: "129.152.28.221"},
		QUIC:  &addrresolver.VisorData{RemoteAddr: "129.152.28.221:30178", RemoteAddrV6: "2001:db8::7"},
		WT:    &addrresolver.VisorData{RemoteAddr: "192.168.1.20"},
	}
	require.Equal(t, []string{"129.152.28.221", "2001:db8::7"}, bindingIPs(b), "deduplicated, private dropped")
}

// With no feed set the day settles as before, judged by the visors' marks.
func TestSamePublicIPWithoutFeed(t *testing.T) {
	api := &API{}
	judge, ready := api.samePublicIP()
	require.True(t, ready)
	require.Nil(t, judge)
}
