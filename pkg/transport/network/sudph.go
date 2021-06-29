package network

import (
	"context"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/AudriusButkevicius/pfilter"
	"github.com/skycoin/dmsg/cipher"
	"github.com/skycoin/skywire/internal/packetfilter"
	"github.com/skycoin/skywire/pkg/snet/arclient"
	"github.com/xtaci/kcp-go"
)

// holePunchMessage is sent in a dummy UDP packet that is sent by both parties to establish UDP hole punching.
const (
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

func newSudph(resolved *resolvedClient, addressResolver arclient.APIClient) Client {
	client := &sudphClient{resolvedClient: resolved}
	client.netType = SUDPH
	return client
}

// Serve starts accepting all incoming connections (i.e. connections to all skywire ports)
// Connections that successfuly perform handshakes will be delivered to a listener
// bound to a specific skywire port
func (c *sudphClient) Serve() error {
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
	c.acceptConnections(lis)
}

func (c *sudphClient) listen() (net.Listener, error) {
	packetListener, err := net.ListenPacket("udp", "")
	if err != nil {
		return nil, err
	}
	c.filter = pfilter.NewPacketFilter(packetListener)
	sudphVisorsConn := c.filter.NewConn(visorsConnPriority, nil)
	c.filter.Start()
	c.log.Infof("Binding")
	addrCh, err := c.ar.BindSUDPH(c.filter)
	if err != nil {
		return nil, err
	}
	go c.PICKNAMEFORME(sudphVisorsConn, addrCh)
	return kcp.ServeConn(nil, 0, 0, sudphVisorsConn)
}

// todo: name
func (c *sudphClient) PICKNAMEFORME(conn net.PacketConn, addrCh <-chan arclient.RemoteVisor) {
	for addr := range addrCh {
		udpAddr, err := net.ResolveUDPAddr("udp", addr.Addr)
		if err != nil {
			c.log.WithError(err).Errorf("Failed to resolve UDP address %q", addr)
			continue
		}

		c.log.Infof("Sending hole punch packet to %v", addr)

		if _, err := conn.WriteTo([]byte(holePunchMessage), udpAddr); err != nil {
			c.log.WithError(err).Errorf("Failed to send hole punch packet to %v", udpAddr)
			continue
		}
		c.log.Infof("Sent hole punch packet to %v", addr)
	}
}

func (c *sudphClient) Dial(ctx context.Context, rPK cipher.PubKey, rPort uint16) (*Conn, error) {
	if c.isClosed() {
		return nil, io.ErrClosedPipe
	}

	conn, err := c.dialVisor(ctx, rPK, c.dialWithTimeout)
	if err != nil {
		return nil, err
	}

	return c.initConnection(ctx, conn, c.lPK, rPK, rPort)
}

func (c *sudphClient) dialWithTimeout(ctx context.Context, addr string) (net.Conn, error) {
	timedCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	c.log.Infof("Dialing %v", addr)

	for {
		select {
		case <-timedCtx.Done():
			return nil, timedCtx.Err()
		default:
			conn, err := c.dial(addr)
			if err == nil {
				c.log.Infof("Dialed %v", addr)
				return conn, nil
			}
			c.log.WithError(err).
				Warnf("Failed to dial %v, trying again: %v", addr, err)
		}
	}
}

func (c *sudphClient) dial(remoteAddr string) (net.Conn, error) {
	rAddr, err := net.ResolveUDPAddr("udp", remoteAddr)
	if err != nil {
		return nil, fmt.Errorf("net.ResolveUDPAddr (remote): %w", err)
	}

	dialConn := c.filter.NewConn(dialConnPriority, packetfilter.NewKCPConversationFilter())

	if _, err := dialConn.WriteTo([]byte(holePunchMessage), rAddr); err != nil {
		return nil, fmt.Errorf("dialConn.WriteTo: %w", err)
	}

	kcpConn, err := kcp.NewConn(remoteAddr, nil, 0, 0, dialConn)
	if err != nil {
		return nil, err
	}

	return kcpConn, nil
}
