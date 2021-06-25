package network

import (
	"context"
	"fmt"
	"io"
	"net"

	"github.com/skycoin/dmsg/cipher"
	"github.com/skycoin/skywire/pkg/app/appevent"
	"github.com/skycoin/skywire/pkg/snet/arclient"
)

type stcprClient struct {
	*genericClient
	addressResolver arclient.APIClient
}

func newStcpr(generic *genericClient, addressResolver arclient.APIClient) Client {
	client := &stcprClient{genericClient: generic, addressResolver: addressResolver}
	client.netType = STCPR
	return client
}

func (c *stcprClient) Dial(ctx context.Context, rPK cipher.PubKey, rPort uint16) (*Conn, error) {
	if c.isClosed() {
		return nil, io.ErrClosedPipe
	}

	c.log.Infof("Dialing PK %v", rPK)
	visorData, err := c.addressResolver.Resolve(ctx, string(STCPR), rPK)
	if err != nil {
		return nil, fmt.Errorf("resolve PK: %w", err)
	}

	c.log.Infof("Resolved PK %v to visor data %v", rPK, visorData)

	conn, err := c.dialVisor(visorData)
	if err != nil {
		return nil, err
	}

	return c.initConnection(ctx, conn, c.lPK, rPK, rPort)
}

func (c *stcprClient) dialVisor(visorData arclient.VisorData) (net.Conn, error) {
	if visorData.IsLocal {
		for _, host := range visorData.Addresses {
			addr := net.JoinHostPort(host, visorData.Port)
			conn, err := c.dial(addr)
			if err == nil {
				return conn, nil
			}
		}
	}
	addr := visorData.RemoteAddr
	if _, _, err := net.SplitHostPort(addr); err != nil {
		addr = net.JoinHostPort(addr, visorData.Port)
	}
	return c.dial(addr)
}

func (c *stcprClient) dial(addr string) (net.Conn, error) {
	data := appevent.TCPDialData{RemoteNet: string(STCPR), RemoteAddr: addr}
	event := appevent.NewEvent(appevent.TCPDial, data)
	_ = c.eb.Broadcast(context.Background(), event) //nolint:errcheck
	return net.Dial("tcp", addr)
}

// Serve starts accepting all incoming connections (i.e. connections to all skywire ports)
// Connections that successfuly perform handshakes will be delivered to a listener
// bound to a specific skywire port
func (c *stcprClient) Serve() error {
	if c.connListener != nil {
		return ErrAlreadyListening
	}
	go c.serve()
	return nil
}

func (c *stcprClient) serve() {
	lis, err := net.Listen("tcp", c.listenAddr)
	if err != nil {
		c.log.Errorf("Failed to listen on %q: %v", c.listenAddr, err)
		return
	}
	c.acceptConnections(lis)
}
