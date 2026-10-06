package skydns

import (
	"context"
	"errors"
	"io"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/miekg/dns"
	"gvisor.dev/gvisor/pkg/buffer"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/link/channel"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
	"gvisor.dev/gvisor/pkg/tcpip/transport/udp"

	"github.com/skycoin/skywire/pkg/cipher"
)

// fakeUpstream answers every name with 192.0.2.7, or fails when told to.
type fakeUpstream struct {
	mu    sync.Mutex
	names []string
	fail  bool
}

func (f *fakeUpstream) Exchange(_ context.Context, m *dns.Msg) (*dns.Msg, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.names = append(f.names, m.Question[0].Name)
	if f.fail {
		return nil, errors.New("upstream down")
	}
	r := new(dns.Msg)
	r.SetReply(m)
	r.Answer = append(r.Answer, &dns.A{
		Hdr: dns.RR_Header{Name: m.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60},
		A:   net.IPv4(192, 0, 2, 7),
	})
	return r, nil
}

// meshDial records each dial and echoes whatever is written to it.
type meshDial struct {
	mu    sync.Mutex
	dials []string
}

func (d *meshDial) dial(_ context.Context, scheme string, dest cipher.PubKey, port uint16) (net.Conn, error) {
	d.mu.Lock()
	d.dials = append(d.dials, scheme+"|"+dest.Hex()+"|"+netip.AddrPortFrom(netip.IPv4Unspecified(), port).String())
	d.mu.Unlock()
	a, b := net.Pipe()
	go func() {
		_, _ = io.Copy(b, b) //nolint:errcheck
		_ = b.Close()        //nolint:errcheck
	}()
	return a, nil
}

// phone is a second netstack wired to the engine, standing in for the apps
// on the other side of the tunnel.
type phone struct{ s *stack.Stack }

func newPhone(t *testing.T, e *Engine) *phone {
	t.Helper()
	s := stack.New(stack.Options{
		NetworkProtocols:   []stack.NetworkProtocolFactory{ipv4.NewProtocol},
		TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol, udp.NewProtocol},
	})
	ep := channel.New(512, defaultMTU, "")
	if err := s.CreateNIC(1, ep); err != nil {
		t.Fatal(err)
	}
	addr := tcpip.ProtocolAddress{
		Protocol:          ipv4.ProtocolNumber,
		AddressWithPrefix: tcpip.AddressWithPrefix{Address: tcpip.AddrFrom4([4]byte{10, 0, 0, 2}), PrefixLen: 24},
	}
	if err := s.AddProtocolAddress(1, addr, stack.AddressProperties{}); err != nil {
		t.Fatal(err)
	}
	s.SetRouteTable([]tcpip.Route{{Destination: header.IPv4EmptySubnet, NIC: 1}})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		_ = e.Close() //nolint:errcheck
		s.Destroy()
	})
	go func() {
		for {
			pkt := ep.ReadContext(ctx)
			if pkt == nil {
				return
			}
			if Owns(pkt.ToView().AsSlice()) {
				_, _ = e.Write(pkt.ToView().AsSlice()) //nolint:errcheck
			}
			pkt.DecRef()
		}
	}()
	go func() {
		buf := make([]byte, defaultMTU)
		for {
			n, err := e.Read(buf)
			if err != nil {
				return
			}
			pkt := stack.NewPacketBuffer(stack.PacketBufferOptions{Payload: buffer.MakeWithData(append([]byte(nil), buf[:n]...))})
			ep.InjectInbound(header.IPv4ProtocolNumber, pkt)
			pkt.DecRef()
		}
	}()
	return &phone{s: s}
}

func (p *phone) dialTCP(t *testing.T, addr netip.AddrPort) (net.Conn, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return gonet.DialContextTCP(ctx, p.s, tcpip.FullAddress{NIC: 1, Addr: tcpip.AddrFrom4(addr.Addr().As4()), Port: addr.Port()}, ipv4.ProtocolNumber)
}

func (p *phone) lookup(t *testing.T, network, name string, qtype uint16) *dns.Msg {
	t.Helper()
	resolver := netip.AddrPortFrom(netip.MustParseAddr(ResolverAddr), 53)
	var conn net.Conn
	var err error
	if network == "tcp" {
		conn, err = p.dialTCP(t, resolver)
	} else {
		full := tcpip.FullAddress{NIC: 1, Addr: tcpip.AddrFrom4(resolver.Addr().As4()), Port: 53}
		conn, err = gonet.DialUDP(p.s, nil, &full, ipv4.ProtocolNumber)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close() //nolint:errcheck
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(name), qtype)
	resp, err := roundTrip(context.Background(), &dns.Conn{Conn: conn}, m)
	if err != nil {
		t.Fatalf("lookup %s over %s: %v", name, network, err)
	}
	return resp
}

// newEngine starts an engine wired to a phone. The phone's cleanup closes it.
func newEngine(t *testing.T, up Exchanger) (*phone, *meshDial) {
	t.Helper()
	d := &meshDial{}
	e, err := New(Config{Dial: d.dial, Upstream: up})
	if err != nil {
		t.Fatal(err)
	}
	return newPhone(t, e), d
}

const testPK = "022e607e0914d6e7ccda7587f95790c09e126bbd506cc476a1eda852325aadd1aa"

// testLabel is testPK as a DNS name can carry it. A 66-character hex label is
// over the 63-octet limit of RFC 1035 section 2.3.4.
var testLabel = func() string {
	var pk cipher.PubKey
	if err := pk.Set(testPK); err != nil {
		panic(err)
	}
	return pk.DNSLabel()
}()

func answerIP(t *testing.T, resp *dns.Msg) netip.Addr {
	t.Helper()
	if resp.Rcode != dns.RcodeSuccess || len(resp.Answer) != 1 {
		t.Fatalf("want one answer, got rcode %d: %v", resp.Rcode, resp.Answer)
	}
	a, ok := resp.Answer[0].(*dns.A)
	if !ok {
		t.Fatalf("want an A record, got %T", resp.Answer[0])
	}
	ip, _ := netip.AddrFromSlice(a.A.To4())
	return ip
}

func TestMeshNamesGetSyntheticIPs(t *testing.T) {
	p, _ := newEngine(t, &fakeUpstream{})
	pool := netip.MustParsePrefix(PoolCIDR)

	dmsg := answerIP(t, p.lookup(t, "udp", testLabel+".dmsg", dns.TypeA))
	if !pool.Contains(dmsg) {
		t.Fatalf("%s is outside %s", dmsg, PoolCIDR)
	}
	if again := answerIP(t, p.lookup(t, "udp", testLabel+".dmsg", dns.TypeA)); again != dmsg {
		t.Fatalf("same name leased twice: %s then %s", dmsg, again)
	}
	skynet := answerIP(t, p.lookup(t, "udp", testLabel+".skynet", dns.TypeA))
	if !pool.Contains(skynet) || skynet == dmsg {
		t.Fatalf("skynet name got %s (dmsg %s)", skynet, dmsg)
	}
	// IPv4 only, so AAAA is an empty success and the A record still gets asked.
	if r := p.lookup(t, "udp", testLabel+".dmsg", dns.TypeAAAA); r.Rcode != dns.RcodeSuccess || len(r.Answer) != 0 {
		t.Fatalf("AAAA: rcode %d, %d answers", r.Rcode, len(r.Answer))
	}
	if r := p.lookup(t, "udp", "nope.dmsg", dns.TypeA); r.Rcode != dns.RcodeNameError {
		t.Fatalf("a name with no PK: rcode %d", r.Rcode)
	}
}

func TestOtherNamesGoUpstream(t *testing.T) {
	up := &fakeUpstream{}
	p, _ := newEngine(t, up)
	if ip := answerIP(t, p.lookup(t, "udp", "example.com", dns.TypeA)); ip != netip.MustParseAddr("192.0.2.7") {
		t.Fatalf("got %s", ip)
	}
	if ip := answerIP(t, p.lookup(t, "tcp", "example.org", dns.TypeA)); ip != netip.MustParseAddr("192.0.2.7") {
		t.Fatalf("over tcp got %s", ip)
	}
	up.mu.Lock()
	defer up.mu.Unlock()
	if len(up.names) != 2 || up.names[0] != "example.com." || up.names[1] != "example.org." {
		t.Fatalf("upstream saw %v", up.names)
	}
}

func TestUpstreamFailureIsServFail(t *testing.T) {
	p, _ := newEngine(t, &fakeUpstream{fail: true})
	if r := p.lookup(t, "udp", "example.com", dns.TypeA); r.Rcode != dns.RcodeServerFailure {
		t.Fatalf("rcode %d", r.Rcode)
	}
}

func TestSyntheticIPRidesTheMesh(t *testing.T) {
	p, d := newEngine(t, &fakeUpstream{})
	ip := answerIP(t, p.lookup(t, "udp", testLabel+".dmsg", dns.TypeA))

	conn, err := p.dialTCP(t, netip.AddrPortFrom(ip, 80))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close() //nolint:errcheck
	if _, err := conn.Write([]byte("GET / HTTP/1.0\r\n\r\n")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 18)
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(conn, buf); err != nil || string(buf) != "GET / HTTP/1.0\r\n\r\n" {
		t.Fatalf("echo: %q, %v", buf, err)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if want := "dmsg|" + testPK + "|0.0.0.0:80"; len(d.dials) != 1 || d.dials[0] != want {
		t.Fatalf("mesh dials %v, want [%s]", d.dials, want)
	}
}

// Android probes 853 on the resolver for Private DNS. A refusal sends it back
// to plain DNS on 53, which is what SkyDNS answers.
func TestPrivateDNSProbeIsRefused(t *testing.T) {
	p, _ := newEngine(t, &fakeUpstream{})
	if _, err := p.dialTCP(t, netip.AddrPortFrom(netip.MustParseAddr(ResolverAddr), 853)); err == nil {
		t.Fatal("853 on the resolver accepted a connection")
	}
}

func TestOwns(t *testing.T) {
	packet := func(dst string) []byte {
		p := make([]byte, header.IPv4MinimumSize)
		p[0] = 0x45
		a := netip.MustParseAddr(dst).As4()
		copy(p[16:20], a[:])
		return p
	}
	for dst, want := range map[string]bool{
		ResolverAddr: true, "198.18.0.1": true, "198.19.255.255": true,
		"198.20.0.1": false, "1.1.1.1": false, "100.64.0.1": false,
	} {
		if got := Owns(packet(dst)); got != want {
			t.Errorf("Owns(%s) = %v", dst, got)
		}
	}
	if Owns([]byte{0x60, 0, 0}) {
		t.Error("a short IPv6 packet was claimed")
	}
}
