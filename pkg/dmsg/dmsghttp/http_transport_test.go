// Package dmsghttp pkg/dmsghttp/http_transport_test.go
package dmsghttp

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/nettest"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
)

// TestRewindBody pins the helper that the pooled-stream-failed retry
// path relies on. http.NewRequest auto-sets req.GetBody for *bytes.Buffer
// payloads (the form used by httpauth/AR clients), so a drained body
// can be reset before the fresh-dial retry. The bug this guards against
// is the AR re-registration warning "ContentLength=N with Body length 0":
// the first req.Write to a stale pooled stream consumed the body, and
// the retry sent zero bytes against the original ContentLength.
func TestRewindBody(t *testing.T) {
	t.Run("rewinds_drained_body", func(t *testing.T) {
		const payload = `{"port":"30178","addresses":["10.0.0.1"]}`
		req, err := http.NewRequest(http.MethodPost, "http://example.com", bytes.NewBufferString(payload))
		require.NoError(t, err)
		require.Equal(t, int64(len(payload)), req.ContentLength)

		// Simulate req.Write consuming the body on a doomed pooled stream.
		_, err = io.Copy(io.Discard, req.Body)
		require.NoError(t, err)

		require.NoError(t, rewindBody(req))

		got, err := io.ReadAll(req.Body)
		require.NoError(t, err)
		assert.Equal(t, payload, string(got))
		assert.Equal(t, int64(len(payload)), req.ContentLength,
			"ContentLength must stay matched to the rewound body")
	})

	t.Run("nil_get_body_is_noop", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, "http://example.com", nil)
		require.NoError(t, err)
		assert.Nil(t, req.GetBody)
		assert.NoError(t, rewindBody(req))
	})
}

func TestHTTPTransport_RoundTrip(t *testing.T) {
	logging.SetLevel(logrus.WarnLevel)

	const (
		nSrvs       = 1
		minSessions = 1
		maxSessions = 10
	)

	// Ensure HTTP request/response works.
	// Arrange:
	// - A typical dmsg environment with a dmsg discovery and a number of dmsg servers.
	// - There will be a dmsg client that hosts a http server with multiple endpoints.
	// Act:
	// - We create multiple dmsg clients that dial to the http server.
	// Assert:
	// - The http server receives all requests and the request data is of what is sent.
	// - The http clients receives all responses, and the response data is of what is sent.
	t.Run("request_response", func(t *testing.T) {

		// Arrange: constants and dmsg environment.
		const port = uint16(80)
		const nReqs = 3
		dc := startDmsgEnv(t, nSrvs, maxSessions)

		// Arrange: Result channels.
		// - server0Results has 'nReq x 3' because we have 3 http clients sending 'nReq' requests each.
		server0Results := make(chan httpServerResult, nReqs*3)
		client1Results := make(chan httpClientResult, nReqs)
		client2Results := make(chan httpClientResult, nReqs)
		client3Results := make(chan httpClientResult, nReqs)
		t.Cleanup(func() {
			close(server0Results)
			close(client1Results)
			close(client2Results)
			close(client3Results)
		})

		// Arrange: start http server (served via dmsg).
		lis, err := newDmsgClient(t, dc, minSessions, "clientA").Listen(port)
		require.NoError(t, err)
		startHTTPServer(t, server0Results, lis)
		addr := lis.Addr().String()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		// Arrange: create http clients (in which each http client has an underlying dmsg client).
		// Configure timeouts to prevent hanging on errors.
		httpC1 := http.Client{
			Transport: MakeHTTPTransport(ctx, newDmsgClient(t, dc, minSessions, "client1")),
			Timeout:   30 * time.Second,
		}
		httpC2 := http.Client{
			Transport: MakeHTTPTransport(ctx, newDmsgClient(t, dc, minSessions, "client2")),
			Timeout:   30 * time.Second,
		}
		httpC3 := http.Client{
			Transport: MakeHTTPTransport(ctx, newDmsgClient(t, dc, minSessions, "client3")),
			Timeout:   30 * time.Second,
		}

		// Allow time for dmsg sessions to stabilize across all platforms.
		// CI runners are slower; 200ms was insufficient for noise handshakes
		// to complete across 5 servers × 4 clients.
		time.Sleep(2 * time.Second)

		// Act: http clients send requests concurrently.
		// - client1 sends "/index.html" requests.
		// - client2 sends "/echo" requests.
		// - client3 sends "/hash" requests.
		for i := 0; i < nReqs; i++ {
			go func() {
				client1Results <- requestHTTP(&httpC1, http.MethodGet, "http://"+addr+endpointHTML, nil)
			}()
			go func(i int) {
				body := []byte(fmt.Sprintf("This is message %d! And it should be echoed!", i))
				client2Results <- requestHTTP(&httpC2, http.MethodPost, "http://"+addr+endpointEcho, body)
			}(i)
			go func(i int) {
				body := []byte(fmt.Sprintf("This is message %d! And it should be hashed!", i))
				client3Results <- requestHTTP(&httpC3, http.MethodPost, "http://"+addr+endpointHash, body)
			}(i)
		}

		// Assert: ensure we get expected behavior from both the http client and server perspectives.
		// Use a deadline to prevent hanging if a DMSG session fails.
		deadline := time.After(60 * time.Second)
		for i := 0; i < nReqs; i++ {
			select {
			case r := <-server0Results:
				r.Assert(t, i)
			case <-deadline:
				t.Fatal("timed out waiting for server results")
			}
			select {
			case r := <-client1Results:
				r.Assert(t, i)
			case <-deadline:
				t.Fatal("timed out waiting for client1 results")
			}
			select {
			case r := <-client2Results:
				r.Assert(t, i)
			case <-deadline:
				t.Fatal("timed out waiting for client2 results")
			}
			select {
			case r := <-client3Results:
				r.Assert(t, i)
			case <-deadline:
				t.Fatal("timed out waiting for client3 results")
			}
		}
	})
}

func startDmsgEnv(t *testing.T, nSrvs, maxSessions int) disc.APIClient {
	dc := disc.NewMock(0)

	for i := 0; i < nSrvs; i++ {
		pk, sk := cipher.GenerateKeyPair()

		conf := dmsg.ServerConfig{
			MaxSessions:    maxSessions,
			UpdateInterval: 0,
		}
		srv := dmsg.NewServer(pk, sk, dc, &conf, nil)
		srv.SetLogger(logging.MustGetLogger(fmt.Sprintf("server_%d", i)))

		lis, err := nettest.NewLocalListener("tcp")
		require.NoError(t, err)

		errCh := make(chan error, 1)
		go func() {
			errCh <- srv.Serve(lis, "")
			close(errCh)
		}()

		<-srv.Ready()
		t.Cleanup(func() {
			// listener is also closed when dmsg server is closed
			assert.NoError(t, srv.Close())
			assert.NoError(t, <-errCh)
		})
	}

	return dc
}

// nolint:unparam
func newDmsgClient(t *testing.T, dc disc.APIClient, minSessions int, name string) *dmsg.Client {
	pk, sk := cipher.GenerateKeyPair()

	dmsgC := dmsg.NewClient(pk, sk, dc, &dmsg.Config{
		MinSessions: minSessions,
		Callbacks:   nil,
	})
	dmsgC.SetLogger(logging.MustGetLogger(name))
	go dmsgC.Serve(context.Background())

	t.Cleanup(func() {
		assert.NoError(t, dmsgC.Close())
	})

	select {
	case <-dmsgC.Ready():
	case <-time.After(30 * time.Second):
		t.Fatal("timed out waiting for dmsg client to be ready")
	}
	return dmsgC
}

// TestHTTPTransport_TransparentGzip pins the "gzip gap" fix: because this
// transport is a custom http.RoundTripper that bypasses net/http.Transport,
// it must itself advertise Accept-Encoding: gzip on the caller's behalf and
// transparently decompress the response — otherwise dmsg service callers
// (TPD/AR/SD) receive uncompressed bodies even though the server can gzip,
// driving dmsg-server write CPU + egress (Beta's post-#2980 residual).
func TestHTTPTransport_TransparentGzip(t *testing.T) {
	logging.SetLevel(logrus.WarnLevel)

	const (
		nSrvs       = 1
		minSessions = 1
		maxSessions = 10
		port        = uint16(80)
	)
	// A payload large/repetitive enough that gzip is unmistakably applied.
	payload := strings.Repeat("the quick brown fox jumps over the lazy dog. ", 200)

	dc := startDmsgEnv(t, nSrvs, maxSessions)

	lis, err := newDmsgClient(t, dc, minSessions, "gzipsrv").Listen(port)
	require.NoError(t, err)

	gotAcceptEncoding := make(chan string, 4)
	mux := http.NewServeMux()
	mux.HandleFunc("/gzip", func(w http.ResponseWriter, r *http.Request) {
		gotAcceptEncoding <- r.Header.Get("Accept-Encoding")
		var buf bytes.Buffer
		gz := gzip.NewWriter(&buf)
		_, _ = gz.Write([]byte(payload)) //nolint:errcheck
		_ = gz.Close()                   //nolint:errcheck
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write(buf.Bytes()) //nolint:errcheck
	})
	mux.HandleFunc("/plain", func(w http.ResponseWriter, r *http.Request) {
		gotAcceptEncoding <- r.Header.Get("Accept-Encoding")
		_, _ = w.Write([]byte(payload)) //nolint:errcheck
	})
	srv := &http.Server{ReadHeaderTimeout: 3 * time.Second, Handler: mux}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(lis); close(errCh) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx) //nolint:errcheck
		_ = lis.Close()       //nolint:errcheck
		<-errCh
	})

	addr := lis.Addr().String()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	httpC := http.Client{
		Transport: MakeHTTPTransport(ctx, newDmsgClient(t, dc, minSessions, "gzipcli")),
		Timeout:   30 * time.Second,
	}
	time.Sleep(2 * time.Second)

	waitAE := func(t *testing.T) string {
		t.Helper()
		select {
		case ae := <-gotAcceptEncoding:
			return ae
		case <-time.After(10 * time.Second):
			t.Fatal("server never received the request")
			return ""
		}
	}

	t.Run("gzip_response_transparently_decompressed", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, "http://"+addr+"/gzip", nil)
		require.NoError(t, err)
		resp, err := httpC.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }() //nolint:errcheck

		assert.Contains(t, waitAE(t), "gzip", "transport must advertise Accept-Encoding: gzip")

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Equal(t, payload, string(body), "body must be transparently decompressed")
		assert.True(t, resp.Uncompressed, "resp.Uncompressed must be set after transparent decode")
		assert.Empty(t, resp.Header.Get("Content-Encoding"), "Content-Encoding must be stripped")
	})

	t.Run("caller_accept_encoding_preserved_no_autodecode", func(t *testing.T) {
		// A caller that sets its own Accept-Encoding must be left alone:
		// we neither override the header nor auto-decompress (net/http parity).
		req, err := http.NewRequest(http.MethodGet, "http://"+addr+"/plain", nil)
		require.NoError(t, err)
		req.Header.Set("Accept-Encoding", "identity")
		resp, err := httpC.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }() //nolint:errcheck

		assert.Equal(t, "identity", waitAE(t), "caller's Accept-Encoding must be preserved")
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Equal(t, payload, string(body))
	})
}
