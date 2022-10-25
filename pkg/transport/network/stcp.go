// Package network pkg/transport/network/stcp.go
package network

import (
	"context"
	"errors"
	"io"
	"net"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport/network/stcp"
)

// STCPConfig defines config for Skywire-TCP network.
type STCPConfig struct {
	PKTable          map[cipher.PubKey]string `json:"pk_table"`
	ListeningAddress string                   `json:"listening_address"`
}

type stcpClient struct {
	*genericClient
	table stcp.PKTable
}

func newStcp(generic *genericClient, table stcp.PKTable) Client {
	client := &stcpClient{genericClient: generic, table: table}
	client.netType = STCP
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
	c.eb.SendTCPDial(context.Background(), string(STCP), addr)
	dialer := net.Dialer{}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}

	c.log.Debugf("Dialed %v:%v@%v", rPK, rPort, conn.RemoteAddr())
	return c.initTransport(ctx, conn, rPK, rPort)
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
