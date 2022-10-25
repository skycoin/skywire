// Package appserver pkg/app/appserver/rpc_ingress_client_test.go
package appserver

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/rpc"
	"testing"
	"time"

	"github.com/skycoin/dmsg/pkg/dmsg"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/nettest"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/internal/testhelpers"
	"github.com/skycoin/skywire/pkg/app/appcommon"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/routing"
)

func TestRPCIngressClient_SetDetailedStatus(t *testing.T) {
	proc := &Proc{}

	rpcL, closeL := prepListener(t)
	defer closeL()

	rpcS := prepRPCServer(t, NewRPCGateway(nil, proc))
	go rpcS.Accept(rpcL)

	rpcC := prepRPCClient(t, rpcL.Addr().Network(), rpcL.Addr().String())

	wantStatus := "status"
	err := rpcC.SetDetailedStatus(wantStatus)
	require.NoError(t, err)

	proc.statusMx.RLock()
	gotStatus := wantStatus
	proc.statusMx.RUnlock()
	require.Equal(t, wantStatus, gotStatus)
}

func TestRPCIngressClient_Dial(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		rpcL, closeL := prepListener(t)
		defer closeL()

		rpcS := prepRPCServer(t, NewRPCGateway(nil, nil))
		go rpcS.Accept(rpcL)

		rpcC := prepRPCClient(t, rpcL.Addr().Network(), rpcL.Addr().String())

		dmsgLocal, dmsgRemote, _, remote := prepAddrs()

		dialCtx := context.Background()
		dialConn := &appcommon.MockConn{}
		dialConn.On("LocalAddr").Return(dmsgLocal)
		dialConn.On("RemoteAddr").Return(dmsgRemote)

		n := &appnet.MockNetworker{}
		n.On("DialContext", dialCtx, remote).Return(dialConn, testhelpers.NoErr)

		appnet.ClearNetworkers()
		err := appnet.AddNetworker(appnet.TypeDmsg, n)
		require.NoError(t, err)

		connID, localPort, err := rpcC.Dial(remote)
		require.NoError(t, err)
		require.Equal(t, connID, uint16(1))
		require.Equal(t, localPort, routing.Port(dmsgLocal.Port))
	})

	t.Run("dial error", func(t *testing.T) {
		s := prepRPCServer(t, NewRPCGateway(nil, nil))
		rpcL, lisCleanup := prepListener(t)
		defer lisCleanup()
		go s.Accept(rpcL)

		cl := prepRPCClient(t, rpcL.Addr().Network(), rpcL.Addr().String())

		_, _, _, remote := prepAddrs()

		dialCtx := context.Background()
		var dialConn net.Conn
		dialErr := errors.New("dial error")

		n := &appnet.MockNetworker{}
		n.On("DialContext", dialCtx, remote).Return(dialConn, dialErr)

		appnet.ClearNetworkers()
		err := appnet.AddNetworker(appnet.TypeDmsg, n)
		require.NoError(t, err)

		connID, localPort, err := cl.Dial(remote)
		require.Error(t, err)
		require.Equal(t, err.Error(), dialErr.Error())
		require.Equal(t, connID, uint16(0))
		require.Equal(t, localPort, routing.Port(0))
	})
}

func TestRPCIngressClient_Listen(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		s := prepRPCServer(t, NewRPCGateway(nil, nil))
		rpcL, lisCleanup := prepListener(t)
		defer lisCleanup()
		go s.Accept(rpcL)

		cl := prepRPCClient(t, rpcL.Addr().Network(), rpcL.Addr().String())

		_, _, local, _ := prepAddrs()

		listenCtx := context.Background()
		var listenLis net.Listener
		var noErr error

		n := &appnet.MockNetworker{}
		n.On("ListenContext", listenCtx, local).Return(listenLis, noErr)

		appnet.ClearNetworkers()
		err := appnet.AddNetworker(appnet.TypeDmsg, n)
		require.NoError(t, err)

		lisID, err := cl.Listen(local)
		require.NoError(t, err)
		require.Equal(t, lisID, uint16(1))
	})

	t.Run("listen error", func(t *testing.T) {
		s := prepRPCServer(t, NewRPCGateway(nil, nil))
		rpcL, lisCleanup := prepListener(t)
		defer lisCleanup()
		go s.Accept(rpcL)

		cl := prepRPCClient(t, rpcL.Addr().Network(), rpcL.Addr().String())

		_, _, local, _ := prepAddrs()

		listenCtx := context.Background()
		var listenLis net.Listener
		listenErr := errors.New("listen error")

		n := &appnet.MockNetworker{}
		n.On("ListenContext", listenCtx, local).Return(listenLis, listenErr)

		appnet.ClearNetworkers()
		err := appnet.AddNetworker(appnet.TypeDmsg, n)
		require.NoError(t, err)

		lisID, err := cl.Listen(local)
		require.Error(t, err)
		require.Equal(t, err.Error(), listenErr.Error())
		require.Equal(t, lisID, uint16(0))
	})
}

func TestRPCIngressClient_Accept(t *testing.T) {
	dmsgLocal, dmsgRemote, local, _ := prepAddrs()

	t.Run("ok", func(t *testing.T) {
		gateway := NewRPCGateway(nil, nil)

		lisConn := &appcommon.MockConn{}
		lisConn.On("LocalAddr").Return(dmsgLocal)
		lisConn.On("RemoteAddr").Return(dmsgRemote)

		lis := &appcommon.MockListener{}
		lis.On("Accept").Return(lisConn, testhelpers.NoErr)

		prepNetworkerWithListener(t, lis, local)

		var lisID uint16
		err := gateway.Listen(&local, &lisID)
		require.NoError(t, err)

		s := prepRPCServer(t, gateway)
		rpcL, lisCleanup := prepListener(t)
		defer lisCleanup()
		go s.Accept(rpcL)

		cl := prepRPCClient(t, rpcL.Addr().Network(), rpcL.Addr().String())

		wantRemote := appnet.Addr{
			Net:    appnet.TypeDmsg,
			PubKey: dmsgRemote.PK,
			Port:   routing.Port(dmsgRemote.Port),
		}

		connID, remote, err := cl.Accept(lisID)
		require.NoError(t, err)
		require.Equal(t, connID, uint16(1))
		require.Equal(t, remote, wantRemote)
	})

	t.Run("accept error", func(t *testing.T) {
		gateway := NewRPCGateway(nil, nil)

		var lisConn net.Conn
		listenErr := errors.New("accept error")

		lis := &appcommon.MockListener{}
		lis.On("Accept").Return(lisConn, listenErr)

		prepNetworkerWithListener(t, lis, local)

		var lisID uint16
		err := gateway.Listen(&local, &lisID)
		require.NoError(t, err)

		s := prepRPCServer(t, gateway)
		rpcL, lisCleanup := prepListener(t)
		defer lisCleanup()
		go s.Accept(rpcL)

		cl := prepRPCClient(t, rpcL.Addr().Network(), rpcL.Addr().String())

		connID, remote, err := cl.Accept(lisID)
		require.Error(t, err)
		require.Equal(t, err.Error(), listenErr.Error())
		require.Equal(t, connID, uint16(0))
		require.Equal(t, remote, appnet.Addr{})
	})
}

func TestRPCIngressClient_Write(t *testing.T) {
	dmsgLocal, dmsgRemote, _, remote := prepAddrs()

	t.Run("ok", func(t *testing.T) {
		gateway := NewRPCGateway(nil, nil)

		writeBuf := []byte{1, 1, 1, 1, 1, 1, 1, 1, 1, 1}
		writeN := 10
		var noErr error

		conn := &appcommon.MockConn{}
		conn.On("Write", writeBuf).Return(writeN, noErr)
		conn.On("LocalAddr").Return(dmsgLocal)
		conn.On("RemoteAddr").Return(dmsgRemote)

		prepNetworkerWithConn(t, conn, remote)

		var dialResp DialResp
		err := gateway.Dial(&remote, &dialResp)
		require.NoError(t, err)

		s := prepRPCServer(t, gateway)
		rpcL, lisCleanup := prepListener(t)
		defer lisCleanup()
		go s.Accept(rpcL)

		cl := prepRPCClient(t, rpcL.Addr().Network(), rpcL.Addr().String())

		n, err := cl.Write(dialResp.ConnID, writeBuf)
		require.NoError(t, err)
		require.Equal(t, n, writeN)
	})

	t.Run("write error", func(t *testing.T) {
		gateway := NewRPCGateway(nil, nil)

		writeBuf := []byte{1, 1, 1, 1, 1, 1, 1, 1, 1, 1}
		writeN := 0
		writeErr := errors.New("write error")

		conn := &appcommon.MockConn{}
		conn.On("Write", writeBuf).Return(writeN, writeErr)
		conn.On("LocalAddr").Return(dmsgLocal)
		conn.On("RemoteAddr").Return(dmsgRemote)

		prepNetworkerWithConn(t, conn, remote)

		var dialResp DialResp
		err := gateway.Dial(&remote, &dialResp)
		require.NoError(t, err)

		s := prepRPCServer(t, gateway)
		rpcL, lisCleanup := prepListener(t)
		defer lisCleanup()
		go s.Accept(rpcL)

		cl := prepRPCClient(t, rpcL.Addr().Network(), rpcL.Addr().String())

		n, err := cl.Write(dialResp.ConnID, writeBuf)
		require.Error(t, err)
		require.Equal(t, err.Error(), writeErr.Error())
		require.Equal(t, n, 0)
	})
}

func TestRPCIngressClient_Read(t *testing.T) {
	dmsgLocal, dmsgRemote, _, remote := prepAddrs()

	t.Run("ok", func(t *testing.T) {
		gateway := NewRPCGateway(nil, nil)

		readBufLen := 10
		readBuf := make([]byte, readBufLen)
		readN := 5
		var noErr error

		conn := &appcommon.MockConn{}
		conn.On("Read", readBuf).Return(readN, noErr)
		conn.On("LocalAddr").Return(dmsgLocal)
		conn.On("RemoteAddr").Return(dmsgRemote)

		prepNetworkerWithConn(t, conn, remote)

		var dialResp DialResp
		err := gateway.Dial(&remote, &dialResp)
		require.NoError(t, err)

		s := prepRPCServer(t, gateway)
		rpcL, lisCleanup := prepListener(t)
		defer lisCleanup()
		go s.Accept(rpcL)

		cl := prepRPCClient(t, rpcL.Addr().Network(), rpcL.Addr().String())

		n, err := cl.Read(dialResp.ConnID, readBuf)
		require.NoError(t, err)
		require.Equal(t, n, readN)
	})

	t.Run("read error", func(t *testing.T) {
		gateway := NewRPCGateway(nil, nil)

		readBufLen := 10
		readBuf := make([]byte, readBufLen)
		readN := 0
		readErr := errors.New("read error")

		conn := &appcommon.MockConn{}
		conn.On("Read", readBuf).Return(readN, readErr)
		conn.On("LocalAddr").Return(dmsgLocal)
		conn.On("RemoteAddr").Return(dmsgRemote)

		prepNetworkerWithConn(t, conn, remote)

		var dialResp DialResp
		err := gateway.Dial(&remote, &dialResp)
		require.NoError(t, err)

		s := prepRPCServer(t, gateway)
		rpcL, lisCleanup := prepListener(t)
		defer lisCleanup()
		go s.Accept(rpcL)

		cl := prepRPCClient(t, rpcL.Addr().Network(), rpcL.Addr().String())

		n, err := cl.Read(dialResp.ConnID, readBuf)
		require.Error(t, err)
		require.Equal(t, err.Error(), readErr.Error())
		require.Equal(t, n, readN)
	})
}

func TestRPCIngressClient_CloseConn(t *testing.T) {
	dmsgLocal, dmsgRemote, _, remote := prepAddrs()

	t.Run("ok", func(t *testing.T) {
		gateway := NewRPCGateway(nil, nil)

		var noErr error

		conn := &appcommon.MockConn{}
		conn.On("Close").Return(noErr)
		conn.On("LocalAddr").Return(dmsgLocal)
		conn.On("RemoteAddr").Return(dmsgRemote)

		prepNetworkerWithConn(t, conn, remote)

		var dialResp DialResp
		err := gateway.Dial(&remote, &dialResp)
		require.NoError(t, err)

		s := prepRPCServer(t, gateway)
		rpcL, lisCleanup := prepListener(t)
		defer lisCleanup()
		go s.Accept(rpcL)

		cl := prepRPCClient(t, rpcL.Addr().Network(), rpcL.Addr().String())

		err = cl.CloseConn(dialResp.ConnID)
		require.NoError(t, err)
	})

	t.Run("close error", func(t *testing.T) {
		gateway := NewRPCGateway(nil, nil)

		closeErr := errors.New("close error")

		conn := &appcommon.MockConn{}
		conn.On("Close").Return(closeErr)
		conn.On("LocalAddr").Return(dmsgLocal)
		conn.On("RemoteAddr").Return(dmsgRemote)

		prepNetworkerWithConn(t, conn, remote)

		var dialResp DialResp
		err := gateway.Dial(&remote, &dialResp)
		require.NoError(t, err)

		s := prepRPCServer(t, gateway)
		rpcL, lisCleanup := prepListener(t)
		defer lisCleanup()
		go s.Accept(rpcL)

		cl := prepRPCClient(t, rpcL.Addr().Network(), rpcL.Addr().String())

		err = cl.CloseConn(dialResp.ConnID)
		require.Error(t, err)
		require.Equal(t, err.Error(), closeErr.Error())
	})
}

func TestRPCIngressClient_CloseListener(t *testing.T) {
	_, _, local, _ := prepAddrs()

	t.Run("ok", func(t *testing.T) {
		gateway := NewRPCGateway(nil, nil)

		var noErr error

		lis := &appcommon.MockListener{}
		lis.On("Close").Return(noErr)

		prepNetworkerWithListener(t, lis, local)

		var lisID uint16
		err := gateway.Listen(&local, &lisID)
		require.NoError(t, err)

		s := prepRPCServer(t, gateway)
		rpcL, lisCleanup := prepListener(t)
		defer lisCleanup()
		go s.Accept(rpcL)

		cl := prepRPCClient(t, rpcL.Addr().Network(), rpcL.Addr().String())

		err = cl.CloseListener(lisID)
		require.NoError(t, err)
	})

	t.Run("close error", func(t *testing.T) {
		gateway := NewRPCGateway(nil, nil)

		closeErr := errors.New("close error")

		lis := &appcommon.MockListener{}
		lis.On("Close").Return(closeErr)

		prepNetworkerWithListener(t, lis, local)

		var lisID uint16
		err := gateway.Listen(&local, &lisID)
		require.NoError(t, err)

		s := prepRPCServer(t, gateway)
		rpcL, lisCleanup := prepListener(t)
		defer lisCleanup()
		go s.Accept(rpcL)

		cl := prepRPCClient(t, rpcL.Addr().Network(), rpcL.Addr().String())

		err = cl.CloseListener(lisID)
		require.Error(t, err)
		require.Equal(t, err.Error(), closeErr.Error())
	})
}

func TestRPCIngressClient_SetDeadline(t *testing.T) {
	dmsgLocal, dmsgRemote, _, remote := prepAddrs()

	deadline := time.Now().Add(1 * time.Hour)

	t.Run("ok", func(t *testing.T) {
		gateway := NewRPCGateway(nil, nil)

		conn := &appcommon.MockConn{}
		conn.On("SetDeadline", mock.Anything).Return(func(d time.Time) error {
			if !deadline.Equal(d) {
				return fmt.Errorf("expected deadline %v, got %v", deadline, d)
			}

			return testhelpers.NoErr
		})
		conn.On("LocalAddr").Return(dmsgLocal)
		conn.On("RemoteAddr").Return(dmsgRemote)

		prepNetworkerWithConn(t, conn, remote)

		var dialResp DialResp
		err := gateway.Dial(&remote, &dialResp)
		require.NoError(t, err)

		s := prepRPCServer(t, gateway)
		rpcL, lisCleanup := prepListener(t)
		defer lisCleanup()
		go s.Accept(rpcL)

		cl := prepRPCClient(t, rpcL.Addr().Network(), rpcL.Addr().String())

		err = cl.SetDeadline(dialResp.ConnID, deadline)
		require.NoError(t, err)
	})

	t.Run("set deadline error", func(t *testing.T) {
		gateway := NewRPCGateway(nil, nil)

		conn := &appcommon.MockConn{}
		conn.On("SetDeadline", mock.Anything).Return(func(d time.Time) error {
			if !deadline.Equal(d) {
				return fmt.Errorf("expected deadline %v, got %v", deadline, d)
			}

			return testhelpers.Err
		})
		conn.On("LocalAddr").Return(dmsgLocal)
		conn.On("RemoteAddr").Return(dmsgRemote)

		prepNetworkerWithConn(t, conn, remote)

		var dialResp DialResp
		err := gateway.Dial(&remote, &dialResp)
		require.NoError(t, err)

		s := prepRPCServer(t, gateway)
		rpcL, lisCleanup := prepListener(t)
		defer lisCleanup()
		go s.Accept(rpcL)

		cl := prepRPCClient(t, rpcL.Addr().Network(), rpcL.Addr().String())

		err = cl.SetDeadline(dialResp.ConnID, deadline)
		require.Error(t, err)
		require.Equal(t, testhelpers.Err.Error(), err.Error())
	})
}

func TestRPCIngressClient_SetReadDeadline(t *testing.T) {
	dmsgLocal, dmsgRemote, _, remote := prepAddrs()

	deadline := time.Now().Add(1 * time.Hour)

	t.Run("ok", func(t *testing.T) {
		gateway := NewRPCGateway(nil, nil)

		conn := &appcommon.MockConn{}
		conn.On("SetReadDeadline", mock.Anything).Return(func(d time.Time) error {
			if !deadline.Equal(d) {
				return fmt.Errorf("expected deadline %v, got %v", deadline, d)
			}

			return testhelpers.NoErr
		})
		conn.On("LocalAddr").Return(dmsgLocal)
		conn.On("RemoteAddr").Return(dmsgRemote)

		prepNetworkerWithConn(t, conn, remote)

		var dialResp DialResp
		err := gateway.Dial(&remote, &dialResp)
		require.NoError(t, err)

		s := prepRPCServer(t, gateway)
		rpcL, lisCleanup := prepListener(t)
		defer lisCleanup()
		go s.Accept(rpcL)

		cl := prepRPCClient(t, rpcL.Addr().Network(), rpcL.Addr().String())

		err = cl.SetReadDeadline(dialResp.ConnID, deadline)
		require.NoError(t, err)
	})

	t.Run("set deadline error", func(t *testing.T) {
		gateway := NewRPCGateway(nil, nil)

		conn := &appcommon.MockConn{}
		conn.On("SetReadDeadline", mock.Anything).Return(func(d time.Time) error {
			if !deadline.Equal(d) {
				return fmt.Errorf("expected deadline %v, got %v", deadline, d)
			}

			return testhelpers.Err
		})
		conn.On("LocalAddr").Return(dmsgLocal)
		conn.On("RemoteAddr").Return(dmsgRemote)

		prepNetworkerWithConn(t, conn, remote)

		var dialResp DialResp
		err := gateway.Dial(&remote, &dialResp)
		require.NoError(t, err)

		s := prepRPCServer(t, gateway)
		rpcL, lisCleanup := prepListener(t)
		defer lisCleanup()
		go s.Accept(rpcL)

		cl := prepRPCClient(t, rpcL.Addr().Network(), rpcL.Addr().String())

		err = cl.SetReadDeadline(dialResp.ConnID, deadline)
		require.Error(t, err)
		require.Equal(t, testhelpers.Err.Error(), err.Error())
	})
}

func TestRPCIngressClient_SetWriteDeadline(t *testing.T) {
	dmsgLocal, dmsgRemote, _, remote := prepAddrs()

	deadline := time.Now().Add(1 * time.Hour)

	t.Run("ok", func(t *testing.T) {
		gateway := NewRPCGateway(nil, nil)

		conn := &appcommon.MockConn{}
		conn.On("SetWriteDeadline", mock.Anything).Return(func(d time.Time) error {
			if !deadline.Equal(d) {
				return fmt.Errorf("expected deadline %v, got %v", deadline, d)
			}

			return testhelpers.NoErr
		})
		conn.On("LocalAddr").Return(dmsgLocal)
		conn.On("RemoteAddr").Return(dmsgRemote)

		prepNetworkerWithConn(t, conn, remote)

		var dialResp DialResp
		err := gateway.Dial(&remote, &dialResp)
		require.NoError(t, err)

		s := prepRPCServer(t, gateway)
		rpcL, lisCleanup := prepListener(t)
		defer lisCleanup()
		go s.Accept(rpcL)

		cl := prepRPCClient(t, rpcL.Addr().Network(), rpcL.Addr().String())

		err = cl.SetWriteDeadline(dialResp.ConnID, deadline)
		require.NoError(t, err)
	})

	t.Run("set deadline error", func(t *testing.T) {
		gateway := NewRPCGateway(nil, nil)

		conn := &appcommon.MockConn{}
		conn.On("SetWriteDeadline", mock.Anything).Return(func(d time.Time) error {
			if !deadline.Equal(d) {
				return fmt.Errorf("expected deadline %v, got %v", deadline, d)
			}

			return testhelpers.Err
		})
		conn.On("LocalAddr").Return(dmsgLocal)
		conn.On("RemoteAddr").Return(dmsgRemote)

		prepNetworkerWithConn(t, conn, remote)

		var dialResp DialResp
		err := gateway.Dial(&remote, &dialResp)
		require.NoError(t, err)

		s := prepRPCServer(t, gateway)
		rpcL, lisCleanup := prepListener(t)
		defer lisCleanup()
		go s.Accept(rpcL)

		cl := prepRPCClient(t, rpcL.Addr().Network(), rpcL.Addr().String())

		err = cl.SetWriteDeadline(dialResp.ConnID, deadline)
		require.Error(t, err)
		require.Equal(t, testhelpers.Err.Error(), err.Error())
	})
}

func prepNetworkerWithListener(t *testing.T, lis *appcommon.MockListener, local appnet.Addr) {
	var noErr error

	appnet.ClearNetworkers()

	n := &appnet.MockNetworker{}
	n.On("ListenContext", mock.Anything, local).Return(lis, noErr)

	err := appnet.AddNetworker(appnet.TypeDmsg, n)
	require.NoError(t, err)
}

func prepNetworkerWithConn(t *testing.T, conn *appcommon.MockConn, remote appnet.Addr) {
	var noErr error

	networker := &appnet.MockNetworker{}
	networker.On("DialContext", mock.Anything, remote).Return(conn, noErr)

	appnet.ClearNetworkers()
	err := appnet.AddNetworker(appnet.TypeDmsg, networker)
	require.NoError(t, err)
}

// rpcProcKey is shared by prepRPCServer and prepRPCClient
var rpcProcKey = appcommon.RandProcKey()

func prepRPCServer(t *testing.T, gateway *RPCIngressGateway) *rpc.Server {
	s := rpc.NewServer()
	err := s.RegisterName(rpcProcKey.String(), gateway)
	require.NoError(t, err)

	return s
}

func prepRPCClient(t *testing.T, network, addr string) RPCIngressClient {
	rpcCl, err := rpc.Dial(network, addr)
	require.NoError(t, err)

	return NewRPCIngressClient(rpcCl, rpcProcKey)
}

func prepListener(t *testing.T) (lis net.Listener, cleanup func()) {
	lis, err := nettest.NewLocalListener("tcp")
	require.NoError(t, err)

	return lis, func() {
		err := lis.Close()
		require.NoError(t, err)
	}
}

func prepAddrs() (dmsgLocal, dmsgRemote dmsg.Addr, local, remote appnet.Addr) {
	localPK, _ := cipher.GenerateKeyPair()
	localPort := uint16(10)
	dmsgLocal = dmsg.Addr{
		PK:   localPK,
		Port: localPort,
	}
	local = appnet.Addr{
		Net:    appnet.TypeDmsg,
		PubKey: localPK,
		Port:   routing.Port(localPort),
	}

	remotePK, _ := cipher.GenerateKeyPair()
	remotePort := uint16(11)
	dmsgRemote = dmsg.Addr{
		PK:   remotePK,
		Port: remotePort,
	}
	remote = appnet.Addr{
		Net:    appnet.TypeDmsg,
		PubKey: remotePK,
		Port:   routing.Port(remotePort),
	}

	return
}
