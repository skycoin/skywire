package skynetweb

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"golang.org/x/net/proxy"

	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/proxyinterstitial"
	"github.com/skycoin/skywire/pkg/proxystatus"
)

type fakeStatusProvider struct{}

func (fakeStatusProvider) StatusSnapshot(s proxystatus.Surface) (proxystatus.Snapshot, error) {
	return proxystatus.Snapshot{Surface: s, App: "test-" + string(s)}, nil
}

// TestSOCKS5StatusScoping proves a skynet_web-scoped runtime serves ONLY its own
// status host (status.skynet) in-process and lets a different surface's status
// host (status.dmsg) fall through up the chain to the upstream forward.
func TestSOCKS5StatusScoping(t *testing.T) {
	upPort := pickFreePort(t)
	upAddr := fmt.Sprintf("127.0.0.1:%d", upPort)
	upLis, err := net.Listen("tcp", upAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer upLis.Close() //nolint:errcheck
	go func() {
		for {
			c, aerr := upLis.Accept()
			if aerr != nil {
				return
			}
			go func(c net.Conn) {
				_ = proxyinterstitial.ServeSOCKS5(c, "upstream-sink", "skysocks", nil) //nolint:errcheck
				_ = c.Close()                                                          //nolint:errcheck
			}(c)
		}
	}()

	proxyPort := pickFreePort(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = Run(ctx, logging.MustGetLogger("skynetweb-test"), fakeDialer{}, Config{ //nolint:errcheck
			ProxyPort:      proxyPort,
			UpstreamSOCKS:  upAddr,
			StatusProvider: fakeStatusProvider{},
			StatusSurface:  proxystatus.SurfaceSkynet,
		})
	}()
	if err := waitForListener("127.0.0.1", proxyPort, 2*time.Second); err != nil {
		t.Fatal(err)
	}

	socksDialer, err := proxy.SOCKS5("tcp", fmt.Sprintf("127.0.0.1:%d", proxyPort), nil, proxy.Direct)
	if err != nil {
		t.Fatal(err)
	}
	httpc := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Dial: socksDialer.Dial}} //nolint:staticcheck

	// Owned surface: served in-process.
	skynetBody := getBody(t, httpc, "http://status.skynet/")
	if !contains(skynetBody, "per-leg mux") || contains(skynetBody, "upstream-sink") {
		t.Fatalf("status.skynet not served in-process: %q", truncate(skynetBody))
	}

	// Non-owned surface: falls through to the upstream sink.
	dmsgBody := getBody(t, httpc, "http://status.dmsg/")
	if !contains(dmsgBody, "upstream-sink") || contains(dmsgBody, "per-leg mux") {
		t.Fatalf("status.dmsg should have fallen through to upstream: %q", truncate(dmsgBody))
	}
}

func getBody(t *testing.T, httpc *http.Client, url string) string {
	t.Helper()
	resp, err := httpc.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()       //nolint:errcheck
	b, _ := io.ReadAll(resp.Body) //nolint:errcheck
	return string(b)
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func truncate(s string) string {
	if len(s) > 200 {
		return s[:200]
	}
	return s
}
