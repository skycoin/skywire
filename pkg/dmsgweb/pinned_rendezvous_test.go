// Package dmsgweb pkg/dmsgweb/pinned_rendezvous_test.go c4-app-web
package dmsgweb

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/net/proxy"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	dmsg "github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
)

// The pinned-rendezvous hostname — <server-pk>.<dest-pk>.dmsg — dials the
// destination through a NAMED dmsg server instead of resolving it through
// discovery, by ensuring a session to that server and dialing on it.
//
// Fetching one such host must not cost the proxy a live session. A page is
// several requests, each is its own SOCKS5 CONNECT and its own dial of the
// named server, and a dmsg server keeps one session per client PK: before the
// dial coalescing in dmsg.dialSessionOnce every one of those dials built its own
// session and evicted the one before it, so a hostname a user can simply type
// tore down the session (and every stream on it) that the previous request was
// using. Here that shows up as session-disconnect callbacks firing while the
// fetches run.
func TestSOCKS5PinnedRendezvous_ParallelFetchesKeepOneSession(t *testing.T) {
	dc := disc.NewMock(0)

	srvPK, srvSK := cipher.GenerateKeyPair()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := lis.Addr().String()
	srv := dmsg.NewServer(srvPK, srvSK, dc, &dmsg.ServerConfig{MaxSessions: 20, UpdateInterval: 0}, nil)
	srv.SetLogger(logging.MustGetLogger("pinned-srv"))
	srvEntry := disc.NewServerEntry(srvPK, 0, addr, 20)
	require.NoError(t, srvEntry.Sign(srvSK))
	require.NoError(t, dc.PostEntry(t.Context(), srvEntry))
	go func() { _ = srv.Serve(lis, addr) }() //nolint:errcheck
	t.Cleanup(func() { _ = srv.Close() })    //nolint:errcheck

	// The destination: an ordinary client on that server, serving HTTP on
	// dmsg port 80.
	destPK, destSK := cipher.GenerateKeyPair()
	dest := dmsg.NewClient(destPK, destSK, dc, &dmsg.Config{MinSessions: 1})
	dest.SetLogger(logging.MustGetLogger("pinned-dest"))
	go dest.Serve(t.Context())             //nolint:errcheck
	t.Cleanup(func() { _ = dest.Close() }) //nolint:errcheck
	require.Eventually(t, func() bool { _, ok := dest.Session(srvPK); return ok },
		20*time.Second, 100*time.Millisecond, "destination never formed a session")

	dmsgLis, err := dest.Listen(80)
	require.NoError(t, err)
	t.Cleanup(func() { _ = dmsgLis.Close() }) //nolint:errcheck
	go func() {
		_ = http.Serve(dmsgLis, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { //nolint:errcheck,gosec
			fmt.Fprint(w, "pinned-ok") //nolint:errcheck
		}))
	}()

	// The proxy's client. It is NOT served: the pinned dials below are the
	// only dials it makes, so what the test observes is the pinned path and
	// not a race with the client's own reconnect loop.
	var disconnects atomic.Int64
	proxyPK, proxySK := cipher.GenerateKeyPair()
	proxyC := dmsg.NewClient(proxyPK, proxySK, dc, &dmsg.Config{
		MinSessions: 1,
		Callbacks: &dmsg.ClientCallbacks{
			OnSessionDisconnect: func(_, _ string, _ error) { disconnects.Add(1) },
		},
	})
	proxyC.SetLogger(logging.MustGetLogger("pinned-proxy"))
	t.Cleanup(func() { _ = proxyC.Close() }) //nolint:errcheck

	port := freePort(t)
	cfg := Config{DomainSuffix: ".dmsg", ProxyPort: uint(port)}                                          //nolint:gosec
	go func() { _ = serveSOCKS5Direct(t.Context(), logging.MustGetLogger("pinned-web"), proxyC, cfg) }() //nolint:errcheck
	proxyAddr := fmt.Sprintf("127.0.0.1:%d", port)
	waitForListen(t, proxyAddr)

	host := fmt.Sprintf("http://%s.%s.dmsg/", srvPK.Hex(), destPK.Hex())
	const fetches = 6
	var (
		wg     sync.WaitGroup
		start  = make(chan struct{})
		bodies = make([]string, fetches)
		errs   = make([]error, fetches)
	)
	for i := 0; i < fetches; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// A transport each, so every fetch is its own CONNECT — what a
			// browser does when it opens one page.
			d, derr := proxy.SOCKS5("tcp", proxyAddr, nil, proxy.Direct)
			if derr != nil {
				errs[i] = derr
				return
			}
			httpc := &http.Client{
				Timeout:   20 * time.Second,
				Transport: &http.Transport{Dial: d.Dial}, //nolint:staticcheck
			}
			<-start
			resp, gerr := httpc.Get(host)
			if gerr != nil {
				errs[i] = gerr
				return
			}
			defer resp.Body.Close() //nolint:errcheck
			b, rerr := io.ReadAll(resp.Body)
			errs[i], bodies[i] = rerr, string(b)
		}(i)
	}
	close(start)
	wg.Wait()

	for i := 0; i < fetches; i++ {
		require.NoError(t, errs[i], "pinned fetch %d", i)
		require.Equal(t, "pinned-ok", bodies[i], "pinned fetch %d", i)
	}
	require.Equal(t, 1, proxyC.SessionCount(), "the pinned fetches built more than one session to the named server")
	require.Zero(t, disconnects.Load(), "a pinned fetch tore down a live session")
}
