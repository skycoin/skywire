// Package network pkg/transport/network/stcpr.go
package network

import (
	"context"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

const (
	// stcprReRegisterInterval is the interval for re-registering STCPR with address resolver
	// to keep the entry alive before it expires. Should be less than the server's entry timeout.
	stcprReRegisterInterval = 90 * time.Second
	// stcprBindRetryDelay is the initial delay between bind retries (doubles each attempt, caps at 60s)
	stcprBindRetryDelay    = 5 * time.Second
	stcprBindMaxRetryDelay = 60 * time.Second
)

type stcprClient struct {
	*resolvedClient
	port int
}

func newStcpr(resolved *resolvedClient, port int) Client {
	client := &stcprClient{resolvedClient: resolved, port: port}
	client.netType = types.STCPR
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
	c.eb.SendTCPDial(context.Background(), string(types.STCPR), addr)
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
	c.log.Debug("Binding")
	delay := stcprBindRetryDelay
	for attempt := 1; ; attempt++ {
		if err := c.ar.BindSTCPR(context.Background(), port); err == nil {
			c.log.Debugf("Successfully bound stcpr to port %s", port)
			break
		} else {
			c.log.WithError(err).Warnf("Failed to bind STCPR (attempt %d), retrying in %v", attempt, delay)
		}
		if c.isClosed() {
			return
		}
		time.Sleep(delay)
		delay *= 2
		if delay > stcprBindMaxRetryDelay {
			delay = stcprBindMaxRetryDelay
		}
	}

	// Start re-registration loop to keep entry alive in address resolver
	go c.reRegisterLoop(port)

	c.acceptTransports(lis)
}

// reRegisterLoop periodically re-registers with address resolver to keep the entry alive.
// The AR server sets a 2-minute TTL on re-registered entries, so missing a single
// re-registration makes the visor invisible. On failure, retry with backoff rather
// than waiting the full interval.
func (c *stcprClient) reRegisterLoop(port string) {
	ticker := time.NewTicker(stcprReRegisterInterval)
	defer ticker.Stop()

	for {
		select {
		case <-c.done:
			c.log.Debug("Stopping STCPR re-registration loop")
			return
		case <-ticker.C:
			c.log.Debug("Re-registering STCPR with address resolver")
			if err := c.ar.BindSTCPR(context.Background(), port); err != nil {
				c.log.WithError(err).Warn("Failed to re-register STCPR, retrying...")
				// Retry with backoff — a missed re-registration means a 2min blackout
				delay := stcprBindRetryDelay
				for attempt := 1; attempt <= 3; attempt++ {
					if c.isClosed() {
						return
					}
					time.Sleep(delay)
					if err := c.ar.BindSTCPR(context.Background(), port); err == nil {
						c.log.Debugf("Successfully re-registered STCPR after %d retries", attempt)
						break
					}
					c.log.WithError(err).Warnf("STCPR re-registration retry %d/3 failed", attempt)
					delay *= 2
					if delay > stcprBindMaxRetryDelay {
						delay = stcprBindMaxRetryDelay
					}
				}
			} else {
				c.log.Debug("Successfully re-registered STCPR")
			}
		}
	}
}
