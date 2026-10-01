// Package store pkg/deployment/tpd/store/visorbw.go c4-net-discovery
//
// Per-visor daily bandwidth: what the reward system pays pool 2 from.
//
// Pool 2 pays each visor for the bytes it sent, but not for traffic between
// visors on one IP (an operator could otherwise pay themselves for moving bytes
// between their own visors). Telling those transports apart needs the visors'
// IPs, which only the reward system has (from surveys), while the bytes are
// TPD's. Shipping every transport to the reward host so it can do the
// exclusion costs ~9 MB a day; doing it at TPD costs a ~50 KB day.
//
// So the reward system publishes IP CLASSES — per visor, a keyed hash of its
// survey IP, equal for visors on one IP — and TPD, when a day settles, leaves
// out transports whose edges share a class and publishes per-visor totals.
// TPD learns which visors share an IP, never the IP.
package store

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"time"
)

// IPClassPath is the reward system's IP-class leaf.
const IPClassPath = "ipclass/all"

// VisorBWDayPrefix is the per-visor bandwidth feed's sub-tree; a settled day
// is at VisorBWDayPath(date).
const VisorBWDayPrefix = "visorbw/day/"

// VisorBWDayPath is the leaf for one settled day.
func VisorBWDayPath(date string) string { return VisorBWDayPrefix + date }

// IPClasses is the reward system's IP-class leaf.
type IPClasses struct {
	Version     int       `json:"version"`
	GeneratedAt time.Time `json:"generated_at"`
	// Classes maps a visor's public key (hex) to its IP class.
	Classes map[string]string `json:"classes"`
}

// IPClass is the class of ip under key: the first 8 bytes of
// HMAC-SHA256(key, ip), hex. Equal IPs give equal classes; without the key an
// IP cannot be recovered from its class, nor two classes compared to an IP.
func IPClass(key []byte, ip string) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(ip)) //nolint:errcheck,gosec
	return hex.EncodeToString(m.Sum(nil)[:8])
}

// VisorBWDay is one settled day of per-visor bandwidth.
type VisorBWDay struct {
	Version int    `json:"version"`
	Date    string `json:"date"`
	// ClassesAt is when the IP classes the exclusion used were generated;
	// zero when there were none, and nothing could be left out.
	ClassesAt time.Time `json:"classes_at"`
	// Transports is how many transports moved bytes that day, and
	// SameIPExcluded how many of them were left out as same-IP.
	Transports     int `json:"transports"`
	SameIPExcluded int `json:"same_ip_excluded"`
	// Visors maps a visor's public key (hex) to the bytes it sent that day,
	// by transport type.
	Visors map[string]map[string]uint64 `json:"visors"`
}

// VisorBWVersion is the current VisorBWDay version. It continues the reward
// system's bandwidth-file versions (1: a {pk: bytes} map; 2: per-transport
// sent bytes, from bw-collect), so a leaf body IS a hist/<date>_bandwidth.json.
const VisorBWVersion = 3

// SenderBytes is what each edge of a transport sent on one day. Each edge
// reports its own counters on its own schedule, so usually only one has a
// fresh record: that side is trusted for both directions (its recv is what the
// other sent). An edge whose record is all zeros has not reported yet. These
// are the rules the reward system has paid pool 2 by since 2026-06-03.
func SenderBytes(d DailyEdgeBandwidth) (sentA, sentB uint64) {
	aReported := d.A != nil && (d.A.Sent > 0 || d.A.Recv > 0)
	bReported := d.B != nil && (d.B.Sent > 0 || d.B.Recv > 0)
	switch {
	case aReported && bReported:
		return d.A.Sent, d.B.Sent
	case aReported:
		return d.A.Sent, d.A.Recv
	case bReported:
		return d.B.Recv, d.B.Sent
	}
	return 0, 0
}

// ComputeVisorBW reduces one day's per-transport records to per-visor sent
// bytes by type, leaving out transports whose two edges have the same class.
// A visor with no class (no survey) is never matched, as before.
func ComputeVisorBW(records []TransportMetric, date string, classes *IPClasses) VisorBWDay {
	out := VisorBWDay{Version: VisorBWVersion, Date: date, Visors: map[string]map[string]uint64{}}
	var cls map[string]string
	if classes != nil {
		cls = classes.Classes
		out.ClassesAt = classes.GeneratedAt
	}
	add := func(pk, typ string, n uint64) {
		if n == 0 {
			return
		}
		m := out.Visors[pk]
		if m == nil {
			m = map[string]uint64{}
			out.Visors[pk] = m
		}
		m[typ] += n
	}
	for _, tp := range records {
		if len(tp.Edges) != 2 || tp.Edges[0] == tp.Edges[1] {
			continue
		}
		var sentA, sentB uint64
		for _, d := range tp.Daily {
			if d.Date != date {
				continue
			}
			a, b := SenderBytes(d)
			sentA += a
			sentB += b
		}
		if sentA == 0 && sentB == 0 {
			continue
		}
		out.Transports++
		ca, okA := cls[tp.Edges[0]]
		cb, okB := cls[tp.Edges[1]]
		if okA && okB && ca == cb {
			out.SameIPExcluded++
			continue
		}
		add(tp.Edges[0], tp.Type, sentA)
		add(tp.Edges[1], tp.Type, sentB)
	}
	return out
}
