// Package commands cmd/dmsg/dmsg-socks5/commands/dmsg_socks5_test.go c1-net-dmsg
package commands

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/proxy"

	"github.com/skycoin/skywire/pkg/cipher"
	dmsg "github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgtest"
	"github.com/skycoin/skywire/pkg/logging"
)

const testDmsgPort = uint16(1081)

// countingDialer counts DialStream calls so the test can prove the local
// listener really opens a dmsg stream per accepted connection.
type countingDialer struct {
	*dmsg.Client
	dials int64
}

func (d *countingDialer) DialStream(ctx context.Context, addr dmsg.Addr) (*dmsg.Stream, error) {
	atomic.AddInt64(&d.dials, 1)
	return d.Client.DialStream(ctx, addr)
}

// countingListener tallies the bytes that cross the accepted dmsg streams, so
// the test can prove the SOCKS5 handshake and the payload traveled over dmsg
// and not over a plain net dialer on the client host.
type countingListener struct {
	net.Listener
	read    int64
	written int64
	accepts int64
}

func (l *countingListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	atomic.AddInt64(&l.accepts, 1)
	return &countingConn{Conn: c, l: l}, nil
}

type countingConn struct {
	net.Conn
	l *countingListener
}

func (c *countingConn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	atomic.AddInt64(&c.l.read, int64(n))
	return n, err
}

func (c *countingConn) Write(b []byte) (int, error) {
	n, err := c.Conn.Write(b)
	atomic.AddInt64(&c.l.written, int64(n))
	return n, err
}

// startPair brings up an in-process dmsg network with a socks5 server end and a
// local socks5 listener wired to it, and returns the local proxy address.
func startPair(t *testing.T, wlkeys []cipher.PubKey) (proxyAddr string, l *countingListener, d *countingDialer) {
	t.Helper()

	env := dmsgtest.NewEnv(t, dmsgtest.DefaultTimeout)
	require.NoError(t, env.Startup(0, 1, 0, nil))
	t.Cleanup(env.Shutdown)

	srvC, err := env.NewClient(nil)
	require.NoError(t, err)
	cliC, err := env.NewClient(nil)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	log := logging.MustGetLogger("dmsg-socks5-test")

	dmsgL, err := srvC.Listen(testDmsgPort)
	require.NoError(t, err)
	l = &countingListener{Listener: dmsgL}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_ = serveSocks5OverDmsg(ctx, log, l, wlkeys) //nolint:errcheck
	}()

	tcpL, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	d = &countingDialer{Client: cliC}
	go func() {
		defer wg.Done()
		_ = runSocks5Client(ctx, log, tcpL, d, dmsg.Addr{PK: srvC.LocalPK(), Port: testDmsgPort}) //nolint:errcheck
	}()

	t.Cleanup(func() {
		cancel()
		_ = tcpL.Close()  //nolint:errcheck
		_ = dmsgL.Close() //nolint:errcheck
		wg.Wait()
	})

	return tcpL.Addr().String(), l, d
}

func socks5HTTPClient(t *testing.T, proxyAddr string) *http.Client {
	t.Helper()
	dialer, err := proxy.SOCKS5("tcp", proxyAddr, nil, proxy.Direct)
	require.NoError(t, err)
	return &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			DialContext: func(_ context.Context, network, addr string) (net.Conn, error) {
				return dialer.Dial(network, addr)
			},
			DisableKeepAlives: true,
		},
	}
}

// TestSocks5ClientCarriesTrafficOverDmsg asserts that a CONNECT made to the
// client's local SOCKS5 port is answered by the SERVER end over a dmsg stream:
// the target server sees the request, a dmsg stream was dialed for it, and the
// handshake plus payload bytes were counted crossing that stream.
func TestSocks5ClientCarriesTrafficOverDmsg(t *testing.T) {
	var hits int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt64(&hits, 1)
		fmt.Fprint(w, "over-dmsg") //nolint:errcheck,gosec
	}))
	defer target.Close()

	proxyAddr, l, d := startPair(t, nil)

	resp, err := socks5HTTPClient(t, proxyAddr).Get(target.URL)
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "over-dmsg", string(body))
	require.EqualValues(t, 1, atomic.LoadInt64(&hits))

	// One local TCP connection => one dmsg stream, and the SOCKS5 session
	// really crossed it.
	require.EqualValues(t, 1, atomic.LoadInt64(&d.dials), "client did not dial a dmsg stream")
	require.EqualValues(t, 1, atomic.LoadInt64(&l.accepts), "server end accepted no dmsg stream")
	require.Greater(t, atomic.LoadInt64(&l.read), int64(len("GET / HTTP/1.1")),
		"no SOCKS5 request bytes crossed the dmsg stream")
	require.Greater(t, atomic.LoadInt64(&l.written), int64(len("over-dmsg")),
		"no response bytes crossed the dmsg stream")
}

// TestSocks5ServerRejectsUnwhitelistedClient asserts -w still gates the server:
// a client whose key is not whitelisted gets its stream closed before any
// SOCKS5 byte is served, so the CONNECT fails.
func TestSocks5ServerRejectsUnwhitelistedClient(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "should-not-arrive") //nolint:errcheck,gosec
	}))
	defer target.Close()

	otherPK, _ := cipher.GenerateKeyPair()
	proxyAddr, _, _ := startPair(t, []cipher.PubKey{otherPK})

	_, err := socks5HTTPClient(t, proxyAddr).Get(target.URL)
	require.Error(t, err, "unwhitelisted client was served")
}

// TestServerFlagsAreRegistered asserts both subcommands take the shared dmsg
// client flags, so -S/--srv, -e/--sess, -A/--disc-addr, -B/--direct and
// -D/--dmsgconf actually reach InitDmsgWithFlags.
func TestServerFlagsAreRegistered(t *testing.T) {
	for _, cmd := range []*cobra.Command{serveCmd, proxyCmd} {
		for _, name := range []string{"srv", "sess", "disc-addr", "direct", "dmsgconf", "attach"} {
			require.NotNil(t, cmd.Flags().Lookup(name), "%s is missing --%s", cmd.Use, name)
		}
	}
	require.NotNil(t, serveCmd.Flags().Lookup("wl"), "server lost --wl")
	require.NotNil(t, proxyCmd.Flags().Lookup("pk"), "client lost --pk")
}
