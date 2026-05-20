// api_transport.go contains transport management API methods.
package visor

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/router"
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
	MuxRoutes        int    `json:"mux_routes"`
	MinHops          uint16 `json:"min_hops"`
}

// GetRouterSettings returns the current runtime values of the four
// router knobs. None of these survive a visor restart on their own —
// some get persisted via separate config paths (MinHops, MuxRoutes
// are written to Routing.*) but ForceLocalRoutes / ExistingTPOnly
// are runtime-only, mirroring the CLI's behavior.
func (v *Visor) GetRouterSettings() (RouterSettings, error) {
	if v.router == nil {
		return RouterSettings{}, errors.New("router not available")
	}
	hops, err := v.GetMinHops()
	if err != nil {
		return RouterSettings{}, err
	}
	return RouterSettings{
		ForceLocalRoutes: v.router.GetForceLocalRoutes(),
		ExistingTPOnly:   v.router.GetExistingTPOnly(),
		MuxRoutes:        v.router.GetMuxRoutes(),
		MinHops:          hops,
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
	if err := v.SetMuxRoutes(s.MuxRoutes); err != nil {
		return err
	}
	if err := v.SetMinHops(s.MinHops); err != nil {
		return err
	}
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

// SetMuxRoutes implements API.
// Sets the number of parallel mux routes for new connections at runtime.
// Also persists to the visor config file.
func (v *Visor) SetMuxRoutes(n int) error {
	if v.router == nil {
		return errors.New("router not available")
	}
	v.router.SetMuxRoutes(n)
	// Also update the networker so future app dials use the new value
	if skyN, err := appnet.ResolveNetworker(appnet.TypeSkynet); err == nil {
		if sn, ok := skyN.(*appnet.SkywireNetworker); ok {
			sn.MuxRoutes = n
		}
	}
	// Persist to config
	v.conf.Routing.MuxRoutes = n
	if err := v.conf.Flush(); err != nil {
		v.log.WithError(err).Warn("Failed to persist mux_routes to config")
	}
	v.log.Infof("SetMuxRoutes: %v", n)
	return nil
}

// SetMuxMode implements API.
// Sets the weight distribution mode for mux transport selection.
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
	default:
		return fmt.Errorf("unknown mux mode %q (use \"auto\" or \"equal\")", mode)
	}
	v.router.SetMuxMode(m)
	v.log.Infof("SetMuxMode: %v", mode)
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

	v.log.Debugf("Saving transport to %v via %v with label %s", remote, tpType, tpLabel)

	opts := transport.SaveTransportOptions{
		NoRegister: noRegister,
	}

	tp, err := v.tpM.SaveTransportWithOptions(ctx, remote, types.Type(tpType), tpLabel, opts)
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

// SetPersistentTransports sets min_hops routing config of visor
func (v *Visor) SetPersistentTransports(pTps []transport.PersistentTransports) error {
	v.tpM.SetPTpsCache(pTps)
	return v.conf.UpdatePersistentTransports(pTps)
}

// GetPersistentTransports gets min_hops routing config of visor
func (v *Visor) GetPersistentTransports() ([]transport.PersistentTransports, error) {
	return v.conf.GetPersistentTransports()
}
