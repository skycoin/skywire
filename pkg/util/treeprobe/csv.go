// csv.go — emit per-(level, peer) CSV rows from an Aggregator.
//
// Row shape is one row per Cell, with run-summary fields repeated
// across rows so downstream analytics tools (sheets / pandas) can
// pivot/group without joining a separate summary table. This matches
// Synth's directive: one CSV consumable by the harness assertion
// stage (Beta's #2726 mux-probe-assert and successors).

package treeprobe

import (
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
)

// Column headers. Order is the CSV write order; downstream parsers
// should index by header name not position to remain stable across
// future column additions.
var CSVHeaders = []string{
	// Cell identity
	"level",
	"parent_pk",
	"remote_pk",
	// Transport identification
	"tp_id",
	"tp_type",
	// Result classification
	"latency_source",
	"failed",
	"canceled",
	"sample_count",
	// Latency stats (all in milliseconds for human readability;
	// nanosecond precision preserved by carrying as float64).
	"setup_latency_ms",
	"ping_avg_ms",
	"ping_p50_ms",
	"ping_p99_ms",
	"jitter_ms",
	// Errors (most-recent from each phase)
	"setup_err",
	"ping_err",
	"calc_err",
	// Route (joined hop pks; empty for transport_summary)
	"route_hops",
	// Run-level summary fields (repeated on every row so downstream
	// pivot/group can stratify without a second table)
	"run_total_discovered",
	"run_total_pinged",
	"run_total_succeeded",
	"run_total_failed",
	"run_total_skipped_cached",
	"run_wall_time_ms",
	"run_peak_in_flight",
	"run_termination_reason",
	"run_cache_hit_rate",
}

// WriteCSV emits the aggregator's cells as CSV rows to w. Returns
// the number of rows written + any write error.
func WriteCSV(w io.Writer, a *Aggregator) (int, error) {
	cw := csv.NewWriter(w)
	defer cw.Flush()

	if err := cw.Write(CSVHeaders); err != nil {
		return 0, fmt.Errorf("treeprobe: write header: %w", err)
	}

	// Pre-compute run-level scalars so we don't recompute per row.
	run := a.RunDone()
	hitRate := a.CacheHitRate()

	cells := a.Cells()
	for i, c := range cells {
		row := cellToRow(c, run, hitRate)
		if err := cw.Write(row); err != nil {
			return i, fmt.Errorf("treeprobe: write row %d: %w", i, err)
		}
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		return len(cells), fmt.Errorf("treeprobe: flush: %w", err)
	}
	return len(cells), nil
}

func cellToRow(c *Cell, run *RunDone, hitRate float64) []string {
	r := c.Result
	d := c.Discovered

	row := make([]string, 0, len(CSVHeaders))
	// Cell identity (prefer Result fields, fall back to Discovered)
	row = append(row,
		strconv.FormatInt(int64(c.Key.Level), 10),
		c.Key.ParentPK,
		c.Key.RemotePK,
	)
	row = append(row,
		pickStr(r != nil, ifResult(r, "TpID"), d, "TpID"),
		pickStr(r != nil, ifResult(r, "TpType"), d, "TpType"),
	)

	if r != nil {
		row = append(row,
			r.LatencySource,
			strconv.FormatBool(r.Failed),
			strconv.FormatBool(r.Canceled),
			strconv.FormatInt(int64(r.SampleCount), 10),
			nsToMs(int64(r.SetupLatency)),
			nsToMs(int64(r.PingAvgNs)),
			nsToMs(int64(r.PingP50Ns)),
			nsToMs(int64(r.PingP99Ns)),
			nsToMs(int64(r.JitterNs)),
			r.SetupErr,
			r.PingErr,
			r.CalcErr,
			joinRoute(r.Route),
		)
	} else {
		// Cell was discovered but no ping_result arrived (truncated
		// capture or in-flight at stream end). Emit empty fields
		// for the result-derived columns.
		for i := 0; i < 14; i++ {
			row = append(row, "")
		}
	}

	if run != nil {
		row = append(row,
			strconv.FormatInt(int64(run.TotalDiscovered), 10),
			strconv.FormatInt(int64(run.TotalPinged), 10),
			strconv.FormatInt(int64(run.TotalSucceeded), 10),
			strconv.FormatInt(int64(run.TotalFailed), 10),
			strconv.FormatInt(int64(run.TotalSkippedCached), 10),
			nsToMs(int64(run.WallTimeNs)),
			strconv.FormatInt(int64(run.PeakInFlight), 10),
			run.TerminationReason,
			strconv.FormatFloat(hitRate, 'f', 4, 64),
		)
	} else {
		for i := 0; i < 9; i++ {
			row = append(row, "")
		}
	}
	return row
}

// nsToMs formats nanoseconds as milliseconds with microsecond
// precision (3 decimal places). Returns "" for zero — protobuf
// omitempty leaves zero values invisible from the run, but the
// CSV reader benefits from blank rather than 0.000.
func nsToMs(ns int64) string {
	if ns == 0 {
		return ""
	}
	return strconv.FormatFloat(float64(ns)/1e6, 'f', 3, 64)
}

func joinRoute(hops []RouteHop) string {
	if len(hops) == 0 {
		return ""
	}
	out := ""
	for i, h := range hops {
		if i > 0 {
			out += "|"
		}
		out += h.RemotePK
	}
	return out
}

// pickStr returns the value of field 'name' on PingResult when ok,
// else on Discovered. Avoids reflect — straight switch.
func pickStr(ok bool, val string, d *Discovered, name string) string {
	if ok {
		return val
	}
	if d == nil {
		return ""
	}
	switch name {
	case "TpID":
		return d.TpID
	case "TpType":
		return d.TpType
	}
	return ""
}

func ifResult(r *PingResult, name string) string {
	if r == nil {
		return ""
	}
	switch name {
	case "TpID":
		return r.TpID
	case "TpType":
		return r.TpType
	}
	return ""
}
