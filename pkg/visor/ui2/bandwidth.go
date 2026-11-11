// Package ui2 pkg/visor/ui2/bandwidth.go c3-vis-core
package ui2

import (
	"sort"
	"strconv"
	"time"

	"github.com/skycoin/skywire/pkg/visor/stats"
)

// The bandwidth tab, as bandwidth.component.ts: each transport's traffic
// over the window, its live snapshot and its daily history.

// BwRow is one transport.
type BwRow struct {
	*stats.TransportRecord
	Remote string
	Sent   uint64
	Recv   uint64
}

// Bandwidth is the tab.
type Bandwidth struct {
	PK      string
	Days    int
	Rows    []BwRow
	Pager   Pager
	Total   uint64
	Fetched string
	Err     string
}

// BandwidthOf sums each transport's traffic over the window: the live
// snapshot for today, the daily rollups since the cutoff otherwise, and
// sorts the busiest first.
func BandwidthOf(pk string, days int, tps []*stats.TransportRecord, now time.Time) Bandwidth {
	b := Bandwidth{PK: pk, Days: days}
	cutoff := now.UTC().AddDate(0, 0, -days).Format(time.DateOnly)
	for _, t := range tps {
		if t == nil {
			continue
		}
		r := BwRow{TransportRecord: t}
		for _, e := range t.Edges {
			if e.Hex() != pk {
				r.Remote = e.Hex()
			}
		}
		if days == 1 {
			if t.Current != nil {
				r.Sent, r.Recv = t.Current.SentBytes, t.Current.RecvBytes
			}
		} else {
			for _, d := range t.Daily {
				if d.Date >= cutoff {
					r.Sent += d.SentBytes
					r.Recv += d.RecvBytes
				}
			}
		}
		b.Total += r.Sent + r.Recv
		b.Rows = append(b.Rows, r)
	}
	sort.SliceStable(b.Rows, func(i, j int) bool { return b.Rows[i].Sent+b.Rows[i].Recv > b.Rows[j].Sent+b.Rows[j].Recv })
	return b
}

func ms(v float64) string {
	if v <= 0 {
		return "-"
	}
	return strconv.FormatFloat(v, 'f', 0, 64) + " ms"
}

func latencyTriple(minMS, avgMS, maxMS float64) string {
	return ms(minMS) + " / " + ms(avgMS) + " / " + ms(maxMS)
}

func (b Bandwidth) url(days int) string {
	return VisorURL(b.PK, "bandwidth/body?days="+strconv.Itoa(days))
}

// Page keeps the rows that match q and are on its page, 50 to a page.
func (b Bandwidth) Page(q ListQuery) Bandwidth {
	var rows []BwRow
	for _, r := range b.Rows {
		if q.Match(r.ID.String(), r.Remote, r.Type, r.Label) {
			rows = append(rows, r)
		}
	}
	b.Rows, b.Pager = Paginate(rows, q, 50)
	return b
}

// Find is the transport with id.
func (b Bandwidth) Find(id string) (BwRow, bool) {
	for _, r := range b.Rows {
		if r.ID.String() == id {
			return r, true
		}
	}
	return BwRow{}, false
}
