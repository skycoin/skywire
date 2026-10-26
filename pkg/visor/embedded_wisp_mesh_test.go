package visor

import (
	"context"
	"errors"
	"net"
	"strconv"
	"sync"
	"testing"

	"github.com/0magnet/wisp"
	"github.com/miekg/dns"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skynetweb"
	"github.com/skycoin/skywire/pkg/vpnrouter/meshgw"
)

// fakeExit is the exit egress: port 53 answers every query with 10.9.9.9,
// anything else is recorded.
type fakeExit struct {
	mu    sync.Mutex
	dials []string
}

func (f *fakeExit) DialTCP(_ context.Context, host string, port uint16) (net.Conn, error) {
	f.mu.Lock()
	f.dials = append(f.dials, net.JoinHostPort(host, strconv.Itoa(int(port))))
	f.mu.Unlock()
	c, s := net.Pipe()
	if port == 53 {
		go func() {
			defer s.Close() //nolint:errcheck
			for {
				raw, err := readDNSFrame(s)
				if err != nil {
					return
				}
				var q dns.Msg
				_ = q.Unpack(raw) //nolint:errcheck
				m := new(dns.Msg)
				m.SetReply(&q)
				m.Answer = append(m.Answer, &dns.A{Hdr: dns.RR_Header{Name: q.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET}, A: net.IPv4(10, 9, 9, 9)})
				b, _ := m.Pack() //nolint:errcheck
				if writeDNSFrame(s, b) != nil {
					return
				}
			}
		}()
	}
	return c, nil
}

func (f *fakeExit) DialUDP(context.Context, string, uint16) (wisp.DatagramStream, error) {
	return nil, errors.New("no udp")
}

func (f *fakeExit) Describe() string { return "fake exit" }

type meshDialRecord struct {
	scheme string
	pk     cipher.PubKey
	port   uint16
}

func lookupA(t *testing.T, e *meshEgress, name string) net.IP {
	t.Helper()
	conn, err := e.DialTCP(context.Background(), "1.1.1.1", 53)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close() //nolint:errcheck
	q := new(dns.Msg)
	q.SetQuestion(dns.Fqdn(name), dns.TypeA)
	b, _ := q.Pack() //nolint:errcheck
	if err := writeDNSFrame(conn, b); err != nil {
		t.Fatal(err)
	}
	raw, err := readDNSFrame(conn)
	if err != nil {
		t.Fatal(err)
	}
	var m dns.Msg
	if err := m.Unpack(raw); err != nil {
		t.Fatal(err)
	}
	if len(m.Answer) != 1 {
		t.Fatalf("%s: %d answers, rcode %d", name, len(m.Answer), m.Rcode)
	}
	return m.Answer[0].(*dns.A).A
}

func TestWispMeshEgress(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	var mu sync.Mutex
	var dialed []meshDialRecord
	gw, err := meshgw.New(func(_ context.Context, scheme string, dest cipher.PubKey, port uint16, _ []skynetweb.RouteLabel) (net.Conn, error) {
		mu.Lock()
		dialed = append(dialed, meshDialRecord{scheme, dest, port})
		mu.Unlock()
		c, s := net.Pipe()
		_ = s.Close() //nolint:errcheck
		return c, nil
	}, wispMeshCIDR, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	exit := &fakeExit{}
	e := &meshEgress{next: exit, gw: gw}

	// A mesh name gets a synthetic address, without asking the exit.
	ip := lookupA(t, e, pk.DNSLabel()+".dmsg")
	if !gw.PoolCIDR().Contains(ip) {
		t.Fatalf("mesh name got %s, outside %s", ip, wispMeshCIDR)
	}
	// Any other name goes to the server the guest asked, through the exit.
	if got := lookupA(t, e, "example.com"); !got.Equal(net.IPv4(10, 9, 9, 9)) {
		t.Fatalf("example.com = %s, want the exit's answer", got)
	}

	// Connecting by synthetic address or by name both reach the mesh.
	if _, err := e.DialTCP(context.Background(), ip.String(), 80); err != nil {
		t.Fatal(err)
	}
	if _, err := e.DialTCP(context.Background(), "x."+pk.Hex()+".skynet", 8080); err != nil {
		t.Fatal(err)
	}
	// Anything else leaves through the exit.
	if _, err := e.DialTCP(context.Background(), "example.com", 443); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	want := []meshDialRecord{{"dmsg", pk, 80}, {"skynet", pk, 8080}}
	if len(dialed) != len(want) {
		t.Fatalf("mesh dials = %v, want %v", dialed, want)
	}
	for i := range want {
		if dialed[i] != want[i] {
			t.Fatalf("mesh dial %d = %v, want %v", i, dialed[i], want[i])
		}
	}
	exit.mu.Lock()
	defer exit.mu.Unlock()
	if len(exit.dials) != 2 || exit.dials[0] != "1.1.1.1:53" || exit.dials[1] != "example.com:443" {
		t.Fatalf("exit dials = %v, want the example.com lookup and connection only", exit.dials)
	}
}

// TestWispIPv6LookupsGetNoAnswer: the guest network is IPv4 only, so an IPv6
// lookup is answered empty here and never reaches the exit.
func TestWispIPv6LookupsGetNoAnswer(t *testing.T) {
	gw, err := meshgw.New(func(context.Context, string, cipher.PubKey, uint16, []skynetweb.RouteLabel) (net.Conn, error) {
		return nil, errors.New("no mesh")
	}, wispMeshCIDR, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	exit := &fakeExit{}
	e := &meshEgress{next: exit, gw: gw}

	conn, err := e.DialTCP(context.Background(), "1.1.1.1", 53)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close() //nolint:errcheck
	q := new(dns.Msg)
	q.SetQuestion("ifconfig.me.", dns.TypeAAAA)
	b, _ := q.Pack() //nolint:errcheck
	if err := writeDNSFrame(conn, b); err != nil {
		t.Fatal(err)
	}
	raw, err := readDNSFrame(conn)
	if err != nil {
		t.Fatal(err)
	}
	var m dns.Msg
	if err := m.Unpack(raw); err != nil {
		t.Fatal(err)
	}
	if m.Rcode != dns.RcodeSuccess || len(m.Answer) != 0 {
		t.Fatalf("AAAA answer: rcode %d, %d records, want an empty success", m.Rcode, len(m.Answer))
	}
	exit.mu.Lock()
	defer exit.mu.Unlock()
	if len(exit.dials) != 0 {
		t.Fatalf("exit dials = %v, want none", exit.dials)
	}
}
