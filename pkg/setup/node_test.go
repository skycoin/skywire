// +build !no_ci

package setup

import (
	"context"
	"fmt"
	"log"
	"net/rpc"
	"os"
	"testing"
	"time"

	"github.com/SkycoinProject/skywire-mainnet/pkg/setup/setupclient"

	"github.com/SkycoinProject/skywire-mainnet/internal/testhelpers"

	"github.com/SkycoinProject/skywire-mainnet/pkg/snet/snettest"

	"github.com/SkycoinProject/dmsg"
	"github.com/SkycoinProject/dmsg/cipher"
	"github.com/SkycoinProject/skycoin/src/util/logging"
	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

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

func TestNode(t *testing.T) {
	// We are generating five key pairs - one for the `Router` of setup node,
	// the other ones - for the clients along the desired route.
	keys := snettest.GenKeyPairs(5)

	// create test env
	nEnv := snettest.NewEnv(t, keys, []string{dmsg.Type})
	defer nEnv.Teardown()

	type clientWithDMSGAddrAndListener struct {
		*dmsg.Client
		Addr                     dmsg.Addr
		Listener                 *dmsg.Listener
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

			clientLogger := logging.MustGetLogger(fmt.Sprintf("client_%d:%s:%d", i, pk, port))
			c := dmsg.NewClient(pk, sk, nEnv.DmsgD, dmsg.SetLogger(clientLogger))
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
			// exclude setup node
			if i > 0 {
				idx := i
				// passing two rules to each node (forward and reverse routes). Simulate
				// applying intermediary rules.
				r.On("SaveRoutingRules", mock.Anything, mock.Anything).
					Return(func(rules ...routing.Rule) error {
						clients[idx].AppliedIntermediaryRules = append(clients[idx].AppliedIntermediaryRules, rules...)
						return nil
					})

				// simulate reserving IDs.
				r.On("ReserveKeys", 2).Return(reservedIDs, testhelpers.NoErr)

				// destination node. Simulate applying edge rules.
				if i == (n - 1) {
					r.On("IntroduceRules", mock.Anything).Return(func(rules routing.EdgeRules) error {
						clients[idx].AppliedEdgeRules = rules
						return nil
					})
				}

				rpcServer := rpc.NewServer()
				err = rpcServer.Register(router.NewRPCGateway(r))
				require.NoError(t, err)

				go rpcServer.Accept(listener)
			}
		}

		return clients, func() {
			for _, c := range clients {
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

	// generates forward and reverse routes for the bidirectional one.
	generateForwardAndReverseRoutes := func(route routing.BidirectionalRoute) (routing.Route, routing.Route) {
		forwardRoute := routing.Route{
			Desc:      route.Desc,
			Path:      route.Forward,
			KeepAlive: route.KeepAlive,
		}
		reverseRoute := routing.Route{
			Desc:      route.Desc.Invert(),
			Path:      route.Reverse,
			KeepAlive: route.KeepAlive,
		}

		return forwardRoute, reverseRoute
	}

	// generates wanted rules.
	generateRules := func(
		t *testing.T,
		route routing.BidirectionalRoute,
		reservedIDs []routing.RouteID,
	) (forwardRules, consumeRules map[cipher.PubKey]routing.Rule, intermediaryRules RulesMap) {
		wantIDR, _ := newIDReservoir(route.Forward, route.Reverse)
		for pk := range wantIDR.rec {
			wantIDR.ids[pk] = reservedIDs
		}

		forwardRoute, reverseRoute := generateForwardAndReverseRoutes(route)

		forwardRules, consumeRules, intermediaryRules, err := wantIDR.GenerateRules(forwardRoute, reverseRoute)
		require.NoError(t, err)

		return forwardRules, consumeRules, intermediaryRules
	}

	// TEST: Emulates the communication between 4 visor nodes and a setup node,
	// where the first client node initiates a route to the last.
	t.Run("DialRouteGroup", func(t *testing.T) {
		// client index 0 is for setup node.
		// clients index 1 to 4 are for visor nodes.
		clients, closeClients := prepClients(5)
		defer closeClients()

		// prepare and serve setup node (using client 0).
		_, closeSetup := prepSetupNode(clients[0].Client, clients[0].Listener)
		defer closeSetup()

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

		forwardRules, consumeRules, intermediaryRules := generateRules(t, route, reservedIDs)

		forwardRoute, reverseRoute := generateForwardAndReverseRoutes(route)

		wantEdgeRules := routing.EdgeRules{
			Desc:    reverseRoute.Desc,
			Forward: forwardRules[route.Desc.SrcPK()],
			Reverse: consumeRules[route.Desc.SrcPK()],
		}

		testLogger := logging.MustGetLogger("setupclient_test")
		pks := []cipher.PubKey{clients[0].Addr.PK}
		gotEdgeRules, err := setupclient.NewSetupNodeDialer().Dial(ctx, testLogger, nEnv.Nets[1], pks, route)
		require.NoError(t, err)
		require.Equal(t, wantEdgeRules, gotEdgeRules)

		for pk, rules := range intermediaryRules {
			for _, cl := range clients {
				if cl.Addr.PK == pk {
					require.Equal(t, cl.AppliedIntermediaryRules, rules)
					break
				}
			}
		}

		respRouteRules := routing.EdgeRules{
			Desc:    forwardRoute.Desc,
			Forward: forwardRules[route.Desc.DstPK()],
			Reverse: consumeRules[route.Desc.DstPK()],
		}

		require.Equal(t, respRouteRules, clients[4].AppliedEdgeRules)
	})
}
