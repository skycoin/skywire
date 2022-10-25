// Package appnet pkg/app/appnet/networker_test.go
package appnet

import (
	"context"
	"net"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/routing"
)

func TestAddNetworker(t *testing.T) {
	ClearNetworkers()

	nType := TypeDmsg

	var n Networker

	err := AddNetworker(nType, n)
	require.NoError(t, err)

	err = AddNetworker(nType, n)
	require.Equal(t, err, ErrNetworkerAlreadyExists)
}

func TestResolveNetworker(t *testing.T) {
	ClearNetworkers()

	nType := TypeDmsg

	var n Networker

	n, err := ResolveNetworker(nType)
	require.Equal(t, err, ErrNoSuchNetworker)

	err = AddNetworker(nType, n)
	require.NoError(t, err)

	gotN, err := ResolveNetworker(nType)
	require.NoError(t, err)
	require.Equal(t, gotN, n)
}

func TestDial(t *testing.T) {
	addr := prepAddr()

	t.Run("no such networker", func(t *testing.T) {
		ClearNetworkers()

		_, err := Dial(addr)
		require.Equal(t, err, ErrNoSuchNetworker)
	})

	t.Run("ok", func(t *testing.T) {
		ClearNetworkers()

		dialCtx := context.Background()
		var (
			dialConn net.Conn
			dialErr  error
		)

		n := &MockNetworker{}
		n.On("DialContext", dialCtx, addr).Return(dialConn, dialErr)

		err := AddNetworker(addr.Net, n)
		require.NoError(t, err)

		conn, err := Dial(addr)
		require.NoError(t, err)
		require.Equal(t, conn, dialConn)
	})
}

func TestListen(t *testing.T) {
	addr := prepAddr()

	t.Run("no such networker", func(t *testing.T) {
		ClearNetworkers()

		_, err := Listen(addr)
		require.Equal(t, err, ErrNoSuchNetworker)
	})

	t.Run("ok", func(t *testing.T) {
		ClearNetworkers()

		listenCtx := context.Background()
		var (
			listenLis net.Listener
			listenErr error
		)

		n := &MockNetworker{}
		n.On("ListenContext", listenCtx, addr).Return(listenLis, listenErr)

		err := AddNetworker(addr.Net, n)
		require.NoError(t, err)

		lis, err := Listen(addr)
		require.NoError(t, err)
		require.Equal(t, lis, listenLis)
	})
}

func prepAddr() Addr {
	addrPK, _ := cipher.GenerateKeyPair()

	const addrPort routing.Port = 100

	return Addr{
		Net:    TypeDmsg,
		PubKey: addrPK,
		Port:   addrPort,
	}
}
