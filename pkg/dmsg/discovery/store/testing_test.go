package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
)

// TestMockStoreReadsBackAnEntryTheAPIChanged: the API verifies a server
// entry and then sets its ServerType. The server must still read its entry
// back, or it re-registers from scratch every cycle and never advertises
// its WebSocket, QUIC or WebTransport address.
func TestMockStoreReadsBackAnEntryTheAPIChanged(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	e := disc.NewServerEntry(pk, 0, "127.0.0.1:8080", 10)
	require.NoError(t, e.Sign(sk))
	e.Server.ServerType = "official"

	ms := NewMock()
	require.NoError(t, ms.SetEntry(context.Background(), e, 0))
	got, err := ms.Entry(context.Background(), pk)
	require.NoError(t, err)
	require.Equal(t, "official", got.Server.ServerType)
	require.Equal(t, "127.0.0.1:8080", got.Server.Address)
}
