// Package skynetweb is the `.skynet` counterpart to pkg/dmsgweb:
// a localhost SOCKS5 proxy + HTTP bridge that resolves
// `<pk>.skynet[:<port>]` hostnames by dialing the remote visor's
// skynet server over the skywire routing mesh and performing the
// skynet client handshake.
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
	"net/http"
	"net/http/httputil"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/confiant-inc/go-socks5"
	"golang.org/x/net/proxy"

	"github.com/skycoin/skywire/pkg/skynet"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
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
type SkynetDialer interface {
	DialSkynet(ctx context.Context, remote cipher.PubKey, port uint16) (net.Conn, error)
}

// PerformHandshake runs the skynet client-side handshake on an
// already-established connection: reads the server's ready byte,
// sends ClientMsg{Port: port, RawTCP: true}, and returns an error if
// the server replies with one. Exported so visor-layer adapters can
// call it without re-implementing the protocol.
func PerformHandshake(conn net.Conn, port uint16) error {
	// Server writes one byte when noise is fully established — wait
	// for it so the first client write doesn't race the handshake.
	readyBuf := make([]byte, 1)
	if _, err := conn.Read(readyBuf); err != nil {
		return fmt.Errorf("skynet handshake: read ready byte: %w", err)
	}

	req, err := json.Marshal(skynet.ClientMsg{Port: int(port), RawTCP: true})
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

// Config configures a skynetweb runtime. See pkg/dmsgweb.Config for
// the equivalent knobs on the DMSG side — the two are deliberately
// structurally similar so a future combined resolver (one SOCKS5,
// multiple TLD-keyed resolvers) can merge them cleanly.
type Config struct {
	// DomainSuffix is the TLD matched by the resolver (default ".skynet").
	DomainSuffix string
	// WebPort is the HTTP bridge listener port.
	WebPort uint
	// ProxyPort is the SOCKS5 listener port. Zero disables the
	// SOCKS5 front-end (useful when wrapped inside an outer
	// dispatcher).
	ProxyPort uint
	// UpstreamSOCKS forwards non-matching SOCKS5 CONNECTs to this
	// upstream (e.g. chain with skysocks-client for regular web
	// traffic).
	UpstreamSOCKS string

	// Stats, when non-nil, is updated for every HTTP-bridge request.
	// Optional; no collection happens when nil.
	Stats *Stats
}

// Run starts the SOCKS5 proxy + HTTP bridge. Blocks until ctx is
// canceled. Dialer is used for every matched hostname; callers own
// its lifecycle.
func Run(ctx context.Context, log *logging.Logger, dialer SkynetDialer, cfg Config) error {
	if dialer == nil {
		return errors.New("skynetweb: SkynetDialer is nil")
	}
	if log == nil {
		log = logging.MustGetLogger("skynetweb")
	}
	cfg = normalize(cfg)
	if cfg.WebPort == 0 {
		return errors.New("skynetweb: WebPort is required")
	}

	var wg sync.WaitGroup
	errCh := make(chan error, 2)

	if cfg.ProxyPort != 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := serveSOCKS5(ctx, log, cfg); err != nil && !errors.Is(err, net.ErrClosed) {
				errCh <- err
			}
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := serveHTTP(ctx, log, dialer, cfg); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

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

func normalize(cfg Config) Config {
	if cfg.DomainSuffix == "" {
		cfg.DomainSuffix = DefaultDomainSuffix
	}
	return cfg
}

// --- SOCKS5 ---

// The SOCKS5 resolver redirects matching hosts to the local HTTP
// bridge, same trick as dmsgweb. Non-matching traffic falls through
// to UpstreamSOCKS (if configured) or direct.

type skynetResolver struct{ cfg Config }

type resolverPortKey struct{}

func (r *skynetResolver) Resolve(ctx context.Context, name string) (context.Context, net.IP, error) {
	pattern := `\` + r.cfg.DomainSuffix + `(:[0-9]+)?$`
	match, _ := regexp.MatchString(pattern, name) //nolint:errcheck
	if !match {
		return ctx, nil, nil
	}
	ctx = context.WithValue(ctx, resolverPortKey{}, fmt.Sprintf("%d", r.cfg.WebPort))
	return ctx, net.ParseIP("127.0.0.1"), nil
}

func serveSOCKS5(ctx context.Context, log *logging.Logger, cfg Config) error {
	conf := &socks5.Config{
		Resolver: &skynetResolver{cfg: cfg},
		Dial: func(ctx context.Context, network, addr string) (net.Conn, error) {
			// Same pattern as dmsgweb: after resolution the addr is
			// "127.0.0.1:<origPort>", not the original hostname.
			// Check the context key the resolver set instead.
			if port, ok := ctx.Value(resolverPortKey{}).(string); ok && port != "" {
				addr = "localhost:" + port
				log.WithField("addr", addr).Debug("SOCKS5 dial (resolved .skynet)")
				return net.Dial(network, addr)
			}
			if cfg.UpstreamSOCKS != "" {
				up, err := proxy.SOCKS5("tcp", cfg.UpstreamSOCKS, nil, proxy.Direct)
				if err != nil {
					return nil, err
				}
				return up.Dial(network, addr)
			}
			log.WithField("addr", addr).Debug("SOCKS5 dial (direct)")
			return net.Dial(network, addr)
		},
	}
	srv, err := socks5.New(conf)
	if err != nil {
		return fmt.Errorf("create SOCKS5 server: %w", err)
	}
	addr := fmt.Sprintf("127.0.0.1:%d", cfg.ProxyPort)
	log.WithField("addr", addr).Debug("Serving SOCKS5 proxy")

	go func() {
		<-ctx.Done()
		srv.Close() //nolint:gosec
	}()

	return srv.ListenAndServe("tcp", addr)
}

// --- HTTP bridge ---

// serveHTTP listens on cfg.WebPort and translates incoming HTTP
// requests for `<pk>.skynet[:<port>]` into skynet dials.
//
// Unlike dmsgweb (which reuses dmsghttp.MakeHTTPTransport), skynet
// has no native HTTP client. We instead dial the route, handshake,
// and write the raw HTTP request bytes onto the resulting tunnel,
// then pipe the response back. Works for any plain HTTP/1.1 target;
// HTTPS-terminated-at-origin sites are out of scope (the remote would
// need to terminate TLS inside the tunnel, which isn't how skynet
// servers are configured today).
func serveHTTP(ctx context.Context, log *logging.Logger, dialer SkynetDialer, cfg Config) error {
	// Use a custom http.Transport that dials skynet connections.
	// This gives us connection pooling and keep-alive for free —
	// multiple concurrent HTTP requests to the same (pk, port)
	// reuse a single skynet route instead of each request trying
	// to establish its own route (which fails with "already being
	// initialized").
	transport := &http.Transport{
		DialContext: func(dialCtx context.Context, network, addr string) (net.Conn, error) {
			// addr is "host:port" where host is the target key from
			// the reverse proxy's Director.
			target, err := parseHostHeader(addr, "")
			if err != nil {
				return nil, fmt.Errorf("skynet transport dial: %w", err)
			}
			conn, err := dialer.DialSkynet(dialCtx, target.pk, target.port)
			if err != nil {
				return nil, fmt.Errorf("skynet dial: %w", err)
			}
			return conn, nil
		},
		MaxIdleConns:        10,
		MaxConnsPerHost:     1,
		MaxIdleConnsPerHost: 1,
		IdleConnTimeout:     300 * time.Second,
	}

	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			target, err := parseHostHeader(req.Host, cfg.DomainSuffix)
			if err != nil {
				log.WithError(err).Debug("reverse proxy: bad host")
				return
			}
			req.URL.Scheme = "http"
			req.URL.Host = fmt.Sprintf("%s:%d", target.pk.Hex(), target.port)
		},
		Transport: transport,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log.WithError(err).WithField("host", r.Host).Debug("skynet reverse proxy error")
			http.Error(w, fmt.Sprintf("skynet dial failed: %v", err), http.StatusBadGateway)
		},
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		done := cfg.Stats.RecordRequest()
		var reqErr error
		defer func() { done(reqErr) }()
		proxy.ServeHTTP(w, r)
	})

	srv := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.WebPort),
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.WithField("port", cfg.WebPort).Debug("Serving skynetweb HTTP bridge")

	go func() { //nolint:gosec // G118: shutdown must outlive parent ctx
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.WithError(err).Debug("HTTP shutdown error")
		}
	}()
	return srv.ListenAndServe()
}

type target struct {
	pk   cipher.PubKey
	port uint16
}

// parseHostHeader turns "<pk>.skynet[:<port>]" into (pk, port). Port
// defaults to 80 when absent to match conventional HTTP semantics.
func parseHostHeader(host, suffix string) (target, error) {
	hostParts := strings.SplitN(host, ":", 2)
	pkHost := strings.TrimSuffix(hostParts[0], suffix)
	var pk cipher.PubKey
	if err := pk.Set(pkHost); err != nil {
		return target{}, fmt.Errorf("invalid pk %q: %w", pkHost, err)
	}
	port := uint16(80)
	if len(hostParts) == 2 && hostParts[1] != "" {
		n, err := strconv.ParseUint(hostParts[1], 10, 16)
		if err != nil {
			return target{}, fmt.Errorf("invalid port: %w", err)
		}
		port = uint16(n)
	}
	return target{pk: pk, port: port}, nil
}
