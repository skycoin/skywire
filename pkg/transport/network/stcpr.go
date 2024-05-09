// Package network pkg/transport/network/stcpr.go
package network

import (
	"context"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/netutil"
)

type stcprClient struct {
	*resolvedClient
	port int
}

func newStcpr(resolved *resolvedClient, port int) Client {
	client := &stcprClient{resolvedClient: resolved, port: port}
	client.netType = STCPR
	return client
}

// Dial implements interface
func (c *stcprClient) Dial(ctx context.Context, rPK cipher.PubKey, rPort uint16) (Transport, error) {
	if c.isClosed() {
		return nil, io.ErrClosedPipe
	}
	c.log.Debugf("Dialing PK %v", rPK)
	conn, err := c.dialVisor(ctx, rPK, c.dial)
	if err != nil {
		return nil, err
	}

	return c.initTransport(ctx, conn, rPK, rPort)
}

func (c *stcprClient) dial(ctx context.Context, addr string) (net.Conn, error) {
	c.eb.SendTCPDial(context.Background(), string(STCPR), addr)
	dialer := net.Dialer{}
	return dialer.DialContext(ctx, "tcp", addr)
}

// Start implements Client interface
func (c *stcprClient) Start() error {
	if c.connListener != nil {
		return ErrAlreadyListening
	}
	go c.serve()
	return nil
}

func (c *stcprClient) serve() {
	var lis net.Listener
	var err error
	var confPort string
	if c.port != 0 {
		confPort = fmt.Sprintf(":%d", c.port)
	}
	for {
		lis, err = net.Listen("tcp", confPort)
		if err != nil {
			c.log.WithError(err).Warnf("Failed to listen on port: %d", c.port)
			c.port++
			confPort = fmt.Sprintf(":%d", c.port)
			c.log.Warnf("Trying port %d", c.port)
			continue
		}
		break
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
		c.log.Debug("Not binding STCPR: no public IP address found")
		return
	}
	c.log.Debug("Binding")
	if err := c.ar.BindSTCPR(context.Background(), port); err != nil {
		c.log.Errorf("Failed to bind STCPR: %v", err)
		return
	}
	// simple heartbeat process for stcpr
	go func() {
		for {
			time.Sleep(5 * time.Minute)
			if err := c.ar.BindSTCPR(context.Background(), port); err != nil {
				c.log.Errorf("Failed to bind STCPR: %v", err)
				continue
			}
			c.log.Infof("STCPR rebinded in heartbeating process")
		}
	}()
	c.log.Debugf("Successfully bound stcpr to port %s", port)
	c.acceptTransports(lis)
}
