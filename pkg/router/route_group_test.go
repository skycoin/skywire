// Package router pkg/router/route_group_test.go
package router

import (
	"io"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
)

func TestNewRouteGroup(t *testing.T) {
	rg := createRouteGroup(DefaultRouteGroupConfig())
	require.NotNil(t, rg)
	require.Equal(t, DefaultRouteGroupConfig(), rg.cfg)
}

func TestRouteGroup_LocalAddr(t *testing.T) {
	rg := createRouteGroup(DefaultRouteGroupConfig())
	require.Equal(t, rg.desc.Dst(), rg.LocalAddr())

	require.NoError(t, rg.Close())
}

func TestRouteGroup_RemoteAddr(t *testing.T) {
	rg := createRouteGroup(DefaultRouteGroupConfig())
	require.Equal(t, rg.desc.Src(), rg.RemoteAddr())

	require.NoError(t, rg.Close())
}

// Data that already arrived must be readable even once the remote has closed:
// a peer that writes and immediately hangs up is ordinary (a final frame
// followed by Close), and dropping its payload turns a completed exchange into
// an EOF error. Both signals are ready here, so a select that treats them as
// equals would fail this roughly half the time.
func TestRouteGroup_ReadDrainsBeforeRemoteClose(t *testing.T) {
	rg := createRouteGroup(DefaultRouteGroupConfig())
	t.Cleanup(func() { _ = rg.Close() }) //nolint:errcheck

	payload := []byte("final-frame")
	rg.readCh <- payload
	close(rg.remoteClosed) // the peer hangs up in the same breath

	buf := make([]byte, len(payload))
	n, err := rg.read(buf)
	require.NoError(t, err, "buffered data must be delivered before EOF")
	require.Equal(t, payload, buf[:n])

	// Only once it's drained does the close surface.
	_, err = rg.read(buf)
	require.ErrorIs(t, err, io.EOF)
}

func createRouteGroup(cfg *RouteGroupConfig) *RouteGroup {
	l := logging.NewMasterLogger()
	rt := routing.NewTable(l.PackageLogger("rgt"))

	pk1, _ := cipher.GenerateKeyPair()
	pk2, _ := cipher.GenerateKeyPair()
	port1 := routing.Port(1)
	port2 := routing.Port(2)
	desc := routing.NewRouteDescriptor(pk1, pk2, port1, port2)

	rg := NewRouteGroup(cfg, rt, desc, l)
	return rg
}
