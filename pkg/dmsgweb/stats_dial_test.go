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
	"github.com/skycoin/skywire/pkg/proxystatus"
)

// TestSOCKS5CountsDmsgRequests checks that every dial of a .dmsg host is
// counted in the layer's Stats, as a success or a failure. The counters read
// zero on a live visor before: nothing on the dial path recorded a request.
func TestSOCKS5CountsDmsgRequests(t *testing.T) {
	stats := NewStats()
	requests := proxystatus.NewRequestLog(10)
	skynetDial := func(ctx context.Context, _ cipher.PubKey, _ uint16) (net.Conn, error) {
		proxystatus.NotePath(ctx, "direct transport")
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
	cfg := Config{DomainSuffix: ".dmsg", ProxyPort: uint(port), Stats: stats, Requests: requests, SkynetDial: skynetDial} //nolint:gosec
	go func() { _ = serveSOCKS5Direct(t.Context(), logging.MustGetLogger("stats-web"), nil, cfg) }()                      //nolint:errcheck
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

	got := requests.Recent("")
	require.Len(t, got, 2)
	require.NotEmpty(t, got[0].Err, "the newest request is the failed one")
	require.Equal(t, "dmsg", got[1].Layer)
	require.Equal(t, destPK.Hex()+".dmsg", got[1].Host)
	require.EqualValues(t, 80, got[1].Port)
	require.Equal(t, "skynet direct transport", got[1].Path)
	require.Empty(t, got[1].Err)
}
