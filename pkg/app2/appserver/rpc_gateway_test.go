package appserver

import (
	"context"
	"math"
	"net"
	"strings"
	"testing"

	"github.com/skycoin/skywire/pkg/app2/appcommon"
	"github.com/skycoin/skywire/pkg/app2/idmanager"

	"github.com/pkg/errors"
	"github.com/skycoin/dmsg"
	"github.com/skycoin/dmsg/cipher"
	"github.com/skycoin/skycoin/src/util/logging"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/app2/appnet"
	"github.com/skycoin/skywire/pkg/routing"
)

func TestRPCGateway_Dial(t *testing.T) {
	l := logging.MustGetLogger("rpc_gateway")
	nType := appnet.TypeDMSG

	dialAddr := prepAddr(nType)

	t.Run("ok", func(t *testing.T) {
		appnet.ClearNetworkers()

		localPort := routing.Port(100)

		dialCtx := context.Background()
		dialConn := dmsg.NewTransport(nil, nil, dmsg.Addr{Port: uint16(localPort)}, dmsg.Addr{}, 0, 10, func() {})
		var dialErr error

		n := &appnet.MockNetworker{}
		n.On("DialContext", dialCtx, dialAddr).Return(dialConn, dialErr)

		err := appnet.AddNetworker(nType, n)
		require.NoError(t, err)

		rpc := newRPCGateway(l)

		var resp DialResp
		err = rpc.Dial(&dialAddr, &resp)
		require.NoError(t, err)
		require.Equal(t, resp.ConnID, uint16(1))
		require.Equal(t, resp.LocalPort, localPort)
	})

	t.Run("no more slots for a new conn", func(t *testing.T) {
		rpc := newRPCGateway(l)
		for i, _, err := rpc.cm.ReserveNextID(); i == nil || *i != 0; i, _, err = rpc.cm.ReserveNextID() {
			require.NoError(t, err)
		}

		for i := uint16(0); i < math.MaxUint16; i++ {
			err := rpc.cm.Set(i, nil)
			require.NoError(t, err)
		}
		err := rpc.cm.Set(math.MaxUint16, nil)

		var resp DialResp
		err = rpc.Dial(&dialAddr, &resp)
		require.Equal(t, err, idmanager.ErrNoMoreAvailableValues)
	})

	t.Run("dial error", func(t *testing.T) {
		appnet.ClearNetworkers()

		dialCtx := context.Background()
		var dialConn net.Conn
		dialErr := errors.New("dial error")

		n := &appnet.MockNetworker{}
		n.On("DialContext", dialCtx, dialAddr).Return(dialConn, dialErr)

		err := appnet.AddNetworker(nType, n)
		require.NoError(t, err)

		rpc := newRPCGateway(l)

		var resp DialResp
		err = rpc.Dial(&dialAddr, &resp)
		require.Equal(t, err, dialErr)
	})

	t.Run("error wrapping conn", func(t *testing.T) {
		appnet.ClearNetworkers()

		remoteAddr, localAddr := &appcommon.MockAddr{}, &appcommon.MockAddr{}

		dialCtx := context.Background()
		dialConn := &appcommon.MockConn{}
		dialConn.On("LocalAddr").Return(localAddr)
		dialConn.On("RemoteAddr").Return(remoteAddr)
		var dialErr error

		n := &appnet.MockNetworker{}
		n.On("DialContext", dialCtx, dialAddr).Return(dialConn, dialErr)

		err := appnet.AddNetworker(nType, n)
		require.NoError(t, err)

		rpc := newRPCGateway(l)

		var resp DialResp
		err = rpc.Dial(&dialAddr, &resp)
		require.Equal(t, err, appnet.ErrUnknownAddrType)
	})
}

func TestRPCGateway_Listen(t *testing.T) {
	l := logging.MustGetLogger("rpc_gateway")
	nType := appnet.TypeDMSG

	listenAddr := prepAddr(nType)

	t.Run("ok", func(t *testing.T) {
		appnet.ClearNetworkers()

		listenCtx := context.Background()
		listenLis := &dmsg.Listener{}
		var listenErr error

		n := &appnet.MockNetworker{}
		n.On("ListenContext", listenCtx, listenAddr).Return(listenLis, listenErr)

		err := appnet.AddNetworker(nType, n)
		require.Equal(t, err, listenErr)

		rpc := newRPCGateway(l)

		var lisID uint16

		err = rpc.Listen(&listenAddr, &lisID)
		require.NoError(t, err)
		require.Equal(t, lisID, uint16(1))
	})

	t.Run("no more slots for a new listener", func(t *testing.T) {
		rpc := newRPCGateway(l)
		for i, _, err := rpc.lm.ReserveNextID(); i == nil || *i != 0; i, _, err = rpc.lm.ReserveNextID() {
			require.NoError(t, err)
		}

		for i := uint16(0); i < math.MaxUint16; i++ {
			err := rpc.lm.Set(i, nil)
			require.NoError(t, err)
		}
		err := rpc.lm.Set(math.MaxUint16, nil)
		require.NoError(t, err)

		var lisID uint16

		err = rpc.Listen(&listenAddr, &lisID)
		require.Equal(t, err, idmanager.ErrNoMoreAvailableValues)
	})

	t.Run("listen error", func(t *testing.T) {
		appnet.ClearNetworkers()

		listenCtx := context.Background()
		var listenLis net.Listener
		listenErr := errors.New("listen error")

		n := &appnet.MockNetworker{}
		n.On("ListenContext", listenCtx, listenAddr).Return(listenLis, listenErr)

		err := appnet.AddNetworker(nType, n)
		require.NoError(t, err)

		rpc := newRPCGateway(l)

		var lisID uint16

		err = rpc.Listen(&listenAddr, &lisID)
		require.Equal(t, err, listenErr)
	})
}

func TestRPCGateway_Accept(t *testing.T) {
	l := logging.MustGetLogger("rpc_gateway")

	t.Run("ok", func(t *testing.T) {
		rpc := newRPCGateway(l)

		acceptConn := &dmsg.Transport{}
		var acceptErr error

		lis := &appcommon.MockListener{}
		lis.On("Accept").Return(acceptConn, acceptErr)

		lisID := addListener(t, rpc, lis)

		var resp AcceptResp
		err := rpc.Accept(&lisID, &resp)
		require.NoError(t, err)
		require.Equal(t, resp.Remote, appnet.Addr{Net: appnet.TypeDMSG})
	})

	t.Run("no such listener", func(t *testing.T) {
		rpc := newRPCGateway(l)

		lisID := uint16(1)

		var resp AcceptResp
		err := rpc.Accept(&lisID, &resp)
		require.Error(t, err)
		require.True(t, strings.Contains(err.Error(), "no listener"))
	})

	t.Run("listener is not set", func(t *testing.T) {
		rpc := newRPCGateway(l)

		lisID := addListener(t, rpc, nil)

		var resp AcceptResp
		err := rpc.Accept(&lisID, &resp)
		require.Error(t, err)
		require.True(t, strings.Contains(err.Error(), "no listener"))
	})

	t.Run("no more slots for a new conn", func(t *testing.T) {
		rpc := newRPCGateway(l)

		for i, _, err := rpc.cm.ReserveNextID(); i == nil || *i != 0; i, _, err = rpc.cm.ReserveNextID() {
			require.NoError(t, err)
		}

		for i := uint16(0); i < math.MaxUint16; i++ {
			err := rpc.cm.Set(i, nil)
			require.NoError(t, err)
		}
		err := rpc.cm.Set(math.MaxUint16, nil)
		require.NoError(t, err)

		lisID := addListener(t, rpc, &appcommon.MockListener{})

		var resp AcceptResp
		err = rpc.Accept(&lisID, &resp)
		require.Equal(t, err, idmanager.ErrNoMoreAvailableValues)
	})

	t.Run("error wrapping conn", func(t *testing.T) {
		rpc := newRPCGateway(l)

		remoteAddr, localAddr := &appcommon.MockAddr{}, &appcommon.MockAddr{}

		acceptConn := &appcommon.MockConn{}
		acceptConn.On("LocalAddr").Return(localAddr)
		acceptConn.On("RemoteAddr").Return(remoteAddr)
		var acceptErr error

		lis := &appcommon.MockListener{}
		lis.On("Accept").Return(acceptConn, acceptErr)

		lisID := addListener(t, rpc, lis)

		var resp AcceptResp
		err := rpc.Accept(&lisID, &resp)
		require.Equal(t, err, appnet.ErrUnknownAddrType)
	})

	t.Run("accept error", func(t *testing.T) {
		rpc := newRPCGateway(l)

		var acceptConn net.Conn
		acceptErr := errors.New("accept error")

		lis := &appcommon.MockListener{}
		lis.On("Accept").Return(acceptConn, acceptErr)

		lisID := addListener(t, rpc, lis)

		var resp AcceptResp
		err := rpc.Accept(&lisID, &resp)
		require.Equal(t, err, acceptErr)
	})
}

func TestRPCGateway_Write(t *testing.T) {
	l := logging.MustGetLogger("rpc_gateway")

	writeBuff := []byte{1, 1, 1, 1, 1, 1, 1, 1, 1, 1}
	writeN := 10

	t.Run("ok", func(t *testing.T) {
		rpc := newRPCGateway(l)

		var writeErr error

		conn := &appcommon.MockConn{}
		conn.On("Write", writeBuff).Return(writeN, writeErr)

		connID := addConn(t, rpc, conn)

		req := WriteReq{
			ConnID: connID,
			B:      writeBuff,
		}

		var n int
		err := rpc.Write(&req, &n)
		require.NoError(t, err)
		require.Equal(t, n, writeN)
	})

	t.Run("no such conn", func(t *testing.T) {
		rpc := newRPCGateway(l)

		connID := uint16(1)

		req := WriteReq{
			ConnID: connID,
			B:      writeBuff,
		}

		var n int
		err := rpc.Write(&req, &n)
		require.Error(t, err)
		require.True(t, strings.Contains(err.Error(), "no conn"))
	})

	t.Run("conn is not set", func(t *testing.T) {
		rpc := newRPCGateway(l)

		connID := addConn(t, rpc, nil)

		req := WriteReq{
			ConnID: connID,
			B:      writeBuff,
		}

		var n int
		err := rpc.Write(&req, &n)
		require.Error(t, err)
		require.True(t, strings.Contains(err.Error(), "no conn"))
	})

	t.Run("write error", func(t *testing.T) {
		rpc := newRPCGateway(l)

		writeErr := errors.New("write error")

		conn := &appcommon.MockConn{}
		conn.On("Write", writeBuff).Return(writeN, writeErr)

		connID := addConn(t, rpc, conn)

		req := WriteReq{
			ConnID: connID,
			B:      writeBuff,
		}

		var n int
		err := rpc.Write(&req, &n)
		require.Error(t, err)
		require.Equal(t, err, writeErr)
	})
}

func TestRPCGateway_Read(t *testing.T) {
	l := logging.MustGetLogger("rpc_gateway")

	readBufLen := 10
	readBuf := make([]byte, readBufLen)

	t.Run("ok", func(t *testing.T) {
		rpc := newRPCGateway(l)

		readN := 10
		var readErr error

		conn := &appcommon.MockConn{}
		conn.On("Read", readBuf).Return(readN, readErr)

		connID := addConn(t, rpc, conn)

		req := ReadReq{
			ConnID: connID,
			BufLen: readBufLen,
		}

		wantResp := ReadResp{
			B: readBuf,
			N: readN,
		}

		var resp ReadResp
		err := rpc.Read(&req, &resp)
		require.NoError(t, err)
		require.Equal(t, resp, wantResp)
	})

	t.Run("no such conn", func(t *testing.T) {
		rpc := newRPCGateway(l)

		connID := uint16(1)

		req := ReadReq{
			ConnID: connID,
			BufLen: readBufLen,
		}

		var resp ReadResp
		err := rpc.Read(&req, &resp)
		require.Error(t, err)
		require.True(t, strings.Contains(err.Error(), "no conn"))
	})

	t.Run("conn is not set", func(t *testing.T) {
		rpc := newRPCGateway(l)

		connID := addConn(t, rpc, nil)

		req := ReadReq{
			ConnID: connID,
			BufLen: readBufLen,
		}

		var resp ReadResp
		err := rpc.Read(&req, &resp)
		require.Error(t, err)
		require.True(t, strings.Contains(err.Error(), "no conn"))
	})

	t.Run("read error", func(t *testing.T) {
		rpc := newRPCGateway(l)

		readN := 0
		readErr := errors.New("read error")

		conn := &appcommon.MockConn{}
		conn.On("Read", readBuf).Return(readN, readErr)

		connID := addConn(t, rpc, conn)

		req := ReadReq{
			ConnID: connID,
			BufLen: readBufLen,
		}

		var resp ReadResp
		err := rpc.Read(&req, &resp)
		require.Equal(t, err, readErr)
	})
}

func TestRPCGateway_CloseConn(t *testing.T) {
	l := logging.MustGetLogger("rpc_gateway")

	t.Run("ok", func(t *testing.T) {
		rpc := newRPCGateway(l)

		var closeErr error

		conn := &appcommon.MockConn{}
		conn.On("Close").Return(closeErr)

		connID := addConn(t, rpc, conn)

		err := rpc.CloseConn(&connID, nil)
		require.NoError(t, err)
		_, ok := rpc.cm.Get(connID)
		require.False(t, ok)
	})

	t.Run("no such conn", func(t *testing.T) {
		rpc := newRPCGateway(l)

		connID := uint16(1)

		err := rpc.CloseConn(&connID, nil)
		require.Error(t, err)
		require.True(t, strings.Contains(err.Error(), "no conn"))
	})

	t.Run("conn is not set", func(t *testing.T) {
		rpc := newRPCGateway(l)

		connID := addConn(t, rpc, nil)

		err := rpc.CloseConn(&connID, nil)
		require.Error(t, err)
		require.True(t, strings.Contains(err.Error(), "no conn"))
	})

	t.Run("close error", func(t *testing.T) {
		rpc := newRPCGateway(l)

		closeErr := errors.New("close error")

		conn := &appcommon.MockConn{}
		conn.On("Close").Return(closeErr)

		connID := addConn(t, rpc, conn)

		err := rpc.CloseConn(&connID, nil)
		require.Equal(t, err, closeErr)
	})
}

func TestRPCGateway_CloseListener(t *testing.T) {
	l := logging.MustGetLogger("rpc_gateway")

	t.Run("ok", func(t *testing.T) {
		rpc := newRPCGateway(l)

		var closeErr error

		lis := &appcommon.MockListener{}
		lis.On("Close").Return(closeErr)

		lisID := addListener(t, rpc, lis)

		err := rpc.CloseListener(&lisID, nil)
		require.NoError(t, err)
		_, ok := rpc.cm.Get(lisID)
		require.False(t, ok)
	})

	t.Run("no such listener", func(t *testing.T) {
		rpc := newRPCGateway(l)

		lisID := uint16(1)

		err := rpc.CloseListener(&lisID, nil)
		require.Error(t, err)
		require.True(t, strings.Contains(err.Error(), "no listener"))
	})

	t.Run("listener is not set", func(t *testing.T) {
		rpc := newRPCGateway(l)

		lisID := addListener(t, rpc, nil)

		err := rpc.CloseListener(&lisID, nil)
		require.Error(t, err)
		require.True(t, strings.Contains(err.Error(), "no listener"))
	})

	t.Run("close error", func(t *testing.T) {
		rpc := newRPCGateway(l)

		closeErr := errors.New("close error")

		lis := &appcommon.MockListener{}
		lis.On("Close").Return(closeErr)

		lisID := addListener(t, rpc, lis)

		err := rpc.CloseListener(&lisID, nil)
		require.Equal(t, err, closeErr)
	})
}

func prepAddr(nType appnet.Type) appnet.Addr {
	pk, _ := cipher.GenerateKeyPair()
	port := routing.Port(100)

	return appnet.Addr{
		Net:    nType,
		PubKey: pk,
		Port:   port,
	}
}

func addConn(t *testing.T, rpc *RPCGateway, conn net.Conn) uint16 {
	connID, _, err := rpc.cm.ReserveNextID()
	require.NoError(t, err)

	err = rpc.cm.Set(*connID, conn)
	require.NoError(t, err)

	return *connID
}

func addListener(t *testing.T, rpc *RPCGateway, lis net.Listener) uint16 {
	lisID, _, err := rpc.lm.ReserveNextID()
	require.NoError(t, err)

	err = rpc.lm.Set(*lisID, lis)
	require.NoError(t, err)

	return *lisID
}
