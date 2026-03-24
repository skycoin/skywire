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

// RouteGroups implements API.
func (v *Visor) RouteGroups() (rgs []RouteGroupInfo, err error) {
	if v.router == nil {
		return nil, nil
	}

	// Protect against panics from corrupt/race-condition rules
	defer func() {
		if r := recover(); r != nil {
			rgs = nil
			err = fmt.Errorf("recovered panic in RouteGroups: %v", r)
		}
	}()

	rules := v.router.Rules()
	for _, consumeRule := range rules {
		func() {
			defer func() { recover() }() //nolint:errcheck

			if len(consumeRule) == 0 || consumeRule.Type() != routing.RuleReverse {
				return
			}

			consumeSummary := consumeRule.Summary()
			if consumeSummary == nil || consumeSummary.ConsumeFields == nil {
				return
			}

			fwdRID := consumeRule.NextRouteID()
			fwdRule, err := v.router.Rule(fwdRID)
			if err != nil || fwdRule == nil || len(fwdRule) == 0 {
				return
			}

			fwdSummary := fwdRule.Summary()
			if fwdSummary == nil {
				return
			}

			info := RouteGroupInfo{
				ConsumeRuleID: consumeSummary.KeyRouteID,
				FwdRuleID:     fwdSummary.KeyRouteID,
				Desc:          consumeSummary.ConsumeFields.RouteDescriptor,
			}
			if fwdSummary.ForwardFields != nil {
				info.FwdNextTpID = fwdSummary.ForwardFields.NextTID.String()
			}

			rgs = append(rgs, info)
		}()
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

// SetSyncTPDData sets sync_tpd_data transport config of visor
func (v *Visor) SetSyncTPDData(enabled bool) error {
	// Update transport manager's TPD sync setting
	v.tpM.SetSyncTPDData(enabled)
	return v.conf.UpdateSyncTPDData(enabled)
}

// GetSyncTPDData gets sync_tpd_data transport config of visor
func (v *Visor) GetSyncTPDData() (bool, error) {
	return v.conf.GetSyncTPDData(), nil
}
