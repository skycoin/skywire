package node

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/node/transport"
)

// String is computed once: debug lines format it on every message.
func TestConnStringCached(t *testing.T) {
	sPK, sSK := cipher.GenerateKeyPair()
	cPK, cSK := cipher.GenerateKeyPair()
	srv := transport.NewTCPFactory(sPK, sSK)
	accepted := make(chan *transport.Connection, 1)
	srv.AcceptedCallback = func(c *transport.Connection) { accepted <- c }
	require.NoError(t, srv.Listen("127.0.0.1:0"))
	defer srv.Close() //nolint:errcheck
	fc, err := transport.NewTCPFactory(cPK, cSK).Connect(srv.ListenerAddress())
	require.NoError(t, err)
	defer fc.Close() //nolint:errcheck

	c := &Conn{Connection: fc}
	want := "↑ tcp://" + fc.GetRemoteAddr().String()
	require.Equal(t, want, c.String())
	// The second call returns the stored string. Counting allocations instead
	// would also count the live connection's goroutines.
	cached := c.str.Load()
	require.NotNil(t, cached)
	require.Equal(t, want, c.String())
	require.Same(t, cached, c.str.Load())
}
