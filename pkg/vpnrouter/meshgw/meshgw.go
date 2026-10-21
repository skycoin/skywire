// Package meshgw pkg/vpnrouter/meshgw/meshgw.go c4-app-vpn
//
// meshgw is the vpn-router's "mesh gateway": it lets any LAN device behind the
// router reach dmsg/skynet services by name — e.g. `curl http://<pk>.dmsg` —
// with no per-device configuration and no SOCKS proxy.
//
// A .dmsg / .skynet name is NOT a normal hostname: it encodes a public key, and
// the destination is a routing port on that key reachable only over the mesh —
// there is no IP to route to and often no externally-listening TCP port at all.
// So the gateway can't just NAT it. Instead it works in two halves:
//
//  1. DNS: the router's DNS server (see pkg/vpnrouter) hands `*.dmsg` / `*.skynet`
//     queries to InstallDNS, which parses the name to a destination PK, leases a
//     SYNTHETIC IP from a private pool, remembers `synthetic-IP → (scheme, pk)`,
//     and answers with that IP. The client now has an address it can connect to.
//
//  2. Transparent proxy: the router REDIRECTs (iptables) TCP traffic destined for
//     the synthetic-IP pool to ServeTransparent. For each connection it reads the
//     ORIGINAL destination via SO_ORIGINAL_DST — the synthetic IP tells it which
//     PK, and the original destination PORT IS the mesh routing port to dial
//     (`<pk>:<port>`). It dials that over the mesh (the injected MeshDial) and
//     splices the two streams.
//
// This reuses the same name-parsing as dmsgweb (skynetweb.ParseResolverHost) and
// leaves the actual mesh dial to the caller (the vpn-router app wires it to its
// app.Client, which dials over dmsg/skynet). TLS-MITM for HTTPS to .dmsg hosts
// (à la dmsgweb + skynetca) and alias→PK resolution via service discovery are
// deliberate follow-ups; this is the plaintext/by-PK foundation.
package meshgw

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/proxystatus"
	"github.com/skycoin/skywire/pkg/skynetca"
	"github.com/skycoin/skywire/pkg/skynetweb"
)

// MeshDial dials a mesh service and returns a byte stream to splice with the
// LAN client's TCP connection. scheme is "dmsg" or "skynet"; dest is the target
// visor; port is the mesh routing port (taken from the client's original
// destination port). The vpn-router app implements this over its app.Client.
type MeshDial func(ctx context.Context, scheme string, dest cipher.PubKey, port uint16) (net.Conn, error)

// LocalDial reaches a name this visor serves itself, such as status.skywire,
// on port. It is what the resolving proxies answer for that name.
type LocalDial func(ctx context.Context, host string, port uint16) (net.Conn, error)

type target struct {
	scheme string // "dmsg" | "skynet" | "skywire"
	dest   cipher.PubKey
	host   string // set for a name the visor serves itself, reached by local
}

// Gateway resolves .dmsg/.skynet names to synthetic IPs and proxies TCP to them
// into mesh streams. Safe for concurrent use.
type Gateway struct {
	dial    MeshDial
	local   LocalDial                // optional: the visor's own names (status pages)
	aliases map[string]cipher.PubKey // optional friendly-name → PK
	log     logrus.FieldLogger

	// TLS-MITM (optional): when minter is non-nil, connections whose original
	// destination port is tlsPort are TLS-terminated here — the gateway presents
	// a leaf minted for the client's SNI and bridges plaintext to the plain-HTTP
	// mesh upstream. LAN clients must trust minter's CA. See EnableTLSMITM.
	minter  skynetca.LeafMinter
	tlsPort uint16

	// tlsUpstreamPort is the mesh port a TLS-terminated connection is sent to,
	// the visor's plain-HTTP port. Nothing listens on the mesh's 443.
	tlsUpstreamPort uint16

	mu    sync.Mutex
	pool  *ipPool
	byIP  map[[4]byte]target // synthetic IP → mesh target
	byKey map[string][4]byte // "<scheme>|<pk>" → synthetic IP (stable reuse)
}

// EnableTLSMITM turns on TLS termination for connections whose original
// destination port is tlsPort (default 443 when 0). The gateway answers the
// client's TLS handshake with a leaf minted by minter for the requested SNI
// (falling back to `<pk-label>.<scheme>` when no SNI is sent), and splices
// plaintext to the plain-HTTP mesh upstream — the dmsg/skynet counterpart of
// dmsgweb's browser MITM. LAN clients must trust minter's CA, whose name
// constraints must permit `.dmsg`/`.skynet` (skynetca.DefaultPermittedDomains
// does). Call before ServeTransparent.
func (g *Gateway) EnableTLSMITM(minter skynetca.LeafMinter, tlsPort uint16) {
	if tlsPort == 0 {
		tlsPort = 443
	}
	g.minter = minter
	g.tlsPort = tlsPort
	g.tlsUpstreamPort = 80
}

// SetLocal serves the reserved status hosts (status.dmsg, status.skynet,
// status.skywire, status.skysocks) through local instead of a mesh dial. Call
// before the gateway answers names.
func (g *Gateway) SetLocal(local LocalDial) { g.local = local }

// New builds a Gateway that leases synthetic IPs from cidr (e.g.
// "100.64.0.0/16" — a slice of shared/CGNAT space unlikely to collide with the
// LAN). dial performs the mesh dial; aliases (may be nil) maps friendly names to
// PKs for names that aren't a raw PK label.
func New(dial MeshDial, cidr string, aliases map[string]cipher.PubKey, log logrus.FieldLogger) (*Gateway, error) {
	if dial == nil {
		return nil, errors.New("meshgw: nil MeshDial")
	}
	if log == nil {
		log = logrus.New()
	}
	pool, err := newIPPool(cidr)
	if err != nil {
		return nil, err
	}
	return &Gateway{
		dial:    dial,
		aliases: aliases,
		log:     log,
		pool:    pool,
		byIP:    make(map[[4]byte]target),
		byKey:   make(map[string][4]byte),
	}, nil
}

// PoolCIDR reports the synthetic-IP range the router must REDIRECT into
// ServeTransparent (so router_linux can build the iptables rule).
func (g *Gateway) PoolCIDR() *net.IPNet { return g.pool.net }

// resolve leases (or reuses) a synthetic IP for a parsed name under scheme.
// Returns the IP to answer DNS with.
func (g *Gateway) resolve(scheme, name string) (net.IP, error) {
	var t target
	var key string
	if _, ok := proxystatus.Match(name); ok && g.local != nil && strings.HasSuffix(name, "."+scheme) {
		t, key = target{scheme: scheme, host: name}, "local|"+name
	} else {
		if scheme == "skywire" {
			return nil, fmt.Errorf("meshgw: %q is not a name this visor serves", name)
		}
		dest, err := g.parse(scheme, name)
		if err != nil {
			return nil, err
		}
		t, key = target{scheme: scheme, dest: dest}, scheme+"|"+dest.Hex()
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	if ip, ok := g.byKey[key]; ok {
		return net.IP(ip[:]).To4(), nil
	}
	ip4, err := g.pool.nextIP()
	if err != nil {
		return nil, err
	}
	g.byIP[ip4] = t
	g.byKey[key] = ip4
	g.log.WithField("name", name).WithField("ip", net.IP(ip4[:]).String()).
		WithField("scheme", scheme).Debug("mesh gateway: leased synthetic IP")
	return net.IP(ip4[:]).To4(), nil
}

// parse turns a query name (without trailing dot) into a destination PK, reusing
// dmsgweb's resolver-host grammar (raw hex PK, DNSLabel, or alias, plus optional
// route labels — routes are accepted but the direct dest is used here).
func (g *Gateway) parse(scheme, name string) (cipher.PubKey, error) {
	suffix := "." + scheme
	_, _, dest, _, err := skynetweb.ParseResolverHost(name, suffix, g.aliases)
	if err != nil {
		return cipher.PubKey{}, fmt.Errorf("meshgw: parse %q: %w", name, err)
	}
	return dest, nil
}

func (g *Gateway) lookup(ip net.IP) (target, bool) {
	v4 := ip.To4()
	if v4 == nil {
		return target{}, false
	}
	var k [4]byte
	copy(k[:], v4)
	g.mu.Lock()
	defer g.mu.Unlock()
	t, ok := g.byIP[k]
	return t, ok
}

// Resolve leases (or reuses) a synthetic IP for a .dmsg or .skynet name, as a
// DNS answer from InstallDNS would.
func (g *Gateway) Resolve(scheme, name string) (net.IP, error) {
	return g.resolve(scheme, normalizeName(name))
}

// DialIP dials port on the mesh target leased for ip.
func (g *Gateway) DialIP(ctx context.Context, ip net.IP, port uint16) (net.Conn, error) {
	t, ok := g.lookup(ip)
	if !ok {
		return nil, fmt.Errorf("meshgw: no mesh name holds %s", ip)
	}
	return g.dialTarget(ctx, t, port)
}

// dialTarget reaches t on port: the visor's own handler for a reserved name,
// otherwise the mesh.
func (g *Gateway) dialTarget(ctx context.Context, t target, port uint16) (net.Conn, error) {
	if t.host != "" {
		return g.local(ctx, t.host, port)
	}
	return g.dial(ctx, t.scheme, t.dest, port)
}

// ServeTransparent accepts REDIRECT'd TCP connections on lis and bridges each to
// its mesh target. Blocks until ctx is done or lis errors.
func (g *Gateway) ServeTransparent(ctx context.Context, lis net.Listener) error {
	go func() { <-ctx.Done(); _ = lis.Close() }() //nolint:errcheck // best-effort close on shutdown
	for {
		conn, err := lis.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		go g.handleConn(ctx, conn)
	}
}

func (g *Gateway) handleConn(ctx context.Context, conn net.Conn) {
	defer conn.Close() //nolint:errcheck // best-effort
	tc, ok := conn.(*net.TCPConn)
	if !ok {
		return
	}
	origIP, origPort, err := originalDst(tc)
	if err != nil {
		g.log.WithError(err).Debug("mesh gateway: no SO_ORIGINAL_DST")
		return
	}
	g.bridge(ctx, conn, origIP, origPort)
}

// Bridge carries conn to the mesh target leased for origIP, dialing origPort on
// it. For hosts that see the original destination directly, as a netstack does.
func (g *Gateway) Bridge(ctx context.Context, conn net.Conn, origIP net.IP, origPort uint16) {
	defer conn.Close() //nolint:errcheck // best-effort
	g.bridge(ctx, conn, origIP, origPort)
}

func (g *Gateway) bridge(ctx context.Context, conn net.Conn, origIP net.IP, origPort uint16) {
	t, ok := g.lookup(origIP)
	if !ok {
		g.log.WithField("ip", origIP.String()).Debug("mesh gateway: unknown synthetic IP")
		return
	}
	// HTTPS is terminated here and carried to the peer as plain HTTP, so it
	// goes to the peer's HTTP port rather than the 443 it was asked for.
	mitm := g.minter != nil && origPort == g.tlsPort
	dialPort := origPort
	if mitm {
		dialPort = g.tlsUpstreamPort
	}
	mesh, err := g.dialTarget(ctx, t, dialPort)
	if err != nil {
		g.log.WithError(err).WithField("dest", t.dest.Hex()).WithField("host", t.host).WithField("port", dialPort).
			Debug("mesh gateway: dial failed")
		return
	}
	defer mesh.Close() //nolint:errcheck // best-effort

	// HTTPS: terminate the client's TLS with a minted leaf and bridge plaintext
	// to the plain-HTTP mesh upstream (same model as dmsgweb's browser MITM).
	if mitm {
		g.spliceTLS(conn, mesh, t)
		return
	}
	splice(conn, mesh)
}

// spliceTLS TLS-terminates the LAN client's connection — minting a leaf for the
// client's SNI (or `<pk-label>.<scheme>` when none is offered) — and splices the
// decrypted stream to the plain-HTTP mesh upstream.
func (g *Gateway) spliceTLS(client, mesh net.Conn, t target) {
	fallback := t.dest.DNSLabel() + "." + t.scheme
	if t.host != "" {
		fallback = t.host
	}
	cfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
		GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			name := strings.ToLower(hello.ServerName)
			if name == "" {
				name = fallback
			}
			crt, err := g.minter.For(name)
			// An SNI the CA won't sign (e.g. an alias outside its name
			// constraints) falls back to the destination's own PK label.
			if err != nil && name != fallback {
				crt, err = g.minter.For(fallback)
			}
			return crt, err
		},
	}
	// tls.Server is lazy; the handshake runs on the first splice IO.
	splice(tls.Server(client, cfg), mesh)
}

// splice copies bytes both ways until either side closes.
func splice(a, b net.Conn) {
	done := make(chan struct{}, 2)
	cp := func(dst, src net.Conn) {
		_, _ = io.Copy(dst, src) //nolint:errcheck // best-effort byte pump
		if c, ok := dst.(interface{ CloseWrite() error }); ok {
			_ = c.CloseWrite() //nolint:errcheck // half-close so the peer sees EOF
		}
		done <- struct{}{}
	}
	go cp(a, b)
	go cp(b, a)
	<-done
	<-done
}

// normalizeName strips a trailing dot from a DNS QNAME.
func normalizeName(qname string) string {
	return strings.TrimSuffix(strings.ToLower(qname), ".")
}
