package dmsgweb

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/net/proxy"

	"github.com/skycoin/skywire/pkg/cipher"
	dmsg "github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
)

func TestParseDmsgTarget(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()

	t.Run("pk only → default port 80", func(t *testing.T) {
		got, err := ParseDmsgTarget(pk.Hex())
		require.NoError(t, err)
		require.Equal(t, pk, got.PK)
		require.EqualValues(t, 80, got.Port)
	})

	t.Run("pk with explicit port", func(t *testing.T) {
		got, err := ParseDmsgTarget(pk.Hex() + ":8080")
		require.NoError(t, err)
		require.EqualValues(t, 8080, got.Port)
	})

	t.Run("empty", func(t *testing.T) {
		_, err := ParseDmsgTarget("")
		require.Error(t, err)
	})

	t.Run("bad pk", func(t *testing.T) {
		_, err := ParseDmsgTarget("not-a-pk")
		require.Error(t, err)
	})

	t.Run("bad port", func(t *testing.T) {
		_, err := ParseDmsgTarget(pk.Hex() + ":notaport")
		require.Error(t, err)
	})

	t.Run("port overflow", func(t *testing.T) {
		_, err := ParseDmsgTarget(pk.Hex() + ":99999")
		require.Error(t, err)
	})
}

func TestNormalize(t *testing.T) {
	// Empty suffix → default.
	require.Equal(t, DefaultDomainSuffix, normalize(Config{}).DomainSuffix)
	// Provided suffix preserved.
	require.Equal(t, ".skynet", normalize(Config{DomainSuffix: ".skynet"}).DomainSuffix)
}

func TestTCPAddrConn(t *testing.T) {
	c := &tcpAddrConn{Conn: nil}
	// Both must report a *net.TCPAddr (go-socks5 does an unchecked assertion).
	_, okR := c.RemoteAddr().(*net.TCPAddr)
	_, okL := c.LocalAddr().(*net.TCPAddr)
	require.True(t, okR)
	require.True(t, okL)
}

func TestDmsgResolver(t *testing.T) {
	r := &dmsgResolver{cfg: Config{DomainSuffix: ".dmsg"}}

	t.Run("matching suffix sets port key", func(t *testing.T) {
		ctx, ip, err := r.Resolve(context.Background(), "tpd.dmsg")
		require.NoError(t, err)
		require.True(t, net.IPv4(127, 0, 0, 1).Equal(ip))
		require.Equal(t, "tpd.dmsg", ctx.Value(dmsgOrigHostKey))
		require.NotNil(t, ctx.Value(dmsgResolverPortKey)) // .dmsg matched
	})

	t.Run("non-matching suffix omits port key", func(t *testing.T) {
		ctx, ip, err := r.Resolve(context.Background(), "example.com")
		require.NoError(t, err)
		require.True(t, net.IPv4(127, 0, 0, 1).Equal(ip)) // still loopback (no real DNS)
		require.Equal(t, "example.com", ctx.Value(dmsgOrigHostKey))
		require.Nil(t, ctx.Value(dmsgResolverPortKey)) // not .dmsg → upstream path
	})

	t.Run("suffix with port still matches", func(t *testing.T) {
		ctx, _, err := r.Resolve(context.Background(), "tpd.dmsg:8080")
		require.NoError(t, err)
		require.NotNil(t, ctx.Value(dmsgResolverPortKey))
	})
}

func TestRunGuards(t *testing.T) {
	log := logging.MustGetLogger("test")

	t.Run("nil client", func(t *testing.T) {
		err := Run(context.Background(), log, nil, Config{})
		require.Error(t, err)
		require.Contains(t, err.Error(), "dmsg client is nil")
	})

	t.Run("TLSMITM without LeafMinter", func(t *testing.T) {
		err := Run(context.Background(), log, &dmsg.Client{}, Config{TLSMITM: true})
		require.Error(t, err)
		require.Contains(t, err.Error(), "LeafMinter")
	})

	t.Run("no bridges → clean nil return", func(t *testing.T) {
		// ProxyPort 0 and no ResolveAddr → nothing to serve; Run's wait
		// group is empty so it returns nil immediately.
		err := Run(context.Background(), log, &dmsg.Client{}, Config{})
		require.NoError(t, err)
	})

	t.Run("nil log is tolerated", func(t *testing.T) {
		err := Run(context.Background(), nil, &dmsg.Client{}, Config{})
		require.NoError(t, err)
	})
}

// TestSOCKS5HomePage drives the SOCKS5 resolver end-to-end for the reserved
// "home" directory, which is served entirely in-process (no dmsg dial). It
// exercises serveSOCKS5Direct, dmsgResolver.Resolve, the Dial-callback home
// branch and tcpAddrConn together.
func TestSOCKS5HomePage(t *testing.T) {
	port := freePort(t)

	selfPK, _ := cipher.GenerateKeyPair()
	tpdPK, _ := cipher.GenerateKeyPair()
	cfg := Config{
		DomainSuffix: ".dmsg",
		ProxyPort:    uint(port),
		LocalPK:      selfPK,
		Aliases:      map[string]cipher.PubKey{"skywire": selfPK, "tpd": tpdPK},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	log := logging.MustGetLogger("test")

	errCh := make(chan error, 1)
	go func() { errCh <- serveSOCKS5Direct(ctx, log, &dmsg.Client{}, cfg) }()

	proxyAddr := fmt.Sprintf("127.0.0.1:%d", port)
	waitForListen(t, proxyAddr)

	dialer, err := proxy.SOCKS5("tcp", proxyAddr, nil, proxy.Direct)
	require.NoError(t, err)
	httpc := &http.Client{
		Timeout:   5 * time.Second,
		Transport: &http.Transport{Dial: dialer.Dial}, //nolint:staticcheck
	}

	resp, err := httpc.Get("http://home.dmsg/")
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "text/html; charset=utf-8", resp.Header.Get("Content-Type"))

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Contains(t, string(body), "skywire resolver")
	require.Contains(t, string(body), tpdPK.Hex()) // the alias is listed

	// Clean shutdown: closing the listener via ctx makes Serve return nil.
	cancel()
	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("serveSOCKS5Direct did not return after ctx cancel")
	}
}

// freePort reserves an ephemeral TCP port and releases it for the caller to
// reuse. The brief window between close and reuse is acceptable in tests.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := l.Addr().(*net.TCPAddr).Port
	require.NoError(t, l.Close())
	return port
}

// TestSOCKS5Passthrough exercises the non-.dmsg branch of the Dial callback:
// a hostname that doesn't match the domain suffix resolves to loopback (no
// real DNS) and, with no upstream SOCKS configured, is dialed directly with
// net.Dial — here landing on a local httptest server.
func TestSOCKS5Passthrough(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "hello-from-backend")
	}))
	defer backend.Close()
	_, backendPort, err := net.SplitHostPort(backend.Listener.Addr().String())
	require.NoError(t, err)

	port := freePort(t)
	cfg := Config{DomainSuffix: ".dmsg", ProxyPort: uint(port)}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	log := logging.MustGetLogger("test")
	errCh := make(chan error, 1)
	go func() { errCh <- serveSOCKS5Direct(ctx, log, &dmsg.Client{}, cfg) }()

	proxyAddr := fmt.Sprintf("127.0.0.1:%d", port)
	waitForListen(t, proxyAddr)

	dialer, err := proxy.SOCKS5("tcp", proxyAddr, nil, proxy.Direct)
	require.NoError(t, err)
	httpc := &http.Client{
		Timeout:   5 * time.Second,
		Transport: &http.Transport{Dial: dialer.Dial}, //nolint:staticcheck
	}

	// A non-.dmsg host on the backend's port → resolves to 127.0.0.1, no
	// port key set → net.Dial("127.0.0.1:<backendPort>") reaches the backend.
	resp, err := httpc.Get("http://plain.example:" + backendPort + "/")
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, "hello-from-backend", string(body))

	cancel()
	<-errCh
}

func TestServeTCPListenError(t *testing.T) {
	port := freePort(t)
	// Occupy the port so serveTCP's bind fails.
	occupied, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	require.NoError(t, err)
	defer occupied.Close() //nolint:errcheck

	pk, _ := cipher.GenerateKeyPair()
	cfg := Config{WebPorts: []uint{uint(port)}, ResolveAddr: []DmsgTarget{{PK: pk, Port: 80}}}
	err = serveTCP(context.Background(), logging.MustGetLogger("test"), &dmsg.Client{}, cfg, 0)
	require.Error(t, err)
	require.Contains(t, err.Error(), "TCP listen")
}

func TestServeTCPCleanShutdown(t *testing.T) {
	port := freePort(t)
	pk, _ := cipher.GenerateKeyPair()
	cfg := Config{WebPorts: []uint{uint(port)}, ResolveAddr: []DmsgTarget{{PK: pk, Port: 80}}}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- serveTCP(ctx, logging.MustGetLogger("test"), &dmsg.Client{}, cfg, 0) }()

	// Let the listener bind, then cancel — closing the listener makes the
	// accept loop return nil. We intentionally do NOT dial the listener: a
	// real connection would invoke handleTCPConn, which dials dmsg on a zero
	// client. (handleTCPConn itself needs a live dmsg network to cover.)
	time.Sleep(150 * time.Millisecond)
	cancel()
	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("serveTCP did not return after ctx cancel")
	}
}

// TestRunSOCKS5 drives the full Run orchestration in SOCKS5 mode: it spawns
// the resolver goroutine, serves the in-process home page through it, then
// returns context.Canceled on shutdown.
func TestRunSOCKS5(t *testing.T) {
	port := freePort(t)
	tpdPK, _ := cipher.GenerateKeyPair()
	cfg := Config{
		DomainSuffix: ".dmsg",
		ProxyPort:    uint(port),
		Aliases:      map[string]cipher.PubKey{"tpd": tpdPK},
	}

	ctx, cancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)
	go func() { runErr <- Run(ctx, logging.MustGetLogger("test"), &dmsg.Client{}, cfg) }()

	proxyAddr := fmt.Sprintf("127.0.0.1:%d", port)
	waitForListen(t, proxyAddr)

	dialer, err := proxy.SOCKS5("tcp", proxyAddr, nil, proxy.Direct)
	require.NoError(t, err)
	httpc := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Dial: dialer.Dial}} //nolint:staticcheck
	resp, err := httpc.Get("http://home.dmsg/")
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	cancel()
	select {
	case err := <-runErr:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return after ctx cancel")
	}
}

// TestRunFixedMapping drives Run in fixed-mapping (--resolve) mode through the
// serveTCP goroutine path, then returns context.Canceled on shutdown. No
// connection is dialed (that would invoke handleTCPConn → dmsg).
func TestRunFixedMapping(t *testing.T) {
	port := freePort(t)
	pk, _ := cipher.GenerateKeyPair()
	cfg := Config{WebPorts: []uint{uint(port)}, ResolveAddr: []DmsgTarget{{PK: pk, Port: 80}}}

	ctx, cancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)
	go func() { runErr <- Run(ctx, logging.MustGetLogger("test"), &dmsg.Client{}, cfg) }()

	// Let the listener bind, then cancel. We deliberately do NOT dial it: a
	// connection would invoke handleTCPConn, which dials dmsg on a zero client.
	time.Sleep(150 * time.Millisecond)
	cancel()
	select {
	case err := <-runErr:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return after ctx cancel")
	}
}

// TestSOCKS5Upstream covers the upstream-forwarding branch of the Dial
// callback: a non-.dmsg request on a proxy configured with UpstreamSOCKS is
// relayed through a second SOCKS5 proxy, which dials the backend.
func TestSOCKS5Upstream(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "via-upstream")
	}))
	defer backend.Close()
	_, backendPort, err := net.SplitHostPort(backend.Listener.Addr().String())
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	log := logging.MustGetLogger("test")

	// Upstream proxy in plain passthrough mode.
	upPort := freePort(t)
	upAddr := fmt.Sprintf("127.0.0.1:%d", upPort)
	go func() { _ = serveSOCKS5Direct(ctx, log, &dmsg.Client{}, Config{DomainSuffix: ".dmsg", ProxyPort: uint(upPort)}) }()
	waitForListen(t, upAddr)

	// Front proxy forwards non-.dmsg to the upstream.
	frontPort := freePort(t)
	frontAddr := fmt.Sprintf("127.0.0.1:%d", frontPort)
	go func() {
		_ = serveSOCKS5Direct(ctx, log, &dmsg.Client{}, Config{DomainSuffix: ".dmsg", ProxyPort: uint(frontPort), UpstreamSOCKS: upAddr})
	}()
	waitForListen(t, frontAddr)

	dialer, err := proxy.SOCKS5("tcp", frontAddr, nil, proxy.Direct)
	require.NoError(t, err)
	httpc := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Dial: dialer.Dial}} //nolint:staticcheck

	resp, err := httpc.Get("http://plain.example:" + backendPort + "/")
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, "via-upstream", string(body))
}

func TestHomeNatSortHelpers(t *testing.T) {
	// Same prefix, numeric ordering.
	require.True(t, homeNatLess("dmsg2", "dmsg10"))
	require.False(t, homeNatLess("dmsg10", "dmsg2"))
	// Different prefix → lexical on prefix.
	require.True(t, homeNatLess("aaa", "bbb"))
	// Identical labels → not less (final tie branch).
	require.False(t, homeNatLess("dmsg2", "dmsg2"))
	// No trailing digits → number is -1.
	p, n := homeSplitNum("skywire")
	require.Equal(t, "skywire", p)
	require.Equal(t, -1, n)
	// Trailing digits split off.
	p, n = homeSplitNum("dmsg42")
	require.Equal(t, "dmsg", p)
	require.Equal(t, 42, n)
}

func waitForListen(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			_ = c.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("listener at %s never came up", addr)
}
