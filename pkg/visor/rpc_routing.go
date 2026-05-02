// Package visor pkg/visor/rpc.go
package visor

import (
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/util/rpcutil"
)

// RoutingRules obtains all routing rules of the RoutingTable.
func (r *RPC) RoutingRules(_ *struct{}, out *[]routing.Rule) (err error) {
	defer rpcutil.LogCall(r.log, "RoutingRules", nil)(out, &err)

	*out, err = r.visor.RoutingRules()
	return err
}

// RoutingRule obtains a routing rule of given RouteID.
func (r *RPC) RoutingRule(key *routing.RouteID, rule *routing.Rule) (err error) {
	defer rpcutil.LogCall(r.log, "RoutingRule", key)(rule, &err)

	*rule, err = r.visor.RoutingRule(*key)
	return err
}

// SaveRoutingRule saves a routing rule.
func (r *RPC) SaveRoutingRule(in *routing.Rule, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "SaveRoutingRule", in)(nil, &err)

	return r.visor.SaveRoutingRule(*in)
}

// RemoveRoutingRule removes a RoutingRule based on given RouteID key.
func (r *RPC) RemoveRoutingRule(key *routing.RouteID, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "RemoveRoutingRule", key)(nil, &err)

	return r.visor.RemoveRoutingRule(*key)
}

// RouteGroups retrieves routegroups via rules of the routing table.
func (r *RPC) RouteGroups(_ *struct{}, out *[]RouteGroupInfo) (err error) {
	defer rpcutil.LogCall(r.log, "RouteGroups", nil)(out, &err)

	rgs, err := r.visor.RouteGroups()
	*out = rgs

	return err
}

// RouteGroupMuxInfo retrieves per-mux-leg counters for active rg's
// tagged with the named app.
func (r *RPC) RouteGroupMuxInfo(in *string, out *[]MuxRouteGroupInfo) (err error) {
	defer rpcutil.LogCall(r.log, "RouteGroupMuxInfo", in)(out, &err)
	if in == nil {
		empty := ""
		in = &empty
	}
	infos, err := r.visor.RouteGroupMuxInfo(*in)
	*out = infos
	return err
}

// FetchServiceData proxies a GET request to a deployment service.
func (r *RPC) FetchServiceData(in *FetchServiceDataIn, out *[]byte) (err error) {
	defer rpcutil.LogCall(r.log, "FetchServiceData", in)(out, &err)
	data, err := r.visor.FetchServiceData(in.Service, in.Path)
	*out = data
	return err
}

// ServiceHealth checks all deployment services.
func (r *RPC) ServiceHealth(_ *struct{}, out *[]ServiceHealthEntry) (err error) {
	defer rpcutil.LogCall(r.log, "ServiceHealth", nil)(out, &err)
	entries, err := r.visor.ServiceHealth()
	*out = entries
	return err
}

// SetMinHops sets min_hops in visor's routing config
func (r *RPC) SetMinHops(n *uint16, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "SetMinHops", *n)
	err = r.visor.SetMinHops(*n)
	return
}

// GetMinHops returns the visor's configured routing.min_hops value.
func (r *RPC) GetMinHops(_ *struct{}, out *uint16) (err error) {
	defer rpcutil.LogCall(r.log, "GetMinHops", nil)(out, &err)
	n, err := r.visor.GetMinHops()
	if err != nil {
		return err
	}
	*out = n
	return nil
}

// SetCalculateRoutes sets calculate_routes in visor's routing config
func (r *RPC) SetCalculateRoutes(enabled *bool, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "SetCalculateRoutes", *enabled)(nil, &err)
	err = r.visor.SetCalculateRoutes(*enabled)
	return
}

// GetCalculateRoutes gets calculate_routes from visor's routing config
func (r *RPC) GetCalculateRoutes(_ *struct{}, out *bool) (err error) {
	defer rpcutil.LogCall(r.log, "GetCalculateRoutes", nil)(out, &err)
	*out, err = r.visor.GetCalculateRoutes()
	return
}

// SetForceLocalRoutes sets whether to skip the route finder and use local route calculation
func (r *RPC) SetForceLocalRoutes(enabled *bool, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "SetForceLocalRoutes", *enabled)(nil, &err)
	err = r.visor.SetForceLocalRoutes(*enabled)
	return err
}

// SetMuxRoutes sets the number of parallel mux routes for new connections
func (r *RPC) SetMuxRoutes(n *int, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "SetMuxRoutes", *n)(nil, &err)
	err = r.visor.SetMuxRoutes(*n)
	return err
}

// SetMuxMode sets the weight distribution mode for mux transport selection
func (r *RPC) SetMuxMode(mode *string, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "SetMuxMode", *mode)(nil, &err)
	err = r.visor.SetMuxMode(*mode)
	return err
}

// ActiveRoutes returns all active routes with app associations and live stats
func (r *RPC) ActiveRoutes(_ *struct{}, out *[]AppRouteStatus) (err error) {
	defer rpcutil.LogCall(r.log, "ActiveRoutes", nil)(out, &err)
	routes, err := r.visor.ActiveRoutes()
	if routes != nil {
		*out = routes
	}
	return err
}

// AddMuxRoute adds a mux route to an app's active connection
func (r *RPC) AddMuxRoute(in *MuxRouteInput, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "AddMuxRoute", in)(nil, &err)
	return r.visor.AddMuxRoute(in.AppName, in.SrcPort)
}

// RemoveMuxRoute removes a mux route from an app's active connection
func (r *RPC) RemoveMuxRoute(in *MuxRouteInput, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "RemoveMuxRoute", in)(nil, &err)
	return r.visor.RemoveMuxRoute(in.AppName, in.TransportID, in.SrcPort)
}
