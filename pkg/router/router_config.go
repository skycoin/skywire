// router_config.go contains setters, config methods, and Close.
package router

import (
	"time"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

// SetupIsTrusted checks if setup node is trusted.
func (r *router) SetupIsTrusted(sPK cipher.PubKey) bool {
	_, ok := r.trustedVisors[sPK]
	return ok
}

// SetMinHop set minhop when visor running
func (r *router) SetMinHop(minhop uint16) {
	r.conf.MinHops = minhop
}

// SetExistingTPOnly sets whether to only use existing transports for routing.
// When true, no new transports will be created when dialing routes.
func (r *router) SetExistingTPOnly(enabled bool) {
	r.existingTpOnlyMu.Lock()
	defer r.existingTpOnlyMu.Unlock()
	r.existingTpOnly = enabled
	r.logger.Infof("SetExistingTPOnly: %v", enabled)
}

// SetForceLocalRoutes sets whether to skip the route finder and use local route calculation.
// When true, routes are calculated locally using transport manager and TPD data.
func (r *router) SetForceLocalRoutes(enabled bool) {
	r.forceLocalRoutesMu.Lock()
	defer r.forceLocalRoutesMu.Unlock()
	r.forceLocalRoutes = enabled
	r.logger.Infof("SetForceLocalRoutes: %v", enabled)
}

// SetMuxRoutes sets the number of parallel mux routes for new connections.
func (r *router) SetMuxRoutes(n int) {
	r.muxRoutes = n
	r.logger.Infof("SetMuxRoutes: %v", n)
}

// SetMuxMode sets the weight distribution mode for mux transport selection.
// Propagates to all active route groups with mux enabled.
func (r *router) SetMuxMode(mode WeightMode) {
	r.muxMode = mode
	// Propagate to active route groups
	r.mx.Lock()
	for _, nrg := range r.rgsNs {
		rg := nrg.rg
		if rg.mux != nil && rg.mux.tpSelector != nil {
			rg.mux.tpSelector.SetMode(mode)
			rg.mux.tpSelector.Rebuild(rg.tps)
		}
	}
	r.mx.Unlock()
	r.logger.Infof("SetMuxMode: %v", mode)
}

// GetLastRouteCalcTime returns the time it took to calculate the last local route.
func (r *router) GetLastRouteCalcTime() time.Duration {
	r.lastRouteCalcMu.Lock()
	defer r.lastRouteCalcMu.Unlock()
	return r.lastRouteCalcTime
}

// Close safely stops Router.
func (r *router) Close() error {
	if r == nil {
		return nil
	}

	r.logger.Debug("Closing all App connections and RouteGroups")
	r.once.Do(func() {
		close(r.done)
		r.mx.Lock()
		close(r.accept)
		r.mx.Unlock()
	})
	if err := r.sl.Close(); err != nil {
		r.logger.WithError(err).Warnf("closing route_manager returned error")
		return err
	}

	return nil
}

func (r *router) isTpdExist(rPK cipher.PubKey) bool {
	// check stcpr transport if exist
	_, err := r.tm.GetTransport(rPK, types.STCPR)
	if err == nil {
		return true
	}
	// check sudph transport if exist
	_, err = r.tm.GetTransport(rPK, types.SUDPH)
	if err == nil {
		return true
	}
	// check dmsg transport if exist
	_, err = r.tm.GetTransport(rPK, types.DMSG)
	return err == nil
}
