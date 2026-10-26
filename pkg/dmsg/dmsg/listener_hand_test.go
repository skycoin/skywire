package dmsg

import (
	"net"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
)

// TestListenerHand: a connection handed to a listener comes out of Accept with
// the listener's address and the caller's, and the process-wide lookup finds
// the listener until it closes.
func TestListenerHand(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	c := NewClient(pk, sk, disc.NewMock(0), nil)
	t.Cleanup(func() { _ = c.Close() }) //nolint:errcheck

	lis, err := c.Listen(80)
	require.NoError(t, err)
	got, ok := LocalListener(Addr{PK: pk, Port: 80})
	require.True(t, ok)
	require.Same(t, lis, got)

	caller, _ := cipher.GenerateKeyPair()
	a, b := net.Pipe()
	t.Cleanup(func() { _ = b.Close() }) //nolint:errcheck
	require.NoError(t, lis.Hand(a, Addr{PK: caller}))

	conn, err := lis.Accept()
	require.NoError(t, err)
	require.Equal(t, Addr{PK: caller}, conn.RemoteAddr())
	require.Equal(t, Addr{PK: pk, Port: 80}, conn.LocalAddr())
	go func() { _, _ = b.Write([]byte("hi")) }() //nolint:errcheck
	buf := make([]byte, 2)
	_, err = conn.Read(buf)
	require.NoError(t, err)
	require.Equal(t, "hi", string(buf))

	require.NoError(t, lis.Close())
	_, ok = LocalListener(Addr{PK: pk, Port: 80})
	require.False(t, ok, "a closed listener is no longer found")
	require.ErrorIs(t, lis.Hand(a, Addr{PK: caller}), ErrEntityClosed)
}
