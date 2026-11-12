package transport

import (
	"context"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport/network"
	"github.com/skycoin/skywire/pkg/transport/network/addrresolver"
)

// TestEnsureLocalTransportLeavesOtherPeersAlone: a peer the address resolver
// does not see behind this visor's public IP gets ErrNotLocal and no
// transport, and it is not looked up again at once.
func TestEnsureLocalTransportLeavesOtherPeersAlone(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	ar := addrresolver.NewMockAPIClient(t)
	ar.On("Resolve", mock.Anything, "stcpr", mock.Anything).
		Return(addrresolver.VisorData{RemoteAddr: "203.0.113.7:7777"}, nil).Once()
	ar.On("LocalPublicIP").Return("198.51.100.2")
	tm, err := NewManager(nil, ar, nil, &ManagerConfig{PubKey: pk, SecKey: sk, DiscoveryClient: NewDiscoveryMock()}, network.ClientFactory{})
	require.NoError(t, err)

	remote, _ := cipher.GenerateKeyPair()
	require.ErrorIs(t, tm.EnsureLocalTransport(context.Background(), remote), ErrNotLocal)
	require.ErrorIs(t, tm.EnsureLocalTransport(context.Background(), remote), ErrNotLocal)
	require.Zero(t, tm.TransportCount())
}

func TestSameIP(t *testing.T) {
	require.True(t, sameIP("198.51.100.2:7777", "198.51.100.2"))
	require.True(t, sameIP("198.51.100.2", "198.51.100.2"))
	require.False(t, sameIP("203.0.113.7:7777", "198.51.100.2"))
	require.False(t, sameIP("", ""), "an unknown public IP is no match")
}
