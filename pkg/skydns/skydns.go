// Package skydns pkg/skydns/skydns.go
//
// SkyDNS lets every app on a phone open .dmsg and .skynet names, whatever its
// proxy support. It sits on a tunnel's packet path and owns [Range]: apps ask
// [ResolverAddr] for names, mesh names get a synthetic IP from the mesh
// gateway's pool, and every other name goes to an [Exchanger]. TCP to a
// synthetic IP is carried over the mesh by the gateway (pkg/vpnrouter/meshgw).
package skydns

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"sync"
	"time"

	"github.com/miekg/dns"
	"github.com/sirupsen/logrus"
	"gvisor.dev/gvisor/pkg/buffer"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/link/channel"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
	"gvisor.dev/gvisor/pkg/tcpip/transport/udp"
	"gvisor.dev/gvisor/pkg/waiter"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skynetca"
	"github.com/skycoin/skywire/pkg/vpnrouter/meshgw"
)

const (
	// Range is RFC 2544 benchmark space, so no real network uses it. Unlike
	// 100.64.0.0/10 it never holds a carrier's CGNAT address for the phone.
	Range = "198.18.0.0/15"
	// PoolCIDR is where mesh names get their synthetic IPs.
	PoolCIDR = "198.18.0.0/16"
	// ResolverAddr is the DNS server apps are pointed at.
	ResolverAddr = "198.19.0.53"
	// LocalAddr is the standalone tunnel's own address.
	LocalAddr = "198.19.0.1"

	nicID        = 1
	defaultMTU   = 1500
	queryTimeout = 5 * time.Second
)

var rangePrefix = netip.MustParsePrefix(Range)

// Exchanger answers a DNS query, as an [Upstream] does.
type Exchanger interface {
	Exchange(ctx context.Context, m *dns.Msg) (*dns.Msg, error)
}

// Config is what an [Engine] needs from its host.
type Config struct {
	// Dial carries a connection to a synthetic IP over the mesh.
	Dial meshgw.MeshDial
	// Upstream answers every name that is not a mesh name.
	Upstream Exchanger
	// Aliases maps friendly names to PKs. Optional.
	Aliases map[string]cipher.PubKey
	// Minter, when set, terminates HTTPS to mesh names. Optional.
	Minter skynetca.LeafMinter
	// MTU of the tunnel. Zero means 1500.
	MTU int
	Log logrus.FieldLogger
}

// Engine is SkyDNS on one tunnel. Write hands it the packets [Owns] picks out,
// and Read returns the packets it sends back. Safe for concurrent use.
type Engine struct {
	gw   *meshgw.Gateway
	up   Exchanger
	log  logrus.FieldLogger
	pool netip.Prefix

	stack *stack.Stack
	ep    *channel.Endpoint
	udp   net.PacketConn
	tcp   net.Listener

	ctx       context.Context
	cancel    context.CancelFunc
	closeOnce sync.Once
	closed    chan struct{}
}

// Owns reports whether p is an IPv4 packet for SkyDNS, one addressed to [Range].
func Owns(p []byte) bool {
	if len(p) < header.IPv4MinimumSize || p[0]>>4 != 4 {
		return false
	}
	return rangePrefix.Contains(netip.AddrFrom4([4]byte(p[16:20])))
}

// New starts an engine. Close it to stop.
func New(cfg Config) (*Engine, error) {
	if cfg.Upstream == nil {
		return nil, errors.New("skydns: no upstream resolver")
	}
	log := cfg.Log
	if log == nil {
		log = logrus.New()
	}
	mtu := cfg.MTU
	if mtu <= 0 {
		mtu = defaultMTU
	}
	gw, err := meshgw.New(cfg.Dial, PoolCIDR, cfg.Aliases, log)
	if err != nil {
		return nil, err
	}
	if cfg.Minter != nil {
		gw.EnableTLSMITM(cfg.Minter, 443)
	}

	s := stack.New(stack.Options{
		NetworkProtocols:   []stack.NetworkProtocolFactory{ipv4.NewProtocol},
		TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol, udp.NewProtocol},
	})
	ep := channel.New(512, uint32(mtu), "")
	ctx, cancel := context.WithCancel(context.Background())
	e := &Engine{
		gw:     gw,
		up:     cfg.Upstream,
		log:    log,
		pool:   netip.MustParsePrefix(PoolCIDR),
		stack:  s,
		ep:     ep,
		ctx:    ctx,
		cancel: cancel,
		closed: make(chan struct{}),
	}
	if err := e.configure(); err != nil {
		e.Close() //nolint:errcheck,gosec
		return nil, err
	}
	return e, nil
}

// configure brings up the NIC and the resolver on it. Promiscuous mode with
// spoofing lets the stack accept, and answer from, every synthetic IP.
func (e *Engine) configure() error {
	if err := e.stack.CreateNIC(nicID, e.ep); err != nil {
		return fmt.Errorf("skydns: CreateNIC: %s", err)
	}
	resolver := tcpip.AddrFrom4(netip.MustParseAddr(ResolverAddr).As4())
	addr := tcpip.ProtocolAddress{
		Protocol:          ipv4.ProtocolNumber,
		AddressWithPrefix: tcpip.AddressWithPrefix{Address: resolver, PrefixLen: 32},
	}
	if err := e.stack.AddProtocolAddress(nicID, addr, stack.AddressProperties{}); err != nil {
		return fmt.Errorf("skydns: AddProtocolAddress: %s", err)
	}
	if err := e.stack.SetPromiscuousMode(nicID, true); err != nil {
		return fmt.Errorf("skydns: SetPromiscuousMode: %s", err)
	}
	if err := e.stack.SetSpoofing(nicID, true); err != nil {
		return fmt.Errorf("skydns: SetSpoofing: %s", err)
	}
	e.stack.SetRouteTable([]tcpip.Route{{Destination: header.IPv4EmptySubnet, NIC: nicID}})

	fwd := tcp.NewForwarder(e.stack, 0, 1024, e.handleTCP)
	e.stack.SetTransportProtocolHandler(tcp.ProtocolNumber, fwd.HandlePacket)
	// UDP to anything but the resolver is dropped. There is nothing behind it.
	e.stack.SetTransportProtocolHandler(udp.ProtocolNumber, func(stack.TransportEndpointID, *stack.PacketBuffer) bool {
		return true
	})

	at := tcpip.FullAddress{NIC: nicID, Addr: resolver, Port: 53}
	pc, err := gonet.DialUDP(e.stack, &at, nil, ipv4.ProtocolNumber)
	if err != nil {
		return fmt.Errorf("skydns: resolver udp: %w", err)
	}
	e.udp = pc
	ln, err := gonet.ListenTCP(e.stack, at, ipv4.ProtocolNumber)
	if err != nil {
		return fmt.Errorf("skydns: resolver tcp: %w", err)
	}
	e.tcp = ln

	mux := dns.NewServeMux()
	e.gw.InstallDNS(mux)
	mux.HandleFunc(".", e.forward)
	for _, srv := range []*dns.Server{{PacketConn: pc, Handler: mux}, {Listener: ln, Handler: mux}} {
		go func(srv *dns.Server) {
			if err := srv.ActivateAndServe(); err != nil && e.ctx.Err() == nil {
				e.log.WithError(err).Warn("skydns: resolver stopped")
			}
		}(srv)
	}
	return nil
}

// handleTCP takes a connection to a synthetic IP and carries it over the mesh.
// Anything else, the Private DNS probe on 853 included, is refused.
func (e *Engine) handleTCP(r *tcp.ForwarderRequest) {
	id := r.ID()
	dst := netip.AddrFrom4(id.LocalAddress.As4())
	if !e.pool.Contains(dst) {
		r.Complete(true)
		return
	}
	var wq waiter.Queue
	ep, err := r.CreateEndpoint(&wq)
	if err != nil {
		r.Complete(true)
		return
	}
	r.Complete(false)
	go e.gw.Bridge(e.ctx, gonet.NewTCPConn(&wq, ep), net.IP(dst.AsSlice()), id.LocalPort)
}

// forward answers a name that is not a mesh name from the upstream.
func (e *Engine) forward(w dns.ResponseWriter, r *dns.Msg) {
	ctx, cancel := context.WithTimeout(e.ctx, queryTimeout)
	defer cancel()
	resp, err := e.up.Exchange(ctx, r)
	if err != nil {
		e.log.WithError(err).Debug("skydns: upstream failed")
		resp = new(dns.Msg)
		resp.SetRcode(r, dns.RcodeServerFailure)
	}
	resp.Id = r.Id
	if _, overUDP := w.RemoteAddr().(*net.UDPAddr); overUDP {
		size := dns.MinMsgSize
		if opt := r.IsEdns0(); opt != nil && int(opt.UDPSize()) > size {
			size = int(opt.UDPSize())
		}
		resp.Truncate(size)
	}
	_ = w.WriteMsg(resp) //nolint:errcheck
}

// Write hands the engine one packet from the tunnel.
func (e *Engine) Write(p []byte) (int, error) {
	select {
	case <-e.closed:
		return 0, io.ErrClosedPipe
	default:
	}
	pkt := stack.NewPacketBuffer(stack.PacketBufferOptions{
		Payload: buffer.MakeWithData(append([]byte(nil), p...)),
	})
	e.ep.InjectInbound(header.IPv4ProtocolNumber, pkt)
	pkt.DecRef()
	return len(p), nil
}

// Read blocks for one packet the engine sends back into the tunnel. It
// returns io.EOF once the engine is closed.
func (e *Engine) Read(p []byte) (int, error) {
	pkt := e.ep.ReadContext(e.ctx)
	if pkt == nil {
		return 0, io.EOF
	}
	defer pkt.DecRef()
	v := pkt.ToView()
	n, err := v.Read(p)
	if err != nil && err != io.EOF {
		return 0, err
	}
	if v.Size() > 0 {
		return n, fmt.Errorf("skydns: %d-byte read buffer truncated a packet", len(p))
	}
	return n, nil
}

// Close stops the resolver and drops every connection the engine carries.
func (e *Engine) Close() error {
	e.closeOnce.Do(func() {
		close(e.closed)
		e.cancel()
		if e.udp != nil {
			_ = e.udp.Close() //nolint:errcheck
		}
		if e.tcp != nil {
			_ = e.tcp.Close() //nolint:errcheck
		}
		e.stack.Destroy()
	})
	return nil
}
