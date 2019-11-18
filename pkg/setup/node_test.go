// +build !no_ci

package setup

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/rpc"
	"os"
	"testing"
	"time"

	"github.com/SkycoinProject/skywire-mainnet/internal/testhelpers"

	"github.com/SkycoinProject/skywire-mainnet/pkg/snet/snettest"

	"github.com/SkycoinProject/dmsg"
	"github.com/SkycoinProject/dmsg/cipher"
	"github.com/SkycoinProject/dmsg/disc"
	"github.com/SkycoinProject/skycoin/src/util/logging"
	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/nettest"

	"github.com/SkycoinProject/skywire-mainnet/internal/skyenv"
	"github.com/SkycoinProject/skywire-mainnet/pkg/metrics"
	"github.com/SkycoinProject/skywire-mainnet/pkg/router"
	"github.com/SkycoinProject/skywire-mainnet/pkg/routing"
)

func TestMain(m *testing.M) {
	loggingLevel, ok := os.LookupEnv("TEST_LOGGING_LEVEL")
	if ok {
		lvl, err := logging.LevelFromString(loggingLevel)
		if err != nil {
			log.Fatal(err)
		}
		logging.SetLevel(lvl)
	} else {
		logging.Disable()
	}

	os.Exit(m.Run())
}

// TODO(Darkren): fix this test. Explanation below
// Test may finish in 3 different ways:
// 1. Pass
// 2. Fail
// 3. Hang
// Adding `time.Sleep` at the start of `Write` operation in the DMSG makes it less possible to hang
// From observations seems like something's wrong in the DMSG, probably writing right after `Dial/Accept`
// causes this.
// 1. Test has possibility to pass, this means the test itself is correct
// 2. Test failure always comes with unexpected `context deadline exceeded`. In `read` operation of
// `setup proto` we ensure additional timeout, that's where this error comes from. This fact proves that
// DMSG has a related bug
// 3. Hanging may be not the problem of the DMSG. Probably some of the communication part here is wrong.
// The reason I think so is that - if we ensure read timeouts, why doesn't this test constantly fail?
// Maybe some wrapper for DMSG is wrong, or some internal operations before the actual communication behave bad
func TestNode(t *testing.T) {
	// We are generating two key pairs - one for the a `Router`, the other to send packets to `Router`.
	keys := snettest.GenKeyPairs(5)

	// create test env
	nEnv := snettest.NewEnv(t, keys)
	defer nEnv.Teardown()

	// Prepare dmsg server.
	server, serverErr := createServer(t, nEnv.DmsgD)
	defer func() {
		require.NoError(t, server.Close())
		require.NoError(t, errWithTimeout(serverErr))
	}()

	type clientWithDMSGAddrAndListener struct {
		*dmsg.Client
		Addr                     dmsg.Addr
		Listener                 *dmsg.Listener
		RPCServer                *rpc.Server
		Router                   router.Router
		AppliedIntermediaryRules []routing.Rule
		AppliedEdgeRules         routing.EdgeRules
	}

	ctx := context.TODO()

	reservedIDs := []routing.RouteID{1, 2}

	// CLOSURE: sets up dmsg clients.
	prepClients := func(n int) ([]clientWithDMSGAddrAndListener, func()) {
		clients := make([]clientWithDMSGAddrAndListener, n)
		for i := 0; i < n; i++ {
			var port uint16
			// setup node
			if i == 0 {
				port = skyenv.DmsgSetupPort
			} else {
				port = skyenv.DmsgAwaitSetupPort
			}
			pk, sk := keys[i].PK, keys[i].SK
			t.Logf("client[%d] PK: %s\n", i, pk)
			c := dmsg.NewClient(pk, sk, nEnv.DmsgD, dmsg.SetLogger(logging.MustGetLogger(fmt.Sprintf("client_%d:%s:%d", i, pk, port))))
			require.NoError(t, c.InitiateServerConnections(ctx, 1))
			listener, err := c.Listen(port)
			require.NoError(t, err)
			clients[i] = clientWithDMSGAddrAndListener{
				Client: c,
				Addr: dmsg.Addr{
					PK:   pk,
					Port: port,
				},
				Listener: listener,
			}

			fmt.Printf("Client %d PK: %s\n", i, clients[i].Addr.PK)

			r := &router.MockRouter{}
			// for intermediary nodes and the destination one
			if i >= 1 {
				// passing two rules to each node (forward and reverse routes)
				r.On("SaveRoutingRules", mock.Anything, mock.Anything).
					Return(func(rules ...routing.Rule) error {
						clients[i].AppliedIntermediaryRules = append(clients[i].AppliedIntermediaryRules, rules...)
						return nil
					})

				r.On("ReserveKeys", 2).Return(reservedIDs, testhelpers.NoErr)

				if i == (n - 1) {
					r.On("IntroduceRules", mock.Anything).Return(func(rules routing.EdgeRules) error {
						clients[i].AppliedEdgeRules = rules
						return nil
					})
				}
			}

			clients[i].Router = r
			if i != 0 {
				rpcGateway := router.NewRPCGateway(r)
				rpcServer := rpc.NewServer()
				err = rpcServer.Register(rpcGateway)
				require.NoError(t, err)
				clients[i].RPCServer = rpcServer
				go func(idx int) {
					for {
						conn, err := listener.Accept()
						if err != nil {
							//fmt.Printf("Error accepting RPC conn: %v\n", err)
							continue
						}

						//fmt.Println("Accepted RPC conn")
						go clients[idx].RPCServer.ServeConn(conn)
					}
				}(i)
				//go clients[i].RPCServer.Accept(listener)
			}
		}
		return clients, func() {
			for _, c := range clients {
				//require.NoError(t, c.Listener.Close())
				require.NoError(t, c.Close())
			}
		}
	}

	// CLOSURE: sets up setup node.
	prepSetupNode := func(c *dmsg.Client, listener *dmsg.Listener) (*Node, func()) {
		sn := &Node{
			logger:  logging.MustGetLogger("setup_node"),
			dmsgC:   c,
			dmsgL:   listener,
			metrics: metrics.NewDummy(),
		}
		go func() {
			if err := sn.Serve(); err != nil {
				sn.logger.WithError(err).Error("Failed to serve")
			}
		}()
		return sn, func() {
			require.NoError(t, sn.Close())
		}
	}

	// TEST: Emulates the communication between 4 visor nodes and a setup node,
	// where the first client node initiates a route to the last.
	t.Run("DialRouteGroup", func(t *testing.T) {
		// client index 0 is for setup node.
		// clients index 1 to 4 are for visor nodes.
		clients, closeClients := prepClients(5)
		defer closeClients()

		// prepare and serve setup node (using client 0).
		sn, closeSetup := prepSetupNode(clients[0].Client, clients[0].Listener)
		defer closeSetup()

		//setupPK := clients[0].Addr.PK

		// prepare loop creation (client_1 will use this to request loop creation with setup node).
		desc := routing.NewRouteDescriptor(clients[1].Addr.PK, clients[4].Addr.PK, 1, 1)

		forwardHops := []routing.Hop{
			{From: clients[1].Addr.PK, To: clients[2].Addr.PK, TpID: uuid.New()},
			{From: clients[2].Addr.PK, To: clients[3].Addr.PK, TpID: uuid.New()},
			{From: clients[3].Addr.PK, To: clients[4].Addr.PK, TpID: uuid.New()},
		}

		reverseHops := []routing.Hop{
			{From: clients[4].Addr.PK, To: clients[3].Addr.PK, TpID: uuid.New()},
			{From: clients[3].Addr.PK, To: clients[2].Addr.PK, TpID: uuid.New()},
			{From: clients[2].Addr.PK, To: clients[1].Addr.PK, TpID: uuid.New()},
		}

		route := routing.BidirectionalRoute{
			Desc:      desc,
			KeepAlive: 1 * time.Hour,
			Forward:   forwardHops,
			Reverse:   reverseHops,
		}
		/////////////////////////////////////////////////////////////////
		/*reservoir, total := newIDReservoir(route.Forward, route.Reverse)
		sn.logger.Infof("There are %d route IDs to reserve.", total)

		err := reservoir.ReserveIDs(ctx, sn.logger, sn.dmsgC, routerclient.ReserveIDs)
		require.NoError(t, err)

		sn.logger.Infof("Successfully reserved route IDs.")*/
		///////////////////////////////////////////////////////////////////////////
		//logger := logging.MustGetLogger("setup_client_test")

		//gotEdgeRules, err := setupclient.DialRouteGroup(ctx, logger, nEnv.Nets[1], []cipher.PubKey{setupPK}, route)
		gotEdgeRules, err := sn.handleDialRouteGroup(context.Background(), route)
		require.NoError(t, err)

		wantEdgeRules := routing.EdgeRules{ // TODO: fill with correct values
			Desc:    desc,
			Forward: nil,
			Reverse: nil,
		}
		require.Equal(t, wantEdgeRules, gotEdgeRules)

		/*
			var addRuleDone sync.WaitGroup
			var nextRouteID uint32
			// CLOSURE: emulates how a visor node should react when expecting an AddRules packet.
			expectAddRules := func(client int, expRule routing.RuleType) {
				conn, err := clients[client].Listener.Accept()
				require.NoError(t, err)

				fmt.Printf("client %v:%v accepted\n", client, clients[client].Addr)

				proto := NewSetupProtocol(conn)

				pt, _, err := proto.ReadPacket()
				require.NoError(t, err)
				require.Equal(t, PacketRequestRouteID, pt)

				fmt.Printf("client %v:%v got PacketRequestRouteID\n", client, clients[client].Addr)

				routeID := atomic.AddUint32(&nextRouteID, 1)

				// TODO: This error is not checked due to a bug in dmsg.
				_ = proto.WritePacket(RespSuccess, []routing.RouteID{routing.RouteID(routeID)}) // nolint:errcheck
				require.NoError(t, err)

				fmt.Printf("client %v:%v responded to with registration ID: %v\n", client, clients[client].Addr, routeID)

				require.NoError(t, conn.Close())

				conn, err = clients[client].Listener.Accept()
				require.NoError(t, err)

				fmt.Printf("client %v:%v accepted 2nd time\n", client, clients[client].Addr)

				proto = NewSetupProtocol(conn)

				pt, pp, err := proto.ReadPacket()
				require.NoError(t, err)
				require.Equal(t, PacketAddRules, pt)

				fmt.Printf("client %v:%v got PacketAddRules\n", client, clients[client].Addr)

				var rs []routing.Rule
				require.NoError(t, json.Unmarshal(pp, &rs))

				for _, r := range rs {
					require.Equal(t, expRule, r.Type())
				}

				// TODO: This error is not checked due to a bug in dmsg.
				err = proto.WritePacket(RespSuccess, nil)
				_ = err

				fmt.Printf("client %v:%v responded for PacketAddRules\n", client, clients[client].Addr)

				require.NoError(t, conn.Close())

				addRuleDone.Done()
			}

			// CLOSURE: emulates how a visor node should react when expecting an OnConfirmLoop packet.
			expectConfirmLoop := func(client int) {
				tp, err := clients[client].Listener.AcceptTransport()
				require.NoError(t, err)

				proto := NewSetupProtocol(tp)

				pt, pp, err := proto.ReadPacket()
				require.NoError(t, err)
				require.Equal(t, PacketConfirmLoop, pt)

				var d routing.LoopData
				require.NoError(t, json.Unmarshal(pp, &d))

				switch client {
				case 1:
					require.Equal(t, ld.Loop, d.Loop)
				case 4:
					require.Equal(t, ld.Loop.Local, d.Loop.Remote)
					require.Equal(t, ld.Loop.Remote, d.Loop.Local)
				default:
					t.Fatalf("We shouldn't be receiving a OnConfirmLoop packet from client %d", client)
				}

				// TODO: This error is not checked due to a bug in dmsg.
				err = proto.WritePacket(RespSuccess, nil)
				_ = err

				require.NoError(t, tp.Close())
			}

			// since the route establishment is asynchronous,
			// we must expect all the messages in parallel
			addRuleDone.Add(4)
			go expectAddRules(4, routing.RuleApp)
			go expectAddRules(3, routing.RuleForward)
			go expectAddRules(2, routing.RuleForward)
			go expectAddRules(1, routing.RuleForward)
			addRuleDone.Wait()
			fmt.Println("FORWARD ROUTE DONE")
			addRuleDone.Add(4)
			go expectAddRules(1, routing.RuleApp)
			go expectAddRules(2, routing.RuleForward)
			go expectAddRules(3, routing.RuleForward)
			go expectAddRules(4, routing.RuleForward)
			addRuleDone.Wait()
			fmt.Println("REVERSE ROUTE DONE")
			expectConfirmLoop(1)
			expectConfirmLoop(4)

		*/
	})
}

func createServer(t *testing.T, dc disc.APIClient) (srv *dmsg.Server, srvErr <-chan error) {
	pk, sk, err := cipher.GenerateDeterministicKeyPair([]byte("s"))
	require.NoError(t, err)
	l, err := nettest.NewLocalListener("tcp")
	require.NoError(t, err)
	srv, err = dmsg.NewServer(pk, sk, "", l, dc)
	require.NoError(t, err)
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Serve()
		close(errCh)
	}()
	return srv, errCh
}

func errWithTimeout(ch <-chan error) error {
	select {
	case err := <-ch:
		return err
	case <-time.After(5 * time.Second):
		return errors.New("timeout")
	}
}
