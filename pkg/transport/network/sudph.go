package network

import (
	"net"

	"github.com/AudriusButkevicius/pfilter"
	"github.com/skycoin/skywire/pkg/snet/arclient"
	"github.com/xtaci/kcp-go"
)

// holePunchMessage is sent in a dummy UDP packet that is sent by both parties to establish UDP hole punching.
const (
	holePunchMessage = "holepunch"
	// dialConnPriority and visorsConnPriority are used to set an order how connection filters apply.
	dialConnPriority   = 2
	visorsConnPriority = 3
)

type sudphClient struct {
	*genericClient
	addressResolver arclient.APIClient
}

func newSudph(generic *genericClient, addressResolver arclient.APIClient) Client {
	client := &stcprClient{genericClient: generic, addressResolver: addressResolver}
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
	listenFilter := pfilter.NewPacketFilter(packetListener)
	sudphVisorsConn := listenFilter.NewConn(visorsConnPriority, nil)
	listenFilter.Start()
	c.log.Infof("Binding")
	addrCh, err := c.addressResolver.BindSUDPH(listenFilter)
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
