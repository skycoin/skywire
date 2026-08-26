// Package dmsgweb pkg/dmsgweb/runtime.go c4-app-web
// `.dmsg` (and other configurable) domain suffixes. It exposes:
//
//   - a SOCKS5 proxy that intercepts hosts ending in the configured
//     DomainSuffix and tunnels them directly as DMSG streams
//   - a raw TCP bridge for fixed (pk, dmsgPort) → localhost mappings
//
// HTTP bridging was removed; raw TCP forwards bytes transparently and
// works for HTTP, WebSockets, server-sent events, chunked transfers,
// and any other protocol the dmsg target speaks. The previous HTTP
// bridge added URL rewriting and per-target connection pooling but no
// functional behavior that raw TCP doesn't already provide.
//
// The package accepts an externally-created *dmsg.Client so the
// runtime can be embedded into either the standalone `skywire dmsg
// web` command or a visor-hosted application (e.g. a resolver inside
// skysocks-client). Neither mode owns the client; lifecycle belongs
// to the caller.
package dmsgweb

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/armon/go-socks5"
	"github.com/chen3feng/safecast"
	"github.com/sirupsen/logrus"
	"golang.org/x/net/proxy"

	"github.com/skycoin/skywire/pkg/cipher"
	dmsg "github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/ioutil"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/proxyinterstitial"
	"github.com/skycoin/skywire/pkg/proxystatus"
	"github.com/skycoin/skywire/pkg/skynetca"
	"github.com/skycoin/skywire/pkg/skynetweb"
)

// DefaultDomainSuffix is the TLD treated as DMSG addresses when
// Config.DomainSuffix is empty. Kept here rather than in a CLI flag
// default so the visor app gets the same behavior.
const DefaultDomainSuffix = ".dmsg"

// proxyHost returns the SOCKS5 bind host, defaulting to loopback when unset so
// the proxy is never accidentally exposed to the network.
func proxyHost(addr string) string {
	if addr == "" {
		return "127.0.0.1"
	}
	return addr
}

// isTLSPort reports whether the string port equals the configured TLS-MITM port.
func isTLSPort(port string, tlsPort uint16) bool {
	p, err := strconv.ParseUint(strings.TrimSpace(port), 10, 16)
	return err == nil && uint16(p) == tlsPort
}

// Config configures a dmsgweb runtime. Two operating modes:
//
//  1. ResolveAddr empty → SOCKS5 resolver mode. Any hostname ending
//     in DomainSuffix is treated as <pk>.dmsg:<port> and tunneled
//     directly as a DMSG stream. The SOCKS5 proxy on ProxyPort
//     rewrites the dial target to localhost so curl/browsers reach
//     .dmsg sites transparently.
//
//  2. ResolveAddr non-empty → fixed mapping mode. Each entry maps to
//     WebPorts[i] and is served as a raw TCP tunnel to (pk, dmsgPort).
//     The SOCKS5 proxy is disabled — callers point their client
//     directly at 127.0.0.1:WebPorts[i]. Raw TCP transparently carries
//     HTTP, WebSockets, and any other protocol.
type Config struct {
	// DomainSuffix is the domain extension that the SOCKS5 resolver
	// treats as DMSG (e.g. ".dmsg"). Defaults to DefaultDomainSuffix.
	DomainSuffix string

	// WebPorts are the localhost TCP listener ports. Used only in mode 2
	// (fixed-mapping); ignored in SOCKS5 mode. One entry per ResolveAddr.
	WebPorts []uint

	// ProxyAddr is the host the SOCKS5 proxy listens on. Empty means
	// loopback ("127.0.0.1") — the safe default. Set to "0.0.0.0" or a
	// specific LAN IP to serve OTHER devices on the network, e.g. a board
	// acting as a `.dmsg` gateway for a home router's LAN. Binding a
	// non-loopback address exposes the proxy to the LAN; only do so on a
	// trusted network.
	ProxyAddr string
	// ProxyPort is the SOCKS5 proxy listener port. Zero disables the
	// SOCKS5 proxy (useful when the runtime is embedded inside an
	// app that already provides its own SOCKS5 front-end).
	ProxyPort uint

	// ResolveAddr is a parallel slice of (pk, dmsgPort) fixed targets.
	// Enables mode 2; must be the same length as WebPorts when set.
	ResolveAddr []DmsgTarget

	// UpstreamSOCKS, when non-empty, sends non-matching CONNECT
	// requests through this upstream SOCKS5 (e.g. "127.0.0.1:1080").
	// Matches the existing `--addproxy` CLI flag.
	UpstreamSOCKS string

	// Stats, when non-nil, is updated for every SOCKS5 dial. The visor
	// layer allocates one per resolver lifetime so counters persist
	// across Start/Stop cycles.
	Stats *Stats

	// TLSMITM enables on-the-fly TLS termination for browser
	// connections to <pk>.dmsg on the TLSPort, using LeafMinter to
	// produce per-host leaf certs signed by a locally-installed CA.
	// When false the runtime behaves exactly as before — pure SOCKS5
	// byte splice. See pkg/skynetca for the trust model.
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
	// dialing out over dmsg back to self (a wasteful, 202-prone round-trip).
	// Set false to force the full transport path — e.g. to test self-transports.
	SelfLoopback bool
	// SelfDial returns an in-process connection to the local service registered
	// on the given dmsg port. Used only when SelfLoopback short-circuits a
	// self-destined request. Nil disables the short-circuit.
	SelfDial func(port uint16) (net.Conn, error)
	// Aliases maps a destination label to a PK (e.g. "skywire" -> LocalPK), so
	// "skywire.dmsg" resolves exactly like "<pk>.dmsg".
	Aliases map[string]cipher.PubKey

	// DirectClient is an optional dmsg client that reaches servers by their
	// configured address rather than via discovery. A dest in DirectServerPKs
	// is dialed through it by self-rendezvous (EnsureAndObtainSession(dest) +
	// DialStream(dest:port)), which makes non-discovery dmsg SERVERS — they
	// serve /health etc. over dmsg but never register a client entry, so the
	// discovery client cannot resolve them — reachable by name. Nil disables
	// this path (dests fall through to the normal discovery dial).
	DirectClient *dmsg.Client
	// DirectServerPKs is the set of destination PKs to dial via DirectClient.
	DirectServerPKs map[cipher.PubKey]struct{}

	// StatusProvider, when non-nil, enables the reserved in-process status hosts
	// served through this proxy — a read-only diagnostic page (logs +
	// route/transport events + per-leg mux view) for a surface. Nil disables the
	// status hosts (they fall through to a normal resolve / upstream forward).
	// See pkg/proxystatus.
	StatusProvider proxystatus.Provider
	// StatusSurface scopes which reserved status host THIS proxy layer owns and
	// answers for (e.g. SurfaceDmsg → only http(s)://status.dmsg/). A status host
	// matching a DIFFERENT surface is NOT served here — it falls through so the
	// request continues up the proxy chain to the layer that owns it. Empty means
	// "serve any matched surface" (the pre-scoping behavior), kept so standalone
	// runtimes and tests need no wiring. Ignored when StatusProvider is nil.
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

// DmsgTarget is a (publicKey, dmsgPort) pair used in fixed-mapping mode.
type DmsgTarget struct {
	PK   cipher.PubKey
	Port uint16
}

// ParseDmsgTarget parses a "<pk>[:<port>]" string. If the port is
// omitted, it defaults to 80 (matching the existing CLI behavior).
func ParseDmsgTarget(s string) (DmsgTarget, error) {
	parts := strings.SplitN(s, ":", 2)
	if len(parts) < 1 || parts[0] == "" {
		return DmsgTarget{}, fmt.Errorf("invalid dmsg address %q: expected <pk>[:<port>]", s)
	}
	var pk cipher.PubKey
	if err := pk.Set(parts[0]); err != nil {
		return DmsgTarget{}, fmt.Errorf("invalid public key in %q: %w", s, err)
	}
	port := uint16(80)
	if len(parts) == 2 && parts[1] != "" {
		n, err := strconv.ParseUint(parts[1], 10, 16)
		if err != nil {
			return DmsgTarget{}, fmt.Errorf("invalid port in %q: %w", s, err)
		}
		port = uint16(n)
	}
	return DmsgTarget{PK: pk, Port: port}, nil
}

// Run starts the configured bridges and blocks until ctx is canceled.
// dmsgC must already be Ready(); its lifecycle is the caller's.
// Returns the first server error encountered, or ctx.Err() on a clean
// shutdown.
func Run(ctx context.Context, log *logging.Logger, dmsgC *dmsg.Client, cfg Config) error {
	if dmsgC == nil {
		return errors.New("dmsgweb: dmsg client is nil")
	}
	if log == nil {
		log = logging.MustGetLogger("dmsgweb")
	}
	cfg = normalize(cfg)
	if cfg.TLSMITM {
		if cfg.LeafMinter == nil {
			return errors.New("dmsgweb: TLSMITM requires LeafMinter")
		}
		if cfg.TLSPort == 0 {
			cfg.TLSPort = 443
		}
	}

	var wg sync.WaitGroup
	errCh := make(chan error, 1+len(cfg.ResolveAddr))

	// --- SOCKS5 resolver mode (no fixed-mapping) ---
	// The Dial callback returns DMSG streams directly as the
	// SOCKS5 tunnel — no intermediate HTTP bridge. Browser HTTP
	// bytes flow straight through the DMSG stream.
	if len(cfg.ResolveAddr) == 0 && cfg.ProxyPort != 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := serveSOCKS5Direct(ctx, log, dmsgC, cfg); err != nil && !errors.Is(err, context.Canceled) {
				errCh <- err
			}
		}()
	}

	// --- Fixed-mapping mode (--resolve) ---
	// Each --resolve entry is exposed as a raw TCP listener that pipes
	// bytes to the dmsg target. HTTP traffic works unchanged because
	// the bytes are forwarded transparently; no URL rewriting needed.
	if len(cfg.ResolveAddr) > 0 {
		for i := range cfg.ResolveAddr {
			wg.Add(1)
			i := i
			go func() {
				defer wg.Done()
				if err := serveTCP(ctx, log, dmsgC, cfg, i); err != nil && !errors.Is(err, net.ErrClosed) {
					errCh <- err
				}
			}()
		}
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		<-done
		return ctx.Err()
	case <-done:
		return nil
	}
}

// tcpAddrConn wraps a net.Conn to return *net.TCPAddr from LocalAddr
// and RemoteAddr. The go-socks5 library does an unchecked type
// assertion to *net.TCPAddr on the connection returned by the Dial
// callback; DMSG streams return dmsg.Addr which panics. This shim
// satisfies the assertion with a dummy loopback address.
type tcpAddrConn struct {
	net.Conn
}

func (c *tcpAddrConn) RemoteAddr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)}
}

func (c *tcpAddrConn) LocalAddr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)}
}

func normalize(cfg Config) Config {
	if cfg.DomainSuffix == "" {
		cfg.DomainSuffix = DefaultDomainSuffix
	}
	return cfg
}

// --- SOCKS5 ---

// serveSOCKS5Direct returns DMSG streams directly as the SOCKS5
// tunnel — no intermediate HTTP bridge. The go-socks5 library
// panics if RemoteAddr() doesn't return *net.TCPAddr, so DMSG
// streams are wrapped in tcpAddrConn.
func serveSOCKS5Direct(ctx context.Context, log *logging.Logger, dmsgC *dmsg.Client, cfg Config) error {
	// Build the upstream SOCKS5 dialer ONCE and reuse it across requests
	// (proxy.SOCKS5 returns a shareable proxy.Dialer). nil when no upstream
	// is configured — those requests dial direct.
	var upstream *upstreamForwarder
	if cfg.UpstreamSOCKS != "" {
		upstream = &upstreamForwarder{addr: cfg.UpstreamSOCKS}
	}
	conf := &socks5.Config{
		// Route go-socks5's own [ERR] lines through logrus so they match the
		// rest of the visor's log format instead of the library's raw stdout
		// default. Cap at Debug: a SOCKS proxy failing a *client-requested*
		// dial is a routine, client-driven condition, not a visor error.
		Logger:   logging.NewStdLoggerLevel(log, logrus.DebugLevel),
		Resolver: &dmsgResolver{cfg: cfg},
		Dial: func(dialCtx context.Context, network, addr string) (conn net.Conn, dialErr error) {
			defer func() {
				if r := recover(); r != nil {
					log.Errorf("PANIC in SOCKS5 dial: %v", r)
					dialErr = fmt.Errorf("internal error: %v", r)
					conn = nil
				}
			}()

			origHost, _ := dialCtx.Value(dmsgOrigHostKey).(string)
			_, origPort, err := net.SplitHostPort(addr) //nolint:errcheck
			if err != nil || origPort == "" {
				origPort = "80"
			}

			// Reserved status host this layer OWNS (http://status.dmsg/): served
			// in-process, never dialed out — the read-only diagnostic page for the
			// surface. Checked before suffix resolution / upstream forwarding. A
			// status host for a DIFFERENT surface is NOT served here; it falls
			// through to the upstream forward so the request reaches the layer that
			// owns it. Plaintext HTTP only (a status page can't ride a raw-TLS
			// tunnel); the owned host is also served over TLS-MITM below.
			if cfg.StatusProvider != nil && proxyinterstitial.ShouldServe(origPort) {
				if surface, ok := proxystatus.Match(origHost); ok && cfg.ownsStatusSurface(surface) {
					log.WithField("surface", string(surface)).Debug("SOCKS5 → serving in-process proxy status page")
					return &tcpAddrConn{Conn: proxystatus.ServeConn(proxystatus.Render(statusSnapshot(cfg, surface)))}, nil
				}
			}

			// Owned status host over HTTPS: status.dmsg is within the resolver CA's
			// name constraints (.dmsg), so a per-host leaf is valid. Terminate the
			// browser's TLS locally and serve the page over it — the TLS analog of
			// the plaintext branch above. Only for the surface this layer owns; a
			// different surface falls through to the upstream forward.
			if cfg.StatusProvider != nil && cfg.TLSMITM && isTLSPort(origPort, cfg.TLSPort) {
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

			// On a transient failure (dmsg route/session still warming, or a
			// not-yet-ready upstream like a booting skysocks-client) for a
			// plaintext-HTTP request, answer with a branded auto-refreshing
			// interstitial instead of a bare SOCKS error — the browser shows
			// "building a route over skywire…" and retries once the route is
			// warm, rather than a raw connection reset. Raw-TLS (443) and other
			// non-HTTP ports fall through to the real error (an HTML page can't
			// ride those). This defer runs before the panic-recover defer on a
			// normal error return, and no-ops on success (conn != nil).
			defer func() {
				if dialErr == nil || conn != nil || !proxyinterstitial.IsTransient(dialErr) {
					return
				}
				target := origHost
				if target == "" {
					target = addr
				}
				switch {
				case proxyinterstitial.ShouldServe(origPort):
					// Stream REAL route-setup progress: hold the browser open with a
					// chunked response and drive a fresh dial via a probe, flushing a
					// line per real attempt, then reload into live content once the
					// route is warm. Falls back to the one-shot page for an HTTP/1.0
					// client (handled inside StreamConn). Wrapped in tcpAddrConn: the
					// stream conn is a net.Pipe (pipeAddr), and go-socks5 asserts
					// *net.TCPAddr on the returned conn when building its BND reply.
					log.WithField("host", target).WithField("err", dialErr).
						Debug("SOCKS5 → serving streaming route interstitial")
					conn, dialErr = &tcpAddrConn{Conn: proxyinterstitial.StreamConn(context.Background(), proxyinterstitial.StreamConfig{
						Target:    target,
						Mechanism: "dmsg",
						Probe:     dmsgRedialProbe(dmsgC, upstream, cfg, origHost, addr, origPort),
					})}, nil
				case cfg.TLSMITM && isTLSPort(origPort, cfg.TLSPort) && skynetca.Permits(cfg.LeafMinter, origHost):
					// HTTPS request whose route is still warming: terminate the
					// browser's TLS locally with a per-host leaf and serve the
					// interstitial HTML over it — same MITM path as a warm TLS
					// dial, but the "upstream" is the fixed interstitial responder.
					// Gated on Permits so a clearnet-HTTPS request (whose host the
					// name-constrained CA can't cover) falls straight through to the
					// real error instead of a guaranteed "does not match permitted
					// suffix" mint failure on every attempt. If minting still fails,
					// fall through to the real error.
					leaf, lerr := cfg.LeafMinter.For(origHost)
					if lerr != nil {
						log.WithField("host", target).WithField("err", lerr).
							Debug("SOCKS5 → TLS interstitial leaf mint failed; surfacing dial error")
						return
					}
					log.WithField("host", target).WithField("err", dialErr).
						Debug("SOCKS5 → serving branded route interstitial over TLS (MITM)")
					conn, dialErr = &tcpAddrConn{Conn: skynetca.MITMTerminate(
						proxyinterstitial.Conn(target, proxyinterstitial.StatusLine(dialErr), "dmsg", false), leaf)}, nil
				}
			}()

			if _, ok := dialCtx.Value(dmsgResolverPortKey).(string); ok {
				// Reserved synthetic directory: serve the alias index in-process.
				// Never dials out and is not a real PK/alias, so intercept it
				// before host resolution (which would fail closed on "home").
				if isHomeHost(origHost, cfg.DomainSuffix) {
					log.Debug("SOCKS5 → resolver home page (in-process)")
					return &tcpAddrConn{Conn: serveHomeInProcess(cfg.Aliases, cfg.DomainSuffix, cfg.LocalPK)}, nil
				}

				vhost, route, dest, _, perr := skynetweb.ParseResolverHost(origHost, cfg.DomainSuffix, cfg.Aliases)
				if perr != nil {
					return nil, fmt.Errorf("invalid dmsg hostname %q: %w", origHost, perr)
				}
				port, err := strconv.ParseUint(origPort, 10, 16)
				if err != nil {
					return nil, fmt.Errorf("invalid port: %w", err)
				}

				// Self-lookup short-circuit: a request whose destination is THIS
				// visor is served from the local service in-process, rather than
				// dialing out over dmsg back to ourselves (a wasteful, 202-prone
				// round-trip). Disabled via SelfLoopback=false to exercise the
				// full self-transport path for testing.
				if cfg.SelfLoopback && cfg.SelfDial != nil && dest == cfg.LocalPK && len(route) == 0 {
					log.WithField("port", port).Debug("SOCKS5 → DMSG self-loopback (in-process)")
					c, derr := cfg.SelfDial(uint16(port))
					if derr != nil {
						return nil, derr
					}
					// Wrap so LocalAddr()/RemoteAddr() return *net.TCPAddr —
					// go-socks5 (request.go:194) does an unchecked assertion
					// when building the BND reply, AFTER this Dial callback
					// returns (so the recover above does not cover it), and
					// would panic on the raw net.Pipe conn otherwise.
					return &tcpAddrConn{Conn: c}, nil
				}

				dstAddr := dmsg.Addr{PK: dest, Port: uint16(port)}
				var stream net.Conn
				_, isDirectServer := cfg.DirectServerPKs[dest]
				switch {
				case isDirectServer && cfg.DirectClient != nil && len(route) == 0:
					// Non-discovery dmsg SERVER: dial it through the direct
					// client by self-rendezvous — connect to the server (the
					// direct client knows its address) and dial the server's
					// own listener over that session. The discovery client
					// can't reach it (no client entry registered).
					log.WithField("server", dest).WithField("port", port).Debug("SOCKS5 → DMSG direct server")
					ses, serr := cfg.DirectClient.EnsureAndObtainSession(ctx, dest)
					if serr != nil {
						return nil, fmt.Errorf("direct session to dmsg server %s: %w", dest, serr)
					}
					str, derr := ses.DialStream(ctx, dstAddr)
					if derr != nil {
						return nil, derr
					}
					stream = str
				case len(route) > 0:
					// Pinned rendezvous: dial the destination THROUGH the named
					// dmsg server's session instead of resolving it via
					// discovery. This lets a browser reach a direct/hidden client
					// by naming its server — <server-pk>.<client-pk>.dmsg (the
					// destination is the label adjacent to the suffix; routing PKs
					// precede it). A .dmsg address carries a single routing PK (the
					// server); if more are present the one nearest the dest wins.
					rl := route[len(route)-1]
					if rl.IsTpID {
						return nil, fmt.Errorf("dmsg address %q: routing label must be a dmsg server PK, not a transport ID", origHost)
					}
					serverPK := rl.PK
					log.WithField("server", serverPK).WithField("port", port).Debug("SOCKS5 → DMSG pinned via server")
					ses, serr := dmsgC.EnsureAndObtainSession(ctx, serverPK)
					if serr != nil {
						return nil, fmt.Errorf("pin via dmsg server %s: %w", serverPK, serr)
					}
					str, derr := ses.DialStream(ctx, dstAddr)
					if derr != nil {
						return nil, derr
					}
					stream = str
				default:
					log.WithField("port", port).Debug("SOCKS5 → DMSG direct")
					c, derr := dmsgC.Dial(ctx, dstAddr)
					if derr != nil {
						return nil, derr
					}
					stream = c
				}

				// Rewrite the Host header to the vhost (the labels before the
				// destination PK) so a vhost-capable backend (caddy/nginx/traefik)
				// serves the right site — the dmsg counterpart of the skynet
				// resolver's host-rewrite, which makes magnetosphere.net.<pk>.dmsg
				// reach the magnetosphere.net site. Only wrap a plaintext-HTTP
				// stream: skip on the TLS port with MITM off, where the browser
				// sends raw TLS that the HTTP parser would corrupt.
				if vhost != "" && (uint16(port) != cfg.TLSPort || cfg.TLSMITM) {
					stream = skynetweb.NewHostRewriteConn(stream, vhost)
				}

				// Optional TLS MITM: terminate the browser's TLS
				// session locally with a leaf cert for the host,
				// splicing plaintext to the merchant's plain-HTTP
				// server reachable over dmsg. The dmsg stream is
				// already authenticated by visor pubkey; the local
				// cert exists only to satisfy the browser's
				// secure-context machinery.
				if cfg.TLSMITM && uint16(port) == cfg.TLSPort {
					leaf, lerr := cfg.LeafMinter.For(origHost)
					if lerr != nil {
						_ = stream.Close() //nolint:errcheck,gosec
						return nil, fmt.Errorf("dmsg mitm leaf: %w", lerr)
					}
					return &tcpAddrConn{Conn: skynetca.MITMTerminate(stream, leaf)}, nil
				}

				return &tcpAddrConn{Conn: stream}, nil
			}

			// Not .dmsg — forward to upstream or direct.
			if upstream != nil {
				if origHost != "" {
					addr = net.JoinHostPort(origHost, origPort)
				}
				return upstream.dial(network, addr)
			}
			return net.Dial(network, addr)
		},
	}
	srv, err := socks5.New(conf)
	if err != nil {
		return fmt.Errorf("create SOCKS5 server: %w", err)
	}
	lisAddr := fmt.Sprintf("%s:%d", proxyHost(cfg.ProxyAddr), cfg.ProxyPort)
	log.WithField("addr", lisAddr).Debug("Serving SOCKS5 direct proxy")
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
	if err = srv.Serve(lis); !errors.Is(err, net.ErrClosed) {
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
// blocking or queueing them.
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

// dmsgRedialProbe builds a proxyinterstitial.Probe that re-attempts the dial the
// streaming interstitial stands in for, reporting the route ready (nil) once it
// succeeds. It is a READINESS probe: for a .dmsg destination it re-dials the
// destination directly (the common case) — a coarse but real signal, since
// pkg/router/dmsg expose no per-hop setup event to observe (see
// pkg/proxyinterstitial/stream.go). A non-.dmsg target re-attempts the
// upstream/direct forward. On success the opened stream is closed immediately;
// the browser's reload then rides the now-warm session/route.
func dmsgRedialProbe(dmsgC *dmsg.Client, upstream *upstreamForwarder, cfg Config, origHost, addr, origPort string) proxyinterstitial.Probe {
	return func(ctx context.Context) error {
		hostOnly := origHost
		if i := strings.IndexByte(hostOnly, ':'); i >= 0 {
			hostOnly = hostOnly[:i]
		}
		if strings.HasSuffix(hostOnly, cfg.DomainSuffix) {
			hp := origHost
			if origPort != "" && origPort != "80" {
				hp = origHost + ":" + origPort
			}
			_, _, dest, port, perr := skynetweb.ParseResolverHost(hp, cfg.DomainSuffix, cfg.Aliases)
			if perr != nil {
				return perr
			}
			c, e := dmsgC.Dial(ctx, dmsg.Addr{PK: dest, Port: port})
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

// Context keys used by the SOCKS5 resolver ↔ Dial callback handshake.
type (
	dmsgResolverPortKey_t struct{} // set when .dmsg matched → presence is the signal; value unused
	dmsgOrigHostKey_t     struct{} // always set → original hostname before resolution
)

var (
	dmsgResolverPortKey = dmsgResolverPortKey_t{}
	dmsgOrigHostKey     = dmsgOrigHostKey_t{}
)

// dmsgResolver implements socks5.NameResolver. Hostnames matching the
// configured domain suffix resolve to 127.0.0.1 with the bridge port
// annotated in context. Non-matching hostnames ALSO resolve to
// 127.0.0.1 (to prevent the library from doing a real DNS lookup on
// fantasy TLDs like .skynet), but the port key is NOT set — the Dial
// callback uses this absence to know "forward to upstream instead".
// The original hostname is always stored in context so the Dial
// callback can pass it through to an upstream SOCKS5 verbatim.
type dmsgResolver struct{ cfg Config }

func (r *dmsgResolver) Resolve(ctx context.Context, name string) (context.Context, net.IP, error) {
	// Always store the original hostname so the Dial callback can
	// forward it to an upstream SOCKS5 if this resolver doesn't
	// handle the TLD.
	ctx = context.WithValue(ctx, dmsgOrigHostKey, name)

	pattern := `\` + r.cfg.DomainSuffix + `(:[0-9]+)?$`
	match, _ := regexp.MatchString(pattern, name) //nolint:errcheck
	if match {
		// The Dial callback only checks presence of this key to know
		// "this is a .dmsg hostname" — the value is irrelevant.
		ctx = context.WithValue(ctx, dmsgResolverPortKey, "match")
	}
	// Always return 127.0.0.1 — prevents the library from doing a
	// DNS lookup that would fail for .dmsg / .skynet / any custom TLD.
	return ctx, net.ParseIP("127.0.0.1"), nil
}

// --- TCP bridge ---

func serveTCP(ctx context.Context, log *logging.Logger, dmsgC *dmsg.Client, cfg Config, idx int) error {
	port := cfg.WebPorts[idx]
	t := cfg.ResolveAddr[idx]
	lc := net.ListenConfig{}
	listener, err := lc.Listen(ctx, "tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return fmt.Errorf("TCP listen port %d: %w", port, err)
	}
	go func() {
		<-ctx.Done()
		_ = listener.Close() //nolint:errcheck
	}()
	log.WithField("port", port).WithField("dst", t.PK.Hex()).Debug("Serving TCP bridge")

	for {
		conn, err := listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) || ctx.Err() != nil {
				return nil
			}
			log.WithError(err).Debug("TCP accept failed")
			continue
		}
		go handleTCPConn(ctx, log, dmsgC, conn, t)
	}
}

func handleTCPConn(ctx context.Context, log *logging.Logger, dmsgC *dmsg.Client, conn net.Conn, t DmsgTarget) {
	defer ioutil.CloseQuietly(conn, log)
	dp, ok := safecast.To[uint16](uint(t.Port))
	if !ok {
		log.WithField("port", t.Port).Warn("port overflow in TCP bridge")
		return
	}
	dmsgConn, err := dmsgC.DialStream(ctx, dmsg.Addr{PK: t.PK, Port: dp})
	if err != nil {
		log.WithError(err).Warn("dmsg dial failed")
		return
	}
	defer ioutil.CloseQuietly(dmsgConn, log)

	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := io.Copy(dmsgConn, conn); err != nil {
			log.WithError(err).Debug("copy conn→dmsg ended")
		}
	}()
	if _, err := io.Copy(conn, dmsgConn); err != nil {
		log.WithError(err).Debug("copy dmsg→conn ended")
	}
	_ = conn.Close()     //nolint:errcheck
	_ = dmsgConn.Close() //nolint:errcheck
	<-done
}
