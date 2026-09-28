// Package tpdpaths pkg/deployment/tpd/tpdpaths/tpdpaths.go c2-net-discovery
//
// The leaf paths the transport discovery publishes on its CXO feeds. They
// live in this leaf package, not in pkg/deployment/tpd/api beside the
// publishers, so a visor-side subscriber can name them without importing
// the TPD server: its api package pulls the server's store and
// VictoriaMetrics, which the mobile build cannot compile for iOS.
package tpdpaths

// All-transports feed: the network-wide transport snapshot, with and
// without the self transports.
const (
	AllTransportsPathWithSelf    = "transports/all/with-self"
	AllTransportsPathWithoutSelf = "transports/all/without-self"
)

// Stats feed.
const (
	// StatsPathNetwork carries the network-wide transport aggregate
	// (the GET /all-transports/stats shape plus a completeness stamp).
	StatsPathNetwork = "stats/network"
	// StatsPathVersions carries the fleet version histogram (the
	// GET /version shape plus a completeness stamp).
	StatsPathVersions = "stats/versions"
	// StatsPathDaily carries the network-wide daily aggregate (the
	// GET /metric shape plus a completeness stamp) — per-day
	// bandwidth, latency and by-type breakdown over statsDailyDays.
	StatsPathDaily = "stats/daily"
)
