// Package visor pkg/visor/service_registry_carrier_test.go c3-vis-core
package visor

import (
	"bufio"
	"net"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/visor/logserver"
)

// A connection the skynet forwarding server dispatched reaches the HTTP
// handler marked as having come over a transport; any other does not.
func TestHTTPHandlerMarksTransportConns(t *testing.T) {
	seen := make(chan bool, 2)
	h := HTTPHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- logserver.OverTransport(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}))
	get := func(wrap func(net.Conn) net.Conn) bool {
		client, server := net.Pipe()
		go h(wrap(server))
		req, _ := http.NewRequest(http.MethodGet, "http://visor/transports", nil) //nolint:errcheck
		require.NoError(t, req.Write(client))
		resp, err := http.ReadResponse(bufio.NewReader(client), req)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		require.NoError(t, client.Close())
		return <-seen
	}
	require.True(t, get(func(c net.Conn) net.Conn { return overTransportConn{c} }))
	require.False(t, get(func(c net.Conn) net.Conn { return c }))
}
