package app

import (
	"errors"
	"testing"

	"github.com/SkycoinProject/dmsg/cipher"
	"github.com/SkycoinProject/skycoin/src/util/logging"
	"github.com/stretchr/testify/require"

	"github.com/SkycoinProject/skywire-mainnet/pkg/app/appcommon"
	"github.com/SkycoinProject/skywire-mainnet/pkg/app/appnet"
	"github.com/SkycoinProject/skywire-mainnet/pkg/app/idmanager"
	"github.com/SkycoinProject/skywire-mainnet/pkg/routing"
)

func TestClient_Dial(t *testing.T) {
	l := logging.MustGetLogger("app2_client")
	visorPK, _ := cipher.GenerateKeyPair()

	remotePK, _ := cipher.GenerateKeyPair()
	remotePort := routing.Port(120)
	remote := appnet.Addr{
		Net:    appnet.TypeDmsg,
		PubKey: remotePK,
		Port:   remotePort,
	}

	t.Run("ok", func(t *testing.T) {
		dialConnID := uint16(1)
		dialLocalPort := routing.Port(1)
		var dialErr error

		rpc := &MockRPCClient{}
		rpc.On("Dial", remote).Return(dialConnID, dialLocalPort, dialErr)

		cl := prepClient(l, visorPK, rpc)

		wantConn := &Conn{
			id:  dialConnID,
			rpc: rpc,
			local: appnet.Addr{
				Net:    remote.Net,
				PubKey: visorPK,
				Port:   dialLocalPort,
			},
			remote: remote,
		}

		conn, err := cl.Dial(remote)
		require.NoError(t, err)

		appConn, ok := conn.(*Conn)
		require.True(t, ok)

		require.Equal(t, wantConn.id, appConn.id)
		require.Equal(t, wantConn.rpc, appConn.rpc)
		require.Equal(t, wantConn.local, appConn.local)
		require.Equal(t, wantConn.remote, appConn.remote)
		require.NotNil(t, appConn.freeConn)

		cmConnIfc, ok := cl.cm.Get(appConn.id)
		require.True(t, ok)
		require.NotNil(t, cmConnIfc)

		cmConn, ok := cmConnIfc.(*Conn)
		require.True(t, ok)
		require.NotNil(t, cmConn.freeConn)
	})

	t.Run("conn already exists", func(t *testing.T) {
		dialConnID := uint16(1)
		dialLocalPort := routing.Port(1)
		var dialErr error

		var closeErr error

		rpc := &MockRPCClient{}
		rpc.On("Dial", remote).Return(dialConnID, dialLocalPort, dialErr)
		rpc.On("CloseConn", dialConnID).Return(closeErr)

		cl := prepClient(l, visorPK, rpc)

		_, err := cl.cm.Add(dialConnID, nil)
		require.NoError(t, err)

		conn, err := cl.Dial(remote)
		require.Equal(t, err, idmanager.ErrValueAlreadyExists)
		require.Nil(t, conn)
	})

	t.Run("conn already exists, conn closed with error", func(t *testing.T) {
		dialConnID := uint16(1)
		dialLocalPort := routing.Port(1)
		var dialErr error

		closeErr := errors.New("close error")

		rpc := &MockRPCClient{}
		rpc.On("Dial", remote).Return(dialConnID, dialLocalPort, dialErr)
		rpc.On("CloseConn", dialConnID).Return(closeErr)

		cl := prepClient(l, visorPK, rpc)

		_, err := cl.cm.Add(dialConnID, nil)
		require.NoError(t, err)

		conn, err := cl.Dial(remote)
		require.Equal(t, err, idmanager.ErrValueAlreadyExists)
		require.Nil(t, conn)
	})

	t.Run("dial error", func(t *testing.T) {
		dialErr := errors.New("dial error")

		rpc := &MockRPCClient{}
		rpc.On("Dial", remote).Return(uint16(0), routing.Port(0), dialErr)

		cl := prepClient(l, visorPK, rpc)

		conn, err := cl.Dial(remote)
		require.Equal(t, dialErr, err)
		require.Nil(t, conn)
	})
}

func TestClient_Listen(t *testing.T) {
	l := logging.MustGetLogger("app2_client")
	visorPK, _ := cipher.GenerateKeyPair()

	port := routing.Port(1)
	local := appnet.Addr{
		Net:    appnet.TypeDmsg,
		PubKey: visorPK,
		Port:   port,
	}

	t.Run("ok", func(t *testing.T) {
		listenLisID := uint16(1)
		var listenErr error

		rpc := &MockRPCClient{}
		rpc.On("Listen", local).Return(listenLisID, listenErr)

		cl := prepClient(l, visorPK, rpc)

		wantListener := &Listener{
			id:   listenLisID,
			rpc:  rpc,
			addr: local,
		}

		listener, err := cl.Listen(appnet.TypeDmsg, port)
		require.Nil(t, err)

		appListener, ok := listener.(*Listener)
		require.True(t, ok)

		require.Equal(t, wantListener.id, appListener.id)
		require.Equal(t, wantListener.rpc, appListener.rpc)
		require.Equal(t, wantListener.addr, appListener.addr)
		require.NotNil(t, appListener.freeLis)
	})

	t.Run("listener already exists", func(t *testing.T) {
		listenLisID := uint16(1)
		var listenErr error

		var closeErr error

		rpc := &MockRPCClient{}
		rpc.On("Listen", local).Return(listenLisID, listenErr)
		rpc.On("CloseListener", listenLisID).Return(closeErr)

		cl := prepClient(l, visorPK, rpc)

		_, err := cl.lm.Add(listenLisID, nil)
		require.NoError(t, err)

		listener, err := cl.Listen(appnet.TypeDmsg, port)
		require.Equal(t, err, idmanager.ErrValueAlreadyExists)
		require.Nil(t, listener)
	})

	t.Run("listener already exists, listener closed with error", func(t *testing.T) {
		listenLisID := uint16(1)
		var listenErr error

		closeErr := errors.New("close error")

		rpc := &MockRPCClient{}
		rpc.On("Listen", local).Return(listenLisID, listenErr)
		rpc.On("CloseListener", listenLisID).Return(closeErr)

		cl := prepClient(l, visorPK, rpc)

		_, err := cl.lm.Add(listenLisID, nil)
		require.NoError(t, err)

		listener, err := cl.Listen(appnet.TypeDmsg, port)
		require.Equal(t, err, idmanager.ErrValueAlreadyExists)
		require.Nil(t, listener)
	})

	t.Run("listen error", func(t *testing.T) {
		listenErr := errors.New("listen error")

		rpc := &MockRPCClient{}
		rpc.On("Listen", local).Return(uint16(0), listenErr)

		cl := prepClient(l, visorPK, rpc)

		listener, err := cl.Listen(appnet.TypeDmsg, port)
		require.Equal(t, listenErr, err)
		require.Nil(t, listener)
	})
}

func TestClient_Close(t *testing.T) {
	l := logging.MustGetLogger("app2_client")
	visorPK, _ := cipher.GenerateKeyPair()

	var (
		closeNoErr error
		closeErr   = errors.New("close error")
	)

	rpc := &MockRPCClient{}

	lisID1 := uint16(1)
	lisID2 := uint16(2)

	rpc.On("CloseListener", lisID1).Return(closeNoErr)
	rpc.On("CloseListener", lisID2).Return(closeErr)

	lm := idmanager.New()

	lis1 := &Listener{id: lisID1, rpc: rpc, cm: idmanager.New()}
	freeLis1, err := lm.Add(lisID1, lis1)
	require.NoError(t, err)

	lis1.freeLis = freeLis1

	lis2 := &Listener{id: lisID2, rpc: rpc, cm: idmanager.New()}
	freeLis2, err := lm.Add(lisID2, lis2)
	require.NoError(t, err)

	lis2.freeLis = freeLis2

	connID1 := uint16(1)
	connID2 := uint16(2)

	rpc.On("CloseConn", connID1).Return(closeNoErr)
	rpc.On("CloseConn", connID2).Return(closeErr)

	cm := idmanager.New()

	conn1 := &Conn{id: connID1, rpc: rpc}
	freeConn1, err := cm.Add(connID1, conn1)
	require.NoError(t, err)

	conn1.freeConn = freeConn1

	conn2 := &Conn{id: connID2, rpc: rpc}
	freeConn2, err := cm.Add(connID2, conn2)
	require.NoError(t, err)

	conn2.freeConn = freeConn2

	cl := prepClient(l, visorPK, rpc)
	cl.cm = cm
	cl.lm = lm

	cl.Close()

	_, ok := lm.Get(lisID1)
	require.False(t, ok)
	_, ok = lm.Get(lisID2)
	require.False(t, ok)

	_, ok = cm.Get(connID1)
	require.False(t, ok)
	_, ok = cm.Get(connID2)
	require.False(t, ok)
}

func prepClient(l *logging.Logger, visorPK cipher.PubKey, rpc RPCClient) *Client {
	var procKey appcommon.ProcKey
	copy(procKey[:], visorPK[:])
	return &Client{
		log: l,
		conf: appcommon.ProcConfig{
			AppName:     "",
			AppSrvAddr:  "",
			ProcKey:     procKey,
			ProcArgs:    nil,
			ProcWorkDir: "",
			VisorPK:     visorPK,
			RoutingPort: 0,
			BinaryLoc:   "",
			LogDBLoc:    "",
		},
		rpc: rpc,
		lm:  idmanager.New(),
		cm:  idmanager.New(),
	}
}
