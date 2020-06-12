package appserver

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/SkycoinProject/dmsg"
	"github.com/SkycoinProject/dmsg/cipher"
	"github.com/SkycoinProject/skycoin/src/util/logging"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/SkycoinProject/skywire-mainnet/internal/testhelpers"
	"github.com/SkycoinProject/skywire-mainnet/pkg/app/appcommon"
	"github.com/SkycoinProject/skywire-mainnet/pkg/app/appnet"
	"github.com/SkycoinProject/skywire-mainnet/pkg/app/idmanager"
	"github.com/SkycoinProject/skywire-mainnet/pkg/routing"
)

func TestRPCGateway_Dial(t *testing.T) {
	l := logging.MustGetLogger("rpc_gateway")
	nType := appnet.TypeDmsg

	dialAddr := prepAddr(nType)

	t.Run("ok", func(t *testing.T) {
		testRPCGatewayDialOK(t, l, nType, dialAddr)
	})

	t.Run("no more slots for a new conn", func(t *testing.T) {
		testRPCGatewayDialNoMoreSlots(t, l, dialAddr)
	})

	t.Run("dial error", func(t *testing.T) {
		testRPCGatewayDialError(t, l, nType, dialAddr)
	})

	t.Run("error wrapping conn", func(t *testing.T) {
		testRPCGatewayDialErrorWrappingConn(t, l, nType, dialAddr)
	})
}

func testRPCGatewayDialOK(t *testing.T, l *logging.Logger, nType appnet.Type, dialAddr appnet.Addr) {
	appnet.ClearNetworkers()

	const localPort routing.Port = 100

	dialCtx := context.Background()
	dialConn := &appcommon.MockConn{}
	dialConn.On("LocalAddr").Return(dmsg.Addr{Port: uint16(localPort)})
	dialConn.On("RemoteAddr").Return(dmsg.Addr{})

	var dialErr error

	n := &appnet.MockNetworker{}
	n.On("DialContext", dialCtx, dialAddr).Return(dialConn, dialErr)

	err := appnet.AddNetworker(nType, n)
	require.NoError(t, err)

	rpc := NewRPCGateway(l)

	var resp DialResp
	err = rpc.Dial(&dialAddr, &resp)
	require.NoError(t, err)

	const wantConnID uint16 = 1

	require.Equal(t, resp.ConnID, wantConnID)
	require.Equal(t, resp.LocalPort, localPort)
}

func testRPCGatewayDialNoMoreSlots(t *testing.T, l *logging.Logger, dialAddr appnet.Addr) {
	rpc := NewRPCGateway(l)

	for i, _, err := rpc.cm.ReserveNextID(); i == nil || *i != 0; i, _, err = rpc.cm.ReserveNextID() {
		require.NoError(t, err)
	}

	for i := uint16(0); i < math.MaxUint16; i++ {
		err := rpc.cm.Set(i, nil)
		require.NoError(t, err)
	}

	err := rpc.cm.Set(math.MaxUint16, nil)
	require.NoError(t, err)

	var resp DialResp
	err = rpc.Dial(&dialAddr, &resp)
	require.Equal(t, err, idmanager.ErrNoMoreAvailableValues)
}

func testRPCGatewayDialError(t *testing.T, l *logging.Logger, nType appnet.Type, dialAddr appnet.Addr) {
	appnet.ClearNetworkers()

	dialCtx := context.Background()
	dialErr := errors.New("dial error")

	var dialConn net.Conn

	n := &appnet.MockNetworker{}
	n.On("DialContext", dialCtx, dialAddr).Return(dialConn, dialErr)

	err := appnet.AddNetworker(nType, n)
	require.NoError(t, err)

	rpc := NewRPCGateway(l)

	var resp DialResp
	err = rpc.Dial(&dialAddr, &resp)
	require.Equal(t, err, dialErr)
}

func testRPCGatewayDialErrorWrappingConn(t *testing.T, l *logging.Logger, nType appnet.Type, dialAddr appnet.Addr) {
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

	rpc := NewRPCGateway(l)

	var resp DialResp
	err = rpc.Dial(&dialAddr, &resp)
	require.Equal(t, err, appnet.ErrUnknownAddrType)
}

func TestRPCGateway_Listen(t *testing.T) {
	l := logging.MustGetLogger("rpc_gateway")
	nType := appnet.TypeDmsg

	listenAddr := prepAddr(nType)

	t.Run("ok", func(t *testing.T) {
		testRPCGatewayListenOK(t, l, nType, listenAddr)
	})

	t.Run("no more slots for a new listener", func(t *testing.T) {
		testRPCGatewayListenNoMoreSlots(t, l, listenAddr)
	})

	t.Run("listen error", func(t *testing.T) {
		testRPCGatewayListenError(t, l, nType, listenAddr)
	})
}

func testRPCGatewayListenOK(t *testing.T, l *logging.Logger, nType appnet.Type, listenAddr appnet.Addr) {
	appnet.ClearNetworkers()

	listenCtx := context.Background()
	listenLis := &dmsg.Listener{}

	var listenErr error

	n := &appnet.MockNetworker{}
	n.On("ListenContext", listenCtx, listenAddr).Return(listenLis, listenErr)

	err := appnet.AddNetworker(nType, n)
	require.Equal(t, err, listenErr)

	rpc := NewRPCGateway(l)

	var lisID uint16

	err = rpc.Listen(&listenAddr, &lisID)
	require.NoError(t, err)

	const wantLisID uint16 = 1

	require.Equal(t, lisID, wantLisID)
}

func testRPCGatewayListenNoMoreSlots(t *testing.T, l *logging.Logger, listenAddr appnet.Addr) {
	rpc := NewRPCGateway(l)

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
}

func testRPCGatewayListenError(t *testing.T, l *logging.Logger, nType appnet.Type, listenAddr appnet.Addr) {
	appnet.ClearNetworkers()

	listenCtx := context.Background()
	listenErr := errors.New("listen error")

	var listenLis net.Listener

	n := &appnet.MockNetworker{}
	n.On("ListenContext", listenCtx, listenAddr).Return(listenLis, listenErr)

	err := appnet.AddNetworker(nType, n)
	require.NoError(t, err)

	rpc := NewRPCGateway(l)

	var lisID uint16

	err = rpc.Listen(&listenAddr, &lisID)
	require.Equal(t, err, listenErr)
}

func TestRPCGateway_Accept(t *testing.T) {
	l := logging.MustGetLogger("rpc_gateway")

	t.Run("ok", func(t *testing.T) {
		testRPCGatewayAcceptOK(t, l)
	})

	t.Run("no such listener", func(t *testing.T) {
		testRPCGatewayAcceptNoSuchListener(t, l)
	})

	t.Run("listener is not set", func(t *testing.T) {
		testRPCGatewayAcceptListenerNotSet(t, l)
	})

	t.Run("no more slots for a new conn", func(t *testing.T) {
		testRPCGatewayAcceptNoMoreSlots(t, l)
	})

	t.Run("error wrapping conn", func(t *testing.T) {
		testRPCGatewayAcceptErrorWrappingConn(t, l)
	})

	t.Run("accept error", func(t *testing.T) {
		testRPCGatewayAcceptError(t, l)
	})
}

func testRPCGatewayAcceptOK(t *testing.T, l *logging.Logger) {
	rpc := NewRPCGateway(l)

	acceptConn := &dmsg.Stream{}

	var acceptErr error

	lis := &appcommon.MockListener{}
	lis.On("Accept").Return(acceptConn, acceptErr)

	lisID := addListener(t, rpc, lis)

	var resp AcceptResp
	err := rpc.Accept(&lisID, &resp)
	require.NoError(t, err)
	require.Equal(t, resp.Remote, appnet.Addr{Net: appnet.TypeDmsg})
}

func testRPCGatewayAcceptNoSuchListener(t *testing.T, l *logging.Logger) {
	rpc := NewRPCGateway(l)

	lisID := uint16(1) // nolint: gomnd

	var resp AcceptResp
	err := rpc.Accept(&lisID, &resp)
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "no listener"))
}

func testRPCGatewayAcceptListenerNotSet(t *testing.T, l *logging.Logger) {
	rpc := NewRPCGateway(l)

	lisID := addListener(t, rpc, nil)

	var resp AcceptResp
	err := rpc.Accept(&lisID, &resp)
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "no listener"))
}

func testRPCGatewayAcceptNoMoreSlots(t *testing.T, l *logging.Logger) {
	rpc := NewRPCGateway(l)

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
}

func testRPCGatewayAcceptErrorWrappingConn(t *testing.T, l *logging.Logger) {
	rpc := NewRPCGateway(l)

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
}

func testRPCGatewayAcceptError(t *testing.T, l *logging.Logger) {
	rpc := NewRPCGateway(l)

	var acceptConn net.Conn

	acceptErr := errors.New("accept error")

	lis := &appcommon.MockListener{}
	lis.On("Accept").Return(acceptConn, acceptErr)

	lisID := addListener(t, rpc, lis)

	var resp AcceptResp
	err := rpc.Accept(&lisID, &resp)
	require.Equal(t, err, acceptErr)
}

func TestRPCGateway_Write(t *testing.T) {
	l := logging.MustGetLogger("rpc_gateway")

	writeBuff := []byte{1, 1, 1, 1, 1, 1, 1, 1, 1, 1}

	t.Run("ok", func(t *testing.T) {
		testRPCGatewayWriteOK(t, l, writeBuff)
	})

	t.Run("no such conn", func(t *testing.T) {
		testRPCGatewayWriteNoSuchConn(t, l, writeBuff)
	})

	t.Run("conn is not set", func(t *testing.T) {
		testRPCGatewayWriteConnNotSet(t, l, writeBuff)
	})

	t.Run("write error", func(t *testing.T) {
		testRPCGatewayWriteError(t, l, writeBuff)
	})
}

func testRPCGatewayWriteOK(t *testing.T, l *logging.Logger, writeBuff []byte) {
	rpc := NewRPCGateway(l)

	var writeErr error

	conn := &appcommon.MockConn{}
	conn.On("Write", writeBuff).Return(len(writeBuff), writeErr)

	connID := addConn(t, rpc, conn)

	req := WriteReq{
		ConnID: connID,
		B:      writeBuff,
	}

	wantResp := WriteResp{
		N:   len(writeBuff),
		Err: nil,
	}

	var resp WriteResp
	err := rpc.Write(&req, &resp)
	require.NoError(t, err)
	require.Equal(t, wantResp, resp)
}

func testRPCGatewayWriteNoSuchConn(t *testing.T, l *logging.Logger, writeBuff []byte) {
	const connID uint16 = 1

	rpc := NewRPCGateway(l)
	req := WriteReq{
		ConnID: connID,
		B:      writeBuff,
	}

	var resp WriteResp
	err := rpc.Write(&req, &resp)
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "no conn"))
}

func testRPCGatewayWriteConnNotSet(t *testing.T, l *logging.Logger, writeBuff []byte) {
	rpc := NewRPCGateway(l)

	connID := addConn(t, rpc, nil)

	req := WriteReq{
		ConnID: connID,
		B:      writeBuff,
	}

	var resp WriteResp
	err := rpc.Write(&req, &resp)
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "no conn"))
}

func testRPCGatewayWriteError(t *testing.T, l *logging.Logger, writeBuff []byte) {
	rpc := NewRPCGateway(l)

	writeErr := errors.New("write error")

	conn := &appcommon.MockConn{}
	conn.On("Write", writeBuff).Return(len(writeBuff)/2, writeErr) // nolint: gomnd

	connID := addConn(t, rpc, conn)

	req := WriteReq{
		ConnID: connID,
		B:      writeBuff,
	}

	wantResp := WriteResp{
		N: len(writeBuff) / 2, // nolint: gomnd
		Err: &RPCIOErr{
			Text:           writeErr.Error(),
			IsNetErr:       false,
			IsTimeoutErr:   false,
			IsTemporaryErr: false,
		},
	}

	var resp WriteResp
	err := rpc.Write(&req, &resp)
	require.NoError(t, err)
	require.Equal(t, wantResp, resp)
}

func TestRPCGateway_Read(t *testing.T) {
	l := logging.MustGetLogger("rpc_gateway")

	readBufLen := 10
	readBuf := make([]byte, readBufLen)

	t.Run("ok", func(t *testing.T) {
		testRPCGatewayReadOK(t, l, readBuf)
	})

	t.Run("no such conn", func(t *testing.T) {
		testRPCGatewayReadNoSuchConn(t, l, readBufLen)
	})

	t.Run("conn is not set", func(t *testing.T) {
		testRPCGatewayReadConnNotSet(t, l, readBufLen)
	})

	t.Run("read error", func(t *testing.T) {
		testRPCGatewayReadError(t, l, readBuf)
	})
}

func testRPCGatewayReadOK(t *testing.T, l *logging.Logger, readBuf []byte) {
	rpc := NewRPCGateway(l)

	readN := 10

	conn := &appcommon.MockConn{}
	conn.On("Read", readBuf).Return(readN, testhelpers.NoErr)

	connID := addConn(t, rpc, conn)

	req := ReadReq{
		ConnID: connID,
		BufLen: len(readBuf),
	}

	wantResp := ReadResp{
		B: readBuf,
		N: readN,
	}

	var resp ReadResp
	err := rpc.Read(&req, &resp)
	require.NoError(t, err)
	require.Equal(t, resp, wantResp)
}

func testRPCGatewayReadNoSuchConn(t *testing.T, l *logging.Logger, readBufLen int) {
	const connID uint16 = 1

	rpc := NewRPCGateway(l)
	req := ReadReq{
		ConnID: connID,
		BufLen: readBufLen,
	}

	var resp ReadResp
	err := rpc.Read(&req, &resp)
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "no conn"))
}

func testRPCGatewayReadConnNotSet(t *testing.T, l *logging.Logger, readBufLen int) {
	rpc := NewRPCGateway(l)

	connID := addConn(t, rpc, nil)

	req := ReadReq{
		ConnID: connID,
		BufLen: readBufLen,
	}

	var resp ReadResp
	err := rpc.Read(&req, &resp)
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "no conn"))
}

func testRPCGatewayReadError(t *testing.T, l *logging.Logger, readBuf []byte) {
	rpc := NewRPCGateway(l)

	readN := 3
	readErr := errors.New("read error")

	conn := &appcommon.MockConn{}
	conn.On("Read", readBuf).Return(readN, readErr)

	connID := addConn(t, rpc, conn)

	req := ReadReq{
		ConnID: connID,
		BufLen: len(readBuf),
	}

	wantResp := ReadResp{
		B: make([]byte, readN),
		N: readN,
		Err: &RPCIOErr{
			Text:           readErr.Error(),
			IsNetErr:       false,
			IsTimeoutErr:   false,
			IsTemporaryErr: false,
		},
	}

	var resp ReadResp
	err := rpc.Read(&req, &resp)
	require.NoError(t, err)
	require.Equal(t, wantResp, resp)
}

func TestRPCGateway_SetWriteDeadline(t *testing.T) {
	l := logging.MustGetLogger("rpc_gateway")

	const delay = 1 * time.Hour

	deadline := time.Now().Add(delay)

	t.Run("ok", func(t *testing.T) {
		testRPCGatewaySetWriteDeadlineOK(t, l, deadline)
	})

	t.Run("no such conn", func(t *testing.T) {
		testRPCGatewaySetWriteDeadlineNoSuchConn(t, l, deadline)
	})

	t.Run("conn is not set", func(t *testing.T) {
		testRPCGatewaySetWriteDeadlineConnNotSet(t, l, deadline)
	})

	t.Run("set read deadline error", func(t *testing.T) {
		testRPCGatewaySetWriteDeadlineError(t, l, deadline)
	})
}

func testRPCGatewaySetWriteDeadlineOK(t *testing.T, l *logging.Logger, deadline time.Time) {
	rpc := NewRPCGateway(l)

	conn := &appcommon.MockConn{}
	conn.On("SetWriteDeadline", mock.Anything).Return(func(d time.Time) error {
		if !deadline.Equal(d) {
			return fmt.Errorf("expected deadline %v, got %v", deadline, d)
		}

		return nil
	})

	connID := addConn(t, rpc, conn)

	req := DeadlineReq{
		ConnID:   connID,
		Deadline: deadline,
	}
	err := rpc.SetWriteDeadline(&req, nil)
	require.NoError(t, err)
}

func testRPCGatewaySetWriteDeadlineNoSuchConn(t *testing.T, l *logging.Logger, deadline time.Time) {
	rpc := NewRPCGateway(l)

	const connID uint16 = 1

	req := DeadlineReq{
		ConnID:   connID,
		Deadline: deadline,
	}

	err := rpc.SetWriteDeadline(&req, nil)
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "no conn"))
}

func testRPCGatewaySetWriteDeadlineConnNotSet(t *testing.T, l *logging.Logger, deadline time.Time) {
	rpc := NewRPCGateway(l)

	connID := addConn(t, rpc, nil)

	req := DeadlineReq{
		ConnID:   connID,
		Deadline: deadline,
	}
	err := rpc.SetWriteDeadline(&req, nil)
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "no conn"))
}

func testRPCGatewaySetWriteDeadlineError(t *testing.T, l *logging.Logger, deadline time.Time) {
	rpc := NewRPCGateway(l)

	conn := &appcommon.MockConn{}
	conn.On("SetWriteDeadline", mock.Anything).Return(func(d time.Time) error {
		if !deadline.Equal(d) {
			return fmt.Errorf("expected deadline %v, got %v", deadline, d)
		}

		return testhelpers.Err
	})

	connID := addConn(t, rpc, conn)

	req := DeadlineReq{
		ConnID:   connID,
		Deadline: deadline,
	}
	err := rpc.SetWriteDeadline(&req, nil)
	require.Equal(t, testhelpers.Err, err)
}

func TestRPCGateway_SetReadDeadline(t *testing.T) {
	l := logging.MustGetLogger("rpc_gateway")

	const delay = 1 * time.Hour

	deadline := time.Now().Add(delay)

	t.Run("ok", func(t *testing.T) {
		testRPCGatewaySetReadDeadlineOK(t, l, deadline)
	})

	t.Run("no such conn", func(t *testing.T) {
		testRPCGatewaySetReadDeadlineNoSuchConn(t, l, deadline)
	})

	t.Run("conn is not set", func(t *testing.T) {
		testRPCGatewaySetReadDeadlineConnNotSet(t, l, deadline)
	})

	t.Run("set read deadline error", func(t *testing.T) {
		testRPCGatewaySetReadDeadlineError(t, l, deadline)
	})
}

func testRPCGatewaySetReadDeadlineOK(t *testing.T, l *logging.Logger, deadline time.Time) {
	rpc := NewRPCGateway(l)

	conn := &appcommon.MockConn{}
	conn.On("SetReadDeadline", mock.Anything).Return(func(d time.Time) error {
		if !deadline.Equal(d) {
			return fmt.Errorf("expected deadline %v, got %v", deadline, d)
		}

		return nil
	})

	connID := addConn(t, rpc, conn)

	req := DeadlineReq{
		ConnID:   connID,
		Deadline: deadline,
	}
	err := rpc.SetReadDeadline(&req, nil)
	require.NoError(t, err)
}

func testRPCGatewaySetReadDeadlineNoSuchConn(t *testing.T, l *logging.Logger, deadline time.Time) {
	rpc := NewRPCGateway(l)

	const connID uint16 = 1

	req := DeadlineReq{
		ConnID:   connID,
		Deadline: deadline,
	}
	err := rpc.SetReadDeadline(&req, nil)
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "no conn"))
}

func testRPCGatewaySetReadDeadlineConnNotSet(t *testing.T, l *logging.Logger, deadline time.Time) {
	rpc := NewRPCGateway(l)

	connID := addConn(t, rpc, nil)

	req := DeadlineReq{
		ConnID:   connID,
		Deadline: deadline,
	}
	err := rpc.SetReadDeadline(&req, nil)
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "no conn"))
}

func testRPCGatewaySetReadDeadlineError(t *testing.T, l *logging.Logger, deadline time.Time) {
	rpc := NewRPCGateway(l)

	conn := &appcommon.MockConn{}
	conn.On("SetReadDeadline", mock.Anything).Return(func(d time.Time) error {
		if !deadline.Equal(d) {
			return fmt.Errorf("expected deadline %v, got %v", deadline, d)
		}

		return testhelpers.Err
	})

	connID := addConn(t, rpc, conn)

	req := DeadlineReq{
		ConnID:   connID,
		Deadline: deadline,
	}
	err := rpc.SetReadDeadline(&req, nil)
	require.Equal(t, testhelpers.Err, err)
}

func TestRPCGateway_SetDeadline(t *testing.T) {
	l := logging.MustGetLogger("rpc_gateway")

	const delay = 1 * time.Hour

	deadline := time.Now().Add(delay)

	t.Run("ok", func(t *testing.T) {
		testRPCGatewaySetDeadlineOK(t, l, deadline)
	})

	t.Run("no such conn", func(t *testing.T) {
		testRPCGatewaySetDeadlineNoSuchConn(t, l, deadline)
	})

	t.Run("conn is not set", func(t *testing.T) {
		testRPCGatewaySetDeadlineConnNotSet(t, l, deadline)
	})

	t.Run("set deadline error", func(t *testing.T) {
		testRPCGatewaySetDeadlineError(t, l, deadline)
	})
}

func testRPCGatewaySetDeadlineOK(t *testing.T, l *logging.Logger, deadline time.Time) {
	rpc := NewRPCGateway(l)

	conn := &appcommon.MockConn{}
	conn.On("SetDeadline", mock.Anything).Return(func(d time.Time) error {
		if !deadline.Equal(d) {
			return fmt.Errorf("expected deadline %v, got %v", deadline, d)
		}

		return nil
	})

	connID := addConn(t, rpc, conn)

	req := DeadlineReq{
		ConnID:   connID,
		Deadline: deadline,
	}
	err := rpc.SetDeadline(&req, nil)
	require.NoError(t, err)
}

func testRPCGatewaySetDeadlineNoSuchConn(t *testing.T, l *logging.Logger, deadline time.Time) {
	rpc := NewRPCGateway(l)

	const connID uint16 = (1)

	req := DeadlineReq{
		ConnID:   connID,
		Deadline: deadline,
	}
	err := rpc.SetDeadline(&req, nil)
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "no conn"))
}

func testRPCGatewaySetDeadlineConnNotSet(t *testing.T, l *logging.Logger, deadline time.Time) {
	rpc := NewRPCGateway(l)

	connID := addConn(t, rpc, nil)

	req := DeadlineReq{
		ConnID:   connID,
		Deadline: deadline,
	}
	err := rpc.SetDeadline(&req, nil)
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "no conn"))
}

func testRPCGatewaySetDeadlineError(t *testing.T, l *logging.Logger, deadline time.Time) {
	rpc := NewRPCGateway(l)

	conn := &appcommon.MockConn{}
	conn.On("SetDeadline", mock.Anything).Return(func(d time.Time) error {
		if !deadline.Equal(d) {
			return fmt.Errorf("expected deadline %v, got %v", deadline, d)
		}

		return testhelpers.Err
	})

	connID := addConn(t, rpc, conn)

	req := DeadlineReq{
		ConnID:   connID,
		Deadline: deadline,
	}
	err := rpc.SetDeadline(&req, nil)
	require.Equal(t, testhelpers.Err, err)
}

func TestRPCGateway_CloseConn(t *testing.T) {
	l := logging.MustGetLogger("rpc_gateway")

	t.Run("ok", func(t *testing.T) {
		testRPCGatewayCloseConnOK(l, t)
	})

	t.Run("no such conn", func(t *testing.T) {
		testRPCGatewayCloseNoSuchConn(t, l)
	})

	t.Run("conn is not set", func(t *testing.T) {
		testRPCGatewayCloseConnNotSet(t, l)
	})

	t.Run("close error", func(t *testing.T) {
		testRPCGatewayCloseConnError(t, l)
	})
}

func testRPCGatewayCloseConnOK(l *logging.Logger, t *testing.T) {
	rpc := NewRPCGateway(l)

	var closeErr error

	conn := &appcommon.MockConn{}
	conn.On("Close").Return(closeErr)

	connID := addConn(t, rpc, conn)

	err := rpc.CloseConn(&connID, nil)
	require.NoError(t, err)

	_, ok := rpc.cm.Get(connID)
	require.False(t, ok)
}

func testRPCGatewayCloseNoSuchConn(t *testing.T, l *logging.Logger) {
	rpc := NewRPCGateway(l)

	connID := uint16(1) // nolint: gomnd

	err := rpc.CloseConn(&connID, nil)
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "no conn"))
}

func testRPCGatewayCloseConnNotSet(t *testing.T, l *logging.Logger) {
	rpc := NewRPCGateway(l)

	connID := addConn(t, rpc, nil)

	err := rpc.CloseConn(&connID, nil)
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "no conn"))
}

func testRPCGatewayCloseConnError(t *testing.T, l *logging.Logger) {
	rpc := NewRPCGateway(l)

	closeErr := errors.New("close error")

	conn := &appcommon.MockConn{}
	conn.On("Close").Return(closeErr)

	connID := addConn(t, rpc, conn)

	err := rpc.CloseConn(&connID, nil)
	require.Equal(t, err, closeErr)
}

func TestRPCGateway_CloseListener(t *testing.T) {
	l := logging.MustGetLogger("rpc_gateway")

	t.Run("ok", func(t *testing.T) {
		testRPCGatewayCloseListenerOK(t, l)
	})

	t.Run("no such listener", func(t *testing.T) {
		testRPCGatewayCloseListenerNoSuchListener(t, l)
	})

	t.Run("listener is not set", func(t *testing.T) {
		testRPCGatewayCloseListenerNotSet(t, l)
	})

	t.Run("close error", func(t *testing.T) {
		testRPCGatewayCloseListenerError(t, l)
	})
}

func testRPCGatewayCloseListenerOK(t *testing.T, l *logging.Logger) {
	rpc := NewRPCGateway(l)

	var closeErr error

	lis := &appcommon.MockListener{}
	lis.On("Close").Return(closeErr)

	lisID := addListener(t, rpc, lis)

	err := rpc.CloseListener(&lisID, nil)
	require.NoError(t, err)

	_, ok := rpc.cm.Get(lisID)
	require.False(t, ok)
}

func testRPCGatewayCloseListenerNoSuchListener(t *testing.T, l *logging.Logger) {
	rpc := NewRPCGateway(l)

	lisID := uint16(1) // nolint: gomnd

	err := rpc.CloseListener(&lisID, nil)
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "no listener"))
}

func testRPCGatewayCloseListenerNotSet(t *testing.T, l *logging.Logger) {
	rpc := NewRPCGateway(l)

	lisID := addListener(t, rpc, nil)

	err := rpc.CloseListener(&lisID, nil)
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "no listener"))
}

func testRPCGatewayCloseListenerError(t *testing.T, l *logging.Logger) {
	rpc := NewRPCGateway(l)

	closeErr := errors.New("close error")

	lis := &appcommon.MockListener{}
	lis.On("Close").Return(closeErr)

	lisID := addListener(t, rpc, lis)

	err := rpc.CloseListener(&lisID, nil)
	require.Equal(t, err, closeErr)
}

func prepAddr(nType appnet.Type) appnet.Addr {
	pk, _ := cipher.GenerateKeyPair()

	const port routing.Port = 100

	return appnet.Addr{
		Net:    nType,
		PubKey: pk,
		Port:   port,
	}
}

func addConn(t *testing.T, rpc *RPCIngressGateway, conn net.Conn) uint16 {
	connID, _, err := rpc.cm.ReserveNextID()
	require.NoError(t, err)

	err = rpc.cm.Set(*connID, conn)
	require.NoError(t, err)

	return *connID
}

func addListener(t *testing.T, rpc *RPCIngressGateway, lis net.Listener) uint16 {
	lisID, _, err := rpc.lm.ReserveNextID()
	require.NoError(t, err)

	err = rpc.lm.Set(*lisID, lis)
	require.NoError(t, err)

	return *lisID
}
