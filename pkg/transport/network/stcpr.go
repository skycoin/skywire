// Package network pkg/transport/network/stcpr.go c2-net-transport
package network

import (
	"context"
	"io"
	"net"

	"github.com/skycoin/skywire/pkg/cipher"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

type stcprClient struct {
	*resolvedClient
	port int
	// sharedListener, when set, is the TCP listener stcpr accepts over instead of
	// binding its own — the raw (non-HTTP) virtual listener of a unified
	// transport_port socket (see tcpDemux). nil = bind a dedicated listener.
	sharedListener net.Listener
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

	tp, err := c.initTransport(ctx, conn, rPK, rPort)
	if err != nil {
		conn.Close() //nolint:errcheck,gosec
		return nil, err
	}
	return tp, nil
}

func (c *stcprClient) dial(ctx context.Context, addr string) (net.Conn, error) {
	c.announceDial(types.STCPR, addr)
	conn, err := c.socks().DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	// TCP_NODELAY for interactive traffic. Without this, Nagle's
	// algorithm batches small writes (e.g., per-keystroke pty bytes
	// from the hypervisor UI's skypty terminal) with 40–200ms of
	// delay each, manifesting as per-keystroke lag for operators
	// using the web pty. Skywire's interactive paths (skypty, ssh
	// app, remote shells, RPC calls with small request payloads)
	// all benefit from disabling Nagle; bulk transfers don't lose
	// anything meaningful (modern TCP segmentation handles
	// throughput-side packaging at the kernel level).
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		_ = tcpConn.SetNoDelay(true) //nolint:errcheck
		configureTCPLiveness(tcpConn)
		configureTCPQueue(tcpConn)
	}
	return conn, nil
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
	if c.sharedListener != nil {
		// Unified transport port: accept over the shared TCP listener's raw
		// (non-HTTP) virtual listener. The master listener lifecycle stays with the
		// demux. The AR is still bound to the actual (shared) port below.
		lis = c.sharedListener
	} else {
		var err error
		lis, err = listenWithFallback(c.genericClient, &c.port, func(addr string) (net.Listener, error) {
			return c.socks().Listen("tcp", addr)
		})
		if err != nil {
			return
		}
	}

	localAddr := lis.Addr().String()
	_, port, err := net.SplitHostPort(localAddr)
	if err != nil {
		c.log.Errorf("Failed to extract port from addr %v: %v", localAddr, err)
		return
	}
	ctx, cancel := c.closeContext()
	bind := func() error { return c.ar.BindSTCPR(ctx, port) }
	if !c.bindUntilBound("STCPR", bind) {
		cancel()
		return
	}
	go func() {
		defer cancel()
		c.reRegister("STCPR", bind, nil)
	}()

	c.acceptTransports(lis)
}
