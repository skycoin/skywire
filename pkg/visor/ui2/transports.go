// Package ui2 pkg/visor/ui2/transports.go c3-vis-core
package ui2

import (
	"cmp"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

// Transport is one row of the transports tab. A persistent transport that is
// not up has no ID and Offline set, as the Angular list shows it.
type Transport struct {
	ID         string
	Remote     string
	Type       string
	Label      string
	Latency    string
	Throughput string
	Sent       string
	Recv       string
	Persistent bool
	Offline    bool

	sentN, recvN    uint64
	latencyN, tputN float64
}

// TypeCount is how many transports of one type there are.
type TypeCount struct {
	Type  string
	Count int
}

// Transports is the transports tab.
type Transports struct {
	PK          string
	List        []Transport
	Rows        []Transport
	Pager       Pager
	ByType      []TypeCount
	Types       []string
	Autoconnect bool
	Public      bool
	Err         string
}

// Online is the number of transports that are up.
func (t Transports) Online() int {
	n := 0
	for _, x := range t.List {
		if !x.Offline {
			n++
		}
	}
	return n
}

// TransportsOf builds the tab from the visor's transports and its persistent
// list.
func TransportsOf(pk string, tps []*visorapi.TransportSummary, persistent []transport.PersistentTransports) Transports {
	t := Transports{PK: pk}
	isPersistent := map[string]bool{}
	for _, p := range persistent {
		isPersistent[p.PK.Hex()+"/"+string(p.NetType)] = true
	}
	seen := map[string]bool{}
	counts := map[string]int{}
	for _, s := range tps {
		if s == nil {
			continue
		}
		k := s.Remote.Hex() + "/" + string(s.Type)
		seen[k] = true
		counts[string(s.Type)]++
		row := Transport{
			ID:         s.ID.String(),
			Remote:     s.Remote.Hex(),
			Type:       string(s.Type),
			Label:      string(s.Label),
			Persistent: isPersistent[k],
		}
		if s.LatencyMS > 0 {
			row.Latency = fmt.Sprintf("%.0f ms", s.LatencyMS)
			row.latencyN = s.LatencyMS
		}
		if s.ThroughputBps > 0 {
			row.Throughput = Bytes(uint64(s.ThroughputBps)) + "/s"
			row.tputN = s.ThroughputBps
		}
		if l := s.Log; l != nil {
			if l.SentBytes != nil {
				row.Sent = Bytes(*l.SentBytes)
				row.sentN = *l.SentBytes
			}
			if l.RecvBytes != nil {
				row.Recv = Bytes(*l.RecvBytes)
				row.recvN = *l.RecvBytes
			}
		}
		t.List = append(t.List, row)
	}
	for _, p := range persistent {
		if !seen[p.PK.Hex()+"/"+string(p.NetType)] {
			t.List = append(t.List, Transport{Remote: p.PK.Hex(), Type: string(p.NetType), Persistent: true, Offline: true})
		}
	}
	sort.SliceStable(t.List, func(i, j int) bool {
		a, b := t.List[i], t.List[j]
		if a.Offline != b.Offline {
			return !a.Offline
		}
		if a.Remote != b.Remote {
			return a.Remote < b.Remote
		}
		return a.Type < b.Type
	})
	for typ, n := range counts {
		t.ByType = append(t.ByType, TypeCount{typ, n})
	}
	sort.Slice(t.ByType, func(i, j int) bool {
		if t.ByType[i].Count != t.ByType[j].Count {
			return t.ByType[i].Count > t.ByType[j].Count
		}
		return t.ByType[i].Type < t.ByType[j].Type
	})
	t.Select(ListQuery{})
	return t
}

// Bytes prints a byte count with a binary unit.
func Bytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

func (t Transports) url(rest string) string { return VisorURL(t.PK, "transports") + rest }

// TypeSummary is the count per type, as "dmsg: 3, stcpr: 1".
func (t Transports) TypeSummary() string {
	parts := make([]string, len(t.ByType))
	for i, c := range t.ByType {
		parts[i] = c.Type + ": " + strconv.Itoa(c.Count)
	}
	return strings.Join(parts, ", ")
}

// transportSorts are the columns the list sorts by.
var transportSorts = map[string]func(a, b Transport) int{
	"id":         func(a, b Transport) int { return cmp.Compare(a.ID, b.ID) },
	"remote":     func(a, b Transport) int { return cmp.Compare(a.Remote, b.Remote) },
	"type":       func(a, b Transport) int { return cmp.Compare(a.Type, b.Type) },
	"sent":       func(a, b Transport) int { return cmp.Compare(a.sentN, b.sentN) },
	"recv":       func(a, b Transport) int { return cmp.Compare(a.recvN, b.recvN) },
	"latency":    func(a, b Transport) int { return cmp.Compare(a.latencyN, b.latencyN) },
	"throughput": func(a, b Transport) int { return cmp.Compare(a.tputN, b.tputN) },
}

// Select filters, sorts and pages the list for q into Rows and Pager.
func (t *Transports) Select(q ListQuery) {
	rows := make([]Transport, 0, len(t.List))
	for _, x := range t.List {
		if q.Match(x.ID, x.Remote, x.Type, x.Label) {
			rows = append(rows, x)
		}
	}
	sortBy(rows, q, transportSorts)
	t.Rows, t.Pager = Paginate(rows, q, ListPageSize)
}
