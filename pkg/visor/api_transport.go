// api_transport.go contains transport management API methods.
package visor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gocarina/gocsv"
	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/router"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

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
func (v *Visor) AddTransport(remote cipher.PubKey, tpType string, timeout time.Duration, label string, noRegister bool, skipLatencyProbe bool) (*TransportSummary, error) {
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

	v.log.Debugf("Saving transport to %v via %v with label %s (skipLatencyProbe=%v)", remote, tpType, tpLabel, skipLatencyProbe)

	opts := transport.SaveTransportOptions{
		NoRegister:       noRegister,
		SkipLatencyProbe: skipLatencyProbe,
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
// Returns transport log entries from the last N days.
func (v *Visor) GetTransportLogs(days int) ([]TransportLogEntry, error) {
	if days <= 0 {
		return nil, nil
	}

	logDir := v.conf.Transport.LogStore.Location
	if logDir == "" {
		return nil, fmt.Errorf("transport log store location not configured")
	}

	var allEntries []TransportLogEntry
	now := time.Now().UTC()

	// Read log files for the specified number of days
	for i := 0; i < days; i++ {
		date := now.AddDate(0, 0, -i)
		filename := filepath.Join(logDir, fmt.Sprintf("%s.csv", date.Format("2006-01-02")))

		entries, err := readTransportLogFile(filename)
		if err != nil {
			// Skip files that don't exist or can't be read
			continue
		}
		allEntries = append(allEntries, entries...)
	}

	return allEntries, nil
}

// csvEntry matches the transport.CsvEntry struct format for gocsv parsing.
type csvEntry struct {
	TpID      uuid.UUID `csv:"tp_id"`
	RecvBytes *uint64   `csv:"recv"`
	SentBytes *uint64   `csv:"sent"`
	TimeStamp int64     `csv:"time_stamp"`
}

// readTransportLogFile reads transport log entries from a CSV file.
func readTransportLogFile(filename string) ([]TransportLogEntry, error) {
	f, err := os.Open(filename) //nolint:gosec // filename is from internal config
	if err != nil {
		return nil, err
	}
	defer f.Close() //nolint:errcheck

	var csvEntries []*csvEntry
	if err := gocsv.UnmarshalFile(f, &csvEntries); err != nil {
		// Handle empty file case
		if errors.Is(err, gocsv.ErrEmptyCSVFile) {
			return nil, nil
		}
		return nil, err
	}

	var entries []TransportLogEntry
	for _, ce := range csvEntries {
		var recv, sent uint64
		if ce.RecvBytes != nil {
			recv = *ce.RecvBytes
		}
		if ce.SentBytes != nil {
			sent = *ce.SentBytes
		}
		entries = append(entries, TransportLogEntry{
			TpID:      ce.TpID,
			RecvBytes: recv,
			SentBytes: sent,
			Timestamp: ce.TimeStamp,
		})
	}

	return entries, nil
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
