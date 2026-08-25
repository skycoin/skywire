package dmsgweb

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/net/proxy"

	dmsg "github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/proxyinterstitial"
	"github.com/skycoin/skywire/pkg/proxystatus"
)

// fakeStatusProvider answers for every surface with a minimal snapshot, so the
// rendered page is deterministic and identifiable ("per-leg mux" is always in
// proxystatus.Render output).
type fakeStatusProvider struct{}

func (fakeStatusProvider) StatusSnapshot(s proxystatus.Surface) (proxystatus.Snapshot, error) {
	return proxystatus.Snapshot{Surface: s, App: "test-" + string(s)}, nil
}

// TestSOCKS5StatusScoping proves a dmsg_web-scoped runtime serves ONLY its own
// status host (status.dmsg) in-process and lets a different surface's status
// host (status.skynet) fall through up the chain to the upstream forward.
func TestSOCKS5StatusScoping(t *testing.T) {
	// Upstream sink: any forwarded CONNECT is answered with a branded
	// interstitial carrying a recognizable detail line ("upstream-sink"), so a
	// fall-through is observable and distinct from the in-process status page.
	upPort := freePort(t)
	upAddr := fmt.Sprintf("127.0.0.1:%d", upPort)
	upLis, err := net.Listen("tcp", upAddr)
	require.NoError(t, err)
	defer upLis.Close() //nolint:errcheck
	go func() {
		for {
			c, aerr := upLis.Accept()
			if aerr != nil {
				return
			}
			go func(c net.Conn) {
				_ = proxyinterstitial.ServeSOCKS5(c, "upstream-sink", "skysocks", nil, nil) //nolint:errcheck
				_ = c.Close()                                                               //nolint:errcheck
			}(c)
		}
	}()

	port := freePort(t)
	cfg := Config{
		DomainSuffix:   ".dmsg",
		ProxyPort:      uint(port), //nolint:gosec
		UpstreamSOCKS:  upAddr,
		StatusProvider: fakeStatusProvider{},
		StatusSurface:  proxystatus.SurfaceDmsg,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = serveSOCKS5Direct(ctx, logging.MustGetLogger("test"), &dmsg.Client{}, cfg) }() //nolint:errcheck

	proxyAddr := fmt.Sprintf("127.0.0.1:%d", port)
	waitForListen(t, proxyAddr)

	dialer, err := proxy.SOCKS5("tcp", proxyAddr, nil, proxy.Direct)
	require.NoError(t, err)
	httpc := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Dial: dialer.Dial}} //nolint:staticcheck

	// Owned surface: served in-process (never reaches the upstream sink).
	dmsgBody := getBody(t, httpc, "http://status.dmsg/")
	require.Contains(t, dmsgBody, "per-leg mux", "status.dmsg should be served in-process")
	require.NotContains(t, dmsgBody, "upstream-sink", "status.dmsg must not fall through")

	// Non-owned surface: NOT served here — falls through to the upstream sink.
	skynetBody := getBody(t, httpc, "http://status.skynet/")
	require.Contains(t, skynetBody, "upstream-sink", "status.skynet should fall through to the upstream")
	require.NotContains(t, skynetBody, "per-leg mux", "status.skynet must not be served in-process by the dmsg layer")
}

func getBody(t *testing.T, httpc *http.Client, url string) string {
	t.Helper()
	resp, err := httpc.Get(url)
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return string(b)
}
