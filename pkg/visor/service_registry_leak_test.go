// Package visor pkg/visor/service_registry_leak_test.go c3-vis-core
package visor

import (
	"bufio"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// HTTPHandler returns once its connection closes. It used to park its
// http.Server in Accept for good, one goroutine per skynet HTTP connection.
func TestHTTPHandlerReturnsWhenConnCloses(t *testing.T) {
	h := HTTPHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	client, server := net.Pipe()
	done := make(chan struct{})
	go func() {
		h(server)
		close(done)
	}()

	req, err := http.NewRequest(http.MethodGet, "http://visor/health", nil)
	require.NoError(t, err)
	require.NoError(t, req.Write(client))
	resp, err := http.ReadResponse(bufio.NewReader(client), req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.NoError(t, client.Close())

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("HTTPHandler still serving after its connection closed")
	}
}
