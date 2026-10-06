package skydns

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miekg/dns"
)

// resolverFixture is a resolver on loopback, with DNS-over-TLS and plain DNS
// on their own ports. dial maps the ports the Upstream asks for onto them.
type resolverFixture struct {
	roots     *x509.CertPool
	tlsAddr   string
	plainAddr string
	tlsDials  atomic.Int32
}

func answer(w dns.ResponseWriter, r *dns.Msg) {
	m := new(dns.Msg)
	m.SetReply(r)
	m.Answer = append(m.Answer, &dns.A{
		Hdr: dns.RR_Header{Name: r.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60},
		A:   net.IPv4(192, 0, 2, 9),
	})
	_ = w.WriteMsg(m) //nolint:errcheck
}

func newResolverFixture(t *testing.T, withTLS bool, idle time.Duration) *resolverFixture {
	t.Helper()
	f := &resolverFixture{roots: x509.NewCertPool()}

	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f.plainAddr = pc.LocalAddr().String()
	serve(t, &dns.Server{PacketConn: pc, Handler: dns.HandlerFunc(answer)})

	if !withTLS {
		return f
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test resolver"},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1)},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IsCA:         true, BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	f.roots.AddCert(cert)
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}},
		MinVersion:   tls.VersionTLS12,
	})
	if err != nil {
		t.Fatal(err)
	}
	f.tlsAddr = ln.Addr().String()
	srv := &dns.Server{Listener: ln, Handler: dns.HandlerFunc(answer)}
	if idle > 0 {
		srv.IdleTimeout = func() time.Duration { return idle }
	}
	serve(t, srv)
	return f
}

func serve(t *testing.T, srv *dns.Server) {
	t.Helper()
	started := make(chan struct{})
	srv.NotifyStartedFunc = func() { close(started) }
	go func() { _ = srv.ActivateAndServe() }() //nolint:errcheck
	<-started
	t.Cleanup(func() { _ = srv.Shutdown() }) //nolint:errcheck
}

func (f *resolverFixture) dial(ctx context.Context, network, address string) (net.Conn, error) {
	_, port, _ := net.SplitHostPort(address) //nolint:errcheck
	var d net.Dialer
	switch {
	case port == "853" && f.tlsAddr != "":
		f.tlsDials.Add(1)
		return d.DialContext(ctx, network, f.tlsAddr)
	case port == "853":
		return nil, &net.OpError{Op: "dial", Net: network, Err: syscallRefused{}}
	case network == "udp":
		return d.DialContext(ctx, network, f.plainAddr)
	}
	return nil, &net.OpError{Op: "dial", Net: network, Err: syscallRefused{}}
}

type syscallRefused struct{}

func (syscallRefused) Error() string { return "connection refused" }

func (f *resolverFixture) upstream(strict bool) *Upstream {
	u := NewUpstream("127.0.0.1", f.dial, strict)
	u.roots = f.roots
	return u
}

func ask(t *testing.T, u *Upstream) (*dns.Msg, error) {
	t.Helper()
	m := new(dns.Msg)
	m.SetQuestion("example.com.", dns.TypeA)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return u.Exchange(ctx, m)
}

func TestUpstreamReusesOneTLSConnection(t *testing.T) {
	f := newResolverFixture(t, true, 0)
	u := f.upstream(true)
	for i := 0; i < 3; i++ {
		resp, err := ask(t, u)
		if err != nil || len(resp.Answer) != 1 {
			t.Fatalf("query %d: %v %v", i, resp, err)
		}
	}
	if n := f.tlsDials.Load(); n != 1 {
		t.Fatalf("%d TLS dials for 3 queries, want 1", n)
	}
}

func TestUpstreamRedialsAfterIdleClose(t *testing.T) {
	f := newResolverFixture(t, true, 50*time.Millisecond)
	u := f.upstream(true)
	if _, err := ask(t, u); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if _, err := ask(t, u); err != nil {
		t.Fatalf("after the resolver dropped the idle connection: %v", err)
	}
	if n := f.tlsDials.Load(); n != 2 {
		t.Fatalf("%d TLS dials, want 2", n)
	}
}

func TestUpstreamFallsBackToPlainWithoutTLS(t *testing.T) {
	f := newResolverFixture(t, false, 0)
	resp, err := ask(t, f.upstream(false))
	if err != nil || len(resp.Answer) != 1 {
		t.Fatalf("opportunistic: %v %v", resp, err)
	}
	if _, err := ask(t, f.upstream(true)); err == nil {
		t.Fatal("strict mode answered over plain DNS")
	}
}

func TestUpstreamStrictRejectsAnUntrustedCertificate(t *testing.T) {
	f := newResolverFixture(t, true, 0)
	u := NewUpstream("127.0.0.1", f.dial, true)
	if _, err := ask(t, u); err == nil {
		t.Fatal("a resolver with an untrusted certificate was accepted")
	}
}

func TestHasTLS(t *testing.T) {
	if !HasTLS("1.1.1.1") || HasTLS("192.168.1.1") {
		t.Fatal("HasTLS")
	}
}

func TestUpstreamSetServerMovesTheNextQuery(t *testing.T) {
	f := newResolverFixture(t, false, 0)
	var mu sync.Mutex
	var dialed []string
	dial := func(ctx context.Context, network, address string) (net.Conn, error) {
		mu.Lock()
		dialed = append(dialed, address)
		mu.Unlock()
		return f.dial(ctx, network, address)
	}
	u := NewUpstream("192.0.2.1", dial, false)
	if _, err := ask(t, u); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	dialed = nil
	mu.Unlock()
	u.SetServer("192.0.2.2")
	if _, err := ask(t, u); err != nil {
		t.Fatal(err)
	}
	// TLS is tried again on the new resolver before plain DNS, as on a new one.
	want := []string{"192.0.2.2:853", "192.0.2.2:53"}
	mu.Lock()
	defer mu.Unlock()
	if len(dialed) != len(want) || dialed[0] != want[0] || dialed[1] != want[1] {
		t.Fatalf("dialed %v, want %v", dialed, want)
	}
	if u.Server() != "192.0.2.2" {
		t.Fatalf("Server() = %q", u.Server())
	}
}

func TestUpstreamAsksPlainlyWhenPort853IsDropped(t *testing.T) {
	f := newResolverFixture(t, false, 0)
	// A resolver that drops 853 rather than refusing it, as some networks do.
	dial := func(ctx context.Context, network, address string) (net.Conn, error) {
		if _, port, _ := net.SplitHostPort(address); port == "853" { //nolint:errcheck
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return f.dial(ctx, network, address)
	}
	resp, err := ask(t, NewUpstream("192.0.2.1", dial, false))
	if err != nil || len(resp.Answer) != 1 {
		t.Fatalf("first query after a dropped 853: %v %v", resp, err)
	}
}
