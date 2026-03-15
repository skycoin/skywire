package got

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"golang.org/x/net/proxy"
)

// UserAgent is the default user agent for got HTTP requests.
var UserAgent = "Got/2.0"

// ErrDownloadAborted is returned when a download is cancelled.
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
func NewWithProxy(ctx context.Context, proxyAddr string) (*Got, error) {
	dialer, err := proxy.SOCKS5("tcp", proxyAddr, nil, proxy.Direct)
	if err != nil {
		return nil, fmt.Errorf("failed to create SOCKS5 dialer: %w", err)
	}

	contextDialer, ok := dialer.(proxy.ContextDialer)
	if !ok {
		return nil, errors.New("SOCKS5 dialer does not support DialContext")
	}

	client := &http.Client{
		Transport: &http.Transport{
			DialContext:         contextDialer.DialContext,
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
func (g *Got) Do(dl *Download) error {
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
	// Validate by parsing
	if _, err := net.ResolveTCPAddr("tcp", ""); err != nil {
		// just a dummy check, actual validation happens in http.NewRequest
	}
	return rawURL, nil
}
