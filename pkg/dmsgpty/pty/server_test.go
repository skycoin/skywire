package pty

// TODO(evanlinjin): Fix this test.
//func TestServer_Serve(t *testing.T) {
//	// prepare PKs
//	aPK, aSK, err := cipher.GenerateDeterministicKeyPair([]byte("a seed"))
//	require.NoError(t, err)
//	bPK, bSK, err := cipher.GenerateDeterministicKeyPair([]byte("b seed"))
//	require.NoError(t, err)
//
//	// prepare auth file
//	authF, err := ioutil.TempFile(os.TempDir(), "")
//	require.NoError(t, err)
//	authFName := authF.Name()
//	defer func() { require.NoError(t, os.Remove(authFName)) }()
//	require.NoError(t, authF.Close())
//	auth, err := ptycfg2.NewJsonFileWhiteList(authFName)
//	require.NoError(t, err)
//	require.NoError(t, auth.Add(aPK, bPK))
//
//	t.Run("Whitelist_Get", func(t *testing.T) {
//		for _, pk := range []cipher.PubKey{aPK, bPK} {
//			ok, err := auth.Get(pk)
//			require.NoError(t, err)
//			require.True(t, ok)
//		}
//	})
//
//	// prepare dmsg env
//	dmsgD := disc.NewMock()
//	sPK, sSK, err := cipher.GenerateDeterministicKeyPair([]byte("dmsg server seed"))
//	require.NoError(t, err)
//	sL, err := nettest.NewLocalListener("tcp")
//	require.NoError(t, err)
//	defer func() { _ = sL.Close() }() //nolint:errcheck
//	dmsgS, err := dmsg.NewServer(sPK, sSK, "", sL, dmsgD)
//	require.NoError(t, err)
//	go func() { _ = dmsgS.Serve() }() //nolint:errcheck
//
//	dcA := dmsg.NewClient(aPK, aSK, dmsgD, dmsg.SetLogger(logging.MustGetLogger("dmsgC_A")))
//	require.NoError(t, dcA.InitiateServerConnections(context.TODO(), 1))
//
//	dcB := dmsg.NewClient(bPK, bSK, dmsgD, dmsg.SetLogger(logging.MustGetLogger("dmsgC_B")))
//	require.NoError(t, dcB.InitiateServerConnections(context.TODO(), 1))
//
//	// prepare server (a)
//	srv, err := NewServer(nil, aPK, aSK, authFName)
//	require.NoError(t, err)
//
//	// serve (a)
//	port := uint16(22)
//	lis, err := dcA.Listen(port)
//	require.NoError(t, err)
//
//	ctx, cancel := context.WithCancel(context.TODO())
//	defer cancel()
//	go srv.Serve(ctx, lis)
//
//	// prepare client (b)
//	tpB, err := dcB.Dial(context.TODO(), aPK, port)
//	require.NoError(t, err)
//
//	ptyB, err := NewPtyClientWithTp(nil, bSK, tpB)
//	require.NoError(t, err)
//
//	cmds := []string{"ls", /*"ps", "pwd"*/}
//	for _, cmd := range cmds {
//		require.NoError(t, ptyB.Start(cmd))
//		readB, err := ioutil.ReadAll(ptyB)
//		require.EqualError(t, err, "EOF")
//		fmt.Println(string(readB))
//	}
//
//	//fmt.Println("starting!")
//	//_ = ptyB.Start("ls")
//	//fmt.Println("started!")
//	//
//	//readB, err := ioutil.ReadAll(ptyB)
//	//require.EqualError(t, err, "EOF")
//	//fmt.Println(string(readB))
//
//	require.NoError(t, ptyB.Close())
//}
