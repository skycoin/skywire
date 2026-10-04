// Package appnet pkg/app/appnet/local_service_test.go
package appnet

import (
	"io"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/routing"
)

// echoService answers one connection by echoing a single read back, then
// closing — the LocalHandler contract (the handler owns the conn).
func echoService(t *testing.T, seen chan<- net.Addr) LocalHandler {
	t.Helper()
	return func(conn net.Conn) {
		defer func() { require.NoError(t, conn.Close()) }()
		seen <- conn.RemoteAddr()
		buf := make([]byte, 16)
		n, err := conn.Read(buf)
		if err != nil {
			return
		}
		_, _ = conn.Write(buf[:n]) //nolint:errcheck
	}
}

func TestLocalServiceRoundTrip(t *testing.T) {
	t.Cleanup(ClearLocalServices)
	pk, _ := cipher.GenerateKeyPair()
	addr := Addr{Net: TypeSkynet, PubKey: pk, Port: routing.Port(4446)}

	seen := make(chan net.Addr, 1)
	RegisterLocalService(pk, LocalService{Port: addr.Port, Label: "skynet_web", Suffixes: []string{".skynet"}}, echoService(t, seen))

	require.True(t, HasLocalService(addr))
	svc, ok := LocalServiceFor(addr)
	require.True(t, ok)
	require.Equal(t, "skynet_web", svc.Label)
	require.Equal(t, []string{".skynet"}, svc.Suffixes)

	conn, err := DialLocalService(addr)
	require.NoError(t, err)
	defer conn.Close() //nolint:errcheck

	// WrapConn is what the app-plane dial path puts every conn through, so a
	// local-service conn has to survive it: net.Pipe's own addresses do not.
	wrapped, err := WrapConn(conn)
	require.NoError(t, err)
	require.Equal(t, addr, wrapped.RemoteAddr())
	// No route group exists, so the app side owns no routing port.
	require.Equal(t, routing.Port(0), wrapped.LocalAddr().(Addr).Port)

	require.NoError(t, conn.SetDeadline(time.Now().Add(5*time.Second)))
	_, err = conn.Write([]byte("hello"))
	require.NoError(t, err)
	buf := make([]byte, 5)
	_, err = io.ReadFull(conn, buf)
	require.NoError(t, err)
	require.Equal(t, "hello", string(buf))

	// The handler's peer is the service address, so a handler that reads a
	// caller PK off RemoteAddr sees this visor's own key.
	select {
	case got := <-seen:
		require.Equal(t, addr, got)
	case <-time.After(5 * time.Second):
		t.Fatal("handler never ran")
	}
}

func TestLocalServiceAddressFamilyIgnored(t *testing.T) {
	t.Cleanup(ClearLocalServices)
	pk, _ := cipher.GenerateKeyPair()
	RegisterLocalService(pk, LocalService{Port: routing.Port(4445), Label: "dmsg_web"}, func(conn net.Conn) {
		_ = conn.Close() //nolint:errcheck
	})

	// Registered once, reachable by either family: a local service is served
	// before any carrier is chosen.
	for _, n := range []Type{TypeSkynet, TypeDmsg} {
		require.True(t, HasLocalService(Addr{Net: n, PubKey: pk, Port: routing.Port(4445)}), n)
	}
}

func TestLocalServiceMisses(t *testing.T) {
	t.Cleanup(ClearLocalServices)
	pk, _ := cipher.GenerateKeyPair()
	other, _ := cipher.GenerateKeyPair()
	RegisterLocalService(pk, LocalService{Port: routing.Port(4445), Label: "dmsg_web"}, func(conn net.Conn) {
		_ = conn.Close() //nolint:errcheck
	})

	// Another visor's PK is not a local service even on the same port — the
	// whole point: only this visor's own apps reach it.
	_, err := DialLocalService(Addr{Net: TypeSkynet, PubKey: other, Port: routing.Port(4445)})
	require.ErrorIs(t, err, ErrNoLocalService)

	// An unregistered port on the right PK misses too.
	_, err = DialLocalService(Addr{Net: TypeSkynet, PubKey: pk, Port: routing.Port(4446)})
	require.ErrorIs(t, err, ErrNoLocalService)

	UnregisterLocalService(pk, routing.Port(4445))
	require.False(t, HasLocalService(Addr{Net: TypeSkynet, PubKey: pk, Port: routing.Port(4445)}))
	_, err = DialLocalService(Addr{Net: TypeSkynet, PubKey: pk, Port: routing.Port(4445)})
	require.ErrorIs(t, err, ErrNoLocalService)
}

func TestLocalServiceLastWriterWins(t *testing.T) {
	t.Cleanup(ClearLocalServices)
	pk, _ := cipher.GenerateKeyPair()
	port := routing.Port(4446)

	RegisterLocalService(pk, LocalService{Port: port, Label: "first"}, func(conn net.Conn) { _ = conn.Close() }) //nolint:errcheck
	second := make(chan struct{}, 1)
	RegisterLocalService(pk, LocalService{Port: port, Label: "second"}, func(conn net.Conn) {
		second <- struct{}{}
		_ = conn.Close() //nolint:errcheck
	})

	svc, _ := LocalServiceFor(Addr{PubKey: pk, Port: port})
	require.Equal(t, "second", svc.Label)

	conn, err := DialLocalService(Addr{Net: TypeSkynet, PubKey: pk, Port: port})
	require.NoError(t, err)
	defer conn.Close() //nolint:errcheck
	select {
	case <-second:
	case <-time.After(5 * time.Second):
		t.Fatal("the replacement handler never ran")
	}
}
