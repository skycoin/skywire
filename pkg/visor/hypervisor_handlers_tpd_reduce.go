// Package visor pkg/visor/hypervisor_handlers_tpd_reduce.go c3-vis-core
//
// Folding TPD's /metrics payload down to what the hvui Transports tab can
// actually render.
//
// TPD returns one record per transport for the whole mesh, with a daily
// bandwidth array per record. On the production deployment that is upwards of
// 180,000 records and over 60 MB of JSON — measured 2026-09-28, and still
// growing. Proxying it verbatim broke in three places at once:
//
//   - the upstream body was cut off mid-record at ~30s, so the client received
//     HTTP 200 and unparseable JSON;
//   - 60 MB exceeded the hypervisor's own 10s WriteTimeout, so the browser got
//     ERR_EMPTY_RESPONSE and the page never loaded at all;
//   - and had it arrived, the Angular table renders every row it is given, which
//     wedged the browser's main thread for minutes.
//
// The client only ever displayed a fold of each record — two bandwidth totals
// from the daily array — sorted by bandwidth, so that fold happens here now.
// The response carries the full total alongside the capped rows, so the UI can
// say what it is not showing rather than quietly lying about the size of the
// mesh.
package visor

import (
	"bytes"
	"encoding/json"
	"errors"
	"sort"
)

// Local mirrors of pkg/deployment/tpd/store's metric types. Declared here
// rather than imported so this proxy keeps decoding whatever TPD sends even if
// the store grows fields the hvui has no use for; the fields below are the only
// ones the Transports tab has ever read.
type tpdEdgeBandwidth struct {
	Sent uint64 `json:"sent"`
	Recv uint64 `json:"recv"`
}

type tpdDailyBandwidth struct {
	Date string            `json:"date"`
	A    *tpdEdgeBandwidth `json:"a"`
	B    *tpdEdgeBandwidth `json:"b"`
}

type tpdTransportLatency struct {
	Min int64 `json:"min"`
	Max int64 `json:"max"`
	Avg int64 `json:"avg"`
}

type tpdTransportMetric struct {
	ID      string               `json:"id"`
	Type    string               `json:"type"`
	Live    bool                 `json:"live"`
	Edges   []string             `json:"edges"`
	Latency *tpdTransportLatency `json:"latency"`
	Daily   []tpdDailyBandwidth  `json:"daily"`
}

// compactTransportRow is one transport with its daily array already folded.
type compactTransportRow struct {
	ID      string               `json:"id"`
	Type    string               `json:"type"`
	Live    bool                 `json:"live"`
	Edges   []string             `json:"edges"`
	Latency *tpdTransportLatency `json:"latency,omitempty"`
	Sent    uint64               `json:"sent"`
	Recv    uint64               `json:"recv"`
}

// compactTransportMetrics is the response the hvui Transports tab consumes.
//
// Total counts every transport TPD reported; Returned is how many survived the
// cap. Partial says the upstream body ended mid-document — which happens on a
// large mesh — and means Total is a floor, not a count.
type compactTransportMetrics struct {
	Metrics          []compactTransportRow `json:"metrics"`
	Total            int                   `json:"total"`
	Live             int                   `json:"live"`
	Returned         int                   `json:"returned"`
	NetworkBandwidth uint64                `json:"network_bandwidth"`
	Partial          bool                  `json:"partial"`
}

// defaultMetricsLimit caps the rows sent to the browser. A visor's own
// transports tab renders ~2500 rows in a few seconds, so this is comfortably
// inside what the same table is known to handle; the mesh-wide view is sorted
// by bandwidth, so the cap keeps the traffic that matters.
const defaultMetricsLimit = 2000

// maxMetricsLimit bounds what a caller may ask for. The point of the cap is
// that the browser cannot render an unbounded table; letting ?limit= defeat it
// entirely would just move the wedge behind a query parameter.
const maxMetricsLimit = 10000

// reduceTransportMetrics folds a TPD /metrics body into the compact form.
//
// Decoding is element-by-element rather than into one big slice: a truncated
// body then yields every record that did arrive, with Partial set, instead of
// failing outright. That matters because the upstream response IS routinely
// truncated on a large deployment, and a partial answer is worth far more here
// than an error page.
func reduceTransportMetrics(body []byte, limit int, liveOnly bool) (compactTransportMetrics, error) {
	if limit <= 0 {
		limit = defaultMetricsLimit
	}
	if limit > maxMetricsLimit {
		limit = maxMetricsLimit
	}

	out := compactTransportMetrics{Metrics: []compactTransportRow{}}

	dec := json.NewDecoder(bytes.NewReader(body))

	// Opening '['. A body that is not an array at all is a real error — most
	// likely TPD returned an error object — and the caller should say so.
	tok, err := dec.Token()
	if err != nil {
		return out, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '[' {
		return out, errNotAnArray
	}

	rows := make([]compactTransportRow, 0, 1024)
	for dec.More() {
		var m tpdTransportMetric
		if err := dec.Decode(&m); err != nil {
			// Truncated mid-record: keep what we have.
			out.Partial = true
			break
		}
		out.Total++
		if m.Live {
			out.Live++
		}

		if len(m.Edges) < 2 {
			continue
		}
		// TPD keeps a transport's bandwidth history long after it dies, and on
		// the production mesh the dead vastly outnumber the living — 153k of
		// 168k in a 2026-09-28 sample. Sorting by bandwidth therefore fills the
		// page with history unless the caller asks for live only, so the filter
		// has to happen BEFORE the cap; applying it afterwards would leave the
		// reader a handful of rows and no way to see the rest.
		if liveOnly && !m.Live {
			continue
		}
		sent, recv := foldDailyBandwidth(m.Daily)
		out.NetworkBandwidth += sent + recv
		if sent+recv == 0 && m.Latency == nil {
			// Same drop the client applied: a transport with neither traffic
			// nor a latency sample has nothing to show in any column.
			continue
		}

		rows = append(rows, compactTransportRow{
			ID:      m.ID,
			Type:    m.Type,
			Live:    m.Live,
			Edges:   []string{m.Edges[0], m.Edges[1]},
			Latency: m.Latency,
			Sent:    sent,
			Recv:    recv,
		})
	}

	// Sorted by total bandwidth, descending — the order the table displays, so
	// the cap keeps the head of the list the reader was going to look at.
	// Ties break on ID to keep the output stable across refreshes.
	sort.Slice(rows, func(i, j int) bool {
		bi, bj := rows[i].Sent+rows[i].Recv, rows[j].Sent+rows[j].Recv
		if bi != bj {
			return bi > bj
		}

		return rows[i].ID < rows[j].ID
	})

	if len(rows) > limit {
		rows = rows[:limit]
	}
	out.Metrics = rows
	out.Returned = len(rows)

	return out, nil
}

// foldDailyBandwidth mirrors verifiedBandwidth() in
// cmd/skywire-cli/commands/tp/tp-metrics.go: where both edges reported, believe
// the smaller of the two figures, since an edge can only overstate what it
// sent.
func foldDailyBandwidth(daily []tpdDailyBandwidth) (aToB, bToA uint64) {
	for _, d := range daily {
		aRep := d.A != nil && (d.A.Sent > 0 || d.A.Recv > 0)
		bRep := d.B != nil && (d.B.Sent > 0 || d.B.Recv > 0)
		switch {
		case aRep && bRep:
			aToB += minU64(d.A.Sent, d.B.Recv)
			bToA += minU64(d.A.Recv, d.B.Sent)
		case aRep:
			aToB += d.A.Sent
			bToA += d.A.Recv
		case bRep:
			aToB += d.B.Recv
			bToA += d.B.Sent
		}
	}

	return aToB, bToA
}

func minU64(a, b uint64) uint64 {
	if a < b {
		return a
	}

	return b
}

// errNotAnArray means TPD answered with something other than the metrics
// array — an error object, most likely — and the caller should surface that
// rather than reporting an empty mesh.
var errNotAnArray = errors.New("tpd metrics: response is not a JSON array")
