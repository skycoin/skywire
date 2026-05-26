// Package network pkg/transport/network/stcp.go
package network

import (
	"context"
	"errors"
	"io"
	"net"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport/network/spec"
	"github.com/skycoin/skywire/pkg/transport/network/stcp"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

// STCPConfig is the wire-format type for Skywire-TCP configuration.
// Aliased from pkg/transport/network/spec so existing callers
// writing `network.STCPConfig{...}` keep compiling, while the
// canonical (WASM-clean) definition lives in the spec leaf package.
type STCPConfig = spec.STCPConfig

type stcpClient struct {
	*genericClient
	table stcp.PKTable
}

func newStcp(generic *genericClient, table stcp.PKTable) Client {
	client := &stcpClient{genericClient: generic, table: table}
	client.netType = types.STCP
	return client
}

// ErrStcpEntryNotFound is returned when requested PK is not found in the local
// PK table
var ErrStcpEntryNotFound = errors.New("entry not found in PK table")

// Dial implements Client interface
func (c *stcpClient) Dial(ctx context.Context, rPK cipher.PubKey, rPort uint16) (Transport, error) {
	if c.isClosed() {
		return nil, io.ErrClosedPipe
	}

	c.log.Debugf("Dialing PK %v", rPK)

	var conn net.Conn
	addr, ok := c.table.Addr(rPK)
	if !ok {
		return nil, ErrStcpEntryNotFound
	}
	c.eb.SendTCPDial(context.Background(), string(types.STCP), addr)
	dialer := net.Dialer{}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	// TCP_NODELAY — see stcpr.go's matching comment. Interactive
	// traffic (skypty, ssh, RPC) bottlenecks on Nagle without this.
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		_ = tcpConn.SetNoDelay(true) //nolint:errcheck
	}

	c.log.Debugf("Dialed %v:%v@%v", rPK, rPort, conn.RemoteAddr())
	tp, err := c.initTransport(ctx, conn, rPK, rPort)
	if err != nil {
		conn.Close() //nolint:errcheck,gosec
		return nil, err
	}
	return tp, nil
}

// Start implements Client interface
func (c *stcpClient) Start() error {
	if c.connListener != nil {
		return ErrAlreadyListening
	}
	go c.serve()
	return nil
}

func (c *stcpClient) serve() {
	lis, err := net.Listen("tcp", c.listenAddr)
	if err != nil {
		c.log.Errorf("Failed to listen on %q: %v", c.listenAddr, err)
		return
	}
	c.acceptTransports(lis)
}
