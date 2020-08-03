package app

import (
	"errors"
	"testing"

	"github.com/skycoin/dmsg/cipher"
	"github.com/skycoin/skycoin/src/util/logging"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/app/idmanager"
	"github.com/skycoin/skywire/pkg/routing"
)

func TestListener_Accept(t *testing.T) {
	l := logging.MustGetLogger("app2_listener")

	lisID := uint16(1)
	visorPK, _ := cipher.GenerateKeyPair()
	local := appnet.Addr{
		Net:    appnet.TypeDmsg,
		PubKey: visorPK,
		Port:   routing.Port(100),
	}

	t.Run("ok", func(t *testing.T) {
		acceptConnID := uint16(1)
		acceptRemotePK, _ := cipher.GenerateKeyPair()
		acceptRemote := appnet.Addr{
			Net:    appnet.TypeDmsg,
			PubKey: acceptRemotePK,
			Port:   routing.Port(100),
		}
		var acceptErr error

		rpc := &MockRPCClient{}
		rpc.On("Accept", acceptConnID).Return(acceptConnID, acceptRemote, acceptErr)

		lis := &Listener{
			log:  l,
			id:   lisID,
			rpc:  rpc,
			addr: local,
			cm:   idmanager.New(),
		}

		wantConn := &Conn{
			id:     acceptConnID,
			rpc:    rpc,
			local:  local,
			remote: acceptRemote,
		}

		conn, err := lis.Accept()
		require.NoError(t, err)

		appConn, ok := conn.(*Conn)
		require.True(t, ok)
		require.Equal(t, wantConn.id, appConn.id)
		require.Equal(t, wantConn.rpc, appConn.rpc)
		require.Equal(t, wantConn.local, appConn.local)
		require.Equal(t, wantConn.remote, appConn.remote)
		require.NotNil(t, appConn.freeConn)

		connIfc, ok := lis.cm.Get(acceptConnID)
		require.True(t, ok)

		appConn, ok = connIfc.(*Conn)
		require.True(t, ok)
		require.NotNil(t, appConn.freeConn)
	})

	t.Run("conn already exists", func(t *testing.T) {
		acceptConnID := uint16(1)
		acceptRemotePK, _ := cipher.GenerateKeyPair()
		acceptRemote := appnet.Addr{
			Net:    appnet.TypeDmsg,
			PubKey: acceptRemotePK,
			Port:   routing.Port(100),
		}
		var acceptErr error

		var closeErr error

		rpc := &MockRPCClient{}
		rpc.On("Accept", acceptConnID).Return(acceptConnID, acceptRemote, acceptErr)
		rpc.On("CloseConn", acceptConnID).Return(closeErr)

		lis := &Listener{
			log:  l,
			id:   lisID,
			rpc:  rpc,
			addr: local,
			cm:   idmanager.New(),
		}

		_, err := lis.cm.Add(acceptConnID, nil)
		require.NoError(t, err)

		conn, err := lis.Accept()
		require.Equal(t, err, idmanager.ErrValueAlreadyExists)
		require.Nil(t, conn)
	})

	t.Run("conn already exists, conn closed with error", func(t *testing.T) {
		acceptConnID := uint16(1)
		acceptRemotePK, _ := cipher.GenerateKeyPair()
		acceptRemote := appnet.Addr{
			Net:    appnet.TypeDmsg,
			PubKey: acceptRemotePK,
			Port:   routing.Port(100),
		}
		var acceptErr error

		closeErr := errors.New("close error")

		rpc := &MockRPCClient{}
		rpc.On("Accept", acceptConnID).Return(acceptConnID, acceptRemote, acceptErr)
		rpc.On("CloseConn", acceptConnID).Return(closeErr)

		lis := &Listener{
			log:  l,
			id:   lisID,
			rpc:  rpc,
			addr: local,
			cm:   idmanager.New(),
		}

		_, err := lis.cm.Add(acceptConnID, nil)
		require.NoError(t, err)

		conn, err := lis.Accept()
		require.Equal(t, err, idmanager.ErrValueAlreadyExists)
		require.Nil(t, conn)
	})

	t.Run("accept error", func(t *testing.T) {
		acceptConnID := uint16(0)
		acceptRemote := appnet.Addr{}
		acceptErr := errors.New("accept error")

		rpc := &MockRPCClient{}
		rpc.On("Accept", lisID).Return(acceptConnID, acceptRemote, acceptErr)

		lis := &Listener{
			log:  l,
			id:   lisID,
			rpc:  rpc,
			addr: local,
			cm:   idmanager.New(),
		}

		conn, err := lis.Accept()
		require.Equal(t, acceptErr, err)
		require.Nil(t, conn)
	})
}

func TestListener_Close(t *testing.T) {
	l := logging.MustGetLogger("app2_listener")

	lisID := uint16(1)
	localPK, _ := cipher.GenerateKeyPair()
	local := appnet.Addr{
		Net:    appnet.TypeDmsg,
		PubKey: localPK,
		Port:   routing.Port(100),
	}

	t.Run("ok", func(t *testing.T) {
		var closeNoErr error
		closeErr := errors.New("close error")

		rpc := &MockRPCClient{}
		rpc.On("CloseListener", lisID).Return(closeNoErr)

		cm := idmanager.New()

		connID1 := uint16(1)
		connID2 := uint16(2)
		connID3 := uint16(3)

		rpc.On("CloseConn", connID1).Return(closeNoErr)
		rpc.On("CloseConn", connID2).Return(closeErr)
		rpc.On("CloseConn", connID3).Return(closeNoErr)

		conn1 := &Conn{id: connID1, rpc: rpc}
		free1, err := cm.Add(connID1, conn1)
		require.NoError(t, err)
		conn1.freeConn = free1

		conn2 := &Conn{id: connID2, rpc: rpc}
		free2, err := cm.Add(connID2, conn2)
		require.NoError(t, err)
		conn2.freeConn = free2

		conn3 := &Conn{id: connID3, rpc: rpc}
		free3, err := cm.Add(connID3, conn3)
		require.NoError(t, err)
		conn3.freeConn = free3

		lis := &Listener{
			log:     l,
			id:      lisID,
			rpc:     rpc,
			addr:    local,
			cm:      cm,
			freeLis: func() bool { return true },
		}

		err = lis.Close()
		require.NoError(t, err)

		_, ok := lis.cm.Get(connID1)
		require.False(t, ok)

		_, ok = lis.cm.Get(connID2)
		require.False(t, ok)

		_, ok = lis.cm.Get(connID3)
		require.False(t, ok)
	})

	t.Run("close error", func(t *testing.T) {
		lisCloseErr := errors.New("close error")

		rpc := &MockRPCClient{}
		rpc.On("CloseListener", lisID).Return(lisCloseErr)

		lis := &Listener{
			log:     l,
			id:      lisID,
			rpc:     rpc,
			addr:    local,
			cm:      idmanager.New(),
			freeLis: func() bool { return true },
		}

		err := lis.Close()
		require.Equal(t, err, lisCloseErr)
	})

	t.Run("already closed", func(t *testing.T) {
		var noErr error

		rpc := &MockRPCClient{}
		rpc.On("CloseListener", lisID).Return(noErr)

		lis := &Listener{
			log:     l,
			id:      lisID,
			rpc:     rpc,
			addr:    local,
			cm:      idmanager.New(),
			freeLis: func() bool { return false },
		}

		err := lis.Close()
		require.Error(t, err)
		require.Equal(t, "listener is already closed", err.Error())
	})
}
