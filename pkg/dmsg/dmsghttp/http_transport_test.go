// Package dmsghttp pkg/dmsghttp/http_transport_test.go
package dmsghttp

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/nettest"

	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
)

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
