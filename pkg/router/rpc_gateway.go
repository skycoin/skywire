package router

import (
	"github.com/SkycoinProject/skycoin/src/util/logging"

	"github.com/SkycoinProject/skywire-mainnet/pkg/routing"
)

type RPCGateway struct {
	logger *logging.Logger
	router Router
}

func NewRPCGateway(router Router) *RPCGateway {
	return &RPCGateway{
		logger: logging.MustGetLogger("router-gateway"),
		router: router,
	}
}

func (r *RPCGateway) AddEdgeRules(rules routing.EdgeRules, ok *bool) error {
	if err := r.router.IntroduceRules(rules); err != nil {
		*ok = false

		r.logger.WithError(err).Warnf("Request completed with error.")

		return routing.Failure{Code: routing.FailureAddRules, Msg: err.Error()}
	}

	*ok = true

	return nil
}

func (r *RPCGateway) AddIntermediaryRules(rules []routing.Rule, ok *bool) error {
	if err := r.router.SaveRoutingRules(rules...); err != nil {
		*ok = false

		r.logger.WithError(err).Warnf("Request completed with error.")

		return routing.Failure{Code: routing.FailureAddRules, Msg: err.Error()}
	}

	*ok = true

	return nil
}

func (r *RPCGateway) ReserveIDs(n uint8, routeIDs *[]routing.RouteID) error {
	ids, err := r.router.ReserveKeys(int(n))
	if err != nil {
		r.logger.WithError(err).Warnf("Request completed with error.")
		return routing.Failure{Code: routing.FailureReserveRtIDs, Msg: err.Error()}
	}

	*routeIDs = ids

	return nil
}
