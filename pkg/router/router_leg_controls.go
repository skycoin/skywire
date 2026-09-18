//go:build !tinygo || (js && wasm)

// Package router pkg/router/router_leg_controls.go c2-net-routing
//
// Per-LEG operator controls on a live route group, and the negotiated view of
// the group they act on.
//
// Two of these existed in the data plane with no way to reach them:
// WeightModeExplicit / SetExplicitWeights (transport_selector.go) had no caller
// outside the routing-policy distribution, and a FORWARD-ONLY leg
// (appendRouteAsymmetric addFwd=true/addRev=false) could only be produced by a
// preset's rotation hook. Both are operator decisions — "put 70% of the send on
// this leg", "this leg is upload capacity only" — so both get an RPC here and a
// CLI verb in `skywire cli proxy mux`.
//
// These are OPTIONAL router capabilities: pkg/visor reaches them by asserting
// this interface on its router rather than widening the Router interface, so a
// router build without them (tinygo source-only) simply reports the control as
// unavailable instead of failing to compile.
package router

import (
	"errors"
	"fmt"
	"sort"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/routing"
)

// LegWeight is one leg's explicit send weight: the first-hop transport that
// identifies the leg, and the fraction of the send schedule it is to carry.
// Weights are normalized by the selector, so {2,1} and {0.667,0.333} are the
// same instruction.
type LegWeight struct {
	TransportID string  `json:"transport_id"`
	Weight      float64 `json:"weight"`
}

// MuxWeightsView is the weights state of one route group after (or without) a
// change: the scheduler mode in force and, when it is the explicit one, the
// per-leg weights it is spreading by.
type MuxWeightsView struct {
	DstPort uint16      `json:"dst_port"`
	SrcPort uint16      `json:"src_port"`
	Mode    string      `json:"mode"`
	Weights []LegWeight `json:"weights,omitempty"`
}

// MuxNegotiated is the per-group view of what the two ends actually agreed on
// and what the sender is applying — the answer to "which caps are in effect on
// THIS group, and with which window".
//
// The capability flags are negotiated on the wire (both ends must advertise);
// the window and margin values are this end's live router settings, which is
// what the send side is actually clamping to. Reporting them per group rather
// than only as visor-wide settings is the point: a sweep that moves
// --ecf-max-window mid-campaign otherwise leaves no record on the group it
// changed.
type MuxNegotiated struct {
	DstPort uint16 `json:"dst_port"`
	SrcPort uint16 `json:"src_port"`
	Remote  string `json:"remote"`
	// AppName is the dialing app's tag on the group, when it has one.
	AppName string `json:"app_name,omitempty"`
	Legs    int    `json:"legs"`
	// Negotiated capabilities — both ends advertised these.
	MuxEnabled     bool `json:"mux_enabled"`
	SACKEnabled    bool `json:"sack_enabled"`
	HOLRetxEnabled bool `json:"hol_retx_enabled"`
	PerFrameNoise  bool `json:"per_frame_noise"`
	FECEnabled     bool `json:"fec_enabled"`
	Directional    bool `json:"directional"`
	// Scheduler in force, and the explicit weights when it is that mode.
	Distribution    string      `json:"distribution"`
	ExplicitWeights []LegWeight `json:"explicit_weights,omitempty"`
	// Send-window shape in effect (the live router settings this end clamps to).
	EcfMinWindowBytes   int64   `json:"ecf_min_window_bytes"`
	EcfMaxWindowBytes   int64   `json:"ecf_max_window_bytes"`
	EcfWindowMargin     float64 `json:"ecf_window_margin"`
	SendWindowWaitMax   string  `json:"send_window_wait_max"`
	ForwardSpill        bool    `json:"forward_spill"`
	ForwardSwitchMargin float64 `json:"forward_switch_margin"`
	// PerLegWindowBytes is each leg's CURRENT send window, in tps[] order —
	// the negotiated shape as the scheduler is actually applying it.
	PerLegWindowBytes []float64 `json:"per_leg_window_bytes,omitempty"`
}

// MuxLegController is the optional router capability the visor asserts for the
// per-leg operator controls. Implemented by *router.
type MuxLegController interface {
	SetMuxExplicitWeights(desc routing.RouteDescriptor, weights map[uuid.UUID]float64) (MuxWeightsView, error)
	ClearMuxExplicitWeights(desc routing.RouteDescriptor) (MuxWeightsView, error)
	MuxWeightsFor(desc routing.RouteDescriptor) (MuxWeightsView, error)
	AddMuxRouteByHopsForward(desc routing.RouteDescriptor, fwd, rev []routing.Hop) error
	MuxNegotiatedForApp(appName string) []MuxNegotiated
}

var _ MuxLegController = (*router)(nil)

// lookupRouteGroup resolves a descriptor to its live noise route group.
func (r *router) lookupRouteGroup(desc routing.RouteDescriptor) (*NoiseRouteGroup, error) {
	r.mx.Lock()
	nrg, ok := r.rgsNs[desc]
	r.mx.Unlock()
	if !ok || nrg == nil || nrg.rg == nil {
		return nil, fmt.Errorf("no active route group for %s", desc.String())
	}
	if nrg.rg.mux == nil || nrg.rg.mux.tpSelector == nil {
		return nil, errors.New("route group does not have mux enabled")
	}
	return nrg, nil
}

// SetMuxExplicitWeights pins this group's send spread to operator-supplied
// per-leg weights, keyed by each leg's FIRST-HOP transport (the id `mux info`
// prints, and the one `mux rm` takes). It installs WeightModeExplicit and
// rebuilds the schedule, so it takes effect on the next frame.
//
// Every named transport must be a leg of this group — a typo would otherwise
// silently weight nothing. A leg the caller did not name is given weight 0: the
// operator's list IS the spread, not a patch onto the current one. All-zero is
// refused, since it would leave the scheduler with nothing to pick.
//
// Weight 0 is a MINIMUM share, not zero traffic: the selector's explicit-mode
// schedule keeps one slot for every live leg so none is fully starved and each
// stays measured (see Rebuild). To take a leg out of the send set entirely,
// remove it (`proxy mux rm`) or let the adaptive engine park it in standby.
func (r *router) SetMuxExplicitWeights(desc routing.RouteDescriptor, weights map[uuid.UUID]float64) (MuxWeightsView, error) {
	nrg, err := r.lookupRouteGroup(desc)
	if err != nil {
		return MuxWeightsView{}, err
	}
	if len(weights) == 0 {
		return MuxWeightsView{}, errors.New("no weights given")
	}

	rg := nrg.rg
	rg.mu.Lock()
	idxOf := make(map[uuid.UUID]int, len(rg.tps))
	w := make([]float64, len(rg.tps))
	for i, tp := range rg.tps {
		if tp == nil {
			continue
		}
		idxOf[tp.Entry.ID] = i
	}
	rg.mu.Unlock()

	var total float64
	unknown := make([]string, 0, len(weights))
	for id, v := range weights {
		i, ok := idxOf[id]
		if !ok {
			unknown = append(unknown, id.String())
			continue
		}
		if v < 0 {
			return MuxWeightsView{}, fmt.Errorf("weight for %s is negative", id)
		}
		w[i] = v
		total += v
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return MuxWeightsView{}, fmt.Errorf("transport(s) %v are not legs of this route group", unknown)
	}
	if total <= 0 {
		return MuxWeightsView{}, errors.New("weights sum to zero; at least one leg must carry")
	}

	sel := rg.mux.tpSelector
	sel.SetExplicitWeights(w)
	sel.SetMode(WeightModeExplicit)
	rg.mu.Lock()
	sel.Rebuild(rg.tps)
	rg.mu.Unlock()
	r.logger.Infof("SetMuxExplicitWeights: %s now spreads by %d explicit leg weight(s)", desc.String(), len(weights))
	return r.MuxWeightsFor(desc)
}

// ClearMuxExplicitWeights releases an explicit pin and returns the group to
// ECF, the mux default every group is built with (see newRouteMux). It does not
// erase the stored weights — re-pinning with `mux weights` reinstalls them —
// it only stops the scheduler consulting them.
func (r *router) ClearMuxExplicitWeights(desc routing.RouteDescriptor) (MuxWeightsView, error) {
	nrg, err := r.lookupRouteGroup(desc)
	if err != nil {
		return MuxWeightsView{}, err
	}
	rg := nrg.rg
	rg.mux.tpSelector.SetMode(WeightModeECF)
	rg.mu.Lock()
	rg.mux.tpSelector.Rebuild(rg.tps)
	rg.mu.Unlock()
	r.logger.Infof("ClearMuxExplicitWeights: %s back to ecf", desc.String())
	return r.MuxWeightsFor(desc)
}

// MuxWeightsFor reports the group's current scheduler mode and, when it is the
// explicit one, the per-leg weights in force.
func (r *router) MuxWeightsFor(desc routing.RouteDescriptor) (MuxWeightsView, error) {
	nrg, err := r.lookupRouteGroup(desc)
	if err != nil {
		return MuxWeightsView{}, err
	}
	rg := nrg.rg
	out := MuxWeightsView{
		DstPort: uint16(desc.DstPort()), //nolint:gosec // routing.Port is uint16
		SrcPort: uint16(desc.SrcPort()), //nolint:gosec // routing.Port is uint16
		Mode:    rg.mux.distributionMode().String(),
	}
	out.Weights = legWeightsOf(rg)
	return out, nil
}

// legWeightsOf pairs the selector's explicit weight slice (leg index order)
// with each leg's first-hop transport id. Nil when no weights are installed.
func legWeightsOf(rg *RouteGroup) []LegWeight {
	if rg == nil || rg.mux == nil || rg.mux.tpSelector == nil {
		return nil
	}
	w := rg.mux.tpSelector.ExplicitWeights()
	if len(w) == 0 {
		return nil
	}
	rg.mu.Lock()
	defer rg.mu.Unlock()
	out := make([]LegWeight, 0, len(rg.tps))
	for i, tp := range rg.tps {
		if tp == nil || i >= len(w) {
			continue
		}
		out = append(out, LegWeight{TransportID: tp.Entry.ID.String(), Weight: w[i]})
	}
	return out
}

// AddMuxRouteByHopsForward adds a FORWARD-ONLY leg over the caller-supplied
// route: the same validation and setup as AddMuxRouteByHops, but the reverse
// rule is dropped instead of installed, so the leg adds upstream send capacity
// without enlarging the download set.
//
// This is the operator's version of what the adaptive preset's AddForwardLeg
// does under sustained upload saturation. To CLEAR the pin, remove the leg
// (`proxy mux rm <tp-id>`) and re-add it without --forward-only: a leg's
// direction is decided by the rules installed for it, so there is nothing to
// toggle in place.
func (r *router) AddMuxRouteByHopsForward(desc routing.RouteDescriptor, fwd, rev []routing.Hop) error {
	return r.addMuxRouteByHops(desc, fwd, rev, false)
}

// MuxNegotiatedForApp returns the negotiated view of every live route group
// belonging to appName — what the two ends agreed and what this end is applying.
func (r *router) MuxNegotiatedForApp(appName string) []MuxNegotiated {
	infos := r.RouteGroupMuxInfoForApp(appName)
	out := make([]MuxNegotiated, 0, len(infos))
	for _, info := range infos {
		n := MuxNegotiated{
			DstPort:             uint16(info.Desc.DstPort()), //nolint:gosec // routing.Port is uint16
			SrcPort:             uint16(info.Desc.SrcPort()), //nolint:gosec // routing.Port is uint16
			Remote:              info.Desc.DstPK().String(),
			AppName:             appName,
			Legs:                len(info.Legs),
			MuxEnabled:          info.MuxEnabled,
			SACKEnabled:         info.SACKEnabled,
			HOLRetxEnabled:      info.HOLRetxEnabled,
			PerFrameNoise:       info.PerFrameNoise,
			FECEnabled:          info.FECEnabled,
			Directional:         info.Directional,
			Distribution:        info.Distribution,
			EcfMinWindowBytes:   EcfMinWindowBytes(),
			EcfMaxWindowBytes:   EcfMaxWindowBytes(),
			EcfWindowMargin:     EcfWindowMargin(),
			SendWindowWaitMax:   SendWindowWaitMax().String(),
			ForwardSpill:        ForwardSpill(),
			ForwardSwitchMargin: ForwardSwitchMargin(),
		}
		for _, leg := range info.Legs {
			n.PerLegWindowBytes = append(n.PerLegWindowBytes, leg.WindowBytes)
		}
		if v, err := r.MuxWeightsFor(info.Desc); err == nil {
			n.ExplicitWeights = v.Weights
		}
		out = append(out, n)
	}
	return out
}
