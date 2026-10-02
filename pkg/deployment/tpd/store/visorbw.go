// Package store pkg/deployment/tpd/store/visorbw.go c4-net-discovery
//
// Per-visor daily bandwidth: what the reward system pays pool 2 from.
//
// Pool 2 pays each visor for the bytes it sent, but not for traffic between
// visors on one IP: an operator could otherwise pay themselves for moving bytes
// between their own visors. Each visor marks such transports itself — a peer
// it reaches at a private address or at its own public IP is on its own
// network (transport.ManagedTransport.SameNetwork) — and TPD keeps that mark
// on the transport's day (DailyEdgeBandwidth.SameNetwork). When a day settles,
// TPD reduces it to per-visor totals without the marked transports: a ~50 KB
// day, where shipping every transport to the reward host to judge there cost
// ~9 MB.
package store

// VisorBWDayPrefix is the per-visor bandwidth feed's sub-tree; a settled day
// is at VisorBWDayPath(date).
const VisorBWDayPrefix = "visorbw/day/"

// VisorBWDayPath is the leaf for one settled day.
func VisorBWDayPath(date string) string { return VisorBWDayPrefix + date }

// VisorBWDay is one settled day of per-visor bandwidth.
type VisorBWDay struct {
	Version int    `json:"version"`
	Date    string `json:"date"`
	// Transports is how many transports moved bytes that day, and
	// SameNetworkExcluded how many of them were left out because an edge
	// marked the other as on its own network.
	Transports          int `json:"transports"`
	SameNetworkExcluded int `json:"same_network_excluded"`
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
// bytes by type, leaving out transports marked same-network that day.
func ComputeVisorBW(records []TransportMetric, date string) VisorBWDay {
	out := VisorBWDay{Version: VisorBWVersion, Date: date, Visors: map[string]map[string]uint64{}}
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
		sameNetwork := false
		for _, d := range tp.Daily {
			if d.Date != date {
				continue
			}
			a, b := SenderBytes(d)
			sentA += a
			sentB += b
			sameNetwork = sameNetwork || d.SameNetwork
		}
		if sentA == 0 && sentB == 0 {
			continue
		}
		out.Transports++
		if sameNetwork {
			out.SameNetworkExcluded++
			continue
		}
		add(tp.Edges[0], tp.Type, sentA)
		add(tp.Edges[1], tp.Type, sentB)
	}
	return out
}
