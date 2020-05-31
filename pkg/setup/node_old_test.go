// +build !no_ci

package setup

// TODO(evanlinjin): Either fix or rewrite these tests.

// func TestMain(m *testing.M) {
// 	loggingLevel, ok := os.LookupEnv("TEST_LOGGING_LEVEL")
// 	if ok {
// 		lvl, err := logging.LevelFromString(loggingLevel)
// 		if err != nil {
// 			log.Fatal(err)
// 		}
//
// 		logging.SetLevel(lvl)
// 	} else {
// 		logging.Disable()
// 	}
//
// 	os.Exit(m.Run())
// }
//
// type clientWithDMSGAddrAndListener struct {
// 	*dmsg.Client
// 	Addr dmsg.Addr
// 	// Listener                 *dmsg.Listener
// 	AppliedIntermediaryRules []routing.Rule
// 	AppliedEdgeRules         routing.EdgeRules
// }
//
// func TestNode(t *testing.T) {
// 	// We are generating five key pairs - one for the `Router` of setup node,
// 	// the other ones - for the clients along the desired route.
// 	keys := snettest.GenKeyPairs(5)
//
// 	// create test env
// 	nEnv := snettest.NewEnv(t, keys, []string{dmsg.Type})
// 	defer nEnv.Teardown()
//
// 	reservedIDs := []routing.RouteID{1, 2}
//
// 	// TEST: Emulates the communication between 4 visors and a setup node,
// 	// where the first client visor initiates a route to the last.
// 	t.Run("DialRouteGroup", func(t *testing.T) {
// 		testDialRouteGroup(t, keys, nEnv, reservedIDs)
// 	})
// }
//
// func testDialRouteGroup(t *testing.T, keys []snettest.KeyPair, nEnv *snettest.Env, reservedIDs []routing.RouteID) {
// 	// client index 0 is for setup node.
// 	// clients index 1 to 4 are for visors.
// 	clients, closeClients := prepClients(t, keys, nEnv, reservedIDs, 5)
// 	defer closeClients()
//
// 	// prepare and serve setup node (using client 0).
// 	_, closeSetup := prepSetupNode(t, clients[0].Client)
// 	defer closeSetup()
//
// 	route := prepBidirectionalRoute(clients)
//
// 	forwardRules, consumeRules, intermediaryRules := generateRules(t, route, reservedIDs)
//
// 	forwardRoute, reverseRoute := route.ForwardAndReverse()
//
// 	wantEdgeRules := routing.EdgeRules{
// 		Desc:    reverseRoute.Desc,
// 		Forward: forwardRules[route.Desc.SrcPK()],
// 		Reverse: consumeRules[route.Desc.SrcPK()],
// 	}
//
// 	testLogger := logging.MustGetLogger("setupclient_test")
// 	pks := []cipher.PubKey{clients[0].Addr.PK}
// 	gotEdgeRules, err := setupclient.NewSetupNodeDialer().Dial(context.TODO(), testLogger, nEnv.Nets[1], pks, route)
// 	require.NoError(t, err)
// 	require.Equal(t, wantEdgeRules, gotEdgeRules)
//
// 	for pk, rules := range intermediaryRules {
// 		for _, cl := range clients {
// 			if cl.Addr.PK == pk {
// 				require.Equal(t, cl.AppliedIntermediaryRules, rules)
// 				break
// 			}
// 		}
// 	}
//
// 	respRouteRules := routing.EdgeRules{
// 		Desc:    forwardRoute.Desc,
// 		Forward: forwardRules[route.Desc.DstPK()],
// 		Reverse: consumeRules[route.Desc.DstPK()],
// 	}
//
// 	require.Equal(t, respRouteRules, clients[4].AppliedEdgeRules)
// }
//
// func prepBidirectionalRoute(clients []clientWithDMSGAddrAndListener) routing.BidirectionalRoute {
// 	// prepare route group creation (client_1 will use this to request a route group creation with setup node).
// 	desc := routing.NewRouteDescriptor(clients[1].Addr.PK, clients[4].Addr.PK, 1, 1)
//
// 	forwardHops := []routing.Hop{
// 		{From: clients[1].Addr.PK, To: clients[2].Addr.PK, TpID: uuid.New()},
// 		{From: clients[2].Addr.PK, To: clients[3].Addr.PK, TpID: uuid.New()},
// 		{From: clients[3].Addr.PK, To: clients[4].Addr.PK, TpID: uuid.New()},
// 	}
//
// 	reverseHops := []routing.Hop{
// 		{From: clients[4].Addr.PK, To: clients[3].Addr.PK, TpID: uuid.New()},
// 		{From: clients[3].Addr.PK, To: clients[2].Addr.PK, TpID: uuid.New()},
// 		{From: clients[2].Addr.PK, To: clients[1].Addr.PK, TpID: uuid.New()},
// 	}
//
// 	route := routing.BidirectionalRoute{
// 		Desc:      desc,
// 		KeepAlive: 1 * time.Hour,
// 		Forward:   forwardHops,
// 		Reverse:   reverseHops,
// 	}
//
// 	return route
// }
//
// func generateRules(
// 	t *testing.T,
// 	route routing.BidirectionalRoute,
// 	reservedIDs []routing.RouteID,
// ) (
// 	forwardRules map[cipher.PubKey]routing.Rule,
// 	consumeRules map[cipher.PubKey]routing.Rule,
// 	intermediaryRules RulesMap,
// ) {
// 	wantIDR, _ := NewIDReserver(route.Forward, route.Reverse)
// 	for pk := range wantIDR.rec {
// 		wantIDR.ids[pk] = reservedIDs
// 	}
//
// 	forwardRoute, reverseRoute := route.ForwardAndReverse()
//
// 	forwardRules, consumeRules, intermediaryRules, err := wantIDR.GenerateRules(forwardRoute, reverseRoute)
// 	require.NoError(t, err)
//
// 	return forwardRules, consumeRules, intermediaryRules
// }
//
// func prepClients(
// 	t *testing.T,
// 	keys []snettest.KeyPair,
// 	nEnv *snettest.Env,
// 	reservedIDs []routing.RouteID,
// 	n int,
// ) ([]clientWithDMSGAddrAndListener, func()) {
// 	clients := make([]clientWithDMSGAddrAndListener, n)
//
// 	for i := 0; i < n; i++ {
// 		var port uint16
// 		// setup node
// 		if i == 0 {
// 			port = skyenv.DmsgSetupPort
// 		} else {
// 			port = skyenv.DmsgAwaitSetupPort
// 		}
//
// 		pk, sk := keys[i].PK, keys[i].SK
// 		t.Logf("client[%d] PK: %s\n", i, pk)
//
// 		clientLogger := logging.MustGetLogger(fmt.Sprintf("client_%d:%s:%d", i, pk, port))
// 		c := dmsg.NewClient(pk, sk, nEnv.DmsgD, &dmsg.Config{MinSessions: 1})
// 		c.SetLogger(clientLogger)
//
// 		go c.Serve()
//
// 		listener, err := c.Listen(port)
// 		require.NoError(t, err)
//
// 		clients[i] = clientWithDMSGAddrAndListener{
// 			Client: c,
// 			Addr: dmsg.Addr{
// 				PK:   pk,
// 				Port: port,
// 			},
// 			// Listener: listener,
// 		}
//
// 		fmt.Printf("Client %d PK: %s\n", i, clients[i].Addr.PK)
//
// 		// exclude setup node
// 		if i == 0 {
// 			continue
// 		}
//
// 		r := prepRouter(&clients[i], reservedIDs, i == n-1)
//
// 		startRPC(t, r, listener)
// 	}
//
// 	return clients, func() {
// 		for _, c := range clients {
// 			require.NoError(t, c.Close())
// 		}
// 	}
// }
//
// func prepRouter(client *clientWithDMSGAddrAndListener, reservedIDs []routing.RouteID, last bool) *router.MockRouter {
// 	r := &router.MockRouter{}
// 	// passing two rules to each visor (forward and reverse routes). Simulate
// 	// applying intermediary rules.
// 	r.On("SaveRoutingRules", mock.Anything, mock.Anything).
// 		Return(func(rules ...routing.Rule) error {
// 			client.AppliedIntermediaryRules = append(client.AppliedIntermediaryRules, rules...)
// 			return nil
// 		})
//
// 	// simulate reserving IDs.
// 	r.On("ReserveKeys", 2).Return(reservedIDs, testhelpers.NoErr)
//
// 	// destination visor. Simulate applying edge rules.
// 	if last {
// 		r.On("IntroduceRules", mock.Anything).Return(func(rules routing.EdgeRules) error {
// 			client.AppliedEdgeRules = rules
// 			return nil
// 		})
// 	}
//
// 	return r
// }
//
// func startRPC(t *testing.T, r router.Router, listener net.Listener) {
// 	rpcServer := rpc.NewServer()
// 	require.NoError(t, rpcServer.Register(router.NewRPCGateway(r)))
//
// 	go rpcServer.Accept(listener)
// }
//
// func prepSetupNode(t *testing.T, c *dmsg.Client) (*Node, func()) {
// 	sn := &Node{
// 		log:     logging.MustGetLogger("setup_node"),
// 		dmsgC:   c,
// 		metrics: metrics.NewDummy(),
// 	}
//
// 	go func() {
// 		if err := sn.Serve(); err != nil {
// 			sn.log.WithError(err).Error("Failed to serve")
// 		}
// 	}()
//
// 	return sn, func() {
// 		require.NoError(t, sn.Close())
// 	}
// }
