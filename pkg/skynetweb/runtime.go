// Package skynetweb is the `.skynet` counterpart to pkg/dmsgweb:
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
	"net/http"
	"net/http/httputil"
	"regexp"
	"strconv"
	"strings"
	"sync"

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

// Config configures a skynetweb runtime.
type Config struct {
	// DomainSuffix is the TLD matched by the resolver (default ".skynet").
	DomainSuffix string
	// ProxyPort is the SOCKS5 listener port.
	ProxyPort uint
	// UpstreamSOCKS forwards non-matching SOCKS5 CONNECTs to this
	// upstream (e.g. chain with skysocks-client for regular web
	// traffic).
	UpstreamSOCKS string

	// Stats, when non-nil, is updated for every request.
	// Optional; no collection happens when nil.
	Stats *Stats
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
	// Skynet transport for HTTP: pools connections per (PK, port) so
	// multiple browser requests reuse one skynet route instead of each
	// trying DialRoutes (which fails with "already being initialized").
	skynetTransport := &http.Transport{
		DialContext: func(dialCtx context.Context, network, addr string) (net.Conn, error) {
			t, err := parseHostHeader(addr, "")
			if err != nil {
				return nil, fmt.Errorf("skynet transport dial: %w", err)
			}
			log.WithField("pk", t.pk.Hex()[:16]+"...").
				WithField("port", t.port).
				WithField("addr", addr).
				Debug("skynet transport: dialing")
			conn, err := dialer.DialSkynet(dialCtx, t.pk, t.port)
			if err != nil {
				return nil, fmt.Errorf("skynet dial: %w", err)
			}
			return &tcpAddrConn{Conn: conn}, nil
		},
		MaxIdleConns:        10,
		MaxConnsPerHost:     0, // unlimited — WebSocket upgrades hold a conn
		MaxIdleConnsPerHost: 2,
		IdleConnTimeout:     0, // keep routes alive
	}

	conf := &socks5.Config{
		Resolver: &skynetResolver{cfg: cfg},
		Dial: func(dialCtx context.Context, network, addr string) (net.Conn, error) {
			origHost, _ := dialCtx.Value(skynetOrigHostKey{}).(string)

			// Check if hostname matches .skynet suffix.
			if origHost != "" && isSkynetHost(origHost, cfg.DomainSuffix) {
				target, err := parseHostHeader(origHost, cfg.DomainSuffix)
				if err != nil {
					return nil, fmt.Errorf("skynet dial: %w", err)
				}

				done := cfg.Stats.RecordRequest()
				log.WithField("pk", target.pk.Hex()[:16]+"...").
					WithField("port", target.port).
					Debug("SOCKS5 → skynet")

				// Use an in-process HTTP reverse proxy that reuses
				// skynet connections via the pooling transport.
				// The SOCKS5 CONNECT model (one conn per request)
				// conflicts with skynet routing (one route per dest),
				// so we pipe the browser's TCP through a local
				// net.Pipe and let the reverse proxy handle it.
				clientConn, serverConn := net.Pipe()
				go func() {
					defer serverConn.Close() //nolint:errcheck
					proxyTarget := fmt.Sprintf("%s:%d", target.pk.Hex(), target.port)
					rp := &httputil.ReverseProxy{
						Director: func(req *http.Request) {
							req.URL.Scheme = "http"
							req.URL.Host = proxyTarget
						},
						Transport: skynetTransport,
						ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
							log.WithError(err).Warn("skynet reverse proxy error")
							http.Error(w, fmt.Sprintf("skynet: %v", err), http.StatusBadGateway)
						},
					}
					srv := &http.Server{Handler: rp}                     //nolint:gosec
					_ = srv.Serve(&singleConnListener{conn: serverConn}) //nolint:errcheck
					done(nil)
				}()
				return &tcpAddrConn{Conn: clientConn}, nil
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
	lisAddr := fmt.Sprintf("127.0.0.1:%d", cfg.ProxyPort)
	log.WithField("addr", lisAddr).Info("Serving skynetweb SOCKS5 proxy")

	go func() {
		<-ctx.Done()
		srv.Close() //nolint:gosec
	}()

	return srv.ListenAndServe("tcp", lisAddr)
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

// singleConnListener yields exactly one connection then blocks until Close.
type singleConnListener struct {
	conn net.Conn
	once sync.Once
	ch   chan struct{}
}

func (l *singleConnListener) Accept() (net.Conn, error) {
	var conn net.Conn
	l.once.Do(func() {
		conn = l.conn
		l.ch = make(chan struct{})
	})
	if conn != nil {
		return conn, nil
	}
	<-l.ch
	return nil, net.ErrClosed
}
func (l *singleConnListener) Close() error {
	if l.ch != nil {
		select {
		case <-l.ch:
		default:
			close(l.ch)
		}
	}
	return nil
}
func (l *singleConnListener) Addr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0}
}

func isSkynetHost(host, suffix string) bool {
	pattern := `\` + suffix + `(:[0-9]+)?$`
	match, _ := regexp.MatchString(pattern, host) //nolint:errcheck
	return match
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
