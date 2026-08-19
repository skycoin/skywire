// Package transport pkg/transport/conformance.go c2-net-transport
// conformance.go implements publish-then-verify for transport discovery: the
// visor periodically diffs the transport set it publishes against the set TPD
// reflects back for its own edge, and surfaces any divergence.
//
// Rationale: TPD silently drifted to reflecting ~9% of the real transport graph
// and nothing detected it — a route-setup / mux problem chased at the routing
// layer for a long time was actually a discovery lie handed up from below. A
// publisher that never checks the authority mirrors what it published cannot
// notice when the authority stops. This loop makes that class of failure loud:
// a low ratio is a WARN log now, and a first-class field for the CLI / runtime
// state surface. It is the regression guard for the discovery-fill fixes
// (#4009/#4010/#4011/#4012): if any of them erodes, this alarms.
package transport

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

const (
	// discoveryConformanceInterval is how often we re-verify our published
	// transport set against TPD's reflected view. Discovery changes are not
	// latency-critical, so this is loose; it exists to catch drift, not to
	// drive control decisions.
	discoveryConformanceInterval = 5 * time.Minute
	// discoveryConformanceMinFloor suppresses the WARN for trivially small
	// sets, where a single in-flight registration skews the ratio and the
	// absolute miss is not actionable.
	discoveryConformanceMinFloor = 3
	// discoveryConformanceWarnRatio is the matched/published floor below which
	// discovery is considered to be diverging from what we publish.
	discoveryConformanceWarnRatio = 0.9
	// conformanceListCap bounds the missing/stale ID lists retained for
	// display; the *counts* are always exact, only the enumerated sample is
	// capped so the runtime-state snapshot stays small on a busy visor.
	conformanceListCap = 32
)

// DiscoveryConformance is a point-in-time publish-then-verify result: what this
// visor published to transport discovery vs what discovery reflects back for
// this visor's edge. Surfaced so a divergence is observable (and alarmable)
// rather than silent. Ratio == 1 means discovery fully reflects our set.
type DiscoveryConformance struct {
	At           time.Time   `json:"at"`
	Published    int         `json:"published"`                // transports we currently hold + publish
	Reflected    int         `json:"reflected"`                // transports TPD returns for our edge
	Matched      int         `json:"matched"`                  // intersection by transport ID
	MissingCount int         `json:"missing_count"`            // published but not reflected (TPD behind)
	StaleCount   int         `json:"stale_count"`              // reflected but no longer held (TPD stale)
	Missing      []uuid.UUID `json:"missing_sample,omitempty"` // capped sample of MissingCount
	Stale        []uuid.UUID `json:"stale_sample,omitempty"`   // capped sample of StaleCount
	Ratio        float64     `json:"ratio"`                    // matched / published (1.0 = fully reflected)
	Err          string      `json:"err,omitempty"`            // last query error, if any
}

// DiscoveryConformance returns the most recent publish-then-verify result
// (zero value until the first check runs).
func (tm *Manager) DiscoveryConformance() DiscoveryConformance {
	tm.conformanceMu.RLock()
	defer tm.conformanceMu.RUnlock()
	return tm.conformance
}

func (tm *Manager) setConformance(dc DiscoveryConformance) {
	tm.conformanceMu.Lock()
	tm.conformance = dc
	tm.conformanceMu.Unlock()
}

// runDiscoveryConformance periodically verifies our published transport set is
// reflected by TPD. Runs until ctx/done. Registered as a Serve() goroutine.
func (tm *Manager) runDiscoveryConformance(ctx context.Context) {
	defer tm.wg.Done()
	t := time.NewTicker(discoveryConformanceInterval)
	defer t.Stop()
	// One check shortly after startup would race registration; wait a full
	// interval so the initial publish + TPD ingest have settled.
	for {
		select {
		case <-ctx.Done():
			return
		case <-tm.done:
			return
		case <-t.C:
			tm.checkDiscoveryConformance(ctx)
		}
	}
}

// checkDiscoveryConformance runs one publish-then-verify cycle and stores the
// result. Best-effort: a query error is recorded, not fatal.
func (tm *Manager) checkDiscoveryConformance(ctx context.Context) {
	dc := tm.Conf.DiscoveryClient
	if dc == nil {
		return
	}

	published := tm.currentBareEntries()

	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	reflected, err := dc.GetTransportsByEdge(cctx, tm.Conf.PubKey)
	cancel()

	if err != nil {
		res := DiscoveryConformance{At: time.Now(), Published: len(published), Ratio: 1, Err: err.Error()}
		tm.setConformance(res)
		tm.Logger.WithError(err).Debug("discovery conformance: edge query failed")
		return
	}

	res := conformanceDiff(published, reflected)
	res.At = time.Now()
	tm.setConformance(res)

	if res.Published >= discoveryConformanceMinFloor && res.Ratio < discoveryConformanceWarnRatio {
		tm.Logger.WithField("published", res.Published).
			WithField("reflected", res.Reflected).
			WithField("matched", res.Matched).
			WithField("missing", res.MissingCount).
			WithField("ratio", fmt.Sprintf("%.2f", res.Ratio)).
			Warn("discovery conformance: TPD reflects less than we published — discovery is diverging")
	}
}

// conformanceDiff is the pure set-diff of published vs reflected entries (by
// transport ID). Split out from checkDiscoveryConformance so the ratio/missing/
// stale accounting is unit-testable without a live Manager or TPD client. Does
// not set At/Err — the caller stamps those.
func conformanceDiff(published, reflected []*Entry) DiscoveryConformance {
	pubIDs := make(map[uuid.UUID]struct{}, len(published))
	for _, e := range published {
		if e != nil {
			pubIDs[e.ID] = struct{}{}
		}
	}
	refIDs := make(map[uuid.UUID]struct{}, len(reflected))
	for _, e := range reflected {
		if e != nil {
			refIDs[e.ID] = struct{}{}
		}
	}
	res := DiscoveryConformance{Published: len(pubIDs), Reflected: len(refIDs), Ratio: 1}
	for id := range pubIDs {
		if _, ok := refIDs[id]; ok {
			res.Matched++
			continue
		}
		res.MissingCount++
		if len(res.Missing) < conformanceListCap {
			res.Missing = append(res.Missing, id)
		}
	}
	for id := range refIDs {
		if _, ok := pubIDs[id]; !ok {
			res.StaleCount++
			if len(res.Stale) < conformanceListCap {
				res.Stale = append(res.Stale, id)
			}
		}
	}
	if res.Published > 0 {
		res.Ratio = float64(res.Matched) / float64(res.Published)
	}
	return res
}
