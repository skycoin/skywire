// Package skynetweb pkg/skynetweb/runtime.go c4-app-skynet
// a localhost SOCKS5 proxy that resolves `<pk>.skynet[:<port>]`
// hostnames by dialing the remote visor's skynet server over the
// skywire routing mesh and performing the skynet client handshake.
//
// The SOCKS5 proxy dials skynet directly — no localhost HTTP bridge.
// The browser sends CONNECT, the proxy establishes a raw TCP tunnel
// through skynet, and bytes flow end-to-end.
//
// The package deliberately does not import pkg/router — route
// establishment is the visor's concern, so callers inject a
// SkynetDialer that wraps their own dialing primitive. In practice
// that's the visor's router (via pkg/visor/embedded_skynetweb.go),
// but the interface also lets tests and alternative consumers
// substitute a mock.
package skynetweb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"regexp"

	"github.com/armon/go-socks5"
	"golang.org/x/net/proxy"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/skynet"
	"github.com/skycoin/skywire/pkg/skynetca"
)

// DefaultDomainSuffix is the TLD treated as skynet addresses when
// Config.DomainSuffix is empty.
const DefaultDomainSuffix = ".skynet"

// SkynetDialer establishes a raw TCP tunnel to (remote, port) over
// the skywire routing mesh. The returned net.Conn must already have
// the skynet server's ready byte consumed and the client request
// (ClientMsg) / server reply (ServerReply) handshake completed — the
// caller then just pipes bytes through it.
//
// See pkg/visor/embedded_skynetweb.go for the visor-side adapter
// that fulfills this interface using router.DialRoutes + the handshake
// helper below (PerformHandshake).
//
// route is an optional explicit source-route to the destination, parsed from
// the hostname (e.g. <hop>.<dest>.skynet). Each element is a visor PK
// (any/auto transport) or a specific transport ID. When empty the dialer picks
// the path itself (direct transport, else route-finder). A non-empty route is
// single-path: no mux, and the reverse path mirrors the forward path.
type SkynetDialer interface {
	DialSkynet(ctx context.Context, remote cipher.PubKey, port uint16, route []RouteLabel) (net.Conn, error)
}

// PerformHandshake runs the skynet client-side handshake on an
// already-established connection: reads the server's ready byte,
// sends ClientMsg{Port: port}, and returns an error if the server
// replies with one. Exported so visor-layer adapters can call it
// without re-implementing the protocol.
func PerformHandshake(conn net.Conn, port uint16) error {
	// Server writes one byte when noise is fully established — wait
	// for it so the first client write doesn't race the handshake.
	readyBuf := make([]byte, 1)
	if _, err := conn.Read(readyBuf); err != nil {
		return fmt.Errorf("skynet handshake: read ready byte: %w", err)
	}

	req, err := json.Marshal(skynet.ClientMsg{Port: int(port)})
	if err != nil {
		return fmt.Errorf("skynet handshake: marshal request: %w", err)
	}
	if _, err := conn.Write(req); err != nil {
		return fmt.Errorf("skynet handshake: send request: %w", err)
	}

	respBuf := make([]byte, 32*1024)
	n, err := conn.Read(respBuf)
	if err != nil {
		return fmt.Errorf("skynet handshake: read reply: %w", err)
	}
	var reply skynet.ServerReply
	if err := json.Unmarshal(respBuf[:n], &reply); err != nil {
		return fmt.Errorf("skynet handshake: parse reply: %w", err)
	}
	if reply.Error != nil {
		return fmt.Errorf("skynet server error: %s", *reply.Error)
	}
	return nil
}

// Config configures a skynetweb runtime.
type Config struct {
	// DomainSuffix is the TLD matched by the resolver (default ".skynet").
	DomainSuffix string
	// ProxyAddr is the host the SOCKS5 proxy listens on. Empty means loopback
	// ("127.0.0.1"). Set to "0.0.0.0" or a LAN IP to serve other devices on
	// the network (a `.skynet` gateway for a home router's LAN). Only bind a
	// non-loopback address on a trusted network.
	ProxyAddr string
	// ProxyPort is the SOCKS5 listener port.
	ProxyPort uint
	// UpstreamSOCKS forwards non-matching SOCKS5 CONNECTs to this
	// upstream (e.g. chain with skysocks-client for regular web
	// traffic).
	UpstreamSOCKS string

	// Stats, when non-nil, is updated for every request.
	// Optional; no collection happens when nil.
	Stats *Stats

	// TLSMITM enables on-the-fly TLS termination for browser
	// connections to <pk>.skynet on the TLSPort, using LeafMinter to
	// produce per-host leaf certs signed by a locally-installed CA.
	// When false the runtime behaves exactly as before — pure SOCKS5
	// byte splice. See pkg/skynetca for the trust model and the
	// security argument for why TLS is terminated locally rather than
	// end-to-end.
	TLSMITM bool
	// TLSPort selects the destination port treated as TLS for MITM.
	// Defaults to 443. Ignored when TLSMITM is false.
	TLSPort uint16
	// LeafMinter mints per-host leaf certs. Required when TLSMITM
	// is true; ignored otherwise.
	LeafMinter skynetca.LeafMinter

	// LocalPK is this visor's public key, used to detect self-lookups for the
	// SelfLoopback short-circuit.
	LocalPK cipher.PubKey
	// SelfLoopback, when true (the default), serves a request whose destination
	// is LocalPK from the local service in-process via SelfDial, instead of
	// routing out over skynet back to ourselves. Set false to force the full
	// self-route path — e.g. to test self-transports (a legitimate use case).
	SelfLoopback bool
	// SelfDial returns an in-process connection to the local service registered
	// on the given port. Used only when SelfLoopback short-circuits a
	// self-destined request. Nil disables the short-circuit.
	SelfDial func(port uint16) (net.Conn, error)
	// Aliases maps a destination label to a PK (e.g. "skywire" -> LocalPK), so
	// "skywire.skynet" resolves exactly like "<pk>.skynet".
	Aliases map[string]cipher.PubKey
}

// Run starts the SOCKS5 proxy. Blocks until ctx is canceled.
// The dialer is called directly for every .skynet hostname — no
// localhost HTTP bridge is involved.
func Run(ctx context.Context, log *logging.Logger, dialer SkynetDialer, cfg Config) error {
	if dialer == nil {
		return errors.New("skynetweb: SkynetDialer is nil")
	}
	if log == nil {
		log = logging.MustGetLogger("skynetweb")
	}
	if cfg.DomainSuffix == "" {
		cfg.DomainSuffix = DefaultDomainSuffix
	}
	if cfg.ProxyPort == 0 {
		return errors.New("skynetweb: ProxyPort is required")
	}
	if cfg.TLSMITM {
		if cfg.LeafMinter == nil {
			return errors.New("skynetweb: TLSMITM requires LeafMinter")
		}
		if cfg.TLSPort == 0 {
			cfg.TLSPort = 443
		}
	}

	return serveSOCKS5(ctx, log, dialer, cfg)
}

// --- SOCKS5 ---

// skynetOrigHostKey stores the original hostname in context so the
// Dial callback can parse it (the SOCKS5 library resolves the name
// to an IP before calling Dial, losing the hostname).
type skynetOrigHostKey struct{}

type skynetResolver struct {
	cfg Config
}

func (r *skynetResolver) Resolve(ctx context.Context, name string) (context.Context, net.IP, error) {
	// Always store the original hostname for the Dial callback.
	ctx = context.WithValue(ctx, skynetOrigHostKey{}, name)

	// Return 127.0.0.1 for all hostnames to prevent the library from
	// doing a real DNS lookup (which fails for fantasy TLDs like .skynet).
	// The Dial callback uses the original hostname from context to either
	// dial skynet (for .skynet) or forward to the upstream SOCKS5.
	return ctx, net.ParseIP("127.0.0.1"), nil
}

func serveSOCKS5(ctx context.Context, log *logging.Logger, dialer SkynetDialer, cfg Config) error {
	conf := &socks5.Config{
		// Route go-socks5's own [ERR] lines through logrus so they match the
		// rest of the visor's log format (colored, module-tagged) instead of
		// the library's raw stdout "2006/01/02 ... [ERR] socks:" default.
		Logger:   logging.NewStdLogger(log),
		Resolver: &skynetResolver{cfg: cfg},
		Dial: func(dialCtx context.Context, network, addr string) (retConn net.Conn, retErr error) {
			// Fail the single request instead of crashing the whole visor if
			// anything in the dial path panics. go-socks5 invokes this from its
			// per-connection serve goroutine with no recover of its own, so an
			// unguarded panic here (e.g. a nil cfg.Stats / cfg.LeafMinter on a
			// partially-populated config, or a resolver edge case) propagates
			// up and takes the process down.
			defer func() {
				if rec := recover(); rec != nil {
					log.WithField("panic", rec).Error("recovered panic in SOCKS5 dial — failing the request, not crashing the visor")
					retConn = nil
					retErr = fmt.Errorf("skynet dial: recovered panic: %v", rec)
				}
			}()

			origHost, _ := dialCtx.Value(skynetOrigHostKey{}).(string)

			// Check if hostname matches .skynet suffix.
			if origHost != "" && isSkynetHost(origHost, cfg.DomainSuffix) {
				// The SOCKS5 library strips the port from the hostname
				// before passing to the resolver. Reconstruct it from
				// addr (which has the resolved IP + original port).
				_, addrPort, _ := net.SplitHostPort(addr) //nolint:errcheck
				hostWithPort := origHost
				if addrPort != "" && addrPort != "80" {
					hostWithPort = origHost + ":" + addrPort
				}
				vhost, route, dest, hport, err := ParseResolverHost(hostWithPort, cfg.DomainSuffix, cfg.Aliases)
				if err != nil {
					return nil, fmt.Errorf("skynet dial: %w", err)
				}

				// Self-lookup short-circuit: serve a request destined for THIS
				// visor from the local service in-process instead of routing out
				// over skynet back to ourselves. SelfLoopback=false forces the
				// full self-route path (a valid self-transport test).
				if cfg.SelfLoopback && cfg.SelfDial != nil && dest == cfg.LocalPK && len(route) == 0 {
					log.WithField("port", hport).Debug("SOCKS5 → skynet self-loopback (in-process)")
					c, derr := cfg.SelfDial(hport)
					if derr != nil {
						return nil, derr
					}
					// Wrap so LocalAddr()/RemoteAddr() return *net.TCPAddr —
					// go-socks5 (request.go:194) does an unchecked assertion
					// when building the BND reply and would panic on the raw
					// net.Pipe conn's pipeAddr otherwise.
					return &tcpAddrConn{Conn: c}, nil
				}

				done := cfg.Stats.RecordRequest()
				log.WithField("pk", dest.Hex()).
					WithField("port", hport).
					WithField("hops", len(route)).
					Debug("SOCKS5 → skynet")

				conn, err := dialer.DialSkynet(dialCtx, dest, hport, route)
				if err != nil {
					done(err)
					return nil, fmt.Errorf("skynet dial: %w", err)
				}
				// done() is recorded at the ACTUAL outcome paths below, not
				// here: the request is not truly successful until the full
				// conn stack (host-rewrite + optional MITM-leaf mint) is
				// built. Recording done(nil) right after the dial mis-counted
				// a request that then failed at MITM-leaf minting as a success.

				// Build the conn stack inside-out. The raw skywire
				// conn is the innermost layer. Optional host-rewrite
				// wraps it next (so the rewriter sees plaintext
				// HTTP). Optional MITM TLS terminator is the outer
				// layer (so the browser sees a TLS server).
				//
				// Order rationale:
				//
				//   Browser  ──TLS──► MITMTerminate ──plaintext──► hostRewriteConn ──plaintext──► raw conn ──► backend
				//
				// hostRewriteConn parses HTTP/1.1 between MITM
				// decryption and the wire. It must only wrap a stream
				// that is actually plaintext HTTP: a non-TLS port, or
				// the TLS port WITH MITM (which decrypts to plaintext
				// first). On the TLS port with MITM off, the browser
				// sends raw TLS — feeding that to the HTTP parser
				// corrupts the stream and kills the connection.
				stack := conn
				if vhost != "" && (hport != cfg.TLSPort || cfg.TLSMITM) {
					// `subdomain` already encodes the operator's
					// intent (the URL has labels before the PK).
					// Use it verbatim as the rewritten Host.
					stack = NewHostRewriteConn(stack, vhost)
					log.WithField("pk", dest.Hex()).
						WithField("rewrite_host", vhost).
						Debug("SOCKS5 → skynet host-rewrite active")
				}

				// Optional TLS MITM: terminate the browser's TLS
				// session locally with a leaf cert for the host,
				// splicing plaintext to the merchant's plain-HTTP
				// server reachable over the skywire transport. The
				// underlying skywire conn is already authenticated
				// by visor pubkey; the local cert exists only to
				// satisfy the browser's secure-context machinery.
				if cfg.TLSMITM && hport == cfg.TLSPort {
					leaf, lerr := cfg.LeafMinter.For(origHost)
					if lerr != nil {
						_ = stack.Close() //nolint:errcheck,gosec
						done(lerr)
						return nil, fmt.Errorf("skynet mitm leaf: %w", lerr)
					}
					stack = skynetca.MITMTerminate(stack, leaf)
				}

				done(nil)
				return &tcpAddrConn{Conn: stack}, nil
			}

			// Not .skynet — forward to upstream or direct.
			if cfg.UpstreamSOCKS != "" {
				// Reconstruct host:port using the original hostname
				// (the resolved addr has 127.0.0.1 instead of the hostname).
				if origHost != "" {
					_, port, _ := net.SplitHostPort(addr) //nolint:errcheck
					if port == "" {
						port = "443"
					}
					addr = net.JoinHostPort(origHost, port)
				}
				log.WithField("addr", addr).Debug("SOCKS5 → upstream")
				up, err := proxy.SOCKS5("tcp", cfg.UpstreamSOCKS, nil, proxy.Direct)
				if err != nil {
					return nil, err
				}
				return up.Dial(network, addr)
			}
			return net.Dial(network, addr)
		},
	}
	srv, err := socks5.New(conf)
	if err != nil {
		return fmt.Errorf("create SOCKS5 server: %w", err)
	}
	host := cfg.ProxyAddr
	if host == "" {
		host = "127.0.0.1"
	}
	lisAddr := fmt.Sprintf("%s:%d", host, cfg.ProxyPort)
	log.WithField("addr", lisAddr).Info("Serving skynetweb SOCKS5 proxy")

	// Open the listener ourselves so we can close it on ctx cancel —
	// armon/go-socks5's Serve returns when the listener is closed.
	lis, err := net.Listen("tcp", lisAddr)
	if err != nil {
		return fmt.Errorf("SOCKS5 listen: %w", err)
	}
	go func() {
		<-ctx.Done()
		_ = lis.Close() //nolint:errcheck
	}()

	if err := srv.Serve(lis); err != nil && !errors.Is(err, net.ErrClosed) {
		return fmt.Errorf("SOCKS5 serve: %w", err)
	}
	return nil
}

// tcpAddrConn wraps a net.Conn so that LocalAddr/RemoteAddr return
// *net.TCPAddr. The go-socks5 library does a type assertion to
// *net.TCPAddr in handleConnect; skynet connections return routing.Addr
// which causes a panic without this wrapper.
type tcpAddrConn struct {
	net.Conn
}

func (c *tcpAddrConn) RemoteAddr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0}
}

func (c *tcpAddrConn) LocalAddr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0}
}

func isSkynetHost(host, suffix string) bool {
	pattern := `\` + suffix + `(:[0-9]+)?$`
	match, _ := regexp.MatchString(pattern, host) //nolint:errcheck
	return match
}

// parsePKLabel accepts either the legacy 66-char hex form or the
// 53-char base32 DNSLabel form. Base32 is the canonical form going
// forward — it's the only one that fits in a DNS label (RFC 1035, 63
// octets) and an X.509 Subject.CommonName (X.520 ub-common-name, 64),
// so TLS MITM URLs must use it. Hex remains accepted because plain-
// HTTP skynet URLs minted before this change are in circulation.
func parsePKLabel(label string) (cipher.PubKey, error) {
	switch len(label) {
	case cipher.PubKeyDNSLabelLen: // 53 — base32
		return cipher.ParseDNSLabel(label)
	default: // hex (66) or anything else — let PubKey.Set complain
		var pk cipher.PubKey
		if err := pk.Set(label); err != nil {
			return cipher.PubKey{}, err
		}
		return pk, nil
	}
}
