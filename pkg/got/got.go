package got

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/proxy"
)

// UserAgent is the default user agent for got HTTP requests.
var UserAgent = "Got/2.0"

// ErrDownloadAborted is returned when a download is canceled.
var ErrDownloadAborted = errors.New("download aborted")

// Header represents an HTTP header key-value pair.
type Header struct {
	Key   string
	Value string
}

// ProgressFunc is called periodically during downloads to report progress.
type ProgressFunc func(d *Download)

// Got holds the configuration for HTTP operations.
type Got struct {
	ProgressFunc

	// Client is the HTTP client used for requests. If nil, a default client is used.
	Client *http.Client

	ctx context.Context
}

// DefaultClient returns a new default HTTP client.
func DefaultClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			MaxIdleConns:        10,
			IdleConnTimeout:     30 * time.Second,
			TLSHandshakeTimeout: 5 * time.Second,
			Proxy:               http.ProxyFromEnvironment,
		},
	}
}

// New returns a new Got with default context and client.
func New() *Got {
	return NewWithContext(context.Background())
}

// NewWithContext returns a Got with the given context and default HTTP client.
func NewWithContext(ctx context.Context) *Got {
	return &Got{
		ctx:    ctx,
		Client: DefaultClient(),
	}
}

// NewWithProxy returns a Got configured to route through a SOCKS5 proxy.
//
// The proxyAddr is accepted in any of:
//
//	socks5h://host:port  destination resolved by the proxy (curl --socks5-hostname).
//	                     Required for proxies that resolve non-DNS hostnames
//	                     themselves — e.g. dmsgweb's <pk>.dmsg URLs.
//	socks5://host:port   destination resolved by this client first, then the
//	                     IP is sent to the proxy (curl --socks5).
//	host:port            bare form, treated as socks5h:// for backward compat.
func NewWithProxy(ctx context.Context, proxyAddr string) (*Got, error) {
	addr, resolveLocally, err := parseProxyAddr(proxyAddr)
	if err != nil {
		return nil, err
	}

	dialer, err := proxy.SOCKS5("tcp", addr, nil, proxy.Direct)
	if err != nil {
		return nil, fmt.Errorf("failed to create SOCKS5 dialer: %w", err)
	}

	contextDialer, ok := dialer.(proxy.ContextDialer)
	if !ok {
		return nil, errors.New("SOCKS5 dialer does not support DialContext")
	}

	dialContext := contextDialer.DialContext
	if resolveLocally {
		dialContext = resolveBeforeDial(dialContext)
	}

	client := &http.Client{
		Transport: &http.Transport{
			DialContext:         dialContext,
			MaxIdleConns:        10,
			IdleConnTimeout:     30 * time.Second,
			TLSHandshakeTimeout: 5 * time.Second,
		},
	}

	return &Got{
		ctx:    ctx,
		Client: client,
	}, nil
}

// parseProxyAddr returns the bare host:port to hand to proxy.SOCKS5 and
// whether the destination hostname should be resolved client-side before
// the dial. See NewWithProxy's docstring for the accepted forms.
func parseProxyAddr(raw string) (addr string, resolveLocally bool, err error) {
	if !strings.Contains(raw, "://") {
		return raw, false, nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", false, fmt.Errorf("parse proxy address %q: %w", raw, err)
	}
	if u.Host == "" {
		return "", false, fmt.Errorf("proxy address %q has no host", raw)
	}
	switch strings.ToLower(u.Scheme) {
	case "socks5h":
		return u.Host, false, nil
	case "socks5":
		return u.Host, true, nil
	default:
		return "", false, fmt.Errorf("unsupported proxy scheme %q (expected socks5:// or socks5h://)", u.Scheme)
	}
}

// resolveBeforeDial wraps a SOCKS5 dialer with client-side DNS resolution
// of the destination address. IP literals pass through unchanged so this
// is a no-op when the URL host is already an IP. This is the
// `socks5://` (no h) semantic; for `socks5h://` we leave the dialer alone
// and the SOCKS5 protocol encodes the hostname as ATYP=FQDN, leaving
// resolution to the proxy.
func resolveBeforeDial(inner func(context.Context, string, string) (net.Conn, error)) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, splitErr := net.SplitHostPort(address)
		if splitErr != nil {
			return inner(ctx, network, address)
		}
		if net.ParseIP(host) != nil {
			return inner(ctx, network, address)
		}
		ips, lookupErr := net.DefaultResolver.LookupIPAddr(ctx, host)
		if lookupErr != nil {
			return nil, &net.OpError{Op: "dial", Net: network, Err: lookupErr}
		}
		if len(ips) == 0 {
			return nil, &net.OpError{Op: "dial", Net: network, Err: fmt.Errorf("no addresses for %s", host)}
		}
		return inner(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
	}
}

// Download creates a Download for the given URL and destination, then runs it.
func (g *Got) Download(URL, dest string) error {
	return g.Do(&Download{
		ctx:    g.ctx,
		URL:    URL,
		Dest:   dest,
		Client: g.Client,
	})
}

// Do initializes and runs a Download, calling ProgressFunc if set.
//
// Callers who construct a *Download by hand (e.g. `skywire-cli got dl`)
// typically don't set Client or ctx — without this propagation step,
// Download.Init falls back to DefaultClient(), silently discarding the
// proxy / context configured on the Got receiver. That manifests as the
// SOCKS5 proxy being bypassed (DNS lookups resolved locally instead of
// through the dialer wired up in NewWithProxy) — exactly the failure
// mode that turned out to outlive #3029.
func (g *Got) Do(dl *Download) error {
	if dl.Client == nil {
		dl.Client = g.Client
	}
	if dl.ctx == nil {
		dl.ctx = g.ctx
	}
	if err := dl.Init(); err != nil {
		return err
	}

	if g.ProgressFunc != nil {
		defer func() {
			dl.StopProgress = true
		}()
		go dl.RunProgress(g.ProgressFunc)
	}

	return dl.Start()
}

// Request performs a general HTTP request and writes the response body to output.
// It returns the response (body already consumed) and any error.
func (g *Got) Request(method, URL string, headers []Header, body io.Reader, output io.Writer) (*http.Response, error) {
	client := g.Client
	if client == nil {
		client = DefaultClient()
	}

	req, err := http.NewRequestWithContext(g.ctx, method, URL, body)
	if err != nil {
		return nil, err
	}

	req.Header.Set("User-Agent", UserAgent)
	for _, h := range headers {
		req.Header.Set(h.Key, h.Value)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close() //nolint:errcheck

	if output != nil {
		if _, err := io.Copy(output, resp.Body); err != nil {
			return resp, err
		}
	}

	return resp, nil
}

// NewRequest creates an http.Request with the given method, URL, headers, and optional body.
func NewRequest(ctx context.Context, method, URL string, headers []Header, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, URL, body)
	if err != nil {
		return nil, err
	}

	req.Header.Set("User-Agent", UserAgent)
	for _, h := range headers {
		req.Header.Set(h.Key, h.Value)
	}

	return req, nil
}

// ParseHeaders parses header strings in "Key: Value" format.
func ParseHeaders(raw []string) ([]Header, error) {
	var headers []Header
	for _, h := range raw {
		parts := strings.SplitN(h, ":", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("malformed header %q (expected \"Key: Value\")", h)
		}
		headers = append(headers, Header{
			Key:   strings.TrimSpace(parts[0]),
			Value: strings.TrimSpace(parts[1]),
		})
	}
	return headers, nil
}

// NormalizeURL ensures a URL has a scheme, defaulting to https.
func NormalizeURL(rawURL string) (string, error) {
	if !strings.Contains(rawURL, "://") {
		rawURL = "https://" + rawURL
	}
	return rawURL, nil
}
