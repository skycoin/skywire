package router

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/SkycoinProject/dmsg/cipher"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/nettest"

	"github.com/SkycoinProject/skywire-mainnet/pkg/routing"
)

func TestNewRouteGroup(t *testing.T) {
	_, rg := prepare()
	require.NotNil(t, rg)
}

func TestRouteGroup_Close(t *testing.T) {
	_, rg := prepare()
	require.NotNil(t, rg)

	require.False(t, rg.isClosed())
	require.NoError(t, rg.Close())
	require.True(t, rg.isClosed())
}

// TODO: implement better tests
func TestRouteGroup_Read(t *testing.T) {
	_, rg := prepare()
	require.NotNil(t, rg)

	buf := make([]byte, defaultReadChBufSize)
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(3*time.Second))
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		_, err := rg.Read(buf)
		errCh <- err
	}()

	var err error
	select {
	case <-ctx.Done():
		err = ctx.Err()
	case err = <-errCh:
	}
	require.Equal(t, context.DeadlineExceeded, err)
}

// TODO: implement better tests
func TestRouteGroup_Write(t *testing.T) {
	_, rg := prepare()
	require.NotNil(t, rg)

	buf := make([]byte, defaultReadChBufSize)
	_, err := rg.Write(buf)
	require.Equal(t, ErrNoTransports, err)
}

func TestRouteGroup_LocalAddr(t *testing.T) {
	desc, rg := prepare()
	require.Equal(t, desc.Src(), rg.LocalAddr())
}

func TestRouteGroup_RemoteAddr(t *testing.T) {
	desc, rg := prepare()
	require.Equal(t, desc.Dst(), rg.RemoteAddr())
}

func TestRouteGroup_SetReadDeadline(t *testing.T) {
	_, rg := prepare()
	now := time.Now()

	require.NoError(t, rg.SetReadDeadline(now))
	require.Equal(t, now.UnixNano(), rg.readDeadline)
}

func TestRouteGroup_SetWriteDeadline(t *testing.T) {
	_, rg := prepare()
	now := time.Now()

	require.NoError(t, rg.SetWriteDeadline(now))
	require.Equal(t, now.UnixNano(), rg.writeDeadline)
}

func TestRouteGroup_SetDeadline(t *testing.T) {
	_, rg := prepare()
	now := time.Now()

	assert.NoError(t, rg.SetDeadline(now))
	assert.Equal(t, now.UnixNano(), rg.readDeadline)
	assert.Equal(t, now.UnixNano(), rg.writeDeadline)
}

func TestRouteGroup_TestConn(t *testing.T) {
	mp := func() (c1, c2 net.Conn, stop func(), err error) {
		_, c1 = prepare()
		_, c2 = prepare()
		stop = func() {
			assert.NoError(t, c1.Close())
			assert.NoError(t, c2.Close())
		}
		err = nil
		return
	}
	nettest.TestConn(t, mp)
}

func prepare() (routing.RouteDescriptor, *RouteGroup) {
	rt := routing.NewTable(routing.DefaultConfig())

	pk1, _ := cipher.GenerateKeyPair()
	pk2, _ := cipher.GenerateKeyPair()
	port1 := routing.Port(1)
	port2 := routing.Port(2)
	desc := routing.NewRouteDescriptor(pk1, pk2, port1, port2)

	rg := NewRouteGroup(DefaultRouteGroupConfig(), rt, desc)
	return desc, rg
}
