package cliskychat

import (
	"bytes"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

// The commands here speak HTTP to skychat at --addr. A skychat started with
// --portless has no such address, and one on a --via visor is not local, so
// requests for --addr go through the visor's RPC instead when nothing listens
// there or --via is set. An explicit --addr is always used as given.
var (
	chatTransport = &visorTransport{next: http.DefaultTransport}
	chatClient    = &http.Client{Transport: chatTransport}
)

// visorClient is the RPC client the transport uses; tests replace it.
var visorClient = func() (visorapi.API, error) { return clirpc.ClientQuiet(RootCmd.Flags()) }

type visorTransport struct {
	next http.RoundTripper

	once   sync.Once
	direct bool
}

func (t *visorTransport) useDirect() bool {
	t.once.Do(func() {
		if RootCmd.PersistentFlags().Changed("addr") {
			t.direct = true
			return
		}
		if clirpc.Via != "" || clirpc.VisorPK != "" {
			return
		}
		c, err := net.DialTimeout("tcp", httpAddr, 300*time.Millisecond)
		if err == nil {
			_ = c.Close() //nolint:errcheck,gosec
			t.direct = true
		}
	})
	return t.direct
}

func (t *visorTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Host != httpAddr || t.useDirect() {
		return t.next.RoundTrip(r)
	}
	path := strings.TrimPrefix(r.URL.Path, "/")
	if path == "sse" || path == "events" {
		return nil, errors.New("this skychat has no port, and live streams need one: start it with --addr, or pass this command --addr")
	}
	var body []byte
	if r.Body != nil {
		b, err := io.ReadAll(r.Body)
		_ = r.Body.Close() //nolint:errcheck,gosec
		if err != nil {
			return nil, err
		}
		body = b
	}
	rc, err := visorClient()
	if err != nil {
		return nil, err
	}
	out, err := rc.SkychatHTTP(visorapi.SkychatHTTPIn{
		Method:  r.Method,
		Path:    path,
		Query:   r.URL.RawQuery,
		Headers: r.Header,
		Body:    body,
	})
	if err != nil {
		return nil, err
	}
	hdr := http.Header(out.Headers)
	if hdr == nil {
		hdr = http.Header{}
	}
	hdr.Set("Content-Length", strconv.Itoa(len(out.Body)))
	return &http.Response{
		Status:        strconv.Itoa(out.Status) + " " + http.StatusText(out.Status),
		StatusCode:    out.Status,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        hdr,
		Body:          io.NopCloser(bytes.NewReader(out.Body)),
		ContentLength: int64(len(out.Body)),
		Request:       r,
	}, nil
}
