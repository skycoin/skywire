// api_routing.go contains routing rule and route group API methods.
package visor

import (
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/routing"
)

// RoutingRules implements API.
func (v *Visor) RoutingRules() ([]routing.Rule, error) {
	if v.router == nil {
		return nil, nil
	}
	return v.router.Rules(), nil
}

// RoutingRule implements API.
func (v *Visor) RoutingRule(key routing.RouteID) (routing.Rule, error) {
	return v.router.Rule(key)
}

// SaveRoutingRule implements API.
func (v *Visor) SaveRoutingRule(rule routing.Rule) error {
	return v.router.SaveRule(rule)
}

// RemoveRoutingRule implements API.
func (v *Visor) RemoveRoutingRule(key routing.RouteID) error {
	v.router.DelRules([]routing.RouteID{key})
	return nil
}

// RouteGroups implements API. Returns one entry per active route group on
// the visor, with the descriptor, hop path, and initiator flag.
//
// The previous implementation walked v.router.Rules() looking for Reverse
// rules and then dereferenced the matching forward rule via NextRouteID().
// Reverse (Consume) rules don't carry a NextRouteID, so the lookup always
// failed and this function returned an empty list. We now drive directly
// off the router's active route-group set (rgsNs) via ActiveRouteStatuses,
// which already holds the descriptor, hops, and initiator flag.
func (v *Visor) RouteGroups() (rgs []RouteGroupInfo, err error) {
	if v.router == nil {
		return nil, nil
	}

	defer func() {
		if r := recover(); r != nil {
			rgs = nil
			err = fmt.Errorf("recovered panic in RouteGroups: %v", r)
		}
	}()

	statuses := v.router.ActiveRouteStatuses()
	rgs = make([]RouteGroupInfo, 0, len(statuses))
	for _, s := range statuses {
		info := RouteGroupInfo{
			Desc: routing.RouteDescriptorFields{
				DstPK:   s.LocalPK,
				SrcPK:   s.RemotePK,
				DstPort: s.LocalPort,
				SrcPort: s.RemotePort,
			},
			Initiator: s.Initiator,
		}
		// Primary forward + reverse rules are the first entries in the
		// mux transport set (mux secondaries follow at index 1+).
		if len(s.Transports) > 0 {
			info.FwdRuleID = s.Transports[0].FwdRuleID
			info.ConsumeRuleID = s.Transports[0].RvsRuleID
			info.FwdNextTpID = s.Transports[0].ID.String()
		}
		if len(s.Hops) > 0 {
			hops := make([]RouteHopInfo, len(s.Hops))
			for i, h := range s.Hops {
				hops[i] = RouteHopInfo{TpID: h.TpID, From: h.From, To: h.To, TpType: h.TpType}
			}
			info.Hops = hops
		}
		rgs = append(rgs, info)
	}

	return rgs, nil
}

// ActiveRoutes implements API.
// Returns all active routes with their app associations and live stats.
func (v *Visor) ActiveRoutes() ([]AppRouteStatus, error) {
	if v.router == nil {
		return nil, nil
	}

	statuses := v.router.ActiveRouteStatuses()

	// Build port → app name map.
	// Collect names first, then look up ports outside the Range callback
	// to avoid RWMutex deadlock (Range holds RLock, GetAppPort also needs RLock,
	// but a pending writer between them causes deadlock).
	portToApp := make(map[routing.Port]string)
	if v.procM != nil {
		var names []string
		v.procM.Range(func(name string, _ *appserver.Proc) bool {
			names = append(names, name)
			return true
		})
		for _, name := range names {
			if port, err := v.procM.GetAppPort(name); err == nil && port != 0 {
				portToApp[port] = name
			}
		}
	}

	result := make([]AppRouteStatus, 0, len(statuses))
	for _, s := range statuses {
		appName := portToApp[s.LocalPort]
		if appName == "" {
			appName = fmt.Sprintf("port:%d", s.LocalPort)
		}
		result = append(result, AppRouteStatus{
			AppName: appName,
			Route:   s,
		})
	}

	return result, nil
}

// findRouteDescForApp finds the route descriptor for a running app by matching its port.
func (v *Visor) findRouteDescForApp(appName string) (routing.RouteDescriptor, error) {
	var desc routing.RouteDescriptor

	if v.procM == nil {
		return desc, errors.New("process manager not available")
	}
	port, err := v.procM.GetAppPort(appName)
	if err != nil {
		return desc, fmt.Errorf("app %q not found or not running: %w", appName, err)
	}

	// Find the route group whose local port matches the app's port
	statuses := v.router.ActiveRouteStatuses()
	for _, s := range statuses {
		if s.LocalPort == port {
			return routing.NewRouteDescriptor(s.RemotePK, s.LocalPK, s.RemotePort, s.LocalPort), nil
		}
	}

	return desc, fmt.Errorf("no active route found for app %q on port %d", appName, port)
}

// AddMuxRoute implements API.
// Adds a new mux route to the specified app's active route group.
func (v *Visor) AddMuxRoute(appName string, tpID uuid.UUID) error {
	if v.router == nil {
		return errors.New("router not available")
	}

	desc, err := v.findRouteDescForApp(appName)
	if err != nil {
		return err
	}

	return v.router.AddMuxRouteByTransport(desc, tpID)
}

// RemoveMuxRoute implements API.
// Removes a mux route using the specified transport from the app's route group.
func (v *Visor) RemoveMuxRoute(appName string, tpID uuid.UUID) error {
	if v.router == nil {
		return errors.New("router not available")
	}

	desc, err := v.findRouteDescForApp(appName)
	if err != nil {
		return err
	}

	return v.router.RemoveMuxRouteByTransport(desc, tpID)
}

// SetMinHops sets min_hops routing config of visor
func (v *Visor) SetMinHops(in uint16) error {
	v.router.SetMinHop(in)
	return v.conf.UpdateMinHops(in)
}

// GetMinHops returns the visor's configured routing.min_hops value.
func (v *Visor) GetMinHops() (uint16, error) {
	if v.conf == nil || v.conf.Routing == nil {
		return 0, nil
	}
	return v.conf.Routing.MinHops, nil
}

// SetCalculateRoutes sets calculate_routes routing config of visor
func (v *Visor) SetCalculateRoutes(enabled bool) error {
	// Update router's local route calculation setting
	v.router.SetForceLocalRoutes(enabled)
	return v.conf.UpdateCalculateRoutes(enabled)
}

// GetCalculateRoutes gets calculate_routes routing config of visor
func (v *Visor) GetCalculateRoutes() (bool, error) {
	return v.conf.GetCalculateRoutes(), nil
}
