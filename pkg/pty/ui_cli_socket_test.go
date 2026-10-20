//go:build !windows && !js

package pty

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/require"

	dmsg "github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgtest"
)

// serveCLI runs a persistent-session host on a CLI unix socket and returns
// the socket path, as a visor's own /pty dials it.
func serveCLI(ctx context.Context, t *testing.T, dc *dmsg.Client) string {
	wl, delWL := tempWhitelist(t, dc)
	t.Cleanup(delWL)
	host := NewHost(dc, wl)
	host.SetPersistentSessions(true, time.Minute)

	sock := filepath.Join(t.TempDir(), "pty.sock")
	lis, err := net.Listen("unix", sock)
	require.NoError(t, err)
	go host.ServeCLI(ctx, lis) //nolint:errcheck
	return sock
}

// typeAndExpect sizes the terminal, waits typeAfter, types a command and
// reads until its output arrives.
func typeAndExpect(ctx context.Context, t *testing.T, ws *websocket.Conn, typeAfter time.Duration) {
	require.NoError(t, ws.Write(ctx, websocket.MessageText, []byte(`{"type":"resize","cols":100,"rows":30}`)))
	time.Sleep(typeAfter)
	require.NoError(t, ws.Write(ctx, websocket.MessageBinary, []byte("echo HI-$((40+2))\n")))

	readCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var out strings.Builder
	for !strings.Contains(out.String(), "HI-42") {
		_, b, err := ws.Read(readCtx)
		require.NoError(t, err, "terminal output so far: %q", out.String())
		out.Write(b)
	}
}

// TestUIOverCLISocket drives the web terminal over the host's CLI socket.
func TestUIOverCLISocket(t *testing.T) {
	env := dmsgtest.NewEnv(t, dmsgtest.DefaultTimeout)
	require.NoError(t, env.Startup(dmsgtest.DefaultTimeout, 1, 1, &dmsg.Config{MinSessions: 1}))
	t.Cleanup(env.Shutdown)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sock := serveCLI(ctx, t, env.AllClients()[0])

	srv := httptest.NewServer(NewUI(NetUIDialer("unix", sock), DefaultUIConfig()).Handler(nil))
	defer srv.Close()
	ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	require.NoError(t, err)
	defer ws.CloseNow() //nolint:errcheck

	typeAndExpect(ctx, t, ws, 0)
}

// TestUIOverDmsgHTTPServer serves the terminal as a visor's log server does,
// from an http.Server on a dmsg listener. net/http times out a read when it
// hijacks the connection, which once left the dmsg stream unreadable.
func TestUIOverDmsgHTTPServer(t *testing.T) {
	testUIOverDmsgHTTPServer(t, 60*time.Second, 0)
}

// TestUIOutlivesWriteTimeout types after the server's write timeout has
// passed, since a terminal outlives any one request.
func TestUIOutlivesWriteTimeout(t *testing.T) {
	testUIOverDmsgHTTPServer(t, time.Second, 2*time.Second)
}

func testUIOverDmsgHTTPServer(t *testing.T, writeTimeout, typeAfter time.Duration) {
	env := dmsgtest.NewEnv(t, dmsgtest.DefaultTimeout)
	require.NoError(t, env.Startup(dmsgtest.DefaultTimeout, 1, 2, &dmsg.Config{MinSessions: 1}))
	t.Cleanup(env.Shutdown)

	srvC, cliC := env.AllClients()[0], env.AllClients()[1]
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sock := serveCLI(ctx, t, srvC)

	lis, err := srvC.Listen(80)
	require.NoError(t, err)
	srv := &http.Server{
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      writeTimeout,
		ReadHeaderTimeout: 10 * time.Second,
		Handler:           NewUI(NetUIDialer("unix", sock), DefaultUIConfig()).Handler(nil),
	}
	go srv.Serve(lis) //nolint:errcheck
	defer srv.Close() //nolint:errcheck

	httpC := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return cliC.Dial(ctx, dmsg.Addr{PK: srvC.LocalPK(), Port: 80})
		},
	}}
	ws, _, err := websocket.Dial(ctx, "ws://"+srvC.LocalPK().Hex()+".dmsg/pty", &websocket.DialOptions{HTTPClient: httpC})
	require.NoError(t, err)
	defer ws.CloseNow() //nolint:errcheck

	typeAndExpect(ctx, t, ws, typeAfter)
}
