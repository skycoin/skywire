// Package visor pkg/visor/hostbridge_test.go c3-vis-wasm
package visor

import (
	"context"
	"io"
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/hostbridge"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// cutProxy forwards TCP to target and can drop every connection it holds,
// which ends the visor's session to the bridge behind it.
type cutProxy struct {
	l     net.Listener
	mu    sync.Mutex
	conns []net.Conn
}

func newCutProxy(t *testing.T, target string) *cutProxy {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	p := &cutProxy{l: l}
	t.Cleanup(func() { _ = l.Close(); p.cut() }) //nolint:errcheck
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			up, err := net.Dial("tcp", target)
			if err != nil {
				_ = c.Close() //nolint:errcheck
				continue
			}
			p.mu.Lock()
			p.conns = append(p.conns, c, up)
			p.mu.Unlock()
			go func() { _, _ = io.Copy(up, c); _ = up.Close() }() //nolint:errcheck
			go func() { _, _ = io.Copy(c, up); _ = c.Close() }()  //nolint:errcheck
		}
	}()
	return p
}

func (p *cutProxy) cut() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.conns {
		_ = c.Close() //nolint:errcheck
	}
	p.conns = nil
}

func freeLoopbackAddr(t *testing.T) string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := l.Addr().String()
	require.NoError(t, l.Close())
	return addr
}

func echoOnce(t *testing.T, addr, msg string) {
	t.Helper()
	c, err := net.DialTimeout("tcp", addr, 5*time.Second)
	require.NoError(t, err)
	defer c.Close() //nolint:errcheck
	require.NoError(t, c.SetDeadline(time.Now().Add(5*time.Second)))
	_, err = io.WriteString(c, msg)
	require.NoError(t, err)
	b := make([]byte, len(msg))
	_, err = io.ReadFull(c, b)
	require.NoError(t, err)
	require.Equal(t, msg, string(b))
}

func TestHostBridgeForwardAndReconnect(t *testing.T) {
	const token = "0123456789abcdef"
	srv, err := hostbridge.NewServer(hostbridge.Config{Token: token})
	require.NoError(t, err)
	bl, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = bl.Close() }) //nolint:errcheck
	go srv.Serve(bl)                     //nolint:errcheck
	proxy := newCutProxy(t, bl.Addr().String())

	// The visor's local service, an echo server.
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = echo.Close() }) //nolint:errcheck
	go func() {
		for {
			c, err := echo.Accept()
			if err != nil {
				return
			}
			go func() { _, _ = io.Copy(c, c); _ = c.Close() }() //nolint:errcheck
		}
	}()

	_, sk := cipher.GenerateKeyPair()
	common, err := visorconfig.NewCommon(logging.NewMasterLogger(), filepath.Join(t.TempDir(), "c.json"), &sk)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	v := &Visor{ctx: ctx, conf: &visorconfig.V1{Common: common}}

	_, err = v.HostBridgeForward("127.0.0.1:0", echo.Addr().String())
	require.ErrorIs(t, err, errHostBridgeNotConnected)

	url := "ws://" + proxy.l.Addr().String() + "/" + token
	require.Error(t, v.HostBridgeConnect("ws://"+proxy.l.Addr().String()+"/wrong"))
	require.NoError(t, v.HostBridgeConnect(url))

	host := freeLoopbackAddr(t)
	f, err := v.HostBridgeForward(host, echo.Addr().String())
	require.NoError(t, err)
	require.Equal(t, host, f.Bound)
	echoOnce(t, host, "through the bridge")

	// Cut the session. The visor notices, redials after a second and reopens
	// the forward.
	proxy.cut()
	require.Eventually(t, func() bool {
		st, _ := v.HostBridgeStatus() //nolint:errcheck
		return !st.Connected
	}, 5*time.Second, 10*time.Millisecond, "visor did not notice the cut")
	require.Eventually(t, func() bool {
		st, _ := v.HostBridgeStatus() //nolint:errcheck
		return st.Connected && len(st.Forwards) == 1 && st.Forwards[0].Bound == host
	}, 15*time.Second, 50*time.Millisecond, "visor did not reconnect")
	echoOnce(t, host, "after the reconnect")

	require.NoError(t, v.HostBridgeUnforward(host))
	require.Eventually(t, func() bool {
		c, err := net.Dial("tcp", host)
		if err == nil {
			_ = c.Close() //nolint:errcheck
		}
		return err != nil
	}, 5*time.Second, 50*time.Millisecond, "host port still open after unforward")

	require.NoError(t, v.HostBridgeDisconnect())
	st, err := v.HostBridgeStatus()
	require.NoError(t, err)
	require.False(t, st.Connected)
	require.Empty(t, st.URL)
}
