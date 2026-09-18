// Package visor pkg/visor/api_transport.go c3-vis-core
// api_transport.go contains transport management API methods.
package visor

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/router"
	"github.com/skycoin/skywire/pkg/router/policy/preset"
	"github.com/skycoin/skywire/pkg/transport"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

// RouterSettings bundles the visor-wide router knobs the universal
// "Router Settings" UI panel surfaces. Mirrors the per-flag CLI
// surface ('cli proxy start --local-route' etc.) so the UI can
// achieve parity in one round-trip rather than four separate calls.
type RouterSettings struct {
	ForceLocalRoutes bool   `json:"force_local_routes"`
	ExistingTPOnly   bool   `json:"existing_tp_only"`
	MinHops          uint16 `json:"min_hops"`
	// TransportPreference is the transport-type priority order: which type
	// is tried first when a transport has to be created, and which existing
	// one a route rides when several reach the same peer. GET always answers
	// the full order in effect. On PUT:
	//   omitted / null — leave the order unchanged;
	//   []             — revert to the built-in default;
	//   non-empty      — that order, most-preferred first (unlisted types
	//                    sort after it).
	// Written to routing.transport_preference, so it survives a restart —
	// but not a config regen (no skywire.conf field).
	TransportPreference []string `json:"transport_preference,omitempty"`

	// The mux send-window shape and the adaptive park hold, live from
	// pkg/router/settings.go. Each zero value means "leave unchanged" on PUT,
	// so an older client that does not know a field cannot reset it. GET always
	// answers the values in force.
	//
	// windowRefreshInterval is deliberately absent: it becomes a per-route-group
	// ticker when the group is built, so moving it live is a locking change
	// rather than a knob.
	EcfMaxWindowBytes int64         `json:"ecf_max_window_bytes,omitempty"`
	EcfMinWindowBytes int64         `json:"ecf_min_window_bytes,omitempty"`
	EcfWindowMargin   float64       `json:"ecf_window_margin,omitempty"`
	SendWindowWaitMax time.Duration `json:"send_window_wait_max,omitempty"`
	LegParkMinHold    time.Duration `json:"leg_park_min_hold,omitempty"`

	// The dead-route exclusion window and its ceiling, live from
	// pkg/router/settings.go with the same zero-means-unchanged rule.
	DeadRouteHold    time.Duration `json:"dead_route_hold,omitempty"`
	DeadRouteHoldMax time.Duration `json:"dead_route_hold_max,omitempty"`

	// Shared-bottleneck detection: how many per-leg delay samples a verdict
	// needs, and the minimum spacing between two per-SACK samples for one leg.
	// Same zero-means-unchanged rule.
	SBDMinSamples     int           `json:"sbd_min_samples,omitempty"`
	SBDSampleInterval time.Duration `json:"sbd_sample_interval,omitempty"`

	// The terms of the park TRIAL that qualifies a shared-bottleneck ruling: how
	// long the park is held before the aggregate goodput is re-read, the fraction
	// of goodput it may cost before it is undone, and the first exemption window
	// the vindicated pair earns. Same zero-means-unchanged rule.
	SBDTrialWindow time.Duration `json:"sbd_trial_window,omitempty"`
	SBDTrialLoss   float64       `json:"sbd_trial_loss,omitempty"`
	SBDBackoff     time.Duration `json:"sbd_backoff,omitempty"`

	// SBDMinEvidenceRate is the aggregate delivered-bytes rate (B/s) a group must
	// be carrying before a shared-bottleneck ruling may park one of its legs: no
	// traffic, no ruling. Same zero-means-unchanged rule.
	SBDMinEvidenceRate int64 `json:"sbd_min_evidence_rate,omitempty"`

	// MuxFEC advertises FEC on mux route groups created from now on; unlike
	// the rest it is a tri-state on PUT, see SetRouterSettings.
	MuxFEC *bool `json:"mux_fec,omitempty"`
}

// GetRouterSettings returns the current runtime values of the four
// router knobs. MinHops and MuxRoutes are written to Routing.* and
// seeded back into the router at boot, so they survive a restart;
// ForceLocalRoutes / ExistingTPOnly are runtime-only, mirroring the
// CLI's behavior. Surviving a restart is not the same as surviving a
// config regen — of these, only MinHops has a skywire.conf field
// (MINHOPS) and is preserved by `config gen -r`.
func (v *Visor) GetRouterSettings() (RouterSettings, error) {
	if v.router == nil {
		return RouterSettings{}, errors.New("router not available")
	}
	hops, err := v.GetMinHops()
	if err != nil {
		return RouterSettings{}, err
	}
	order := types.PreferenceOrder()
	preference := make([]string, 0, len(order))
	for _, t := range order {
		preference = append(preference, string(t))
	}
	fec := v.router.GetMuxFEC()
	return RouterSettings{
		ForceLocalRoutes:    v.router.GetForceLocalRoutes(),
		ExistingTPOnly:      v.router.GetExistingTPOnly(),
		MinHops:             hops,
		TransportPreference: preference,
		EcfMaxWindowBytes:   router.EcfMaxWindowBytes(),
		EcfMinWindowBytes:   router.EcfMinWindowBytes(),
		EcfWindowMargin:     router.EcfWindowMargin(),
		SendWindowWaitMax:   router.SendWindowWaitMax(),
		LegParkMinHold:      router.LegParkMinHold(),
		DeadRouteHold:       router.DeadRouteHold(),
		DeadRouteHoldMax:    router.DeadRouteHoldMax(),
		SBDMinSamples:       router.SBDMinSamples(),
		SBDSampleInterval:   router.SBDSampleInterval(),
		SBDTrialWindow:      router.SBDTrialWindow(),
		SBDTrialLoss:        router.SBDTrialLoss(),
		SBDBackoff:          router.SBDBackoff(),
		SBDMinEvidenceRate:  router.SBDMinEvidenceRate(),
		MuxFEC:              &fec,
	}, nil
}

// SetRouterSettings is the unified setter behind PUT
// /visors/{pk}/router-settings. Each field is applied via the
// existing per-knob setter so the same persistence rules apply.
// Returns an error mid-way if any setter fails; partial application
// is possible — UI surfaces should re-fetch on error.
func (v *Visor) SetRouterSettings(s RouterSettings) error {
	if v.router == nil {
		return errors.New("router not available")
	}
	if err := v.SetForceLocalRoutes(s.ForceLocalRoutes); err != nil {
		return err
	}
	if err := v.SetExistingTPOnly(s.ExistingTPOnly); err != nil {
		return err
	}
	if err := v.SetMinHops(s.MinHops); err != nil {
		return err
	}
	if s.TransportPreference != nil {
		if err := v.SetTransportPreference(s.TransportPreference); err != nil {
			return err
		}
	}
	// The mux knobs: zero means "leave it", so a caller that sends only the
	// four original fields changes nothing here.
	for _, k := range []struct {
		name string
		zero bool
		ok   func() bool
	}{
		{"ecf_max_window_bytes", s.EcfMaxWindowBytes == 0, func() bool { return router.SetEcfMaxWindowBytes(s.EcfMaxWindowBytes) }},
		{"ecf_min_window_bytes", s.EcfMinWindowBytes == 0, func() bool { return router.SetEcfMinWindowBytes(s.EcfMinWindowBytes) }},
		{"ecf_window_margin", s.EcfWindowMargin == 0, func() bool { return router.SetEcfWindowMargin(s.EcfWindowMargin) }},
		{"send_window_wait_max", s.SendWindowWaitMax == 0, func() bool { return router.SetSendWindowWaitMax(s.SendWindowWaitMax) }},
		{"leg_park_min_hold", s.LegParkMinHold == 0, func() bool { return router.SetLegParkMinHold(s.LegParkMinHold) }},
		{"dead_route_hold", s.DeadRouteHold == 0, func() bool { return router.SetDeadRouteHold(s.DeadRouteHold) }},
		{"dead_route_hold_max", s.DeadRouteHoldMax == 0, func() bool { return router.SetDeadRouteHoldMax(s.DeadRouteHoldMax) }},
		{"sbd_min_samples", s.SBDMinSamples == 0, func() bool { return router.SetSBDMinSamples(s.SBDMinSamples) }},
		{"sbd_sample_interval", s.SBDSampleInterval == 0, func() bool { return router.SetSBDSampleInterval(s.SBDSampleInterval) }},
		{"sbd_trial_window", s.SBDTrialWindow == 0, func() bool { return router.SetSBDTrialWindow(s.SBDTrialWindow) }},
		{"sbd_trial_loss", s.SBDTrialLoss == 0, func() bool { return router.SetSBDTrialLoss(s.SBDTrialLoss) }},
		{"sbd_backoff", s.SBDBackoff == 0, func() bool { return router.SetSBDBackoff(s.SBDBackoff) }},
		{"sbd_min_evidence_rate", s.SBDMinEvidenceRate == 0, func() bool { return router.SetSBDMinEvidenceRate(s.SBDMinEvidenceRate) }},
	} {
		if k.zero {
			continue
		}
		if !k.ok() {
			return fmt.Errorf("%s must be positive", k.name)
		}
	}
	if s.MuxFEC != nil {
		v.router.SetMuxFEC(*s.MuxFEC)
		v.conf.Routing.MuxFEC = *s.MuxFEC
		if err := v.conf.Flush(); err != nil {
			return err
		}
	}
	return nil
}

// SetTransportPreference installs the transport-type priority order and
// writes it to routing.transport_preference, so it survives a restart — but
// not a config regen: `skywire autoconfig` rebuilds the json from
// /etc/skywire.conf, which has no field for it. An empty slice reverts to
// the built-in default. Every name must be a known transport type — a
// silently-dropped typo would leave the caller believing a preference is
// in force that isn't.
func (v *Visor) SetTransportPreference(order []string) error {
	parsed := types.ParsePreferenceOrder(order)
	if len(parsed) != len(order) {
		return fmt.Errorf("unknown transport type in preference order %v (known: %v)",
			order, types.Known())
	}
	types.SetPreferenceOrder(parsed)
	v.conf.Routing.TransportPreference = order
	if err := v.conf.Flush(); err != nil {
		v.log.WithError(err).Warn("Failed to persist transport_preference to config")
	}
	v.log.Infof("SetTransportPreference: %v", order)
	return nil
}

// SetExistingTPOnly implements API.
// Sets whether to only use existing transports for routing (no new transport creation).
func (v *Visor) SetExistingTPOnly(enabled bool) error {
	if v.router == nil {
		return errors.New("router not available")
	}
	v.router.SetExistingTPOnly(enabled)
	v.log.Infof("SetExistingTPOnly: %v", enabled)
	return nil
}

// SetForceLocalRoutes implements API.
// Sets whether to skip the route finder and use local route calculation.
func (v *Visor) SetForceLocalRoutes(enabled bool) error {
	if v.router == nil {
		return errors.New("router not available")
	}
	v.router.SetForceLocalRoutes(enabled)
	v.log.Infof("SetForceLocalRoutes: %v", enabled)
	return nil
}

// SetMuxMode implements API.
// Sets the weight distribution mode for mux transport selection.
// Runtime-only: held in the router, never written to the config, lost on
// restart. There is no skywire.conf field for it.
func (v *Visor) SetMuxMode(mode string) error {
	if v.router == nil {
		return errors.New("router not available")
	}
	var m router.WeightMode
	switch mode {
	case "auto":
		m = router.WeightModeAuto
	case "equal":
		m = router.WeightModeEqual
	case "capacity":
		m = router.WeightModeCapacity
	case "ecf":
		m = router.WeightModeECF
	case "otias":
		m = router.WeightModeOTIAS
	case "stms":
		m = router.WeightModeSTMS
	default:
		return fmt.Errorf("unknown mux mode %q (use \"auto\", \"equal\", \"capacity\", \"ecf\", \"otias\", or \"stms\")", mode)
	}
	v.router.SetMuxMode(m)
	v.log.Infof("SetMuxMode: %v", mode)
	return nil
}

// SetMuxCap implements API. Sets the hard ceiling on adaptive mux active width
// (the aggregation ceiling) at runtime — the adaptive engine reads it on the
// next tick of every route group, so it takes effect live without a restart.
//
// This drives the process-global preset atomic. For a preset:* (wasm) policy the
// value now reaches the sandboxed wazero guest via the decide/tick input wire
// (the host stamps it, the guest applies it before dispatch — see
// pkg/router/policy/wasm), so the reported applied value is honest for the wasm
// path too, not only the native/wasm-visor in-process path (#4325).
func (v *Visor) SetMuxCap(n int) error {
	applied := preset.SetAdaptCap(n)
	v.log.Infof("SetMuxCap: requested %d, applied %d (clamped to [1, standby])", n, applied)
	return nil
}

// SetMuxWidth implements API. Sets the steady active download width (the floor
// the adaptive engine converges to when idle) at runtime. Takes effect live.
// Clamped to [1, cap]; reaches wasm presets via the input wire (see SetMuxCap).
func (v *Visor) SetMuxWidth(n int) error {
	applied := preset.SetAdaptRevActive(n)
	v.log.Infof("SetMuxWidth: requested %d, applied %d (clamped to [1, cap])", n, applied)
	return nil
}

// SetMuxStandby implements API. Sets the warm-standby reserve pool size — the
// number of full-duplex spare legs the adaptive default holds parked for
// instant, dip-free promotion (decideAdaptive requests Mux = width + standby).
// Takes effect on the next dial (the requested Mux width) and tick. Clamped to
// >= 1; pulls cap (and width) down if they would exceed the new pool. Reaches
// wasm presets via the input wire (see SetMuxCap).
func (v *Visor) SetMuxStandby(n int) error {
	applied := preset.SetAdaptStandbyMax(n)
	v.log.Infof("SetMuxStandby: requested %d, applied %d", n, applied)
	return nil
}

// TransportTypes implements API.
func (v *Visor) TransportTypes() ([]string, error) {
	var tps []string
	if v.tpM == nil {
		return tps, ErrTrpMangerNotAvailable
	}
	for _, netType := range v.tpM.Networks() {
		tps = append(tps, string(netType))
	}
	return tps, nil
}

// Transports implements API.
func (v *Visor) Transports(typeFilters []string, pks []cipher.PubKey, logs bool) ([]*TransportSummary, error) {
	var result []*TransportSummary

	typeIncluded := func(tType types.Type) bool {
		if typeFilters != nil {
			for _, ft := range typeFilters {
				if string(tType) == ft {
					return true
				}
			}
			return false
		}
		return true
	}
	pkIncluded := func(localPK, remotePK cipher.PubKey) bool {
		if pks != nil {
			for _, fpk := range pks {
				if localPK == fpk || remotePK == fpk {
					return true
				}
			}
			return false
		}
		return true
	}
	if v.tpM != nil {
		v.tpM.WalkTransports(func(tp *transport.ManagedTransport) bool {
			if typeIncluded(tp.Type()) && pkIncluded(v.tpM.Local(), tp.Remote()) {
				result = append(result, newTransportSummary(v.tpM, tp, logs, v.router.SetupIsTrusted(tp.Remote())))
			}
			return true
		})
	}

	return result, nil
}

// Transport implements API.
func (v *Visor) Transport(tid uuid.UUID) (*TransportSummary, error) {
	tp := v.tpM.Transport(tid)
	if tp == nil {
		return nil, ErrNotFound
	}

	return newTransportSummary(v.tpM, tp, true, v.router.SetupIsTrusted(tp.Remote())), nil
}

// AddTransport implements API.
func (v *Visor) AddTransport(remote cipher.PubKey, tpType string, timeout time.Duration, label string, noRegister bool, _ bool) (*TransportSummary, error) {
	if v.tpM == nil {
		return nil, ErrTrpMangerNotAvailable
	}

	ctx := context.Background()

	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Second*20)
		defer cancel()
	}

	// Determine label - default to skycoin, use user if explicitly requested
	tpLabel := transport.LabelSkycoin
	if label == string(transport.LabelUser) {
		tpLabel = transport.LabelUser
	}

	// noRegister only valid for user-labeled transports
	if noRegister && tpLabel != transport.LabelUser {
		return nil, fmt.Errorf("--no-register flag is only valid for user-labeled transports")
	}

	// Accept the legacy "quic"/"squic" wire names as well as the canonical "squicr".
	netType := types.NormalizeType(types.Type(tpType))

	v.log.Debugf("Saving transport to %v via %v with label %s", remote, netType, tpLabel)

	opts := transport.SaveTransportOptions{
		NoRegister: noRegister,
	}

	tp, err := v.tpM.SaveTransportWithOptions(ctx, remote, netType, tpLabel, opts)
	if err != nil {
		return nil, err
	}

	v.log.Debugf("Saved transport to %v via %v, label %s", remote, tpType, tp.Entry.Label)

	return newTransportSummary(v.tpM, tp, false, v.router.SetupIsTrusted(tp.Remote())), nil
}

// SetSTCPAddr injects an STCP PK table entry at runtime, mapping a public key to a TCP address.
// This allows creating STCP transports to visors not preconfigured in the STCP config.
func (v *Visor) SetSTCPAddr(pk cipher.PubKey, addr string) error {
	if v.stcpTable == nil {
		return fmt.Errorf("STCP is not configured on this visor")
	}
	v.stcpTable.SetAddr(pk, addr)
	v.log.Infof("Set STCP address for %s -> %s", pk, addr)
	return nil
}

// RemoveTransport implements API.
func (v *Visor) RemoveTransport(tid uuid.UUID) error {
	v.tpM.DeleteTransport(tid)
	return nil
}

// RemoveAllTransports implements API
func (v *Visor) RemoveAllTransports() error {
	v.tpM.DeleteAllTransports()
	return nil
}

// GetTransportLatencyByRemotePK returns the smoothed average RTT
// in milliseconds for the local managed transport to remotePK, or
// 0 if no such transport exists or its latency is not yet sampled.
//
// Used by the ping-tree streaming RPC's UseTransportLatency
// fast-path: at level-1 (direct neighbors) the visor already has
// continuously-sampled transport-level RTT; re-pinging would
// duplicate work without adding signal. Returning 0 lets the
// caller fall back to a live ping when the transport doesn't yet
// have measurements (e.g. just-established).
//
// O(N) over local transports. Stops on first match. Callers should
// not invoke per-packet — this is intended for the BFS level-1
// pass where N is the direct-neighbor count.
func (v *Visor) GetTransportLatencyByRemotePK(remotePK cipher.PubKey) float64 {
	var latency float64
	v.tpM.WalkTransports(func(tp *transport.ManagedTransport) bool {
		if tp.Remote() == remotePK {
			latency = tp.GetLatency()
			return false // stop walk
		}
		return true
	})
	return latency
}

// DiscoverTransportsByPK implements API.
func (v *Visor) DiscoverTransportsByPK(pk cipher.PubKey) ([]*transport.Entry, error) {
	tpD := v.tpDiscClient()

	entries, err := tpD.GetTransportsByEdge(context.Background(), pk)
	if err != nil {
		return nil, err
	}

	return entries, nil
}

// DiscoverTransportByID implements API.
func (v *Visor) DiscoverTransportByID(id uuid.UUID) (*transport.Entry, error) {
	tpD := v.tpDiscClient()

	entry, err := tpD.GetTransportByID(context.Background(), id)
	if err != nil {
		return nil, err
	}

	return entry, nil
}

// GetTransportLogs implements API.
// Returns one entry per (transport, day) for the last N UTC days, drawn
// from the bbolt-backed stats store. The on-disk CSV transport log was
// retired — see pkg/transport/log.go and pkg/visor/stats. Each entry's
// RecvBytes/SentBytes is the within-day delta (matches the historical
// CSV semantics, where the per-day file accumulated bytes from a
// zero-anchored start-of-day baseline). Timestamp is the start of the
// UTC day, expressed as a Unix epoch second.
func (v *Visor) GetTransportLogs(days int) ([]TransportLogEntry, error) {
	if days <= 0 {
		return nil, nil
	}
	if v.statsTracker == nil {
		return nil, errors.New("stats store not available (stats subsystem disabled or not yet initialized)")
	}

	records, err := v.statsTracker.Store().AllTransportRecords()
	if err != nil {
		return nil, fmt.Errorf("read stats store: %w", err)
	}

	// Keep entries whose date is within the last `days` UTC days
	// (inclusive of today). Lexical comparison works because the
	// daily-rollup date format is YYYY-MM-DD.
	cutoff := time.Now().UTC().AddDate(0, 0, -(days - 1)).Format("2006-01-02")

	var out []TransportLogEntry
	for _, rec := range records {
		for _, d := range rec.Daily {
			if d.Date < cutoff {
				continue
			}
			ts, perr := time.Parse("2006-01-02", d.Date)
			if perr != nil {
				continue
			}
			out = append(out, TransportLogEntry{
				TpID:      rec.ID,
				RecvBytes: d.RecvBytes,
				SentBytes: d.SentBytes,
				Timestamp: ts.Unix(),
			})
		}
	}
	return out, nil
}

// SetPersistentTransports replaces the visor's persistent_transports list —
// in the transport manager's cache and in skywire-config.json, so it survives
// a restart. Not a config regen: `skywire autoconfig` rebuilds the json from
// /etc/skywire.conf, which has no field for persistent_transports.
func (v *Visor) SetPersistentTransports(pTps []transport.PersistentTransports) error {
	v.tpM.SetPTpsCache(pTps)
	return v.conf.UpdatePersistentTransports(pTps)
}

// GetPersistentTransports returns the visor's configured persistent_transports.
func (v *Visor) GetPersistentTransports() ([]transport.PersistentTransports, error) {
	return v.conf.GetPersistentTransports()
}
