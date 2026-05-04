// Package visor pkg/visor/api_local_transport_stats.go
//
// Local transport-bandwidth read API. The visor's stats tracker
// (pkg/visor/stats) already keeps a bbolt-backed rollup of every
// transport's sent/recv counters and latency stats — current
// snapshot + per-day daily rollups, retained for a configurable
// window (default 30d). This file exposes that local data through
// the Visor RPC surface so the hvui's per-visor Bandwidth tab can
// render it without round-tripping through TPD.
//
// Mirrors what `GET /stats/transports` and `/stats/transports/history`
// return on the visor's logserver, but reachable via the same
// hypervisor proxy chain everything else in the hvui uses.
package visor

import (
	"errors"
	"sort"
	"time"

	"github.com/skycoin/skywire/pkg/visor/stats"
)

// LocalTransportStatsResponse is the wire shape returned by
// LocalTransportStats. Keep this stable independent of the bbolt
// schema — additive fields only on changes.
type LocalTransportStatsResponse struct {
	// Transports is the per-transport rollup, sorted by total
	// bytes (sent+recv) descending so the busiest transports
	// come first.
	Transports []*stats.TransportRecord `json:"transports"`
	// FetchedAt is when the snapshot was assembled. Lets the
	// hvui surface a "last sample" timestamp.
	FetchedAt time.Time `json:"fetched_at"`
}

// LocalTransportStats returns every per-transport record from the
// local stats store, sorted busiest-first. Returns an empty slice
// (no error) when the stats subsystem isn't initialized — the hvui
// then surfaces "no data" rather than an error.
func (v *Visor) LocalTransportStats() (*LocalTransportStatsResponse, error) {
	if v.statsTracker == nil {
		return &LocalTransportStatsResponse{
			Transports: []*stats.TransportRecord{},
			FetchedAt:  time.Now().UTC(),
		}, nil
	}
	store := v.statsTracker.Store()
	if store == nil {
		return nil, errors.New("stats store not available")
	}
	recs, err := store.AllTransportRecords()
	if err != nil {
		return nil, err
	}
	sort.Slice(recs, func(i, j int) bool {
		ti := totalBytes(recs[i])
		tj := totalBytes(recs[j])
		if ti != tj {
			return ti > tj
		}
		// Stable tie-break by ID so repeated calls return the
		// same order even when the data hasn't changed.
		return recs[i].ID.String() < recs[j].ID.String()
	})
	return &LocalTransportStatsResponse{
		Transports: recs,
		FetchedAt:  time.Now().UTC(),
	}, nil
}

// totalBytes adds Current + every Daily rollup so transports with
// historical traffic but no recent activity still rank above
// genuinely-cold ones.
func totalBytes(r *stats.TransportRecord) uint64 {
	if r == nil {
		return 0
	}
	var total uint64
	if r.Current != nil {
		total += r.Current.SentBytes + r.Current.RecvBytes
	}
	for _, d := range r.Daily {
		total += d.SentBytes + d.RecvBytes
	}
	return total
}
