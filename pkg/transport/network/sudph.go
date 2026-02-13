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

	"github.com/skycoin/skywire/internal/packetfilter"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport/network/addrresolver"
	"github.com/skycoin/skywire/pkg/transport/network/handshake"
	types "github.com/skycoin/skywire/pkg/transport/types"
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
	filter          *pfilter.PacketFilter
	packetListener  net.PacketConn
	sudphVisorsConn net.PacketConn
	port            int
}

func newSudph(resolved *resolvedClient, port int) Client {
	client := &sudphClient{resolvedClient: resolved, port: port}
	client.netType = types.SUDPH
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
		c.log.Errorf("Failed to listen on port: %v", err)
		return
	}
	c.acceptTransports(lis)
}

// listen
func (c *sudphClient) listen() (net.Listener, error) {
	var packetListener net.PacketConn
	var err error
	var confPort string
	if c.port != 0 {
		confPort = fmt.Sprintf(":%d", c.port)
	}
	for {
		packetListener, err = net.ListenPacket("udp", confPort)
		if err != nil {
			c.log.WithError(err).Warnf("Failed to listen on port: %d", c.port)
			c.port++
			confPort = fmt.Sprintf(":%d", c.port)
			c.log.Warnf("Trying port %d", c.port)
			continue
		}
		break
	}
	c.packetListener = packetListener
	c.filter = pfilter.NewPacketFilter(packetListener)
	c.sudphVisorsConn = c.filter.NewConn(visorsConnPriority, nil)
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

	go c.acceptAddresses(c.sudphVisorsConn, addrCh)
	return kcp.ServeConn(nil, 0, 0, c.sudphVisorsConn)
}

// make a handshake function that is compatible with address resolver interface
func (c *sudphClient) makeBindHandshake() func(in net.Conn) (net.Conn, error) {
	emptyAddr := dmsg.Addr{PK: cipher.PubKey{}, Port: 0}
	hs := handshake.InitiatorHandshake(c.SK(), dmsg.Addr{PK: c.PK(), Port: 0}, emptyAddr)
	return func(in net.Conn) (net.Conn, error) {
		return doHandshake(in, hs, types.SUDPH, c.log)
	}
}

// acceptAddresses will read visor addresses from addrCh and send holepunch
// packets to them
// Basically each address coming from addrCh is a dial request from some remote
// visor to us. Dialing visor contacts address resolver and gives the address to
// it, address resolver in turn sends us this address.
func (c *sudphClient) acceptAddresses(conn net.PacketConn, addrCh <-chan addrresolver.RemoteVisor) {
	// Cache resolved LAN addresses to avoid repeated Resolve calls which may
	// trigger additional notifications from the address resolver
	type lanCache struct {
		addresses []string
		port      string
	}
	sameLANCache := make(map[cipher.PubKey]*lanCache)

	for addr := range addrCh {
		// Check if the remote visor shares our public IP (same LAN)
		// If so, try to send hole punch to their LAN addresses instead
		localPublicIP := c.ar.LocalPublicIP()
		remoteIP := addr.Addr
		if host, _, err := net.SplitHostPort(remoteIP); err == nil {
			remoteIP = host
		}

		if localPublicIP != "" && remoteIP == localPublicIP {
			// Same public IP - check cache first, then resolve if needed
			cached, ok := sameLANCache[addr.PK]
			if !ok {
				c.log.Debugf("Remote visor %s shares same public IP, resolving LAN addresses", addr.PK.String()[:8])
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				visorData, err := c.ar.Resolve(ctx, string(c.netType), addr.PK)
				cancel()
				if err == nil && len(visorData.Addresses) > 0 {
					// Filter to usable LAN addresses
					var lanAddrs []string
					for _, lanHost := range visorData.Addresses {
						if lanHost == "127.0.0.1" || lanHost == "::1" || lanHost == localPublicIP {
							continue
						}
						lanAddrs = append(lanAddrs, lanHost)
					}
					cached = &lanCache{addresses: lanAddrs, port: visorData.Port}
				} else {
					cached = &lanCache{} // Empty cache to avoid retrying
					c.log.WithError(err).Debug("Failed to resolve LAN addresses, falling back to public IP")
				}
				sameLANCache[addr.PK] = cached
			}

			if len(cached.addresses) > 0 {
				// Send hole punch to cached LAN addresses
				for _, lanHost := range cached.addresses {
					lanAddr := net.JoinHostPort(lanHost, cached.port)
					udpAddr, err := net.ResolveUDPAddr("udp", lanAddr)
					if err != nil {
						continue
					}
					conn.WriteTo([]byte(holePunchMessage), udpAddr) //nolint:errcheck,gosec
				}
				continue // Skip the public IP hole punch
			}
		}

		udpAddr, err := net.ResolveUDPAddr("udp", addr.Addr)
		if err != nil {
			c.log.WithError(err).Errorf("Failed to resolve UDP address %q", addr)
			continue
		}

		conn.WriteTo([]byte(holePunchMessage), udpAddr) //nolint:errcheck,gosec
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

// Close implements Client interface
// Overrides genericClient.Close to also close the underlying packet connection
func (c *sudphClient) Close() error {
	// First call parent Close to stop acceptTransports loop and close KCP listener
	err := c.resolvedClient.Close()

	// Close the underlying UDP packet connection to stop PacketFilter loop
	// This must be done before closing sudphVisorsConn to prevent the filter
	// from trying to write to a closed connection
	if c.packetListener != nil {
		if closeErr := c.packetListener.Close(); closeErr != nil {
			c.log.WithError(closeErr).Warn("Failed to close packet listener")
		}
	}

	// Close sudphVisorsConn to unblock acceptAddresses goroutine
	if c.sudphVisorsConn != nil {
		if closeErr := c.sudphVisorsConn.Close(); closeErr != nil {
			c.log.WithError(closeErr).Warn("Failed to close SUDPH visors connection")
		}
	}

	return err
}
