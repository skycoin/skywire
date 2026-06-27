// Package router pkg/router/rpc_gateway.go
package router

import (
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
)

// RPCName is the RPC gateway object name (the net/rpc / gobrpc service name the
// route-setup client dials as "RPCGateway.<Method>"). It lives in this untagged
// file so both the native client (routerclient.go, !tinygo) and the TinyGo
// HandleFunc registration (router_setup_rpc_tinygo.go) can reference it.
const RPCName = "RPCGateway"

// RPCGateway is a RPC interface for router.
type RPCGateway struct {
	logger *logging.Logger
	router Router
}

// NewRPCGateway creates a new RPCGateway.
func NewRPCGateway(router Router, mLog *logging.MasterLogger) *RPCGateway {
	return &RPCGateway{
		logger: mLog.PackageLogger("router-gateway"),
		router: router,
	}
}

// AddEdgeRules adds edge rules.
func (r *RPCGateway) AddEdgeRules(rules routing.EdgeRules, ok *bool) error {
	if err := r.router.IntroduceRules(rules); err != nil {
		*ok = false

		r.logger.WithError(err).Warnf("Request completed with error.")

		return routing.Failure{Code: routing.FailureAddRules, Msg: err.Error()}
	}

	*ok = true

	return nil
}

// AddIntermediaryRules adds intermediary rules.
func (r *RPCGateway) AddIntermediaryRules(rules []routing.Rule, ok *bool) error {
	if err := r.router.SaveRoutingRules(rules...); err != nil {
		*ok = false

		r.logger.WithError(err).Warnf("Request completed with error.")

		return routing.Failure{Code: routing.FailureAddRules, Msg: err.Error()}
	}

	*ok = true

	return nil
}

// ReserveIDs reserves route IDs.
func (r *RPCGateway) ReserveIDs(n uint8, routeIDs *[]routing.RouteID) error {
	ids, err := r.router.ReserveKeys(int(n))
	if err != nil {
		r.logger.WithError(err).Warnf("Request completed with error.")
		return routing.Failure{Code: routing.FailureReserveRtIDs, Msg: err.Error()}
	}

	*routeIDs = ids

	return nil
}

// DelRules removes the routing rules identified by routeIDs from this router.
// The setup-node calls this to tear down rules it installed on intermediary /
// edge hops when a later step of route-group setup fails, so a partially built
// route does not orphan rules (each orphan pins its NextTransportID against
// transport teardown until the ~10-min keepalive GC reaps it). Router.DelRules
// logs per-id "rule not found" internally and cannot fail, so this is
// best-effort by contract and always reports ok.
func (r *RPCGateway) DelRules(routeIDs []routing.RouteID, ok *bool) error {
	r.router.DelRules(routeIDs)
	*ok = true
	return nil
}
