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
	"sync"
	"time"

	"github.com/armon/go-socks5"
	"github.com/sirupsen/logrus"
	"golang.org/x/net/proxy"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/proxyinterstitial"
	"github.com/skycoin/skywire/pkg/proxystatus"
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

	// StatusProvider, when non-nil, enables the reserved in-process status hosts
	// served through this proxy — a read-only diagnostic page (logs +
	// route/transport events + per-leg mux view) for a surface. Nil disables the
	// status hosts. See pkg/proxystatus.
	StatusProvider proxystatus.Provider
	// StatusSurface scopes which reserved status host THIS proxy layer owns and
	// answers for (e.g. SurfaceSkynet → only http(s)://status.skynet/). A status
	// host matching a DIFFERENT surface is NOT served here — it falls through so
	// the request continues up the proxy chain to the layer that owns it. Empty
	// means "serve any matched surface" (the pre-scoping behavior), kept so
	// standalone runtimes and tests need no wiring. Ignored when StatusProvider is
	// nil.
	StatusSurface proxystatus.Surface
}

// ownsStatusSurface reports whether this layer should answer for a matched
// status surface: true when no surface is configured (serve-any) or the matched
// surface is exactly the one this layer owns.
func (cfg Config) ownsStatusSurface(s proxystatus.Surface) bool {
	return cfg.StatusSurface == "" || cfg.StatusSurface == s
}

// statusSnapshot fetches the surface's snapshot from the provider, degrading a
// provider error to a rendered note rather than a failed page.
func statusSnapshot(cfg Config, surface proxystatus.Surface) proxystatus.Snapshot {
	snap, err := cfg.StatusProvider.StatusSnapshot(surface)
	if err != nil {
		return proxystatus.Snapshot{Surface: surface, Note: "status unavailable: " + err.Error()}
	}
	return snap
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
	// Build the upstream SOCKS5 dialer ONCE and reuse it across requests
	// (proxy.SOCKS5 returns a shareable proxy.Dialer). nil when no upstream
	// is configured — those requests dial direct.
	var upstream *upstreamForwarder
	if cfg.UpstreamSOCKS != "" {
		upstream = &upstreamForwarder{addr: cfg.UpstreamSOCKS}
	}
	conf := &socks5.Config{
		// Route go-socks5's own [ERR] lines through logrus so they match the
		// rest of the visor's log format (colored, module-tagged) instead of
		// the library's raw stdout "2006/01/02 ... [ERR] socks:" default.
		// Cap at Debug: a SOCKS proxy failing a *client-requested* dial is a
		// routine, client-driven condition, not a visor error.
		Logger:   logging.NewStdLoggerLevel(log, logrus.DebugLevel),
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

			// Reserved status host this layer OWNS (http://status.skynet/): served
			// in-process, never routed out — the read-only diagnostic page for the
			// surface. Checked before the .skynet match / upstream forward. A status
			// host for a DIFFERENT surface is NOT served here; it falls through to
			// the upstream forward so the request reaches the layer that owns it.
			// Plaintext HTTP only; the owned host is also served over TLS-MITM below.
			if cfg.StatusProvider != nil {
				_, sport, _ := net.SplitHostPort(addr) //nolint:errcheck
				if proxyinterstitial.ShouldServe(sport) {
					if surface, ok := proxystatus.Match(origHost); ok && cfg.ownsStatusSurface(surface) {
						log.WithField("surface", string(surface)).Debug("SOCKS5 → serving in-process proxy status page")
						return &tcpAddrConn{Conn: proxystatus.ServeConn(proxystatus.Render(statusSnapshot(cfg, surface)))}, nil
					}
				}
			}

			// Owned status host over HTTPS: status.skynet is within the resolver
			// CA's name constraints (.skynet), so a per-host leaf is valid.
			// Terminate the browser's TLS locally and serve the page over it — the
			// TLS analog of the plaintext branch above. Only for the surface this
			// layer owns; a different surface falls through.
			if cfg.StatusProvider != nil && cfg.TLSMITM {
				_, sport, _ := net.SplitHostPort(addr) //nolint:errcheck
				if isTLSPort(sport, cfg.TLSPort) {
					if surface, ok := proxystatus.Match(origHost); ok && cfg.ownsStatusSurface(surface) {
						leaf, lerr := cfg.LeafMinter.For(origHost)
						if lerr != nil {
							log.WithField("surface", string(surface)).WithField("err", lerr).
								Debug("SOCKS5 → status-over-TLS leaf mint failed; falling through")
						} else {
							log.WithField("surface", string(surface)).Debug("SOCKS5 → serving in-process proxy status page over TLS (MITM)")
							return &tcpAddrConn{Conn: skynetca.MITMTerminate(
								proxystatus.ServeConn(proxystatus.Render(statusSnapshot(cfg, surface))), leaf)}, nil
						}
					}
				}
			}

			// Transient-failure interstitial (see pkg/dmsgweb/runtime.go for the
			// full rationale): on a route-still-warming / upstream-not-ready
			// failure for a plaintext-HTTP request, serve a branded
			// auto-refreshing "building a route over skywire…" page in place of
			// a bare SOCKS error so the browser retries once the route is warm.
			// Raw-TLS (443) and non-HTTP ports fall through to the real error.
			// Runs before the panic-recover defer on a normal return; no-ops on
			// success or panic.
			defer func() {
				if retErr == nil || retConn != nil || !proxyinterstitial.IsTransient(retErr) {
					return
				}
				_, port, _ := net.SplitHostPort(addr) //nolint:errcheck
				target := origHost
				if target == "" {
					target = addr
				}
				switch {
				case proxyinterstitial.ShouldServe(port):
					// Stream REAL route-setup progress over a chunked response,
					// driving a fresh skynet dial via a probe and reloading into live
					// content once the route is warm; HTTP/1.0 clients fall back to the
					// one-shot page inside StreamConn. Wrapped in tcpAddrConn for
					// go-socks5's *net.TCPAddr BND assertion (the stream conn is a
					// net.Pipe). See pkg/dmsgweb/runtime.go for the rationale.
					log.WithField("host", target).WithField("err", retErr).
						Debug("SOCKS5 → serving streaming route interstitial")
					retConn, retErr = &tcpAddrConn{Conn: proxyinterstitial.StreamConn(context.Background(), proxyinterstitial.StreamConfig{
						Target:    target,
						Mechanism: "skynet",
						Probe:     skynetRedialProbe(dialer, cfg, upstream, origHost, addr),
					})}, nil
				case cfg.TLSMITM && isTLSPort(port, cfg.TLSPort) && skynetca.Permits(cfg.LeafMinter, origHost):
					// HTTPS request whose route is still warming: terminate the
					// browser's TLS locally with a per-host leaf and serve the
					// interstitial HTML over it (same MITM path as a warm TLS dial,
					// with the fixed interstitial responder as the "upstream").
					// Gated on Permits so a clearnet-HTTPS request (a host the
					// name-constrained CA can't cover) falls through to the real
					// error rather than logging a guaranteed "does not match
					// permitted suffix" mint failure. If minting still fails, fall
					// through to the real error.
					leaf, lerr := cfg.LeafMinter.For(origHost)
					if lerr != nil {
						log.WithField("host", target).WithField("err", lerr).
							Debug("SOCKS5 → TLS interstitial leaf mint failed; surfacing dial error")
						return
					}
					log.WithField("host", target).WithField("err", retErr).
						Debug("SOCKS5 → serving branded route interstitial over TLS (MITM)")
					retConn, retErr = &tcpAddrConn{Conn: skynetca.MITMTerminate(
						proxyinterstitial.Conn(target, proxyinterstitial.StatusLine(retErr), "skynet", false), leaf)}, nil
				}
			}()

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
			if upstream != nil {
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
				return upstream.dial(network, addr)
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

	// srv.Serve blocks and only ever returns a non-nil error.
	if err := srv.Serve(lis); !errors.Is(err, net.ErrClosed) {
		return fmt.Errorf("SOCKS5 serve: %w", err)
	}
	return nil
}

// upstreamCooldown is how long forwarded dials fast-fail after a recent
// upstream failure, so a burst of requests during the boot window (when the
// upstream, e.g. skysocks-client on :1080, is not yet connected and refuses
// the connection) doesn't each pay a full refused dial. Short by design —
// the client retries.
const upstreamCooldown = 500 * time.Millisecond

// upstreamForwarder lazily builds and caches a SOCKS5 dialer to the upstream
// proxy and reuses it across requests. proxy.SOCKS5 returns a proxy.Dialer
// that is safe to share, so building it once — instead of per request —
// avoids a wasteful allocation on every forwarded CONNECT. After a failed
// dial it fast-fails subsequent requests for upstreamCooldown rather than
// blocking or queueing them. This is skynetweb's readiness posture toward the
// upstream: there is no clean "skysocks-client connected" signal to wait on,
// so the cached dialer + cooldown stands in for one.
type upstreamForwarder struct {
	addr string

	mu       sync.Mutex
	dialer   proxy.Dialer
	failedAt time.Time
}

// dial forwards through the cached upstream dialer, building it on first use.
// It fast-fails during the cooldown window following a recent failure, and
// records/clears the failure timestamp based on the dial outcome.
func (f *upstreamForwarder) dial(network, addr string) (net.Conn, error) {
	f.mu.Lock()
	if !f.failedAt.IsZero() && time.Since(f.failedAt) < upstreamCooldown {
		f.mu.Unlock()
		return nil, fmt.Errorf("upstream SOCKS %s not ready (cooling down)", f.addr)
	}
	d := f.dialer
	if d == nil {
		nd, err := proxy.SOCKS5("tcp", f.addr, nil, proxy.Direct)
		if err != nil {
			f.mu.Unlock()
			return nil, err
		}
		f.dialer = nd
		d = nd
	}
	f.mu.Unlock()

	conn, err := d.Dial(network, addr)
	f.mu.Lock()
	if err != nil {
		f.failedAt = time.Now()
	} else {
		f.failedAt = time.Time{}
	}
	f.mu.Unlock()
	return conn, err
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

// isTLSPort reports whether the string port equals the configured TLS-MITM port.
func isTLSPort(port string, tlsPort uint16) bool {
	return port != "" && port == fmt.Sprintf("%d", tlsPort)
}

// skynetRedialProbe builds a proxyinterstitial.Probe that re-attempts the dial
// the streaming interstitial stands in for. For a .skynet target it re-drives a
// real skynet dial (route setup + handshake) via the same SkynetDialer and
// reports ready (nil) once it succeeds — a coarse but real signal, since the
// router exposes no per-hop setup event to observe. A non-.skynet target
// re-attempts the upstream/direct forward. The probe closes the opened
// conn immediately; the browser's reload then rides the now-warm route.
func skynetRedialProbe(dialer SkynetDialer, cfg Config, upstream *upstreamForwarder, origHost, addr string) proxyinterstitial.Probe {
	return func(ctx context.Context) error {
		if origHost != "" && isSkynetHost(origHost, cfg.DomainSuffix) {
			_, addrPort, _ := net.SplitHostPort(addr) //nolint:errcheck
			hostWithPort := origHost
			if addrPort != "" && addrPort != "80" {
				hostWithPort = origHost + ":" + addrPort
			}
			_, route, dest, hport, err := ParseResolverHost(hostWithPort, cfg.DomainSuffix, cfg.Aliases)
			if err != nil {
				return err
			}
			c, e := dialer.DialSkynet(ctx, dest, hport, route)
			if e == nil {
				_ = c.Close() //nolint:errcheck
			}
			return e
		}
		var (
			c net.Conn
			e error
		)
		if upstream != nil {
			c, e = upstream.dial("tcp", addr)
		} else {
			c, e = net.Dial("tcp", addr)
		}
		if e == nil {
			_ = c.Close() //nolint:errcheck
		}
		return e
	}
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
