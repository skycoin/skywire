// Package vpn pkg/vpn/netstack_tun.go c4-app-vpn
//
// A gVisor userspace netstack behind the same TUNDevice seam the platform
// TUNs implement. On js/wasm it IS the TUN (a browser has no L3 interface to
// open — newTUNDevice in tun_device_js.go builds one of these), but the type
// is pure Go and compiles everywhere: the VPN data plane only needs something
// that emits one IP packet per Read and accepts one IP packet per Write, and
// the netstack's channel endpoint is exactly that. In-tab (or in-process)
// dialers open TCP/UDP connections INSIDE this stack via Client.NetstackDial;
// the stack turns them into IP packets; the unchanged client serve loop
// copies those to the vpn-server over the route group, which does the real
// clearnet egress. The server cannot tell such a client from a native one,
// and no privileges are involved anywhere — no root, no device node.
package vpn

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"sync"

	"gvisor.dev/gvisor/pkg/buffer"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/link/channel"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/icmp"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
	"gvisor.dev/gvisor/pkg/tcpip/transport/udp"
)

const netstackNICID = 1

// netstackTUN adapts a gVisor netstack channel endpoint to the TUNDevice
// contract: Read blocks for, and returns, exactly one outbound IP packet;
// Write injects exactly one inbound IP packet. Closing unblocks Read with
// io.EOF (the serve loop's copy pumps exit like they do on a closed TUN fd).
type netstackTUN struct {
	stack *stack.Stack
	ep    *channel.Endpoint

	ctx    context.Context
	cancel context.CancelFunc

	closeOnce sync.Once
	closed    chan struct{}
}

// newNetstackTUN constructs the stack and its NIC. The device carries no
// address until configure (SetupTUN) assigns the server-allocated tunnel IP.
func newNetstackTUN() (*netstackTUN, error) {
	s := stack.New(stack.Options{
		NetworkProtocols:   []stack.NetworkProtocolFactory{ipv4.NewProtocol},
		TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol, udp.NewProtocol, icmp.NewProtocol4},
	})
	ep := channel.New(512, TUNMTU, "")
	if err := s.CreateNIC(netstackNICID, ep); err != nil {
		s.Destroy()
		return nil, fmt.Errorf("netstack: CreateNIC: %s", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &netstackTUN{
		stack:  s,
		ep:     ep,
		ctx:    ctx,
		cancel: cancel,
		closed: make(chan struct{}),
	}, nil
}

// configure assigns the tunnel address and points the default route at the
// NIC. Called by the js SetupTUN once the server handshake has allocated an
// IP (mirrors `ip addr add` + `ip route add default` on Linux).
func (t *netstackTUN) configure(ipCIDR string) error {
	pfx, err := netip.ParsePrefix(ipCIDR)
	if err != nil {
		return fmt.Errorf("netstack: parse tunnel address %q: %w", ipCIDR, err)
	}
	a := pfx.Addr().As4()
	protoAddr := tcpip.ProtocolAddress{
		Protocol: ipv4.ProtocolNumber,
		AddressWithPrefix: tcpip.AddressWithPrefix{
			Address:   tcpip.AddrFrom4(a),
			PrefixLen: pfx.Bits(),
		},
	}
	if err := t.stack.AddProtocolAddress(netstackNICID, protoAddr, stack.AddressProperties{}); err != nil {
		return fmt.Errorf("netstack: AddProtocolAddress: %s", err)
	}
	t.stack.SetRouteTable([]tcpip.Route{
		{Destination: header.IPv4EmptySubnet, NIC: netstackNICID},
	})
	return nil
}

// Read blocks until the stack emits one outbound IP packet and copies it into
// p (the serve loop hands each Read result to the conn as one frame, the same
// one-packet-per-read contract the platform TUNs give io.Copy).
func (t *netstackTUN) Read(p []byte) (int, error) {
	pkt := t.ep.ReadContext(t.ctx)
	if pkt == nil {
		return 0, io.EOF
	}
	v := pkt.ToView()
	n, err := v.Read(p)
	pkt.DecRef()
	if err != nil && err != io.EOF {
		return 0, err
	}
	if v.Size() > 0 {
		return n, fmt.Errorf("netstack: packet of %d bytes truncated by %d-byte read buffer", n+v.Size(), len(p))
	}
	return n, nil
}

// Write injects one inbound IP packet (a frame the vpn-server sent us).
func (t *netstackTUN) Write(p []byte) (int, error) {
	select {
	case <-t.closed:
		return 0, io.ErrClosedPipe
	default:
	}
	pkt := stack.NewPacketBuffer(stack.PacketBufferOptions{
		Payload: buffer.MakeWithData(append([]byte(nil), p...)),
	})
	t.ep.InjectInbound(header.IPv4ProtocolNumber, pkt)
	pkt.DecRef()
	return len(p), nil
}

func (t *netstackTUN) Close() error {
	t.closeOnce.Do(func() {
		close(t.closed)
		t.cancel()
		t.stack.Destroy()
	})
	return nil
}

// Name mirrors a platform interface name; purely informational here.
func (t *netstackTUN) Name() string { return "netstack0" }

// errNoNetstack is returned by the dial helpers before the tunnel is up.
var errNoNetstack = errors.New("vpn: netstack tunnel is not up")

// NetstackDial dials network ("tcp"/"udp") address host:port THROUGH the
// tunnel: the connection originates inside the netstack and its packets ride
// the VPN to the server's clearnet egress. host must be an IPv4 literal —
// name resolution belongs to the caller (resolve over NetstackDial UDP :53,
// or via whatever resolver the app already uses).
func (c *Client) NetstackDial(ctx context.Context, network, address string) (net.Conn, error) {
	c.tunMu.Lock()
	t, ok := c.tun.(*netstackTUN)
	created := c.tunCreated
	c.tunMu.Unlock()
	if !ok || !created {
		return nil, errNoNetstack
	}
	return t.dial(ctx, network, address)
}

// dial opens a connection inside the stack (see NetstackDial).
func (t *netstackTUN) dial(ctx context.Context, network, address string) (net.Conn, error) {
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return nil, fmt.Errorf("vpn: NetstackDial wants ip:port, got %q: %w", address, err)
	}
	if !ap.Addr().Is4() {
		return nil, fmt.Errorf("vpn: NetstackDial is IPv4-only, got %q", address)
	}
	full := tcpip.FullAddress{
		NIC:  netstackNICID,
		Addr: tcpip.AddrFrom4(ap.Addr().As4()),
		Port: ap.Port(),
	}
	switch network {
	case "tcp", "tcp4":
		return gonet.DialContextTCP(ctx, t.stack, full, ipv4.ProtocolNumber)
	case "udp", "udp4":
		return gonet.DialUDP(t.stack, nil, &full, ipv4.ProtocolNumber)
	default:
		return nil, fmt.Errorf("vpn: NetstackDial does not support network %q", network)
	}
}
