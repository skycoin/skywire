//go:build !tinygo || (js && wasm)

// Package router pkg/router/transport_query_dmsg.go c2-net-routing
//
// dmsg-direct (phase-1) delivery for the RSN-oracle transport-list query.
//
// A source visor S carries an RSN-signed TransportQuery to destination D over
// the always-available dmsg relay — the same way `tp --remote` / the setup
// client reach a remote visor's RPC over dmsg (see setupclient.go). D listens on
// skyenv.DmsgTransportQueryPort, verifies the RSN signature against its
// trusted-RSN allowlist, and returns its own transport list.
//
// Both halves are served over gob RPC to match the existing setup-node path
// (net/rpc server, gobrpc client — wire-compatible), so no new codec is
// introduced. Everything here is inert unless the visor wires it (only started
// when Routing.EnableRSNOracleRoutes is set — default OFF).
package router

import (
	"context"
	"errors"
	"net/rpc"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	gobrpc "github.com/skycoin/skywire/pkg/gobrpc"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/transport"
)

// transportQueryRPCName is the net/rpc service name the query gateway registers
// under; the deliverer calls transportQueryRPCName+".Query".
const transportQueryRPCName = "TransportQueryRPCGateway"

// TransportQueryRPCGateway is the DESTINATION-side (D) RPC gateway that answers
// RSN-oracle transport-list queries. It verifies the RSN signature against the
// visor's trusted-RSN allowlist (via TrustedRSNs) and returns D's own transports
// in compact form.
type TransportQueryRPCGateway struct {
	LocalPK cipher.PubKey
	// TrustedRSNs returns the current trusted-RSN public keys (typically
	// conf.EffectiveRouteSetupNodes) so allowlist edits at runtime are honored.
	TrustedRSNs func() []cipher.PubKey
	TM          *transport.Manager
	Log         *logging.Logger
}

// Query is the net/rpc method D exposes. It verifies the signed query and
// returns D's transport list. Errors (untrusted RSN, bad signature, wrong
// target) propagate to the requester as RPC errors.
func (g *TransportQueryRPCGateway) Query(q *TransportQuery, reply *TransportQueryResponse) error {
	if q == nil {
		return errors.New("transport-query: nil query")
	}
	trusted := map[cipher.PubKey]struct{}{}
	if g.TrustedRSNs != nil {
		for _, pk := range g.TrustedRSNs() {
			trusted[pk] = struct{}{}
		}
	}
	resp, err := BuildTransportQueryResponse(q, g.LocalPK, trusted, g.TM)
	if err != nil {
		if g.Log != nil {
			g.Log.WithError(err).WithField("requester", q.RequesterPK).
				Warn("transport-query: rejected")
		}
		return err
	}
	*reply = *resp
	if g.Log != nil {
		g.Log.WithField("requester", q.RequesterPK).
			WithField("entries", len(resp.Entries)).
			Debug("transport-query: answered")
	}
	return nil
}

// ServeTransportQueryListener listens on skyenv.DmsgTransportQueryPort and serves
// the gateway to any visor that dials it. Blocks until ctx is canceled or the
// listener fails. The caller starts it in a goroutine (see the visor init
// wiring). Only invoked when the RSN-oracle path is enabled — a default visor
// opens no listener here.
func ServeTransportQueryListener(ctx context.Context, dmsgC *dmsg.Client, gw *TransportQueryRPCGateway, log *logging.Logger) error {
	if dmsgC == nil {
		return errors.New("transport-query: nil dmsg client")
	}
	lis, err := dmsgC.Listen(skyenv.DmsgTransportQueryPort)
	if err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		_ = lis.Close() //nolint:errcheck
	}()

	rpcS := rpc.NewServer()
	if err := rpcS.RegisterName(transportQueryRPCName, gw); err != nil {
		return err
	}

	if log != nil {
		log.WithField("dmsg_port", skyenv.DmsgTransportQueryPort).
			Info("RSN-oracle transport-query listener started")
	}
	for {
		conn, err := lis.AcceptStream()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, dmsg.ErrEntityClosed) {
				return nil
			}
			return err
		}
		go func() {
			defer conn.Close()                                     //nolint:errcheck
			_ = conn.SetDeadline(time.Now().Add(30 * time.Second)) //nolint:errcheck
			rpcS.ServeConn(conn)
		}()
	}
}

// dmsgTransportQueryDeliverer implements TransportQueryDeliverer by dialing the
// destination over dmsg and issuing the Query RPC.
type dmsgTransportQueryDeliverer struct {
	dmsgC *dmsg.Client
	log   *logging.Logger
}

// NewDmsgTransportQueryDeliverer builds a dmsg-direct query deliverer.
func NewDmsgTransportQueryDeliverer(dmsgC *dmsg.Client, log *logging.Logger) TransportQueryDeliverer {
	return &dmsgTransportQueryDeliverer{dmsgC: dmsgC, log: log}
}

func (d *dmsgTransportQueryDeliverer) DeliverTransportQuery(ctx context.Context, dst cipher.PubKey, q *TransportQuery) (*TransportQueryResponse, error) {
	if d.dmsgC == nil {
		return nil, errors.New("transport-query: nil dmsg client")
	}
	dialCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	conn, err := d.dmsgC.Dial(dialCtx, dmsg.Addr{PK: dst, Port: skyenv.DmsgTransportQueryPort})
	if err != nil {
		return nil, err
	}
	defer conn.Close() //nolint:errcheck

	rpcC := gobrpc.NewClient(conn)
	defer rpcC.Close() //nolint:errcheck

	var reply TransportQueryResponse
	if err := rpcCall(ctx, rpcC, transportQueryRPCName+".Query", q, &reply); err != nil {
		return nil, err
	}
	return &reply, nil
}

// dmsgRSNOracle is the composed DstTransportOracle used by the visor: per call it
// opens a fresh setup-node RPC connection (RSN connections are per-request, like
// the cascade path in wrappers.go), asks the RSN to sign a transport-query, and
// delivers it to the destination over dmsg.
type dmsgRSNOracle struct {
	dmsgC      *dmsg.Client
	setupNodes []cipher.PubKey
	deliverer  TransportQueryDeliverer
	log        *logging.Logger
}

// NewDmsgRSNOracle builds the production DstTransportOracle: RSN signs over dmsg,
// query delivered to the destination over dmsg. setupNodes is the trusted-RSN
// list (conf.EffectiveRouteSetupNodes). Returns nil when no setup nodes are
// configured, so the caller can leave the oracle unset (path stays inert).
func NewDmsgRSNOracle(dmsgC *dmsg.Client, setupNodes []cipher.PubKey, log *logging.Logger) DstTransportOracle {
	if dmsgC == nil || len(setupNodes) == 0 {
		return nil
	}
	return &dmsgRSNOracle{
		dmsgC:      dmsgC,
		setupNodes: setupNodes,
		deliverer:  NewDmsgTransportQueryDeliverer(dmsgC, log),
		log:        log,
	}
}

func (o *dmsgRSNOracle) DstTransports(ctx context.Context, src, dst cipher.PubKey) ([]*transport.Entry, error) {
	client, err := NewSetupClient(ctx, o.log, o.dmsgC, o.setupNodes)
	if err != nil {
		return nil, err
	}
	defer client.Close() //nolint:errcheck
	return fetchDstTransportsViaOracle(ctx, client.RPCClient(), o.deliverer, src, dst)
}
