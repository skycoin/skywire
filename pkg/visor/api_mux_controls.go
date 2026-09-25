// Package visor pkg/visor/api_mux_controls.go c3-vis-core
//
// The visor side of the DIAL-TIME and PER-LEG routing controls: the dial
// knob set (`skywire cli route settings dial`), the per-leg send-weight
// override and forward-only leg pin (`skywire cli proxy mux weights` /
// `proxy mux add --forward-only`), and the negotiated per-group view
// (`proxy mux negotiated`).
//
// Everything here is one file — the API method, the RPC server method, the RPC
// client method and the two non-visor API stubs for each control — because the
// set is one feature and reading it in one place is worth more than filing each
// half next to its neighbors of the same kind.
//
// The per-leg controls reach the router through router.MuxLegController, an
// OPTIONAL capability interface, rather than through the Router interface: a
// source-only (tinygo) router has no route groups to control, and asserting
// keeps that build compiling without stubs.
package visor

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/visor/visorapi"

	"github.com/skycoin/skywire/pkg/router"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/util/rpcutil"
)

// GetRouterDialSettings implements API. Reads the live dial-time knobs.
func (v *Visor) GetRouterDialSettings() (visorapi.RouterDialSettings, error) {
	pks := router.DialPreferPKs()
	out := visorapi.RouterDialSettings{
		UnknownLatencyCostMs: router.DialUnknownLatencyCostMs(),
		UnknownHopPenaltyMs:  router.DialUnknownHopPenaltyMs(),
		TypePriorScale:       router.DialTypePriorScale(),
		ThroughputPriorScale: router.DialThroughputPriorScale(),
		RouteCandidates:      router.DialRouteCandidates(),
		MuxRouteHeadroom:     router.DialMuxRouteHeadroom(),
		ForegroundMux:        router.DialForegroundMux(),
		TunnelLegs:           router.DialTunnelLegs(),
		DeadRouteYoungAgeMs:  router.DeadRouteYoungAge().Milliseconds(),
		WarmPlanTTLMs:        router.WarmPlanTTL().Milliseconds(),
		WarmPlanBucketCap:    router.WarmPlanBucketCap(),
	}
	for _, pk := range pks {
		out.PreferPKs = append(out.PreferPKs, pk.String())
	}
	return out, nil
}

// SetRouterDialSettings implements API. Applies every field the caller set;
// a zero field is left alone (see RouterDialSettings). A refused value — the
// accessors validate — is reported rather than silently dropped, and the
// settings applied before it stay applied: the caller asked for each one
// independently.
func (v *Visor) SetRouterDialSettings(s visorapi.RouterDialSettings) error {
	var bad []string
	set := func(ok bool, name string) {
		if !ok {
			bad = append(bad, name)
		}
	}
	if s.UnknownLatencyCostMs > 0 {
		set(router.SetDialUnknownLatencyCostMs(s.UnknownLatencyCostMs), "unknown-latency-cost-ms")
	}
	if s.UnknownHopPenaltyMs > 0 {
		set(router.SetDialUnknownHopPenaltyMs(s.UnknownHopPenaltyMs), "unknown-hop-penalty-ms")
	}
	if s.TypePriorScaleSet {
		set(router.SetDialTypePriorScale(s.TypePriorScale), "dial-type-prior-scale")
	}
	if s.ThroughputPriorScaleSet {
		set(router.SetDialThroughputPriorScale(s.ThroughputPriorScale), "dial-throughput-prior-scale")
	}
	if s.RouteCandidates > 0 {
		set(router.SetDialRouteCandidates(s.RouteCandidates), "dial-candidates")
	}
	if s.MuxRouteHeadroom > 0 {
		set(router.SetDialMuxRouteHeadroom(s.MuxRouteHeadroom), "dial-candidate-headroom")
	}
	if s.ForegroundMux > 0 {
		set(router.SetDialForegroundMux(s.ForegroundMux), "dial-foreground-mux")
	}
	if s.TunnelLegsSet {
		set(router.SetDialTunnelLegs(s.TunnelLegs), "dial-tunnel-legs")
	}
	if s.DeadRouteYoungAgeMs > 0 {
		set(router.SetDeadRouteYoungAge(msDuration(s.DeadRouteYoungAgeMs)), "dead-route-young-age")
	}
	if s.WarmPlanTTLMs > 0 {
		set(router.SetWarmPlanTTL(msDuration(s.WarmPlanTTLMs)), "warm-plan-ttl")
	}
	if s.WarmPlanBucketCap > 0 {
		set(router.SetWarmPlanBucketCap(s.WarmPlanBucketCap), "warm-plan-bucket-cap")
	}
	if s.ClearPreferPKs {
		router.SetDialPreferPKs(nil)
	} else if len(s.PreferPKs) > 0 {
		pks, err := parsePubKeys(s.PreferPKs)
		if err != nil {
			return err
		}
		router.SetDialPreferPKs(pks)
	}
	if len(bad) > 0 {
		return fmt.Errorf("refused (out of range): %v", bad)
	}
	// Every scalar above is a catalog knob, so the same persistence map carries
	// it: `route settings dial` and `route settings k=v` write one file and one
	// restore path. The prefer-PK list is not a catalog entry (its payload is
	// not an int64) and stays runtime-only.
	if err := v.persistRouterKnobs(); err != nil {
		return err
	}
	v.log.Infof("SetRouterDialSettings: applied")
	return nil
}

// muxLegController returns the router's optional per-leg control capability.
func (v *Visor) muxLegController() (router.MuxLegController, error) {
	if v.router == nil {
		return nil, errors.New("router not available")
	}
	mc, ok := v.router.(router.MuxLegController)
	if !ok {
		return nil, errors.New("this router build does not support per-leg mux controls")
	}
	return mc, nil
}

// SetMuxWeights implements API. Pins the app's route group to operator-supplied
// per-leg send weights, keyed by the leg's first-hop transport id. rgPort
// disambiguates concurrent rg's exactly as AddMuxRoute does.
func (v *Visor) SetMuxWeights(appName string, weights map[string]float64, rgPort uint16) (router.MuxWeightsView, error) {
	mc, err := v.muxLegController()
	if err != nil {
		return router.MuxWeightsView{}, err
	}
	desc, err := v.findRouteDescForApp(appName, rgPort)
	if err != nil {
		return router.MuxWeightsView{}, err
	}
	parsed := make(map[uuid.UUID]float64, len(weights))
	for k, val := range weights {
		id, perr := uuid.Parse(k)
		if perr != nil {
			return router.MuxWeightsView{}, fmt.Errorf("invalid transport id %q: %w", k, perr)
		}
		parsed[id] = val
	}
	return mc.SetMuxExplicitWeights(desc, parsed)
}

// ClearMuxWeights implements API. Releases an explicit weight pin, returning
// the group to the ECF scheduler every mux is built with.
func (v *Visor) ClearMuxWeights(appName string, rgPort uint16) (router.MuxWeightsView, error) {
	mc, err := v.muxLegController()
	if err != nil {
		return router.MuxWeightsView{}, err
	}
	desc, err := v.findRouteDescForApp(appName, rgPort)
	if err != nil {
		return router.MuxWeightsView{}, err
	}
	return mc.ClearMuxExplicitWeights(desc)
}

// MuxWeights implements API. Reports the group's scheduler mode and, when it is
// the explicit one, the per-leg weights in force.
func (v *Visor) MuxWeights(appName string, rgPort uint16) (router.MuxWeightsView, error) {
	mc, err := v.muxLegController()
	if err != nil {
		return router.MuxWeightsView{}, err
	}
	desc, err := v.findRouteDescForApp(appName, rgPort)
	if err != nil {
		return router.MuxWeightsView{}, err
	}
	return mc.MuxWeightsFor(desc)
}

// AddMuxRouteForward implements API. AddMuxRoute with the reverse direction
// dropped: the leg adds UPSTREAM send capacity only.
func (v *Visor) AddMuxRouteForward(appName string, fwd, rev []routing.Hop, rgPort uint16) error {
	mc, err := v.muxLegController()
	if err != nil {
		return err
	}
	desc, err := v.findRouteDescForApp(appName, rgPort)
	if err != nil {
		return err
	}
	return mc.AddMuxRouteByHopsForward(desc, fwd, rev)
}

// RehomeTunnelLeg implements API. Moves the STANDBY tunnel's whole built chain
// into the ACTIVE tunnel as one more mux leg, in place, with no setup-node
// dial. Both tunnels belong to appName and must join the same peer.
//
// On success the app is told its standby tunnel was CONSUMED rather than lost,
// so it drops it from the pool without the redial backoff reset and pool fill a
// death triggers. The op lands on the app's next settings pull.
func (v *Visor) RehomeTunnelLeg(appName string, targetPort, standbyPort uint16) error {
	mc, err := v.muxLegController()
	if err != nil {
		return err
	}
	if targetPort == 0 || standbyPort == 0 {
		return errors.New("pass both --tunnel <active dst_port> and --from <standby dst_port>")
	}
	if targetPort == standbyPort {
		return errors.New("--tunnel and --from name the same tunnel")
	}
	target, err := v.findRouteDescForApp(appName, targetPort)
	if err != nil {
		return fmt.Errorf("target tunnel: %w", err)
	}
	standby, err := v.findRouteDescForApp(appName, standbyPort)
	if err != nil {
		return fmt.Errorf("standby tunnel: %w", err)
	}
	if err := mc.RehomeStandbyLeg(target, standby); err != nil {
		return err
	}
	if v.procM != nil {
		seq := v.procM.QueueAppOp(appName, appserver.AppOpConsumedTunnel, int64(standbyPort))
		v.log.Infof("RehomeTunnelLeg: :%d adopted the chain of :%d; queued the consumed-tunnel op for %s (op %d)",
			targetPort, standbyPort, appName, seq)
	}
	return nil
}

// RouteGroupMuxNegotiated implements API. Per-group negotiated capabilities and
// the send-window shape in effect.
func (v *Visor) RouteGroupMuxNegotiated(appName string) ([]router.MuxNegotiated, error) {
	mc, err := v.muxLegController()
	if err != nil {
		return nil, err
	}
	return mc.MuxNegotiatedForApp(appName), nil
}

// --- RPC server ---------------------------------------------------------

// GetRouterDialSettings returns the live dial-time router knobs.
func (r *RPC) GetRouterDialSettings(_ *struct{}, out *visorapi.RouterDialSettings) (err error) {
	defer rpcutil.LogCall(r.log, "GetRouterDialSettings", nil)(out, &err)
	*out, err = r.visor.GetRouterDialSettings()
	return err
}

// SetRouterDialSettings applies dial-time router knobs.
func (r *RPC) SetRouterDialSettings(s *visorapi.RouterDialSettings, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "SetRouterDialSettings", s)(nil, &err)
	return r.visor.SetRouterDialSettings(*s)
}

// SetMuxWeights pins per-leg send weights on an app's route group.
func (r *RPC) SetMuxWeights(in *visorapi.MuxWeightsInput, out *router.MuxWeightsView) (err error) {
	defer rpcutil.LogCall(r.log, "SetMuxWeights", in)(out, &err)
	*out, err = r.visor.SetMuxWeights(in.AppName, in.Weights, in.RGPort)
	return err
}

// ClearMuxWeights releases a per-leg weight pin.
func (r *RPC) ClearMuxWeights(in *visorapi.MuxWeightsInput, out *router.MuxWeightsView) (err error) {
	defer rpcutil.LogCall(r.log, "ClearMuxWeights", in)(out, &err)
	*out, err = r.visor.ClearMuxWeights(in.AppName, in.RGPort)
	return err
}

// MuxWeights reports the per-leg weights and scheduler mode in force.
func (r *RPC) MuxWeights(in *visorapi.MuxWeightsInput, out *router.MuxWeightsView) (err error) {
	defer rpcutil.LogCall(r.log, "MuxWeights", in)(out, &err)
	*out, err = r.visor.MuxWeights(in.AppName, in.RGPort)
	return err
}

// AddMuxRouteForward adds a forward-only leg over the caller-supplied route.
func (r *RPC) AddMuxRouteForward(in *visorapi.MuxRouteInput, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "AddMuxRouteForward", in)(nil, &err)
	return r.visor.AddMuxRouteForward(in.AppName, in.Forward, in.Reverse, in.SrcPort)
}

// RehomeTunnelLeg adopts a standby tunnel's chain into an active one as a leg.
func (r *RPC) RehomeTunnelLeg(in *visorapi.MuxRehomeInput, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "RehomeTunnelLeg", in)(nil, &err)
	return r.visor.RehomeTunnelLeg(in.AppName, in.TargetPort, in.StandbyPort)
}

// RouteGroupMuxNegotiated reports per-group negotiated values.
func (r *RPC) RouteGroupMuxNegotiated(in *string, out *[]router.MuxNegotiated) (err error) {
	defer rpcutil.LogCall(r.log, "RouteGroupMuxNegotiated", in)(out, &err)
	name := ""
	if in != nil {
		name = *in
	}
	*out, err = r.visor.RouteGroupMuxNegotiated(name)
	return err
}

// --- RPC client ---------------------------------------------------------

// --- stubs for the non-visor API implementations ------------------------

func (proxyDefaultAPI) GetRouterDialSettings() (visorapi.RouterDialSettings, error) {
	return visorapi.RouterDialSettings{}, ErrProxyNotSupported
}
func (proxyDefaultAPI) SetRouterDialSettings(visorapi.RouterDialSettings) error {
	return ErrProxyNotSupported
}
func (proxyDefaultAPI) SetMuxWeights(string, map[string]float64, uint16) (router.MuxWeightsView, error) {
	return router.MuxWeightsView{}, ErrProxyNotSupported
}
func (proxyDefaultAPI) ClearMuxWeights(string, uint16) (router.MuxWeightsView, error) {
	return router.MuxWeightsView{}, ErrProxyNotSupported
}
func (proxyDefaultAPI) MuxWeights(string, uint16) (router.MuxWeightsView, error) {
	return router.MuxWeightsView{}, ErrProxyNotSupported
}
func (proxyDefaultAPI) AddMuxRouteForward(string, []routing.Hop, []routing.Hop, uint16) error {
	return ErrProxyNotSupported
}
func (proxyDefaultAPI) RouteGroupMuxNegotiated(string) ([]router.MuxNegotiated, error) {
	return nil, ErrProxyNotSupported
}
func (proxyDefaultAPI) RehomeTunnelLeg(string, uint16, uint16) error { return ErrProxyNotSupported }

// msDuration converts a wire-side millisecond count to a Duration. The dial
// settings travel as ms rather than as a Duration so a JSON caller (the hvui,
// a script) writes a plain number instead of a Go duration literal.
func msDuration(ms int64) time.Duration { return time.Duration(ms) * time.Millisecond }

// parsePubKeys turns the operator's comma-separated prefer list into public
// keys, naming the first bad entry rather than silently dropping it — a typo'd
// PK in a prefer list is a prefer list that does nothing.
func parsePubKeys(in []string) ([]cipher.PubKey, error) {
	out := make([]cipher.PubKey, 0, len(in))
	for _, s := range in {
		var pk cipher.PubKey
		if err := pk.Set(s); err != nil {
			return nil, fmt.Errorf("invalid public key %q: %w", s, err)
		}
		out = append(out, pk)
	}
	return out, nil
}
