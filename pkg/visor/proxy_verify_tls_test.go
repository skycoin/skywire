package visor

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type directDialer struct{}

func (directDialer) Dial(network, addr string) (net.Conn, error) { return net.Dial(network, addr) }

// An exit has to complete a TLS handshake to an address, not only relay plain
// HTTP. One that accepts and hangs up, as a dead exit does, is rejected.
func TestTLSThroughExit(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	require.NoError(t, tlsThroughExit(directDialer{}, strings.TrimPrefix(srv.URL, "https://")))

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer lis.Close() //nolint:errcheck
	go func() {
		for {
			c, err := lis.Accept()
			if err != nil {
				return
			}
			_ = c.Close() //nolint:errcheck
		}
	}()
	require.Error(t, tlsThroughExit(directDialer{}, lis.Addr().String()))
}
