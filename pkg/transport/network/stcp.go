package network

import (
	"context"
	"errors"
	"io"
	"net"

	"github.com/skycoin/dmsg/cipher"
	"github.com/skycoin/skywire/pkg/transport/network/stcp"
)

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

func (c *stcpClient) Dial(ctx context.Context, rPK cipher.PubKey, rPort uint16) (*Conn, error) {
	if c.isClosed() {
		return nil, io.ErrClosedPipe
	}

	c.log.Infof("Dialing PK %v", rPK)

	var conn net.Conn
	addr, ok := c.table.Addr(rPK)
	if !ok {
		return nil, ErrStcpEntryNotFound
	}
	c.eb.SendTCPDial(context.Background(), string(STCP), addr)
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		return nil, err
	}

	c.log.Infof("Dialed %v:%v@%v", rPK, rPort, conn.RemoteAddr())
	return c.initConnection(ctx, conn, c.lPK, rPK, rPort)
}

// Serve starts accepting all incoming connections (i.e. connections to all skywire ports)
// Connections that successfuly perform handshakes will be delivered to a listener
// bound to a specific skywire port
func (c *stcpClient) Serve() error {
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
	c.acceptConnections(lis)
}
