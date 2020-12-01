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

	"github.com/skycoin/dmsg"
	"github.com/skycoin/dmsg/cipher"
	"github.com/skycoin/skycoin/src/util/logging"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/internal/testhelpers"
	"github.com/skycoin/skywire/pkg/app/appcommon"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/app/idmanager"
	"github.com/skycoin/skywire/pkg/routing"
)

func TestRPCIngressGateway_SetDetailedStatus(t *testing.T) {
	l := logging.MustGetLogger("rpc_gateway")

	t.Run("ok", func(t *testing.T) {
		testRPCIngressGatewaySetDetailedStatusOK(t, l)
	})
}

func testRPCIngressGatewaySetDetailedStatusOK(t *testing.T, l *logging.Logger) {
	proc := &Proc{}

	rpc := NewRPCGateway(l, proc)

	wantStatus := "status"

	err := rpc.SetDetailedStatus(&wantStatus, nil)
	require.NoError(t, err)

	proc.statusMx.RLock()
	gotStatus := wantStatus
	proc.statusMx.RUnlock()
	require.Equal(t, wantStatus, gotStatus)
}

func TestRPCIngressGateway_Dial(t *testing.T) {
	l := logging.MustGetLogger("rpc_gateway")
	nType := appnet.TypeDmsg

	dialAddr := prepAddr(nType)

	t.Run("ok", func(t *testing.T) {
		testRPCIngressGatewayDialOK(t, l, nType, dialAddr)
	})

	t.Run("no more slots for a new conn", func(t *testing.T) {
		testRPCIngressGatewayDialNoMoreSlots(t, l, dialAddr)
	})

	t.Run("dial error", func(t *testing.T) {
		testRPCIngressGatewayDialError(t, l, nType, dialAddr)
	})

	t.Run("error wrapping conn", func(t *testing.T) {
		testRPCIngressGatewayDialErrorWrappingConn(t, l, nType, dialAddr)
	})
}

func testRPCIngressGatewayDialOK(t *testing.T, l *logging.Logger, nType appnet.Type, dialAddr appnet.Addr) {
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

	rpc := NewRPCGateway(l, nil)

	var resp DialResp
	err = rpc.Dial(&dialAddr, &resp)
	require.NoError(t, err)

	const wantConnID uint16 = 1

	require.Equal(t, resp.ConnID, wantConnID)
	require.Equal(t, resp.LocalPort, localPort)
}

func testRPCIngressGatewayDialNoMoreSlots(t *testing.T, l *logging.Logger, dialAddr appnet.Addr) {
	rpc := NewRPCGateway(l, nil)

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

func testRPCIngressGatewayDialError(t *testing.T, l *logging.Logger, nType appnet.Type, dialAddr appnet.Addr) {
	appnet.ClearNetworkers()

	dialCtx := context.Background()
	dialErr := errors.New("dial error")

	var dialConn net.Conn

	n := &appnet.MockNetworker{}
	n.On("DialContext", dialCtx, dialAddr).Return(dialConn, dialErr)

	err := appnet.AddNetworker(nType, n)
	require.NoError(t, err)

	rpc := NewRPCGateway(l, nil)

	var resp DialResp
	err = rpc.Dial(&dialAddr, &resp)
	require.Equal(t, err, dialErr)
}

func testRPCIngressGatewayDialErrorWrappingConn(t *testing.T, l *logging.Logger, nType appnet.Type, dialAddr appnet.Addr) {
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

	rpc := NewRPCGateway(l, nil)

	var resp DialResp
	err = rpc.Dial(&dialAddr, &resp)
	require.Equal(t, err, appnet.ErrUnknownAddrType)
}

func TestRPCIngressGateway_Listen(t *testing.T) {
	l := logging.MustGetLogger("rpc_gateway")
	nType := appnet.TypeDmsg

	listenAddr := prepAddr(nType)

	t.Run("ok", func(t *testing.T) {
		testRPCIngressGatewayListenOK(t, l, nType, listenAddr)
	})

	t.Run("no more slots for a new listener", func(t *testing.T) {
		testRPCIngressGatewayListenNoMoreSlots(t, l, listenAddr)
	})

	t.Run("listen error", func(t *testing.T) {
		testRPCIngressGatewayListenError(t, l, nType, listenAddr)
	})
}

func testRPCIngressGatewayListenOK(t *testing.T, l *logging.Logger, nType appnet.Type, listenAddr appnet.Addr) {
	appnet.ClearNetworkers()

	listenCtx := context.Background()
	listenLis := &dmsg.Listener{}

	var listenErr error

	n := &appnet.MockNetworker{}
	n.On("ListenContext", listenCtx, listenAddr).Return(listenLis, listenErr)

	err := appnet.AddNetworker(nType, n)
	require.Equal(t, err, listenErr)

	rpc := NewRPCGateway(l, nil)

	var lisID uint16

	err = rpc.Listen(&listenAddr, &lisID)
	require.NoError(t, err)

	const wantLisID uint16 = 1

	require.Equal(t, lisID, wantLisID)
}

func testRPCIngressGatewayListenNoMoreSlots(t *testing.T, l *logging.Logger, listenAddr appnet.Addr) {
	rpc := NewRPCGateway(l, nil)

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

func testRPCIngressGatewayListenError(t *testing.T, l *logging.Logger, nType appnet.Type, listenAddr appnet.Addr) {
	appnet.ClearNetworkers()

	listenCtx := context.Background()
	listenErr := errors.New("listen error")

	var listenLis net.Listener

	n := &appnet.MockNetworker{}
	n.On("ListenContext", listenCtx, listenAddr).Return(listenLis, listenErr)

	err := appnet.AddNetworker(nType, n)
	require.NoError(t, err)

	rpc := NewRPCGateway(l, nil)

	var lisID uint16

	err = rpc.Listen(&listenAddr, &lisID)
	require.Equal(t, err, listenErr)
}

func TestRPCIngressGateway_Accept(t *testing.T) {
	l := logging.MustGetLogger("rpc_gateway")

	t.Run("ok", func(t *testing.T) {
		testRPCIngressGatewayAcceptOK(t, l)
	})

	t.Run("no such listener", func(t *testing.T) {
		testRPCIngressGatewayAcceptNoSuchListener(t, l)
	})

	t.Run("listener is not set", func(t *testing.T) {
		testRPCIngressGatewayAcceptListenerNotSet(t, l)
	})

	t.Run("no more slots for a new conn", func(t *testing.T) {
		testRPCIngressGatewayAcceptNoMoreSlots(t, l)
	})

	t.Run("error wrapping conn", func(t *testing.T) {
		testRPCIngressGatewayAcceptErrorWrappingConn(t, l)
	})

	t.Run("accept error", func(t *testing.T) {
		testRPCIngressGatewayAcceptError(t, l)
	})
}

func testRPCIngressGatewayAcceptOK(t *testing.T, l *logging.Logger) {
	rpc := NewRPCGateway(l, nil)

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

func testRPCIngressGatewayAcceptNoSuchListener(t *testing.T, l *logging.Logger) {
	rpc := NewRPCGateway(l, nil)

	lisID := uint16(1) // nolint: gomnd

	var resp AcceptResp
	err := rpc.Accept(&lisID, &resp)
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "no listener"))
}

func testRPCIngressGatewayAcceptListenerNotSet(t *testing.T, l *logging.Logger) {
	rpc := NewRPCGateway(l, nil)

	lisID := addListener(t, rpc, nil)

	var resp AcceptResp
	err := rpc.Accept(&lisID, &resp)
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "no listener"))
}

func testRPCIngressGatewayAcceptNoMoreSlots(t *testing.T, l *logging.Logger) {
	rpc := NewRPCGateway(l, nil)

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

func testRPCIngressGatewayAcceptErrorWrappingConn(t *testing.T, l *logging.Logger) {
	rpc := NewRPCGateway(l, nil)

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

func testRPCIngressGatewayAcceptError(t *testing.T, l *logging.Logger) {
	rpc := NewRPCGateway(l, nil)

	var acceptConn net.Conn

	acceptErr := errors.New("accept error")

	lis := &appcommon.MockListener{}
	lis.On("Accept").Return(acceptConn, acceptErr)

	lisID := addListener(t, rpc, lis)

	var resp AcceptResp
	err := rpc.Accept(&lisID, &resp)
	require.Equal(t, err, acceptErr)
}

func TestRPCIngressGateway_Write(t *testing.T) {
	l := logging.MustGetLogger("rpc_gateway")

	writeBuff := []byte{1, 1, 1, 1, 1, 1, 1, 1, 1, 1}

	t.Run("ok", func(t *testing.T) {
		testRPCIngressGatewayWriteOK(t, l, writeBuff)
	})

	t.Run("no such conn", func(t *testing.T) {
		testRPCIngressGatewayWriteNoSuchConn(t, l, writeBuff)
	})

	t.Run("conn is not set", func(t *testing.T) {
		testRPCIngressGatewayWriteConnNotSet(t, l, writeBuff)
	})

	t.Run("write error", func(t *testing.T) {
		testRPCIngressGatewayWriteError(t, l, writeBuff)
	})
}

func testRPCIngressGatewayWriteOK(t *testing.T, l *logging.Logger, writeBuff []byte) {
	rpc := NewRPCGateway(l, nil)

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

func testRPCIngressGatewayWriteNoSuchConn(t *testing.T, l *logging.Logger, writeBuff []byte) {
	const connID uint16 = 1

	rpc := NewRPCGateway(l, nil)
	req := WriteReq{
		ConnID: connID,
		B:      writeBuff,
	}

	var resp WriteResp
	err := rpc.Write(&req, &resp)
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "no conn"))
}

func testRPCIngressGatewayWriteConnNotSet(t *testing.T, l *logging.Logger, writeBuff []byte) {
	rpc := NewRPCGateway(l, nil)

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

func testRPCIngressGatewayWriteError(t *testing.T, l *logging.Logger, writeBuff []byte) {
	rpc := NewRPCGateway(l, nil)

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

func TestRPCIngressGateway_Read(t *testing.T) {
	l := logging.MustGetLogger("rpc_gateway")

	readBufLen := 10
	readBuf := make([]byte, readBufLen)

	t.Run("ok", func(t *testing.T) {
		testRPCIngressGatewayReadOK(t, l, readBuf)
	})

	t.Run("no such conn", func(t *testing.T) {
		testRPCIngressGatewayReadNoSuchConn(t, l, readBufLen)
	})

	t.Run("conn is not set", func(t *testing.T) {
		testRPCIngressGatewayReadConnNotSet(t, l, readBufLen)
	})

	t.Run("read error", func(t *testing.T) {
		testRPCIngressGatewayReadError(t, l, readBuf)
	})
}

func testRPCIngressGatewayReadOK(t *testing.T, l *logging.Logger, readBuf []byte) {
	rpc := NewRPCGateway(l, nil)

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

func testRPCIngressGatewayReadNoSuchConn(t *testing.T, l *logging.Logger, readBufLen int) {
	const connID uint16 = 1

	rpc := NewRPCGateway(l, nil)
	req := ReadReq{
		ConnID: connID,
		BufLen: readBufLen,
	}

	var resp ReadResp
	err := rpc.Read(&req, &resp)
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "no conn"))
}

func testRPCIngressGatewayReadConnNotSet(t *testing.T, l *logging.Logger, readBufLen int) {
	rpc := NewRPCGateway(l, nil)

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

func testRPCIngressGatewayReadError(t *testing.T, l *logging.Logger, readBuf []byte) {
	rpc := NewRPCGateway(l, nil)

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

func TestRPCIngressGateway_SetWriteDeadline(t *testing.T) {
	l := logging.MustGetLogger("rpc_gateway")

	const delay = 1 * time.Hour

	deadline := time.Now().Add(delay)

	t.Run("ok", func(t *testing.T) {
		testRPCIngressGatewaySetWriteDeadlineOK(t, l, deadline)
	})

	t.Run("no such conn", func(t *testing.T) {
		testRPCIngressGatewaySetWriteDeadlineNoSuchConn(t, l, deadline)
	})

	t.Run("conn is not set", func(t *testing.T) {
		testRPCIngressGatewaySetWriteDeadlineConnNotSet(t, l, deadline)
	})

	t.Run("set read deadline error", func(t *testing.T) {
		testRPCIngressGatewaySetWriteDeadlineError(t, l, deadline)
	})
}

func testRPCIngressGatewaySetWriteDeadlineOK(t *testing.T, l *logging.Logger, deadline time.Time) {
	rpc := NewRPCGateway(l, nil)

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

func testRPCIngressGatewaySetWriteDeadlineNoSuchConn(t *testing.T, l *logging.Logger, deadline time.Time) {
	rpc := NewRPCGateway(l, nil)

	const connID uint16 = 1

	req := DeadlineReq{
		ConnID:   connID,
		Deadline: deadline,
	}

	err := rpc.SetWriteDeadline(&req, nil)
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "no conn"))
}

func testRPCIngressGatewaySetWriteDeadlineConnNotSet(t *testing.T, l *logging.Logger, deadline time.Time) {
	rpc := NewRPCGateway(l, nil)

	connID := addConn(t, rpc, nil)

	req := DeadlineReq{
		ConnID:   connID,
		Deadline: deadline,
	}
	err := rpc.SetWriteDeadline(&req, nil)
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "no conn"))
}

func testRPCIngressGatewaySetWriteDeadlineError(t *testing.T, l *logging.Logger, deadline time.Time) {
	rpc := NewRPCGateway(l, nil)

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

func TestRPCIngressGateway_SetReadDeadline(t *testing.T) {
	l := logging.MustGetLogger("rpc_gateway")

	const delay = 1 * time.Hour

	deadline := time.Now().Add(delay)

	t.Run("ok", func(t *testing.T) {
		testRPCIngressGatewaySetReadDeadlineOK(t, l, deadline)
	})

	t.Run("no such conn", func(t *testing.T) {
		testRPCIngressGatewaySetReadDeadlineNoSuchConn(t, l, deadline)
	})

	t.Run("conn is not set", func(t *testing.T) {
		testRPCIngressGatewaySetReadDeadlineConnNotSet(t, l, deadline)
	})

	t.Run("set read deadline error", func(t *testing.T) {
		testRPCIngressGatewaySetReadDeadlineError(t, l, deadline)
	})
}

func testRPCIngressGatewaySetReadDeadlineOK(t *testing.T, l *logging.Logger, deadline time.Time) {
	rpc := NewRPCGateway(l, nil)

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

func testRPCIngressGatewaySetReadDeadlineNoSuchConn(t *testing.T, l *logging.Logger, deadline time.Time) {
	rpc := NewRPCGateway(l, nil)

	const connID uint16 = 1

	req := DeadlineReq{
		ConnID:   connID,
		Deadline: deadline,
	}
	err := rpc.SetReadDeadline(&req, nil)
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "no conn"))
}

func testRPCIngressGatewaySetReadDeadlineConnNotSet(t *testing.T, l *logging.Logger, deadline time.Time) {
	rpc := NewRPCGateway(l, nil)

	connID := addConn(t, rpc, nil)

	req := DeadlineReq{
		ConnID:   connID,
		Deadline: deadline,
	}
	err := rpc.SetReadDeadline(&req, nil)
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "no conn"))
}

func testRPCIngressGatewaySetReadDeadlineError(t *testing.T, l *logging.Logger, deadline time.Time) {
	rpc := NewRPCGateway(l, nil)

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

func TestRPCIngressGateway_SetDeadline(t *testing.T) {
	l := logging.MustGetLogger("rpc_gateway")

	const delay = 1 * time.Hour

	deadline := time.Now().Add(delay)

	t.Run("ok", func(t *testing.T) {
		testRPCIngressGatewaySetDeadlineOK(t, l, deadline)
	})

	t.Run("no such conn", func(t *testing.T) {
		testRPCIngressGatewaySetDeadlineNoSuchConn(t, l, deadline)
	})

	t.Run("conn is not set", func(t *testing.T) {
		testRPCIngressGatewaySetDeadlineConnNotSet(t, l, deadline)
	})

	t.Run("set deadline error", func(t *testing.T) {
		testRPCIngressGatewaySetDeadlineError(t, l, deadline)
	})
}

func testRPCIngressGatewaySetDeadlineOK(t *testing.T, l *logging.Logger, deadline time.Time) {
	rpc := NewRPCGateway(l, nil)

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

func testRPCIngressGatewaySetDeadlineNoSuchConn(t *testing.T, l *logging.Logger, deadline time.Time) {
	rpc := NewRPCGateway(l, nil)

	const connID uint16 = 1

	req := DeadlineReq{
		ConnID:   connID,
		Deadline: deadline,
	}
	err := rpc.SetDeadline(&req, nil)
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "no conn"))
}

func testRPCIngressGatewaySetDeadlineConnNotSet(t *testing.T, l *logging.Logger, deadline time.Time) {
	rpc := NewRPCGateway(l, nil)

	connID := addConn(t, rpc, nil)

	req := DeadlineReq{
		ConnID:   connID,
		Deadline: deadline,
	}
	err := rpc.SetDeadline(&req, nil)
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "no conn"))
}

func testRPCIngressGatewaySetDeadlineError(t *testing.T, l *logging.Logger, deadline time.Time) {
	rpc := NewRPCGateway(l, nil)

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

func TestRPCIngressGateway_CloseConn(t *testing.T) {
	l := logging.MustGetLogger("rpc_gateway")

	t.Run("ok", func(t *testing.T) {
		testRPCIngressGatewayCloseConnOK(l, t)
	})

	t.Run("no such conn", func(t *testing.T) {
		testRPCIngressGatewayCloseNoSuchConn(t, l)
	})

	t.Run("conn is not set", func(t *testing.T) {
		testRPCIngressGatewayCloseConnNotSet(t, l)
	})

	t.Run("close error", func(t *testing.T) {
		testRPCIngressGatewayCloseConnError(t, l)
	})
}

func testRPCIngressGatewayCloseConnOK(l *logging.Logger, t *testing.T) {
	rpc := NewRPCGateway(l, nil)

	var closeErr error

	conn := &appcommon.MockConn{}
	conn.On("Close").Return(closeErr)

	connID := addConn(t, rpc, conn)

	err := rpc.CloseConn(&connID, nil)
	require.NoError(t, err)

	_, ok := rpc.cm.Get(connID)
	require.False(t, ok)
}

func testRPCIngressGatewayCloseNoSuchConn(t *testing.T, l *logging.Logger) {
	rpc := NewRPCGateway(l, nil)

	connID := uint16(1) // nolint: gomnd

	err := rpc.CloseConn(&connID, nil)
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "no conn"))
}

func testRPCIngressGatewayCloseConnNotSet(t *testing.T, l *logging.Logger) {
	rpc := NewRPCGateway(l, nil)

	connID := addConn(t, rpc, nil)

	err := rpc.CloseConn(&connID, nil)
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "no conn"))
}

func testRPCIngressGatewayCloseConnError(t *testing.T, l *logging.Logger) {
	rpc := NewRPCGateway(l, nil)

	closeErr := errors.New("close error")

	conn := &appcommon.MockConn{}
	conn.On("Close").Return(closeErr)

	connID := addConn(t, rpc, conn)

	err := rpc.CloseConn(&connID, nil)
	require.Equal(t, err, closeErr)
}

func TestRPCIngressGateway_CloseListener(t *testing.T) {
	l := logging.MustGetLogger("rpc_gateway")

	t.Run("ok", func(t *testing.T) {
		testRPCIngressGatewayCloseListenerOK(t, l)
	})

	t.Run("no such listener", func(t *testing.T) {
		testRPCIngressGatewayCloseListenerNoSuchListener(t, l)
	})

	t.Run("listener is not set", func(t *testing.T) {
		testRPCIngressGatewayCloseListenerNotSet(t, l)
	})

	t.Run("close error", func(t *testing.T) {
		testRPCIngressGatewayCloseListenerError(t, l)
	})
}

func testRPCIngressGatewayCloseListenerOK(t *testing.T, l *logging.Logger) {
	rpc := NewRPCGateway(l, nil)

	var closeErr error

	lis := &appcommon.MockListener{}
	lis.On("Close").Return(closeErr)

	lisID := addListener(t, rpc, lis)

	err := rpc.CloseListener(&lisID, nil)
	require.NoError(t, err)

	_, ok := rpc.cm.Get(lisID)
	require.False(t, ok)
}

func testRPCIngressGatewayCloseListenerNoSuchListener(t *testing.T, l *logging.Logger) {
	rpc := NewRPCGateway(l, nil)

	lisID := uint16(1) // nolint: gomnd

	err := rpc.CloseListener(&lisID, nil)
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "no listener"))
}

func testRPCIngressGatewayCloseListenerNotSet(t *testing.T, l *logging.Logger) {
	rpc := NewRPCGateway(l, nil)

	lisID := addListener(t, rpc, nil)

	err := rpc.CloseListener(&lisID, nil)
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "no listener"))
}

func testRPCIngressGatewayCloseListenerError(t *testing.T, l *logging.Logger) {
	rpc := NewRPCGateway(l, nil)

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
