package tcpproxy

import (
	"io"
	"net"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := l.Addr().(*net.TCPAddr).Port
	require.NoError(t, l.Close())
	return port
}

func waitForListen(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			_ = c.Close() //nolint
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("listener at %s never came up", addr)
}

// TestListenAndServe_ListenError covers the net.Listen failure path: binding
// an already-occupied address returns the listen error.
func TestListenAndServe_ListenError(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer occupied.Close() //nolint:errcheck

	err = ListenAndServe(occupied.Addr().String(), http.NotFoundHandler())
	require.Error(t, err) // address already in use
}

// TestListenAndServe_ServesRequests covers the happy path: a request sent to
// the PROXY-protocol-wrapped listener reaches the handler and gets a response.
// ListenAndServe blocks for the listener's lifetime, so it runs in a goroutine;
// a plain (non-PROXY-prefixed) connection is accepted by go-proxyproto's
// default lenient policy.
func TestListenAndServe_ServesRequests(t *testing.T) {
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(freePort(t)))

	srvErr := make(chan error, 1)
	go func() {
		srvErr <- ListenAndServe(addr, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("ok")) //nolint
		}))
	}()

	// If ListenAndServe failed fast (e.g. listen error), surface it.
	select {
	case err := <-srvErr:
		t.Fatalf("ListenAndServe returned early: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	waitForListen(t, addr)

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://" + addr + "/")
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck
	require.Equal(t, http.StatusOK, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, "ok", string(body))
}
