// Package dmsgweb implements a browser-facing resolving proxy for
// `.dmsg` (and other configurable) domain suffixes. It exposes:
//
//   - a SOCKS5 proxy that intercepts hosts ending in the configured
//     DomainSuffix and routes them to an internal HTTP bridge
//   - the internal HTTP bridge, which translates HTTP requests into
//     DMSG HTTP client requests
//   - an optional raw TCP bridge, for tunneling non-HTTP protocols
//     to a fixed DMSG address:port
//
// The package accepts an externally-created *dmsg.Client so the
// runtime can be embedded into either the standalone `skywire dmsg
// web` command or a visor-hosted application (e.g. a resolver inside
// skysocks-client). Neither mode owns the client; lifecycle belongs
// to the caller.
//
// This is step 1 of moving .dmsg / .skynet browser support from a
// standalone utility into the visor process: the CLI is reduced to a
// config adapter over Run(), and a future visor app can call Run()
// directly without spawning a separate process.
package dmsgweb

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/chen3feng/safecast"
	"github.com/confiant-inc/go-socks5"
	"github.com/gin-gonic/gin"
	"golang.org/x/net/proxy"

	"github.com/skycoin/skywire/pkg/cipher"
	dmsg "github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsghttp"
	"github.com/skycoin/skywire/pkg/dmsg/ioutil"
	"github.com/skycoin/skywire/pkg/logging"
)

// DefaultDomainSuffix is the TLD treated as DMSG addresses when
// Config.DomainSuffix is empty. Kept here rather than in a CLI flag
// default so the visor app gets the same behavior.
const DefaultDomainSuffix = ".dmsg"

// Config configures a dmsgweb runtime. Two operating modes:
//
//  1. ResolveAddr empty → SOCKS5 resolver mode. Any hostname ending
//     in DomainSuffix is treated as <pk>.dmsg:<port> and bridged via
//     a single HTTP proxy on WebPorts[0]. The SOCKS5 proxy on
//     ProxyPort rewrites the dial target to localhost so curl /
//     browsers can reach .dmsg sites transparently.
//
//  2. ResolveAddr non-empty → fixed mapping mode. Each entry maps to
//     WebPorts[i] and is served either as HTTP (default) or raw TCP
//     (RawTCP[i] == true). The SOCKS5 proxy is disabled — callers
//     point their client directly at 127.0.0.1:WebPorts[i].
type Config struct {
	// DomainSuffix is the domain extension that the SOCKS5 resolver
	// treats as DMSG (e.g. ".dmsg"). Defaults to DefaultDomainSuffix.
	DomainSuffix string

	// WebPorts are the HTTP listener ports. In mode 1 only WebPorts[0]
	// is used; in mode 2 there is one listener per ResolveAddr entry.
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

	// RawTCP controls per-ResolveAddr tunneling mode: false = HTTP,
	// true = raw TCP. Length is normalised to match ResolveAddr.
	RawTCP []bool

	// Stats, when non-nil, is updated for every HTTP-bridge request.
	// The visor layer allocates one per resolver lifetime so counters
	// persist across Start/Stop cycles.
	Stats *Stats
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
	if len(cfg.ResolveAddr) > 0 {
		httpC := http.Client{Transport: dmsghttp.MakeHTTPTransport(ctx, dmsgC)}
		for i := range cfg.ResolveAddr {
			wg.Add(1)
			i := i
			go func() {
				defer wg.Done()
				if cfg.RawTCP[i] {
					if err := serveTCP(ctx, log, dmsgC, cfg, i); err != nil && !errors.Is(err, net.ErrClosed) {
						errCh <- err
					}
					return
				}
				if err := serveHTTP(ctx, log, &httpC, cfg, i); err != nil && !errors.Is(err, http.ErrServerClosed) {
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
	// Normalise RawTCP to match ResolveAddr length so callers of
	// RawTCP[i] are always safe.
	if n := len(cfg.ResolveAddr); len(cfg.RawTCP) < n {
		cfg.RawTCP = append(cfg.RawTCP, make([]bool, n-len(cfg.RawTCP))...)
	} else if len(cfg.RawTCP) > n {
		cfg.RawTCP = cfg.RawTCP[:n]
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
				pkHex := strings.TrimSuffix(origHost, cfg.DomainSuffix)
				var pk cipher.PubKey
				if err := pk.Set(pkHex); err != nil {
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
	go func() {
		<-ctx.Done()
		srv.Close() //nolint:gosec
	}()
	err = srv.ListenAndServe("tcp", lisAddr)
	if err != nil && !errors.Is(err, net.ErrClosed) {
		return fmt.Errorf("SOCKS5 listen: %w", err)
	}
	return nil
}

// Context keys used by the SOCKS5 resolver ↔ Dial callback handshake.
type (
	dmsgResolverPortKey_t struct{} // set when .dmsg matched → value is the bridge port string
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
		ctx = context.WithValue(ctx, dmsgResolverPortKey, fmt.Sprintf("%d", r.cfg.WebPorts[0]))
	}
	// Always return 127.0.0.1 — prevents the library from doing a
	// DNS lookup that would fail for .dmsg / .skynet / any custom TLD.
	return ctx, net.ParseIP("127.0.0.1"), nil
}

// --- HTTP bridge ---

func serveHTTP(ctx context.Context, log *logging.Logger, httpC *http.Client, cfg Config, idx int) error {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())

	r.Any("/*path", func(c *gin.Context) {
		// Bound request body so a hostile client cannot balloon
		// memory.
		const maxBodySize = 10 << 20
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBodySize)

		// Stats bookkeeping is no-op when cfg.Stats is nil.
		var reqErr error
		done := cfg.Stats.RecordRequest()
		defer func() { done(reqErr) }()

		urlStr := buildProxyURL(c, cfg, idx)
		log.WithField("method", c.Request.Method).WithField("url", urlStr).Debug("HTTP bridge")
		req, err := http.NewRequestWithContext(c.Request.Context(), c.Request.Method, urlStr, c.Request.Body)
		if err != nil {
			reqErr = err
			c.String(http.StatusInternalServerError, "failed to build request")
			return
		}
		for h, vs := range c.Request.Header {
			for _, v := range vs {
				req.Header.Add(h, v)
			}
		}
		resp, err := httpC.Do(req)
		if err != nil {
			reqErr = err
			c.String(http.StatusInternalServerError, "failed to reach dmsg target")
			log.WithError(err).Debug("dmsg HTTP error")
			return
		}
		defer ioutil.CloseQuietly(resp.Body, log)
		for h, vs := range resp.Header {
			for _, v := range vs {
				c.Writer.Header().Add(h, v)
			}
		}
		c.Status(resp.StatusCode)
		if _, err := io.Copy(c.Writer, resp.Body); err != nil {
			log.WithError(err).Debug("response copy failed")
			// Copy errors after headers are written are common
			// (client disconnect mid-stream) and don't warrant
			// flagging the whole request as failed — leave reqErr nil.
		}
		// 5xx from the remote counts as a failure for the UI even
		// though locally the bridge "succeeded". Stays below 4xx so
		// simple 404s from the remote don't pollute the error rate.
		if resp.StatusCode >= 500 {
			reqErr = fmt.Errorf("remote status %d", resp.StatusCode)
		}
	})

	port := cfg.WebPorts[0]
	if idx >= 0 {
		port = cfg.WebPorts[idx]
	}
	srv := &http.Server{
		Addr:              fmt.Sprintf(":%d", port),
		Handler:           r,
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.WithField("port", port).Debug("Serving HTTP bridge")

	// Context-driven shutdown; ListenAndServe returns ErrServerClosed
	// once Shutdown is called, which Run() treats as a clean exit.
	// We deliberately derive shutdownCtx from context.Background
	// (not ctx) because ctx is already canceled when we get here —
	// the whole point of a shutdown timeout is giving in-flight
	// requests a brief grace period after cancellation.
	go func() { //nolint:gosec // G118: intentional — shutdown must outlive parent ctx
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.WithError(err).Debug("HTTP shutdown error")
		}
	}()
	return srv.ListenAndServe()
}

func buildProxyURL(c *gin.Context, cfg Config, idx int) string {
	if idx >= 0 {
		// Fixed-mapping mode — target is known.
		t := cfg.ResolveAddr[idx]
		q := ""
		if c.Request.URL.RawQuery != "" {
			q = "?" + c.Request.URL.RawQuery
		}
		return fmt.Sprintf("dmsg://%s:%d%s%s", t.PK.Hex(), t.Port, c.Param("path"), q)
	}
	// Resolver mode — derive target from Host header. The suffix is
	// stripped so "somekey.dmsg:80" becomes "somekey".
	hostParts := strings.Split(c.Request.Host, ":")
	dmsgPort := "80"
	if len(hostParts) > 1 {
		dmsgPort = hostParts[1]
	}
	pkHost := strings.TrimSuffix(hostParts[0], cfg.DomainSuffix)
	q := ""
	if c.Request.URL.RawQuery != "" {
		q = "?" + c.Request.URL.RawQuery
	}
	return fmt.Sprintf("dmsg://%s:%s%s%s", pkHost, dmsgPort, c.Param("path"), q)
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
