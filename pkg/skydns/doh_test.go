package skydns

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"net/netip"
	"testing"

	"github.com/miekg/dns"

	"github.com/skycoin/skywire/pkg/skynetca"
)

// newDoHEngine starts an engine with a name-constrained CA, the kind
// "skywire cli resolver ca gen" makes, and an HTTPS client that trusts it.
func newDoHEngine(t *testing.T, up Exchanger) *http.Client {
	t.Helper()
	ca, key, err := skynetca.GenerateCA(skynetca.CAOptions{})
	if err != nil {
		t.Fatal(err)
	}
	e, err := New(Config{Dial: (&meshDial{}).dial, Upstream: up, Minter: skynetca.NewMinter(ca, key, skynetca.LeafOptions{})})
	if err != nil {
		t.Fatal(err)
	}
	p := newPhone(t, e)
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	tr := &http.Transport{
		DialContext: func(_ context.Context, _, addr string) (net.Conn, error) {
			return p.dialTCP(t, netip.MustParseAddrPort(addr))
		},
		TLSClientConfig:   &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
		ForceAttemptHTTP2: true,
	}
	t.Cleanup(tr.CloseIdleConnections)
	return &http.Client{Transport: tr}
}

func dohQuery(t *testing.T, c *http.Client, method, name string) *dns.Msg {
	t.Helper()
	q := new(dns.Msg)
	q.SetQuestion(dns.Fqdn(name), dns.TypeA)
	q.Id = 0
	raw, err := q.Pack()
	if err != nil {
		t.Fatal(err)
	}
	var req *http.Request
	if method == http.MethodGet {
		req, err = http.NewRequest(method, DoHURL+"?dns="+base64.RawURLEncoding.EncodeToString(raw), nil)
	} else {
		req, err = http.NewRequest(method, DoHURL, bytes.NewReader(raw))
		req.Header.Set("Content-Type", dohType)
	}
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, name, err)
	}
	defer resp.Body.Close() //nolint:errcheck
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != dohType {
		t.Fatalf("%s %s: %s %q: %s", method, name, resp.Status, resp.Header.Get("Content-Type"), body)
	}
	m := new(dns.Msg)
	if err := m.Unpack(body); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestDoHAnswersMeshAndOtherNames(t *testing.T) {
	up := &fakeUpstream{}
	c := newDoHEngine(t, up)

	mesh := answerIP(t, dohQuery(t, c, http.MethodGet, testLabel+".skynet"))
	if !netip.MustParsePrefix(PoolCIDR).Contains(mesh) {
		t.Fatalf("mesh name over DoH got %s, outside %s", mesh, PoolCIDR)
	}
	if got := answerIP(t, dohQuery(t, c, http.MethodPost, "example.com")); got != netip.MustParseAddr("192.0.2.7") {
		t.Fatalf("upstream name over DoH got %s", got)
	}
	if len(up.names) != 1 || up.names[0] != "example.com." {
		t.Fatalf("upstream asked %v, want only example.com.", up.names)
	}
}

func TestDoHRejectsBadRequests(t *testing.T) {
	c := newDoHEngine(t, &fakeUpstream{})
	for _, tc := range []struct {
		method, url, ctype string
		want               int
	}{
		{http.MethodGet, DoHURL, "", http.StatusBadRequest},
		{http.MethodGet, DoHURL + "?dns=AAAA", "", http.StatusBadRequest},
		{http.MethodPost, DoHURL, "text/plain", http.StatusUnsupportedMediaType},
		{http.MethodPut, DoHURL, "", http.StatusMethodNotAllowed},
		{http.MethodGet, "https://" + ResolverAddr + "/other", "", http.StatusNotFound},
	} {
		req, err := http.NewRequest(tc.method, tc.url, nil)
		if err != nil {
			t.Fatal(err)
		}
		if tc.ctype != "" {
			req.Header.Set("Content-Type", tc.ctype)
		}
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close() //nolint:errcheck
		if resp.StatusCode != tc.want {
			t.Errorf("%s %s: %d, want %d", tc.method, tc.url, resp.StatusCode, tc.want)
		}
	}
}

func TestNoDoHWithoutACA(t *testing.T) {
	p, _ := newEngine(t, &fakeUpstream{})
	if conn, err := p.dialTCP(t, netip.AddrPortFrom(netip.MustParseAddr(ResolverAddr), 443)); err == nil {
		_ = conn.Close() //nolint:errcheck
		t.Fatal("port 443 accepted a connection with no CA configured")
	}
}

// The CA's IP constraint must cover the resolver's DoH address.
func TestCARangeIsSkyDNSRange(t *testing.T) {
	if skynetca.PermittedIPRange != Range {
		t.Fatalf("skynetca.PermittedIPRange %s, skydns.Range %s", skynetca.PermittedIPRange, Range)
	}
}
