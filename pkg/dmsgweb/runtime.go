// Package dmsgweb implements a browser-facing resolving proxy for
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

	"github.com/armon/go-socks5"
	"github.com/chen3feng/safecast"
	"golang.org/x/net/proxy"

	"github.com/skycoin/skywire/pkg/cipher"
	dmsg "github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/ioutil"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/skynetca"
)

// DefaultDomainSuffix is the TLD treated as DMSG addresses when
// Config.DomainSuffix is empty. Kept here rather than in a CLI flag
// default so the visor app gets the same behavior.
const DefaultDomainSuffix = ".dmsg"

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
	conf := &socks5.Config{
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

			if _, ok := dialCtx.Value(dmsgResolverPortKey).(string); ok {
				pkLabel := strings.TrimSuffix(origHost, cfg.DomainSuffix)
				pk, err := parsePKLabel(pkLabel)
				if err != nil {
					return nil, fmt.Errorf("invalid PK in hostname %q: %w", origHost, err)
				}
				port, err := strconv.ParseUint(origPort, 10, 16)
				if err != nil {
					return nil, fmt.Errorf("invalid port: %w", err)
				}
				log.WithField("port", port).Debug("SOCKS5 → DMSG direct")
				stream, err := dmsgC.Dial(ctx, dmsg.Addr{PK: pk, Port: uint16(port)})
				if err != nil {
					return nil, err
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
			if cfg.UpstreamSOCKS != "" {
				if origHost != "" {
					addr = net.JoinHostPort(origHost, origPort)
				}
				upstream, err := proxy.SOCKS5("tcp", cfg.UpstreamSOCKS, nil, proxy.Direct)
				if err != nil {
					return nil, err
				}
				return upstream.Dial(network, addr)
			}
			return net.Dial(network, addr)
		},
	}
	srv, err := socks5.New(conf)
	if err != nil {
		return fmt.Errorf("create SOCKS5 server: %w", err)
	}
	lisAddr := fmt.Sprintf("127.0.0.1:%d", cfg.ProxyPort)
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
	err = srv.Serve(lis)
	if err != nil && !errors.Is(err, net.ErrClosed) {
		return fmt.Errorf("SOCKS5 serve: %w", err)
	}
	return nil
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

// parsePKLabel accepts either the legacy 66-char hex form or the
// 53-char base32 DNSLabel form. See pkg/cipher/dnslabel.go for the
// motivation: only the base32 form fits in a DNS label and an X.509
// Subject.CommonName, so TLS-MITM browser URLs must use it. Hex is
// kept accepted for backcompat with plain-HTTP URLs already in use.
func parsePKLabel(label string) (cipher.PubKey, error) {
	switch len(label) {
	case cipher.PubKeyDNSLabelLen: // 53 — base32
		return cipher.ParseDNSLabel(label)
	default:
		var pk cipher.PubKey
		if err := pk.Set(label); err != nil {
			return cipher.PubKey{}, err
		}
		return pk, nil
	}
}
