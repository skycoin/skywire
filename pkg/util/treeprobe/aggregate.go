// aggregate.go — group PingResult events into per-(level, peer) cells.
//
// The BFS server emits PingResult events in completion order, not
// BFS order. The harness CSV row shape is "one row per (level,
// parent_pk, remote_pk)", so we group results into a deterministic
// cell key on receive + carry the canonical RunDone + LevelDone
// aggregates alongside.

package treeprobe

import "sort"

// CellKey identifies one (level, parent_pk, remote_pk) tuple.
// Used as a map key on the aggregator + as the CSV row identity.
type CellKey struct {
	Level    int32
	ParentPK string
	RemotePK string
}

// Cell is one (level, parent, remote) entry's collected state.
// Result is the canonical metric carrier from the server; Discovered
// is set if a `discovered` event preceded the result (always true
// in well-formed streams).
type Cell struct {
	Key        CellKey
	Discovered *Discovered
	Result     *PingResult
}

// Aggregator collects Decoded events into a per-cell map + tracks
// level-and-run totals. Safe for single-goroutine use only — the
// harness feeds it from one parser loop.
type Aggregator struct {
	cells map[CellKey]*Cell
	// levels[level] = LevelDone for that level (last one wins on
	// duplicates; the server emits exactly one per level).
	levels    map[int32]*LevelDone
	runDone   *RunDone
	srvErr    *ServerError
	statusCnt int // raw StatusUpdate event count (informational)
}

// NewAggregator constructs an empty Aggregator.
func NewAggregator() *Aggregator {
	return &Aggregator{
		cells:  make(map[CellKey]*Cell),
		levels: make(map[int32]*LevelDone),
	}
}

// Observe folds one Decoded event into the aggregator's state.
// Returns silently on unknown event-types — Parser.Next already
// validates the discriminator, so unknowns shouldn't reach here.
func (a *Aggregator) Observe(d *Decoded) {
	switch d.Type {
	case TypeDiscovered:
		k := CellKey{
			Level:    d.Discovered.Level,
			ParentPK: d.Discovered.ParentPK,
			RemotePK: d.Discovered.RemotePK,
		}
		cell, ok := a.cells[k]
		if !ok {
			cell = &Cell{Key: k}
			a.cells[k] = cell
		}
		cell.Discovered = d.Discovered

	case TypePingResult:
		k := CellKey{
			Level:    d.PingResult.Level,
			ParentPK: d.PingResult.ParentPK,
			RemotePK: d.PingResult.RemotePK,
		}
		cell, ok := a.cells[k]
		if !ok {
			cell = &Cell{Key: k}
			a.cells[k] = cell
		}
		cell.Result = d.PingResult

	case TypeLevelDone:
		// Keep last-wins on duplicate level entries; in practice
		// the server emits exactly one per level.
		a.levels[d.LevelDone.Level] = d.LevelDone

	case TypeRunDone:
		a.runDone = d.RunDone

	case TypeServerError:
		a.srvErr = d.ServerError

	case TypeStatusUpdate:
		a.statusCnt++
	}
}

// Cells returns the per-cell entries sorted deterministically by
// (Level asc, ParentPK asc, RemotePK asc) so CSV rows stay stable
// across runs of the same input.
func (a *Aggregator) Cells() []*Cell {
	out := make([]*Cell, 0, len(a.cells))
	for _, c := range a.cells {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Key.Level != out[j].Key.Level {
			return out[i].Key.Level < out[j].Key.Level
		}
		if out[i].Key.ParentPK != out[j].Key.ParentPK {
			return out[i].Key.ParentPK < out[j].Key.ParentPK
		}
		return out[i].Key.RemotePK < out[j].Key.RemotePK
	})
	return out
}

// Levels returns the per-level summaries in ascending level order.
func (a *Aggregator) Levels() []*LevelDone {
	out := make([]*LevelDone, 0, len(a.levels))
	for _, l := range a.levels {
		out = append(out, l)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Level < out[j].Level })
	return out
}

// RunDone returns the final RunDone event, or nil if the stream
// ended before run_done fired (i.e., truncated capture).
func (a *Aggregator) RunDone() *RunDone { return a.runDone }

// ServerError returns the terminal ServerError, or nil if the run
// ended cleanly.
func (a *Aggregator) ServerError() *ServerError { return a.srvErr }

// StatusUpdateCount returns the count of status_update events
// observed. Useful for harness diagnostics — not normally surfaced
// to CSV.
func (a *Aggregator) StatusUpdateCount() int { return a.statusCnt }

// CacheHitRate returns the fraction of total_skipped_cached over
// (total_pinged + total_skipped_cached) — the cache hit rate
// from PR #2733's use_transport_latency fast-path. Returns 0 if
// RunDone is missing or denominator is zero.
func (a *Aggregator) CacheHitRate() float64 {
	if a.runDone == nil {
		return 0
	}
	denom := int32(a.runDone.TotalPinged) + a.runDone.TotalSkippedCached
	if denom == 0 {
		return 0
	}
	return float64(a.runDone.TotalSkippedCached) / float64(denom)
}
