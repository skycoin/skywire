// Package skysocks pkg/skysocks/client_meshname_test.go
//
// The one listener answers mesh names as well as the clearnet: a <pk>.skynet
// CONNECT is handed to the visor's resolving proxy and never reaches the exit,
// while an ordinary host still goes to the exit exactly as before.
package skysocks

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/routing"
)

const meshHost = "027087fe40d97f7f0be4a0dc768462ddbb371d4b9e7679d4f11f117d757b9856ed.skynet"

// fakeResolver stands in for the visor's in-process resolving proxy: it speaks
// the SOCKS5 server side, reports the host it was asked for, and echoes.
type fakeResolver struct {
	hostC  chan string
	dialed chan routing.Port
}

func newFakeResolver() *fakeResolver {
	return &fakeResolver{hostC: make(chan string, 4), dialed: make(chan routing.Port, 4)}
}

// resolvers builds the set skysocks-client would install, with every dial served
// by this fake over a pipe — the same shape appnet.DialLocalService returns.
func (f *fakeResolver) resolvers() *LocalResolvers {
	return NewLocalResolvers(
		[]appnet.LocalService{{Port: routing.Port(4446), Label: "skynet_web", Suffixes: []string{".skynet"}}},
		func(port routing.Port) (net.Conn, error) {
			f.dialed <- port
			mine, theirs := net.Pipe()
			go f.serve(mine)
			return theirs, nil
		})
}

func (f *fakeResolver) serve(conn net.Conn) {
	defer conn.Close()                                        //nolint:errcheck
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second)) //nolint:errcheck

	hdr := make([]byte, 2)
	if _, err := io.ReadFull(conn, hdr); err != nil || hdr[0] != 0x05 {
		return
	}
	if _, err := io.ReadFull(conn, make([]byte, int(hdr[1]))); err != nil {
		return
	}
	if _, err := conn.Write([]byte{0x05, 0x00}); err != nil {
		return
	}

	rhdr := make([]byte, 4)
	if _, err := io.ReadFull(conn, rhdr); err != nil || rhdr[3] != 0x03 {
		return
	}
	l := make([]byte, 1)
	if _, err := io.ReadFull(conn, l); err != nil {
		return
	}
	host := make([]byte, int(l[0]))
	if _, err := io.ReadFull(conn, host); err != nil {
		return
	}
	if _, err := io.ReadFull(conn, make([]byte, 2)); err != nil { // port
		return
	}
	f.hostC <- string(host)

	if _, err := conn.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0}); err != nil {
		return
	}
	_ = conn.SetReadDeadline(time.Time{}) //nolint:errcheck
	_, _ = io.Copy(conn, conn)            //nolint:errcheck
}

// A mesh name goes to the resolver, and the exit never sees it.
func TestMeshNameServedByTheLocalResolver(t *testing.T) {
	c, exit := newTestClient(t)
	defer c.Close() //nolint:errcheck

	res := newFakeResolver()
	c.SetLocalResolvers(res.resolvers())
	addr := startClientListener(t, c)

	conn := socks5Connect(t, addr, meshHost, 80)
	defer conn.Close() //nolint:errcheck

	select {
	case host := <-res.hostC:
		require.Equal(t, meshHost, host)
	case <-time.After(5 * time.Second):
		t.Fatal("the resolver never received the mesh name")
	}
	require.Equal(t, routing.Port(4446), <-res.dialed)

	// The splice carries payload both ways.
	_, err := conn.Write([]byte("ping"))
	require.NoError(t, err)
	buf := make([]byte, 4)
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(5*time.Second)))
	_, err = io.ReadFull(conn, buf)
	require.NoError(t, err)
	require.Equal(t, "ping", string(buf))

	select {
	case host := <-exit.hostC:
		t.Fatalf("the exit saw a mesh name it cannot reach: %q", host)
	case <-time.After(200 * time.Millisecond):
	}
}

// A clearnet host is untouched by the resolver wiring.
func TestClearnetStillGoesToTheExit(t *testing.T) {
	c, exit := newTestClient(t)
	defer c.Close() //nolint:errcheck

	res := newFakeResolver()
	c.SetLocalResolvers(res.resolvers())
	addr := startClientListener(t, c)

	conn := socks5Connect(t, addr, "example.com", 80)
	defer conn.Close() //nolint:errcheck

	select {
	case host := <-exit.hostC:
		require.Equal(t, "example.com", host)
	case <-time.After(5 * time.Second):
		t.Fatal("the exit never received the clearnet CONNECT")
	}
	select {
	case host := <-res.hostC:
		t.Fatalf("the resolver was handed a clearnet host: %q", host)
	case <-time.After(200 * time.Millisecond):
	}
}

// The property the separate resolver listeners had and this must not lose: a mesh
// name resolves with NO exit session at all, which is the state ServeDisconnected
// owns (still dialing, or every tunnel dead).
func TestMeshNameServedWhileDisconnected(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := lis.Addr().String()

	res := newFakeResolver()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go ServeDisconnected(ctx, lis, nil, res.resolvers())
	waitDial(t, addr)

	conn := socks5Connect(t, addr, meshHost, 80)
	defer conn.Close() //nolint:errcheck

	select {
	case host := <-res.hostC:
		require.Equal(t, meshHost, host)
	case <-time.After(5 * time.Second):
		t.Fatal("the resolver never received the mesh name while disconnected")
	}

	_, err = conn.Write([]byte("gap"))
	require.NoError(t, err)
	buf := make([]byte, 3)
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(5*time.Second)))
	_, err = io.ReadFull(conn, buf)
	require.NoError(t, err)
	require.Equal(t, "gap", string(buf))
}

// While disconnected, a clearnet host still gets the branded interstitial — the
// request is read by this client now, so the interstitial has to be served from
// what was already parsed.
func TestDisconnectedClearnetStillGetsTheInterstitialWithResolvers(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := lis.Addr().String()

	res := newFakeResolver()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go ServeDisconnected(ctx, lis, nil, res.resolvers())
	waitDial(t, addr)

	conn := socks5Connect(t, addr, "example.com", 80)
	defer conn.Close() //nolint:errcheck

	_, err = conn.Write([]byte("GET / HTTP/1.1\r\nHost: example.com\r\n\r\n"))
	require.NoError(t, err)
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(5*time.Second)))
	body, _ := io.ReadAll(conn) //nolint:errcheck
	require.Contains(t, string(body), "HTTP/1.1 200")
	require.NotEmpty(t, body)
}

// A UDP ASSOCIATE names where the application will send datagrams FROM, not a
// destination, so it must reach the exit even when that address looks like a mesh
// name — the resolver would be answering for the wrong thing entirely.
func TestUDPAssociateIsNotRoutedByItsSourceAddress(t *testing.T) {
	c, _ := newTestClient(t)
	defer c.Close() //nolint:errcheck

	res := newFakeResolver()
	c.SetLocalResolvers(res.resolvers())
	addr := startClientListener(t, c)

	conn, err := net.Dial("tcp", addr)
	require.NoError(t, err)
	defer conn.Close() //nolint:errcheck

	_, err = conn.Write([]byte{0x05, 0x01, 0x00})
	require.NoError(t, err)
	method := make([]byte, 2)
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(5*time.Second)))
	_, err = io.ReadFull(conn, method)
	require.NoError(t, err)

	// CMD 0x03 = UDP ASSOCIATE, with a .skynet address.
	req := []byte{0x05, 0x03, 0x00, 0x03, byte(len(meshHost))}
	req = append(req, meshHost...)
	req = append(req, 0x00, 0x50)
	_, err = conn.Write(req)
	require.NoError(t, err)

	select {
	case port := <-res.dialed:
		t.Fatalf("a UDP ASSOCIATE was sent to the resolver on port %d", port)
	case <-time.After(500 * time.Millisecond):
	}
}
