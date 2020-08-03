package app

import (
	"errors"
	"fmt"
	"net"
	"net/rpc"
	"testing"

	"github.com/SkycoinProject/skycoin/src/util/logging"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/nettest"

	"github.com/skycoin/skywire/internal/testhelpers"
	"github.com/skycoin/skywire/pkg/app/appcommon"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/app/idmanager"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/snet/snettest"
)

func TestConn_Read(t *testing.T) {
	connID := uint16(1)

	tt := []struct {
		name     string
		readBuff []byte
		readN    int
		readErr  error
	}{
		{
			name:     "ok",
			readBuff: make([]byte, 10),
			readN:    2,
		},
		{
			name:     "read error",
			readBuff: make([]byte, 10),
			readErr:  errors.New("read error"),
		},
	}

	for _, tc := range tt {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			rpc := &MockRPCClient{}
			rpc.On("Read", connID, tc.readBuff).Return(tc.readN, tc.readErr)

			conn := &Conn{
				id:  connID,
				rpc: rpc,
			}

			n, err := conn.Read(tc.readBuff)
			require.Equal(t, tc.readErr, err)
			require.Equal(t, tc.readN, n)
		})
	}
}

func TestConn_Write(t *testing.T) {
	connID := uint16(1)

	tt := []struct {
		name      string
		writeBuff []byte
		writeN    int
		writeErr  error
	}{
		{
			name:      "ok",
			writeBuff: make([]byte, 10),
			writeN:    2,
		},
		{
			name:      "write error",
			writeBuff: make([]byte, 10),
			writeErr:  errors.New("write error"),
		},
	}

	for _, tc := range tt {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			rpc := &MockRPCClient{}
			rpc.On("Write", connID, tc.writeBuff).Return(tc.writeN, tc.writeErr)

			conn := &Conn{
				id:  connID,
				rpc: rpc,
			}

			n, err := conn.Write(tc.writeBuff)
			require.Equal(t, tc.writeErr, err)
			require.Equal(t, tc.writeN, n)
		})
	}
}

func TestConn_Close(t *testing.T) {
	connID := uint16(1)

	var noErr error

	t.Run("ok", func(t *testing.T) {
		rpc := &MockRPCClient{}
		rpc.On("CloseConn", connID).Return(noErr)

		conn := &Conn{
			id:       connID,
			rpc:      rpc,
			freeConn: func() bool { return true },
		}

		err := conn.Close()
		require.NoError(t, err)
	})

	t.Run("close error", func(t *testing.T) {
		closeErr := errors.New("close error")

		rpc := &MockRPCClient{}
		rpc.On("CloseConn", connID).Return(closeErr)

		conn := &Conn{
			id:       connID,
			rpc:      rpc,
			freeConn: func() bool { return true },
		}

		err := conn.Close()
		require.Equal(t, closeErr, err)
	})

	t.Run("already closed", func(t *testing.T) {
		rpc := &MockRPCClient{}
		rpc.On("CloseConn", connID).Return(noErr)

		conn := &Conn{
			id:       connID,
			rpc:      rpc,
			freeConn: func() bool { return false },
		}

		err := conn.Close()
		require.Error(t, err)
		require.Equal(t, "conn is already closed", err.Error())
	})
}

type wrappedConn struct {
	net.Conn
	local  routing.Addr
	remote routing.Addr
}

func wrapConn(conn net.Conn, local, remote routing.Addr) *wrappedConn {
	return &wrappedConn{
		Conn:   conn,
		local:  local,
		remote: remote,
	}
}

func (p *wrappedConn) LocalAddr() net.Addr {
	return p.local
}

func (p *wrappedConn) RemoteAddr() net.Addr {
	return p.remote
}

func TestConn_TestConn(t *testing.T) {
	mp := func() (net.Conn, net.Conn, func(), error) {
		netType := appnet.TypeSkynet
		keys := snettest.GenKeyPairs(2)
		fmt.Printf("C1 Local: %s\n", keys[0].PK)
		fmt.Printf("C2 Local: %s\n", keys[1].PK)
		p1, p2 := net.Pipe()
		a1 := appnet.Addr{
			Net:    netType,
			PubKey: keys[0].PK,
			Port:   0,
		}
		a2 := appnet.Addr{
			Net:    netType,
			PubKey: keys[1].PK,
			Port:   0,
		}

		ra1 := routing.Addr{
			PubKey: a1.PubKey,
			Port:   a1.Port,
		}
		ra2 := routing.Addr{
			PubKey: a2.PubKey,
			Port:   a2.Port,
		}

		wc1 := wrapConn(p1, ra1, ra2)
		wc2 := wrapConn(p2, ra2, ra1)

		n := &appnet.MockNetworker{}
		n.On("DialContext", mock.Anything, a1).Return(wc1, testhelpers.NoErr)
		n.On("DialContext", mock.Anything, a2).Return(wc2, testhelpers.NoErr)

		appnet.ClearNetworkers()
		err := appnet.AddNetworker(netType, n)
		if err != nil {
			return nil, nil, nil, err
		}

		rpcL, err := nettest.NewLocalListener("tcp")
		if err != nil {
			return nil, nil, nil, err
		}

		rpcS := rpc.NewServer()

		appKeys := snettest.GenKeyPairs(2)

		gateway1 := appserver.NewRPCGateway(logging.MustGetLogger("test_app_rpc_gateway1"))
		gateway2 := appserver.NewRPCGateway(logging.MustGetLogger("test_app_rpc_gateway2"))
		err = rpcS.RegisterName(appKeys[0].PK.Hex(), gateway1)
		if err != nil {
			return nil, nil, nil, err
		}
		err = rpcS.RegisterName(appKeys[1].PK.Hex(), gateway2)
		if err != nil {
			return nil, nil, nil, err
		}

		go rpcS.Accept(rpcL)

		rpcCl1, err := rpc.Dial(rpcL.Addr().Network(), rpcL.Addr().String())
		if err != nil {
			return nil, nil, nil, err
		}

		cl1 := Client{
			log:     logging.MustGetLogger("test_client_1"),
			visorPK: keys[0].PK,
			rpc:     NewRPCClient(rpcCl1, appcommon.Key(appKeys[0].PK.Hex())),
			lm:      idmanager.New(),
			cm:      idmanager.New(),
		}

		rpcCl2, err := rpc.Dial(rpcL.Addr().Network(), rpcL.Addr().String())
		if err != nil {
			return nil, nil, nil, err
		}

		cl2 := Client{
			log:     logging.MustGetLogger("test_client_2"),
			visorPK: keys[1].PK,
			rpc:     NewRPCClient(rpcCl2, appcommon.Key(appKeys[1].PK.Hex())),
			lm:      idmanager.New(),
			cm:      idmanager.New(),
		}

		c1, err := cl1.Dial(a2)
		if err != nil {
			return nil, nil, nil, err
		}

		c2, err := cl2.Dial(a1)
		if err != nil {
			return nil, nil, nil, err
		}

		stop := func() {
			_ = c1.Close()   //nolint:errcheck
			_ = c2.Close()   //nolint:errcheck
			_ = rpcL.Close() //nolint:errcheck
		}

		return c1, c2, stop, nil
	}

	nettest.TestConn(t, mp)
}
