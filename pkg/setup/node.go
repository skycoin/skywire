// Package setup pkg/setup/node.go
package setup

import (
	"context"
	"fmt"
	"net/http"
	"net/rpc"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/skycoin/dmsg/pkg/disc"
	"github.com/skycoin/dmsg/pkg/dmsg"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/router/routerclient"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/setup/setupmetrics"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/transport/network"
)

var log = logging.MustGetLogger("setup_node")

// Node performs routes setup operations over messaging channel.
type Node struct {
	dmsgC *dmsg.Client
}

// NewNode constructs a new SetupNode.
func NewNode(conf *Config) (*Node, error) {
	if lvl, err := logging.LevelFromString(conf.LogLevel); err == nil {
		logging.SetLevel(lvl)
	}
	masterLogger := logging.NewMasterLogger()
	packageLogger := masterLogger.PackageLogger("node:disc")
	// Connect to dmsg network.
	dmsgDisc := disc.NewHTTP(conf.Dmsg.Discovery, &http.Client{}, packageLogger)
	dmsgConf := &dmsg.Config{MinSessions: conf.Dmsg.SessionsCount}
	dmsgC := dmsg.NewClient(conf.PK, conf.SK, dmsgDisc, dmsgConf)
	go dmsgC.Serve(context.Background())

	log.WithField("local_pk", conf.PK).WithField("dmsg_conf", conf.Dmsg).
		Info("Connecting to the dmsg network.")
	<-dmsgC.Ready()
	log.Info("Connected!")

	node := &Node{
		dmsgC: dmsgC,
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
func (sn *Node) Serve(ctx context.Context, m setupmetrics.Metrics) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	const dmsgPort = skyenv.DmsgSetupPort
	const timeout = 30 * time.Second

	log.WithField("dmsg_port", dmsgPort).Info("Starting listener.")
	lis, err := sn.dmsgC.Listen(skyenv.DmsgSetupPort)
	if err != nil {
		return fmt.Errorf("failed to listen on dmsg port %d: %v", skyenv.DmsgSetupPort, lis)
	}
	go func() {
		<-ctx.Done()
		if err := lis.Close(); err != nil {
			log.WithError(err).Warn("Dmsg listener closed with non-nil error.")
		}
	}()

	log.WithField("dmsg_port", dmsgPort).Info("Accepting dmsg streams.")
	for {
		conn, err := lis.AcceptStream()
		if err != nil {
			return err
		}
		gw := &RPCGateway{
			Metrics: m,
			Ctx:     ctx,
			Conn:    conn,
			ReqPK:   conn.RemoteAddr().(dmsg.Addr).PK,
			Dialer:  routerclient.WrapDmsgClient(sn.dmsgC),
			Timeout: timeout,
		}
		rpcS := rpc.NewServer()
		if err := rpcS.Register(gw); err != nil {
			return err
		}
		go rpcS.ServeConn(conn)
	}
}

// CreateRouteGroup creates a route group by communicating with routers used within the bidirectional route.
// The following steps are taken:
// * Check the validity of bi route input.
// * Route IDs are reserved from the routers.
// * Intermediary rules are broadcasted to the intermediary routers.
// * Edge rules are broadcasted to the responding router.
// * Edge rules is returned (to the initiating router).
func CreateRouteGroup(ctx context.Context, dialer network.Dialer, biRt routing.BidirectionalRoute, metrics setupmetrics.Metrics) (resp routing.EdgeRules, err error) {
	log := logging.MustGetLogger(fmt.Sprintf("request:%s->%s", biRt.Desc.SrcPK(), biRt.Desc.DstPK()))
	log.Info("Processing request.")
	defer metrics.RecordRoute()(&err)

	// Ensure bi routes input is valid.
	if err = biRt.Check(); err != nil {
		return routing.EdgeRules{}, err
	}

	// Reserve route IDs from remote routers.
	rtIDR, err := ReserveRouteIDs(ctx, log, dialer, biRt)
	if err != nil {
		return routing.EdgeRules{}, err
	}
	defer func() { log.WithError(rtIDR.Close()).Debug("Closing route id reserver.") }()

	// Generate forward and reverse routes.
	fwdRt, revRt := biRt.ForwardAndReverse()
	srcPK := biRt.Desc.Src()
	dstPK := biRt.Desc.Dst()

	// Generate routing rules (for edge and intermediary routers) that are to be sent.
	// Rules are grouped by rule type [FWD, REV, INTER].
	fwdRules, revRules, interRules, err := GenerateRules(rtIDR, []routing.Route{fwdRt, revRt})
	if err != nil {
		return routing.EdgeRules{}, err
	}
	initEdge := routing.EdgeRules{Desc: revRt.Desc, Forward: fwdRules[srcPK.String()][0], Reverse: revRules[srcPK.String()][0]}
	respEdge := routing.EdgeRules{Desc: fwdRt.Desc, Forward: fwdRules[dstPK.String()][0], Reverse: revRules[dstPK.String()][0]}

	log.Infof("Generated routing rules:\nInitiating edge: %v\nResponding edge: %v\nIntermediaries: %v",
		initEdge.String(), respEdge.String(), interRules.String())

	// Broadcast intermediary rules to intermediary routers.
	if err := BroadcastIntermediaryRules(ctx, log, rtIDR, interRules); err != nil {
		return routing.EdgeRules{}, err
	}

	// Broadcast rules to responding router.
	log.Debug("Broadcasting responding rules...")
	ok, err := rtIDR.Client(biRt.Desc.DstPK()).AddEdgeRules(ctx, respEdge)
	if err != nil || !ok {
		return routing.EdgeRules{}, fmt.Errorf("failed to broadcast rules to destination router: %v", err)
	}

	// Return rules to initiating router.
	return initEdge, nil
}

// ReserveRouteIDs dials to all routers and reserves required route IDs from them.
// The number of route IDs to be reserved per router, is extrapolated from the 'route' input.
func ReserveRouteIDs(ctx context.Context, log logrus.FieldLogger, dialer network.Dialer, route routing.BidirectionalRoute) (idR IDReserver, err error) {
	log.Debug("Reserving route IDs...")
	defer func() {
		if err != nil {
			log.WithError(err).Warn("Failed to reserve route IDs.")
		}
	}()

	idR, err = NewIDReserver(ctx, dialer, [][]routing.Hop{route.Forward, route.Reverse})
	if err != nil {
		return nil, fmt.Errorf("failed to instantiate route id reserver: %w", err)
	}
	defer func() {
		if err != nil {
			log.WithError(idR.Close()).Warn("Closing router clients due to error.")
		}
	}()

	if err = idR.ReserveIDs(ctx); err != nil {
		return idR, fmt.Errorf("failed to reserve route ids: %w", err)
	}
	return idR, nil
}

// GenerateRules generates rules for given forward and reverse routes.
// The outputs are as follows:
// - maps that relate slices of forward, consume and intermediary routing rules to a given visor's public key.
// - an error (if any).
func GenerateRules(idR IDReserver, routes []routing.Route) (fwdRules, revRules, interRules RulesMap, err error) {
	fwdRules = make(RulesMap)
	revRules = make(RulesMap)
	interRules = make(RulesMap)

	for _, route := range routes {
		// 'firstRID' is the first visor's key routeID
		firstRID, ok := idR.PopID(route.Hops[0].From)
		if !ok {
			return nil, nil, nil, ErrNoKey
		}

		desc := route.Desc
		srcAddr := desc.Src()
		dstAddr := desc.Dst()
		srcPort := desc.SrcPort()
		dstPort := desc.DstPort()

		var rID = firstRID

		for i, hop := range route.Hops {
			nxtRID, ok := idR.PopID(hop.To)
			if !ok {
				return nil, nil, nil, ErrNoKey
			}

			var port routing.Port
			if desc.DstPK() == hop.From {
				port = dstPort
			}
			if desc.SrcPK() == hop.From {
				port = srcPort
			}
			addr := routing.Addr{
				PubKey: hop.From,
				Port:   port,
			}
			if i == 0 {
				rule := routing.ForwardRule(route.KeepAlive, rID, nxtRID, hop.TpID, srcAddr.PubKey, dstAddr.PubKey, srcPort, dstPort)
				fwdRules[addr.String()] = append(fwdRules[addr.String()], rule)
			} else {
				rule := routing.IntermediaryForwardRule(route.KeepAlive, rID, nxtRID, hop.TpID)
				interRules[addr.String()] = append(interRules[addr.String()], rule)
			}

			rID = nxtRID
		}

		rule := routing.ConsumeRule(route.KeepAlive, rID, srcAddr.PubKey, dstAddr.PubKey, srcPort, dstPort)
		revRules[dstAddr.String()] = append(revRules[dstAddr.String()], rule)
	}

	return fwdRules, revRules, interRules, nil
}

// BroadcastIntermediaryRules broadcasts routing rules to the intermediary routers.
func BroadcastIntermediaryRules(ctx context.Context, log logrus.FieldLogger, rtIDR IDReserver, interRules RulesMap) (err error) {
	log.WithField("intermediary_routers", len(interRules)).Debug("Broadcasting intermediary rules...")
	defer func() {
		if err != nil {
			log.WithError(err).Warn("Failed to broadcast intermediary rules.")
		}
	}()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	errCh := make(chan error, len(interRules))
	defer close(errCh)

	for addr, rules := range interRules {
		var pk cipher.PubKey
		stringPK := strings.Split(addr, ":")
		err = pk.Set(stringPK[0])
		if err != nil {
			return err
		}
		go func(pk cipher.PubKey, rules []routing.Rule) {
			_, err := rtIDR.Client(pk).AddIntermediaryRules(ctx, rules)
			if err != nil {
				cancel()
			}
			errCh <- err
		}(pk, rules)
	}

	return firstError(len(interRules), errCh)
}
