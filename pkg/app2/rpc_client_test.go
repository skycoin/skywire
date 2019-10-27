package app2

/*func TestRPCClient_Dial(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		s := prepRPCServer(t, prepGateway())
		rpcL, lisCleanup := prepListener(t)
		defer lisCleanup()
		go s.Accept(rpcL)

		cl := prepClient(t, rpcL.Addr().Network(), rpcL.Addr().String())

		remoteNet := appnet.TypeDMSG
		remotePK, _ := cipher.GenerateKeyPair()
		remotePort := routing.Port(100)
		remote := appnet.Addr{
			Net:    remoteNet,
			PubKey: remotePK,
			Port:   remotePort,
		}

		localPK, _ := cipher.GenerateKeyPair()
		dmsgLocal := dmsg.Addr{
			PK:   localPK,
			Port: 101,
		}
		dmsgRemote := dmsg.Addr{
			PK:   remotePK,
			Port: uint16(remotePort),
		}

		dialCtx := context.Background()
		dialConn := dmsg.NewTransport(&app2.MockConn{}, logging.MustGetLogger("dmsg_tp"),
			dmsgLocal, dmsgRemote, 0, func(_ uint16) {})
		var noErr error

		n := &appnet.MockNetworker{}
		n.On("DialContext", dialCtx, remote).Return(dialConn, noErr)

		appnet.ClearNetworkers()
		err := appnet.AddNetworker(remoteNet, n)
		require.NoError(t, err)

		connID, localPort, err := Dial(remote)
		require.NoError(t, err)
		require.Equal(t, connID, uint16(1))
		require.Equal(t, localPort, routing.Port(dmsgLocal.Port))

	})

	t.Run("dial error", func(t *testing.T) {
		s := prepRPCServer(t, prepGateway())
		rpcL, lisCleanup := prepListener(t)
		defer lisCleanup()
		go s.Accept(rpcL)

		cl := prepClient(t, rpcL.Addr().Network(), rpcL.Addr().String())

		remoteNet := appnet.TypeDMSG
		remotePK, _ := cipher.GenerateKeyPair()
		remotePort := routing.Port(100)
		remote := appnet.Addr{
			Net:    remoteNet,
			PubKey: remotePK,
			Port:   remotePort,
		}

		dialCtx := context.Background()
		var dialConn net.Conn
		dialErr := errors.New("dial error")

		n := &appnet.MockNetworker{}
		n.On("DialContext", dialCtx, remote).Return(dialConn, dialErr)

		appnet.ClearNetworkers()
		err := appnet.AddNetworker(remoteNet, n)
		require.NoError(t, err)

		connID, localPort, err := Dial(remote)
		require.Error(t, err)
		require.Equal(t, err.Error(), dialErr.Error())
		require.Equal(t, connID, uint16(0))
		require.Equal(t, localPort, routing.Port(0))
	})
}

func TestRPCClient_Listen(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		s := prepRPCServer(t, prepGateway())
		rpcL, lisCleanup := prepListener(t)
		defer lisCleanup()
		go s.Accept(rpcL)

		cl := prepClient(t, rpcL.Addr().Network(), rpcL.Addr().String())

		localNet := appnet.TypeDMSG
		localPK, _ := cipher.GenerateKeyPair()
		localPort := routing.Port(100)
		local := appnet.Addr{
			Net:    localNet,
			PubKey: localPK,
			Port:   localPort,
		}

		listenCtx := context.Background()
		var listenLis net.Listener
		var noErr error

		n := &appnet.MockNetworker{}
		n.On("ListenContext", listenCtx, local).Return(listenLis, noErr)

		appnet.ClearNetworkers()
		err := appnet.AddNetworker(localNet, n)
		require.NoError(t, err)

		lisID, err := Listen(local)
		require.NoError(t, err)
		require.Equal(t, lisID, uint16(1))
	})

	t.Run("listen error", func(t *testing.T) {
		s := prepRPCServer(t, prepGateway())
		rpcL, lisCleanup := prepListener(t)
		defer lisCleanup()
		go s.Accept(rpcL)

		cl := prepClient(t, rpcL.Addr().Network(), rpcL.Addr().String())

		localNet := appnet.TypeDMSG
		localPK, _ := cipher.GenerateKeyPair()
		localPort := routing.Port(100)
		local := appnet.Addr{
			Net:    localNet,
			PubKey: localPK,
			Port:   localPort,
		}

		listenCtx := context.Background()
		var listenLis net.Listener
		listenErr := errors.New("listen error")

		n := &appnet.MockNetworker{}
		n.On("ListenContext", listenCtx, local).Return(listenLis, listenErr)

		appnet.ClearNetworkers()
		err := appnet.AddNetworker(localNet, n)
		require.NoError(t, err)

		lisID, err := Listen(local)
		require.Error(t, err)
		require.Equal(t, err.Error(), listenErr.Error())
		require.Equal(t, lisID, uint16(0))
	})
}

func TestRPCClient_Accept(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		gateway := prepGateway()

		localPK, _ := cipher.GenerateKeyPair()
		localPort := uint16(100)
		dmsgLocal := dmsg.Addr{
			PK:   localPK,
			Port: localPort,
		}
		remotePK, _ := cipher.GenerateKeyPair()
		remotePort := uint16(101)
		dmsgRemote := dmsg.Addr{
			PK:   remotePK,
			Port: remotePort,
		}
		lisConn := dmsg.NewTransport(&app2.MockConn{}, logging.MustGetLogger("dmsg_tp"),
			dmsgLocal, dmsgRemote, 0, func(_ uint16) {})
		var noErr error

		lis := &app2.MockListener{}
		lis.On("Accept").Return(lisConn, noErr)

		lisID := uint16(1)

		_, err := gateway.lm.add(lisID, lis)
		require.NoError(t, err)

		s := prepRPCServer(t, gateway)
		rpcL, lisCleanup := prepListener(t)
		defer lisCleanup()
		go s.Accept(rpcL)

		cl := prepClient(t, rpcL.Addr().Network(), rpcL.Addr().String())

		wantRemote := appnet.Addr{
			Net:    appnet.TypeDMSG,
			PubKey: remotePK,
			Port:   routing.Port(remotePort),
		}

		connID, remote, err := Accept(lisID)
		require.NoError(t, err)
		require.Equal(t, connID, uint16(1))
		require.Equal(t, remote, wantRemote)
	})

	t.Run("accept error", func(t *testing.T) {
		gateway := prepGateway()

		var lisConn net.Conn
		listenErr := errors.New("accept error")

		lis := &app2.MockListener{}
		lis.On("Accept").Return(lisConn, listenErr)

		lisID := uint16(1)

		_, err := gateway.lm.add(lisID, lis)
		require.NoError(t, err)

		s := prepRPCServer(t, gateway)
		rpcL, lisCleanup := prepListener(t)
		defer lisCleanup()
		go s.Accept(rpcL)

		cl := prepClient(t, rpcL.Addr().Network(), rpcL.Addr().String())

		connID, remote, err := Accept(lisID)
		require.Error(t, err)
		require.Equal(t, err.Error(), listenErr.Error())
		require.Equal(t, connID, uint16(0))
		require.Equal(t, remote, appnet.Addr{})
	})
}

func TestRPCClient_Write(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		gateway := prepGateway()

		writeBuf := []byte{1, 1, 1, 1, 1, 1, 1, 1, 1, 1}
		writeN := 10
		var noErr error

		conn := &app2.MockConn{}
		conn.On("Write", writeBuf).Return(writeN, noErr)

		connID := uint16(1)

		_, err := gateway.cm.add(connID, conn)
		require.NoError(t, err)

		s := prepRPCServer(t, gateway)
		rpcL, lisCleanup := prepListener(t)
		defer lisCleanup()
		go s.Accept(rpcL)

		cl := prepClient(t, rpcL.Addr().Network(), rpcL.Addr().String())

		n, err := Write(connID, writeBuf)
		require.NoError(t, err)
		require.Equal(t, n, writeN)
	})

	t.Run("write error", func(t *testing.T) {
		gateway := prepGateway()

		writeBuf := []byte{1, 1, 1, 1, 1, 1, 1, 1, 1, 1}
		writeN := 0
		writeErr := errors.New("write error")

		conn := &app2.MockConn{}
		conn.On("Write", writeBuf).Return(writeN, writeErr)

		connID := uint16(1)

		_, err := gateway.cm.add(connID, conn)
		require.NoError(t, err)

		s := prepRPCServer(t, gateway)
		rpcL, lisCleanup := prepListener(t)
		defer lisCleanup()
		go s.Accept(rpcL)

		cl := prepClient(t, rpcL.Addr().Network(), rpcL.Addr().String())

		n, err := Write(connID, writeBuf)
		require.Error(t, err)
		require.Equal(t, err.Error(), writeErr.Error())
		require.Equal(t, n, 0)
	})
}

func TestRPCClient_Read(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		gateway := prepGateway()

		readBufLen := 10
		readBuf := make([]byte, readBufLen)
		readN := 5
		var noErr error

		conn := &app2.MockConn{}
		conn.On("Read", readBuf).Return(readN, noErr)

		connID := uint16(1)

		_, err := gateway.cm.add(connID, conn)
		require.NoError(t, err)

		s := prepRPCServer(t, gateway)
		rpcL, lisCleanup := prepListener(t)
		defer lisCleanup()
		go s.Accept(rpcL)

		cl := prepClient(t, rpcL.Addr().Network(), rpcL.Addr().String())

		n, err := Read(connID, readBuf)
		require.NoError(t, err)
		require.Equal(t, n, readN)
	})

	t.Run("read error", func(t *testing.T) {
		gateway := prepGateway()

		readBufLen := 10
		readBuf := make([]byte, readBufLen)
		readN := 0
		readErr := errors.New("read error")

		conn := &app2.MockConn{}
		conn.On("Read", readBuf).Return(readN, readErr)

		connID := uint16(1)

		_, err := gateway.cm.add(connID, conn)
		require.NoError(t, err)

		s := prepRPCServer(t, gateway)
		rpcL, lisCleanup := prepListener(t)
		defer lisCleanup()
		go s.Accept(rpcL)

		cl := prepClient(t, rpcL.Addr().Network(), rpcL.Addr().String())

		n, err := Read(connID, readBuf)
		require.Error(t, err)
		require.Equal(t, err.Error(), readErr.Error())
		require.Equal(t, n, readN)
	})
}

func TestRPCClient_CloseConn(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		gateway := prepGateway()

		var noErr error

		conn := &app2.MockConn{}
		conn.On("Close").Return(noErr)

		connID := uint16(1)

		_, err := gateway.cm.add(connID, conn)
		require.NoError(t, err)

		s := prepRPCServer(t, gateway)
		rpcL, lisCleanup := prepListener(t)
		defer lisCleanup()
		go s.Accept(rpcL)

		cl := prepClient(t, rpcL.Addr().Network(), rpcL.Addr().String())

		err = CloseConn(connID)
		require.NoError(t, err)
	})

	t.Run("close error", func(t *testing.T) {
		gateway := prepGateway()

		closeErr := errors.New("close error")

		conn := &app2.MockConn{}
		conn.On("Close").Return(closeErr)

		connID := uint16(1)

		_, err := gateway.cm.add(connID, conn)
		require.NoError(t, err)

		s := prepRPCServer(t, gateway)
		rpcL, lisCleanup := prepListener(t)
		defer lisCleanup()
		go s.Accept(rpcL)

		cl := prepClient(t, rpcL.Addr().Network(), rpcL.Addr().String())

		err = CloseConn(connID)
		require.Error(t, err)
		require.Equal(t, err.Error(), closeErr.Error())
	})
}

func TestRPCClient_CloseListener(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		gateway := prepGateway()

		var noErr error

		lis := &app2.MockListener{}
		lis.On("Close").Return(noErr)

		lisID := uint16(1)

		_, err := gateway.lm.add(lisID, lis)
		require.NoError(t, err)

		s := prepRPCServer(t, gateway)
		rpcL, lisCleanup := prepListener(t)
		defer lisCleanup()
		go s.Accept(rpcL)

		cl := prepClient(t, rpcL.Addr().Network(), rpcL.Addr().String())

		err = CloseListener(lisID)
		require.NoError(t, err)
	})

	t.Run("close error", func(t *testing.T) {
		gateway := prepGateway()

		closeErr := errors.New("close error")

		lis := &app2.MockListener{}
		lis.On("Close").Return(closeErr)

		lisID := uint16(1)

		_, err := gateway.lm.add(lisID, lis)
		require.NoError(t, err)

		s := prepRPCServer(t, gateway)
		rpcL, lisCleanup := prepListener(t)
		defer lisCleanup()
		go s.Accept(rpcL)

		cl := prepClient(t, rpcL.Addr().Network(), rpcL.Addr().String())

		err = CloseListener(lisID)
		require.Error(t, err)
		require.Equal(t, err.Error(), closeErr.Error())
	})
}

func prepGateway() *RPCGateway {
	l := logging.MustGetLogger("rpc_gateway")
	return newRPCGateway(l)
}

func prepRPCServer(t *testing.T, gateway *RPCGateway) *rpc.Server {
	s := rpc.NewServer()
	err := s.Register(gateway)
	require.NoError(t, err)

	return s
}

func prepListener(t *testing.T) (lis net.Listener, cleanup func()) {
	lis, err := nettest.NewLocalListener("tcp")
	require.NoError(t, err)

	return lis, func() {
		err := lis.Close()
		require.NoError(t, err)
	}
}

func prepClient(t *testing.T, network, addr string) RPCClient {
	rpcCl, err := rpc.Dial(network, addr)
	require.NoError(t, err)

	return NewRPCClient(rpcCl, "RPCGateway")
}*/
