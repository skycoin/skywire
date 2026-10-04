// Package skysocks pkg/skysocks/client_resolver_stack_test.go
//
// The whole stack in one process, with no stand-ins between the browser and the
// mesh dial: a real skynetweb resolver runtime, published through the real
// pkg/app/appnet local-service table, reached over the real net.Pipe that table
// hands out, behind a real skysocks client listener. Only the two ends are fakes
// — the browser is an http.Client and the far side of the mesh is a canned HTTP
// responder.
//
// What it is evidence for: an operator who points one browser at :1080 gets
// <pk>.skynet answered, without the resolver's own listener being involved at
// all (this test never dials :4446).
package skysocks

import (
	"context"
	"io"
	"net"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/net/proxy"

	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skynetweb"
)

// stackDialer is the far side of the mesh: every DialSkynet answers one HTTP
// request with body, which is what a served port on the destination visor would
// do.
type stackDialer struct {
	body string
	host chan string
}

func (d stackDialer) DialSkynet(_ context.Context, remote cipher.PubKey, _ uint16, _ []skynetweb.RouteLabel) (net.Conn, error) {
	client, server := net.Pipe()
	d.host <- remote.Hex()
	go func() {
		defer server.Close()                                        //nolint:errcheck
		_ = server.SetReadDeadline(time.Now().Add(5 * time.Second)) //nolint:errcheck
		_, _ = server.Read(make([]byte, 4096))                      //nolint:errcheck // the HTTP request
		reply := "HTTP/1.1 200 OK\r\nContent-Length: " + strconv.Itoa(len(d.body)) +
			"\r\nConnection: close\r\n\r\n" + d.body
		_, _ = server.Write([]byte(reply)) //nolint:errcheck
	}()
	return client, nil
}

func TestWholeStackBrowserToMeshNameOnOnePort(t *testing.T) {
	t.Cleanup(appnet.ClearLocalServices)

	// The visor's identity: the PK an app's dial to its own visor carries.
	visorPK, _ := cipher.GenerateKeyPair()
	const resolverPort = routing.Port(4446)

	// 1. The real resolver runtime, publishing itself exactly as the visor's
	// embedded wrapper does (localPublishHooks → appnet.RegisterLocalService).
	far := stackDialer{body: "mesh!", host: make(chan string, 4)}
	published := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() {
		_ = skynetweb.Run(ctx, logging.MustGetLogger("skynetweb-stack"), far, skynetweb.Config{ //nolint:errcheck
			// Its own listener is bound on a free port and never dialed by this
			// test: the point is that the browser does not need it.
			ProxyPort: pickFreeProxyPort(t),
			Publish: func(serve func(net.Conn)) {
				appnet.RegisterLocalService(visorPK, appnet.LocalService{
					Port: resolverPort, Label: "skynet_web", Suffixes: []string{".skynet"},
				}, serve)
				close(published)
			},
		})
	}()
	select {
	case <-published:
	case <-time.After(5 * time.Second):
		t.Fatal("the resolver never published itself as a local service")
	}

	// 2. The real client, wired the way skysocks-client wires it: the services
	// the visor published, dialed through the real local-service table.
	c, _ := newTestClient(t)
	defer c.Close() //nolint:errcheck
	c.SetLocalResolvers(NewLocalResolvers(
		appnet.LocalServices(visorPK),
		func(port routing.Port) (net.Conn, error) {
			return appnet.DialLocalService(appnet.Addr{
				Net: appnet.TypeSkynet, PubKey: visorPK, Port: port,
			})
		}))
	socksAddr := startClientListener(t, c)

	// 3. A browser: one proxy setting, a mesh hostname.
	dest, _ := cipher.GenerateKeyPair()
	meshURL := "http://" + dest.Hex() + ".skynet/"

	dialer, err := proxy.SOCKS5("tcp", socksAddr, nil, proxy.Direct)
	require.NoError(t, err)
	httpC := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			DialContext: func(_ context.Context, network, addr string) (net.Conn, error) {
				return dialer.Dial(network, addr)
			},
		},
	}
	resp, err := httpC.Get(meshURL) //nolint:noctx
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "mesh!", string(body))

	// The resolver dialed the destination the hostname named, over the mesh.
	select {
	case got := <-far.host:
		require.Equal(t, dest.Hex(), got)
	case <-time.After(5 * time.Second):
		t.Fatal("the resolver never dialed the destination")
	}
}

// pickFreeProxyPort is pickFreePort's uint form for a skynetweb Config.
func pickFreeProxyPort(t *testing.T) uint {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := uint(lis.Addr().(*net.TCPAddr).Port) //nolint:gosec // an ephemeral port fits
	require.NoError(t, lis.Close())
	return port
}
