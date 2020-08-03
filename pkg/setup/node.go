package setup

import (
	"context"
	"fmt"
	"net/rpc"
	"sync"
	"time"

	"github.com/SkycoinProject/dmsg"
	"github.com/SkycoinProject/dmsg/disc"
	"github.com/SkycoinProject/skycoin/src/util/logging"

	"github.com/skycoin/skywire/pkg/metrics"
	"github.com/skycoin/skywire/pkg/router/routerclient"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
)

// Node performs routes setup operations over messaging channel.
type Node struct {
	logger        *logging.Logger
	dmsgC         *dmsg.Client
	dmsgL         *dmsg.Listener
	sessionsCount int
	metrics       metrics.Recorder
}

// NewNode constructs a new SetupNode.
func NewNode(conf *Config, metrics metrics.Recorder) (*Node, error) {
	logger := logging.NewMasterLogger()

	if lvl, err := logging.LevelFromString(conf.LogLevel); err == nil {
		logger.SetLevel(lvl)
	}

	log := logger.PackageLogger("setup_node")

	// Prepare dmsg.
	dmsgC := dmsg.NewClient(
		conf.PubKey,
		conf.SecKey,
		disc.NewHTTP(conf.Dmsg.Discovery),
		&dmsg.Config{MinSessions: conf.Dmsg.SessionsCount},
	)
	dmsgC.SetLogger(logger.PackageLogger(dmsg.Type))

	go dmsgC.Serve()

	log.Info("connected to dmsg servers")

	dmsgL, err := dmsgC.Listen(skyenv.DmsgSetupPort)
	if err != nil {
		return nil, fmt.Errorf("failed to listen on dmsg port %d: %v", skyenv.DmsgSetupPort, dmsgL)
	}

	log.Info("started listening for dmsg connections")

	node := &Node{
		logger:        log,
		dmsgC:         dmsgC,
		dmsgL:         dmsgL,
		sessionsCount: conf.Dmsg.SessionsCount,
		metrics:       metrics,
	}

	return node, nil
}

// Close closes underlying dmsg client.
func (sn *Node) Close() error {
	if sn == nil {
		return nil
	}

	return sn.dmsgC.Close()
}

// Serve starts transport listening loop.
func (sn *Node) Serve() error {
	sn.logger.Info("Serving setup node")

	for {
		conn, err := sn.dmsgL.AcceptStream()
		if err != nil {
			return err
		}

		remote := conn.RemoteAddr().(dmsg.Addr)
		sn.logger.WithField("requester", remote.PK).Infof("Received request.")

		const timeout = 30 * time.Second

		rpcS := rpc.NewServer()
		if err := rpcS.Register(NewRPCGateway(remote.PK, sn, timeout)); err != nil {
			return err
		}

		go rpcS.ServeConn(conn)
	}
}

func (sn *Node) handleDialRouteGroup(ctx context.Context, route routing.BidirectionalRoute) (routing.EdgeRules, error) {
	sn.logger.Infof("Setup route from %s to %s", route.Desc.SrcPK(), route.Desc.DstPK())

	idr, err := sn.reserveRouteIDs(ctx, route)
	if err != nil {
		return routing.EdgeRules{}, err
	}

	forwardRoute, reverseRoute := route.ForwardAndReverse()

	// Determine the rules to send to visors using route group descriptor and reserved route IDs.
	forwardRules, consumeRules, intermediaryRules, err := idr.GenerateRules(forwardRoute, reverseRoute)

	if err != nil {
		sn.logger.WithError(err).Error("ERROR GENERATING RULES")
		return routing.EdgeRules{}, err
	}

	sn.logger.Infof("generated forward rules: %v", forwardRules)
	sn.logger.Infof("generated consume rules: %v", consumeRules)
	sn.logger.Infof("generated intermediary rules: %v", intermediaryRules)

	if err := sn.addIntermediaryRules(ctx, intermediaryRules); err != nil {
		return routing.EdgeRules{}, err
	}

	initRouteRules := routing.EdgeRules{
		Desc:    reverseRoute.Desc,
		Forward: forwardRules[route.Desc.SrcPK()],
		Reverse: consumeRules[route.Desc.SrcPK()],
	}

	respRouteRules := routing.EdgeRules{
		Desc:    forwardRoute.Desc,
		Forward: forwardRules[route.Desc.DstPK()],
		Reverse: consumeRules[route.Desc.DstPK()],
	}

	sn.logger.Infof("initRouteRules: Desc(%s), %s", &initRouteRules.Desc, initRouteRules)
	sn.logger.Infof("respRouteRules: Desc(%s), %s", &respRouteRules.Desc, respRouteRules)

	// Confirm routes with responding visor.
	ok, err := routerclient.AddEdgeRules(ctx, sn.logger, sn.dmsgC, route.Desc.DstPK(), respRouteRules)
	if err != nil || !ok {
		return routing.EdgeRules{}, fmt.Errorf("failed to confirm route group with destination visor: %v", err)
	}

	sn.logger.Infof("Returning route rules to initiating visor: %v", initRouteRules)

	return initRouteRules, nil
}

func (sn *Node) addIntermediaryRules(ctx context.Context, intermediaryRules RulesMap) error {
	errCh := make(chan error, len(intermediaryRules))

	var wg sync.WaitGroup

	for pk, rules := range intermediaryRules {
		pk, rules := pk, rules

		sn.logger.WithField("remote", pk).Info("Adding rules to intermediary visor")

		wg.Add(1)

		go func() {
			defer wg.Done()
			if _, err := routerclient.AddIntermediaryRules(ctx, sn.logger, sn.dmsgC, pk, rules); err != nil {
				sn.logger.WithField("remote", pk).WithError(err).Warn("failed to add rules")
				errCh <- err
			}
		}()
	}

	wg.Wait()
	close(errCh)

	return finalError(len(intermediaryRules), errCh)
}

func (sn *Node) reserveRouteIDs(ctx context.Context, route routing.BidirectionalRoute) (*idReservoir, error) {
	reservoir, total := newIDReservoir(route.Forward, route.Reverse)
	sn.logger.Infof("There are %d route IDs to reserve.", total)

	err := reservoir.ReserveIDs(ctx, sn.logger, sn.dmsgC, routerclient.ReserveIDs)

	if err != nil {
		sn.logger.WithError(err).Warnf("Failed to reserve route IDs.")
		return nil, err
	}

	sn.logger.Infof("Successfully reserved route IDs.")

	return reservoir, err
}
