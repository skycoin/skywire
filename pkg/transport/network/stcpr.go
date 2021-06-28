package network

import (
	"context"
	"fmt"
	"io"
	"net"

	"github.com/skycoin/dmsg/cipher"
	"github.com/skycoin/skywire/pkg/snet/arclient"
	"github.com/skycoin/skywire/pkg/util/netutil"
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

	conn, err := c.dialVisor(ctx, visorData)
	if err != nil {
		return nil, err
	}

	return c.initConnection(ctx, conn, c.lPK, rPK, rPort)
}

func (c *stcprClient) dialVisor(ctx context.Context, visorData arclient.VisorData) (net.Conn, error) {
	if visorData.IsLocal {
		for _, host := range visorData.Addresses {
			addr := net.JoinHostPort(host, visorData.Port)
			conn, err := c.dial(ctx, addr)
			if err == nil {
				return conn, nil
			}
		}
	}
	addr := visorData.RemoteAddr
	if _, _, err := net.SplitHostPort(addr); err != nil {
		addr = net.JoinHostPort(addr, visorData.Port)
	}
	return c.dial(ctx, addr)
}

func (c *stcprClient) dial(ctx context.Context, addr string) (net.Conn, error) {
	c.eb.SendTCPDial(context.Background(), string(STCPR), addr)
	dialer := net.Dialer{}
	return dialer.DialContext(ctx, "tcp", addr)
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
	lis, err := net.Listen("tcp", "")
	if err != nil {
		c.log.Errorf("Failed to listen on random port: %v", "", err)
		return
	}

	localAddr := lis.Addr().String()
	_, port, err := net.SplitHostPort(localAddr)
	if err != nil {
		c.log.Errorf("Failed to extract port from addr %v: %v", err)
		return
	}
	hasPublic, err := netutil.HasPublicIP()
	if err != nil {
		c.log.Errorf("Failed to check for public IP: %v", err)
	}
	if !hasPublic {
		c.log.Infof("Not binding STCPR: no public IP address found")
		return
	}
	c.log.Infof("Binding")
	if err := c.addressResolver.BindSTCPR(context.Background(), port); err != nil {
		c.log.Errorf("Failed to bind STCPR: %v", err)
		return
	}
	c.log.Infof("Successfuly bound stcpr to port %s", port)
	c.acceptConnections(lis)
}
