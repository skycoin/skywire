// Package visor pkg/visor/geo_summary.go c3-vis-core
package visor

import (
	"sync"

	"github.com/oschwald/geoip2-golang/v2"

	"github.com/skycoin/skywire/pkg/geoip"
)

// geoSummary holds a process-wide, lazily-opened handle to the embedded
// MaxMind geoip database, reused across TransportSummary enrichments so
// every `cli visor state` / `cli tp` call doesn't re-open (memory-map)
// the multi-MB blob per transport. It is the same embedded db the
// routing-policy provider (policy_provider.go) resolves country codes
// against, kept consistent so a transport's remote_country matches what
// a geo-avoid policy would see. Read-only after open.
var geoSummary struct {
	once sync.Once
	mu   sync.Mutex
	db   *geoip2.Reader // nil when OpenEmbedded failed
}

// geoCountryForIP returns the ISO country code the embedded geoip db
// maps ip to, or "" when the db is unavailable, ip is empty, or there
// is no geoip hit. Concurrency-safe: the reader is opened once and
// lookups are serialized behind a mutex (cheap — a handful of
// transports per snapshot), mirroring the routing-policy provider's own
// guarded access to the shared reader.
func geoCountryForIP(ip string) string {
	if ip == "" {
		return ""
	}
	geoSummary.once.Do(func() {
		if db, err := geoip.OpenEmbedded(); err == nil {
			geoSummary.db = db
		}
	})
	if geoSummary.db == nil {
		return ""
	}
	geoSummary.mu.Lock()
	res, err := geoip.Lookup(geoSummary.db, ip)
	geoSummary.mu.Unlock()
	if err != nil || res == nil {
		return ""
	}
	return res.CountryCode
}
