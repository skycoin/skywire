// Package network pkg/transport/network/sudph.go
package network

import (
	"context"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/AudriusButkevicius/pfilter"
	"github.com/skycoin/dmsg/pkg/dmsg"
	"github.com/xtaci/kcp-go"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/internal/packetfilter"
	"github.com/skycoin/skywire/pkg/transport/network/addrresolver"
	"github.com/skycoin/skywire/pkg/transport/network/handshake"
)

const (
	// holePunchMessage is sent in a dummy UDP packet that is sent by both parties to establish UDP hole punching.
	holePunchMessage = "holepunch"
	// dialConnPriority and visorsConnPriority are used to set an order how connection filters apply.
	dialConnPriority   = 2
	visorsConnPriority = 3
	dialTimeout        = 30 * time.Second
)

type sudphClient struct {
	*resolvedClient
	filter *pfilter.PacketFilter
}

func newSudph(resolved *resolvedClient) Client {
	client := &sudphClient{resolvedClient: resolved}
	client.netType = SUDPH
	return client
}

// Start implements Client interface
func (c *sudphClient) Start() error {
	if c.connListener != nil {
		return ErrAlreadyListening
	}
	go c.serve()
	return nil
}

func (c *sudphClient) serve() {
	lis, err := c.listen()
	if err != nil {
		c.log.Errorf("Failed to listen on random port: %v", err)
		return
	}
	c.acceptTransports(lis)
}

// listen
func (c *sudphClient) listen() (net.Listener, error) {
	packetListener, err := net.ListenPacket("udp", "")
	if err != nil {
		return nil, err
	}
	c.filter = pfilter.NewPacketFilter(packetListener)
	sudphVisorsConn := c.filter.NewConn(visorsConnPriority, nil)
	c.filter.Start()
	c.log.Debug("Binding")
	addrCh, err := c.ar.BindSUDPH(c.filter, c.makeBindHandshake())
	if err != nil {
		return nil, err
	}

	_, localPort, err := net.SplitHostPort(packetListener.LocalAddr().String())
	if err != nil {
		return nil, err
	}

	c.log.Debugf("Successfully bound sudph to port %s", localPort)

	go c.acceptAddresses(sudphVisorsConn, addrCh)
	return kcp.ServeConn(nil, 0, 0, sudphVisorsConn)
}

// make a handshake function that is compatible with address resolver interface
func (c *sudphClient) makeBindHandshake() func(in net.Conn) (net.Conn, error) {
	emptyAddr := dmsg.Addr{PK: cipher.PubKey{}, Port: 0}
	hs := handshake.InitiatorHandshake(c.SK(), dmsg.Addr{PK: c.PK(), Port: 0}, emptyAddr)
	return func(in net.Conn) (net.Conn, error) {
		return doHandshake(in, hs, SUDPH, c.log)
	}
}

// acceptAddresses will read visor addresses from addrCh and send holepunch
// packets to them
// Basically each address coming from addrCh is a dial request from some remote
// visor to us. Dialing visor contacts address resolver and gives the address to
// it, address resolver in turn sends us this address.
func (c *sudphClient) acceptAddresses(conn net.PacketConn, addrCh <-chan addrresolver.RemoteVisor) {
	for addr := range addrCh {
		udpAddr, err := net.ResolveUDPAddr("udp", addr.Addr)
		if err != nil {
			c.log.WithError(err).Errorf("Failed to resolve UDP address %q", addr)
			continue
		}

		c.log.Debugf("Sending hole punch packet to %v", addr)
		if _, err := conn.WriteTo([]byte(holePunchMessage), udpAddr); err != nil {
			c.log.WithError(err).Errorf("Failed to send hole punch packet to %v", udpAddr)
			continue
		}
		c.log.Debugf("Sent hole punch packet to %v", addr)
	}
}

// Dial implements interface
func (c *sudphClient) Dial(ctx context.Context, rPK cipher.PubKey, rPort uint16) (Transport, error) {
	if c.isClosed() {
		return nil, io.ErrClosedPipe
	}
	// this will lookup visor address in address resolver and then dial that address
	conn, err := c.dialVisor(ctx, rPK, c.dialWithTimeout)
	if err != nil {
		return nil, err
	}

	return c.initTransport(ctx, conn, rPK, rPort)
}

func (c *sudphClient) dialWithTimeout(ctx context.Context, addr string) (net.Conn, error) {
	timedCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	c.log.Debugf("Dialing %v", addr)

	for {
		select {
		case <-timedCtx.Done():
			return nil, timedCtx.Err()
		default:
			conn, err := c.dial(addr)
			if err == nil {
				c.log.Debugf("Dialed %v", addr)
				return conn, nil
			}
			c.log.WithError(err).
				Warnf("Failed to dial %v, trying again: %v", addr, err)
		}
	}
}

// dial will send holepunch packet to the remote addr over UDP, and
// return the connection
func (c *sudphClient) dial(remoteAddr string) (net.Conn, error) {
	rAddr, err := net.ResolveUDPAddr("udp", remoteAddr)
	if err != nil {
		return nil, fmt.Errorf("net.ResolveUDPAddr (remote): %w", err)
	}

	dialConn := c.filter.NewConn(dialConnPriority, packetfilter.NewKCPConversationFilter(c.mLog))

	if _, err := dialConn.WriteTo([]byte(holePunchMessage), rAddr); err != nil {
		return nil, fmt.Errorf("dialConn.WriteTo: %w", err)
	}

	kcpConn, err := kcp.NewConn(remoteAddr, nil, 0, 0, dialConn)
	if err != nil {
		return nil, err
	}

	return kcpConn, nil
}
