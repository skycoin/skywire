package skynetweb

import (
	"context"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	"golang.org/x/net/proxy"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/proxystatus"
)

// notingDialer reports the path it took, as the visor's dialer does.
type notingDialer struct{}

func (notingDialer) DialSkynet(ctx context.Context, _ cipher.PubKey, _ uint16, _ []RouteLabel) (net.Conn, error) {
	proxystatus.NotePath(ctx, "via relay")
	client, server := net.Pipe()
	go func() {
		defer server.Close()                                                                                 //nolint:errcheck,gosec
		_ = server.SetReadDeadline(time.Now().Add(2 * time.Second))                                          //nolint:errcheck,gosec
		_, _ = server.Read(make([]byte, 4096))                                                               //nolint:errcheck,gosec
		_, _ = io.WriteString(server, "HTTP/1.1 200 OK\r\nContent-Length: 2\r\nConnection: close\r\n\r\nok") //nolint:errcheck,gosec
	}()
	return client, nil
}

// TestSOCKS5RecordsRequests checks that a .skynet dial lands in the request
// log with the path the dialer reported.
func TestSOCKS5RecordsRequests(t *testing.T) {
	requests := proxystatus.NewRequestLog(10)
	proxyPort := pickFreePort(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = Run(ctx, logging.MustGetLogger("skynetweb-test"), notingDialer{}, Config{ProxyPort: proxyPort, Requests: requests}) //nolint:errcheck,gosec
	}()
	if err := waitForListener(proxyPort); err != nil {
		t.Fatal(err)
	}
	socksDialer, _ := proxy.SOCKS5("tcp", fmt.Sprintf("127.0.0.1:%d", proxyPort), nil, proxy.Direct) //nolint:errcheck,gosec
	host := testPK + ".skynet"
	conn, err := socksDialer.Dial("tcp", host+":80")
	if err != nil {
		t.Fatalf("SOCKS5 dial: %v", err)
	}
	_ = conn.Close() //nolint:errcheck,gosec

	got := requests.Recent("skynet")
	if len(got) != 1 {
		t.Fatalf("requests = %+v", got)
	}
	if got[0].Host != host || got[0].Port != 80 || got[0].Path != "skynet via relay" || got[0].Err != "" {
		t.Errorf("request = %+v", got[0])
	}
}
