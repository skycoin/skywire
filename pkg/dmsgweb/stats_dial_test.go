package dmsgweb

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/proxy"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
)

// TestSOCKS5CountsDmsgRequests checks that every dial of a .dmsg host is
// counted in the layer's Stats, as a success or a failure. The counters read
// zero on a live visor before: nothing on the dial path recorded a request.
func TestSOCKS5CountsDmsgRequests(t *testing.T) {
	stats := NewStats()
	skynetDial := func(context.Context, cipher.PubKey, uint16) (net.Conn, error) {
		c, s := net.Pipe()
		go func() {
			defer s.Close() //nolint:errcheck
			if _, err := http.ReadRequest(bufio.NewReader(s)); err != nil {
				return
			}
			_, _ = io.WriteString(s, "HTTP/1.1 200 OK\r\nContent-Length: 2\r\nConnection: close\r\n\r\nok") //nolint:errcheck
		}()
		return c, nil
	}
	port := freePort(t)
	cfg := Config{DomainSuffix: ".dmsg", ProxyPort: uint(port), Stats: stats, SkynetDial: skynetDial} //nolint:gosec
	go func() { _ = serveSOCKS5Direct(t.Context(), logging.MustGetLogger("stats-web"), nil, cfg) }()  //nolint:errcheck
	proxyAddr := fmt.Sprintf("127.0.0.1:%d", port)
	waitForListen(t, proxyAddr)

	d, err := proxy.SOCKS5("tcp", proxyAddr, nil, proxy.Direct)
	require.NoError(t, err)
	client := &http.Client{Transport: &http.Transport{DialContext: d.(proxy.ContextDialer).DialContext}}

	destPK, _ := cipher.GenerateKeyPair()
	resp, err := client.Get(fmt.Sprintf("http://%s.dmsg/", destPK.Hex()))
	require.NoError(t, err)
	_ = resp.Body.Close() //nolint:errcheck
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// A transport ID is not a valid routing label for a .dmsg host.
	_, err = client.Get(fmt.Sprintf("http://%s.%s.dmsg/", uuid.New(), destPK.Hex()))
	require.Error(t, err)

	snap := stats.Snapshot()
	require.EqualValues(t, 2, snap.TotalRequests, "requests")
	require.EqualValues(t, 1, snap.Successful, "successful")
	require.EqualValues(t, 1, snap.Failed, "failed")
}
