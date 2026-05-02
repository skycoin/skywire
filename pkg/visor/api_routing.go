// api_routing.go contains routing rule and route group API methods.
package visor

import (
	"errors"
	"fmt"
	"strings"

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

// RouteGroupMuxInfo implements API. Returns per-mux-leg byte/packet/
// latency counters for every active route group tagged with the
// named app. The visor's router holds the rg's; we just transcribe
// the internal MuxInfo into the wire-friendly visor-level shape.
func (v *Visor) RouteGroupMuxInfo(appName string) ([]MuxRouteGroupInfo, error) {
	if v.router == nil {
		return nil, nil
	}
	infos := v.router.RouteGroupMuxInfoForApp(appName)
	out := make([]MuxRouteGroupInfo, 0, len(infos))
	for _, info := range infos {
		entry := MuxRouteGroupInfo{
			Desc: routing.RouteDescriptorFields{
				DstPK:   info.Desc.DstPK(),
				SrcPK:   info.Desc.SrcPK(),
				DstPort: info.Desc.DstPort(),
				SrcPort: info.Desc.SrcPort(),
			},
			MuxEnabled:  info.MuxEnabled,
			SACKEnabled: info.SACKEnabled,
			Legs:        make([]MuxLegInfo, 0, len(info.Legs)),
		}
		for _, leg := range info.Legs {
			entry.Legs = append(entry.Legs, MuxLegInfo{
				Index:       leg.Index,
				TransportID: leg.TransportID,
				TpType:      leg.TpType,
				RemotePK:    leg.RemotePK,
				LatencyMS:   leg.LatencyMS,
				SentBytes:   leg.SentBytes,
				SentPackets: leg.SentPackets,
				RecvBytes:   leg.RecvBytes,
				RecvPackets: leg.RecvPackets,
			})
		}
		out = append(out, entry)
	}
	return out, nil
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

// findRouteDescForApp resolves an active rg's descriptor for the
// named app. Uses the rg's stored AppName tag (set via SetAppName
// at dial time from opts.AppName) rather than registered-app-port
// matching — the rg's descriptor uses an ephemeral source port that
// the procManager.GetAppPort lookup can't see.
//
// srcPort disambiguates when multiple rg's are active for the same
// app (one per concurrent SOCKS5 connection on skysocks-client, etc.):
//
//   - srcPort == 0 and exactly 1 rg matches → that rg.
//   - srcPort == 0 and >1 rg matches → error listing all candidates,
//     so the caller can pick.
//   - srcPort != 0 → match the rg whose Desc.SrcPort equals srcPort.
func (v *Visor) findRouteDescForApp(appName string, srcPort uint16) (routing.RouteDescriptor, error) {
	var desc routing.RouteDescriptor
	if v.router == nil {
		return desc, errors.New("router not available")
	}
	infos := v.router.RouteGroupMuxInfoForApp(appName)
	if len(infos) == 0 {
		return desc, fmt.Errorf("no active route group for app %q", appName)
	}
	if srcPort != 0 {
		for _, info := range infos {
			if uint16(info.Desc.SrcPort()) == srcPort { //nolint:gosec
				return info.Desc, nil
			}
		}
		return desc, fmt.Errorf("no rg for app %q with src_port=%d", appName, srcPort)
	}
	if len(infos) == 1 {
		return infos[0].Desc, nil
	}
	// Ambiguous. Surface every candidate so the caller can pass --rg.
	var b strings.Builder
	fmt.Fprintf(&b, "app %q has %d active rg's; pass --rg <src_port>:\n", appName, len(infos))
	for _, info := range infos {
		fmt.Fprintf(&b, "  src_port=%d  remote=%s  port=%d\n",
			info.Desc.SrcPort(), info.Desc.DstPK(), info.Desc.DstPort())
	}
	return desc, errors.New(strings.TrimRight(b.String(), "\n"))
}

// AddMuxRoute implements API. Adds a leg over the named transport
// to the app's active rg; srcPort disambiguates when the app has
// multiple concurrent rg's (use 0 to auto-pick when there's exactly
// one). Caller picks the transport explicitly — auto-disjoint
// selection is deferred until the route finder honors
// ExcludeTransportIDs in the multi-hop branch.
func (v *Visor) AddMuxRoute(appName string, tpID uuid.UUID, srcPort uint16) error {
	if v.router == nil {
		return errors.New("router not available")
	}
	desc, err := v.findRouteDescForApp(appName, srcPort)
	if err != nil {
		return err
	}
	return v.router.AddMuxRouteByTransport(desc, tpID)
}

// RemoveMuxRoute implements API. Drops the leg over the given
// transport from the app's active rg; srcPort disambiguates as in
// AddMuxRoute.
func (v *Visor) RemoveMuxRoute(appName string, tpID uuid.UUID, srcPort uint16) error {
	if v.router == nil {
		return errors.New("router not available")
	}
	desc, err := v.findRouteDescForApp(appName, srcPort)
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
