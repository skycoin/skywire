//go:build !mobile

// Package geoip pkg/geoip/geoip_embedded.go c0-com-util
package geoip

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"io"
	"sync"

	"github.com/oschwald/geoip2-golang/v2"
)

// The GeoLite2-City database is committed gzipped (~30 MB vs ~62 MB raw) to keep
// the embedding binaries — every visor and dmsg-server — that much smaller. It
// is decompressed once, lazily, on first EmbeddedDB() call and cached.
//
// The `mobile` build variant strips this embed entirely (geoip_embedded_mobile.go):
// both visor consumers already degrade — LookupGeo → nil
// (pkg/visor/init_services.go) and routing-policy geo.country() → "??"
// (pkg/visor/policy_provider.go).
//
//go:embed GeoLite2-City.mmdb.gz
var embeddedGz []byte

var (
	embeddedOnce sync.Once
	embedded     []byte
)

// EmbeddedDB returns the raw bytes of the embedded GeoLite2-City database,
// decompressed from the committed gzip on first call and cached thereafter.
// The standalone geoip service uses this when no external `--db` path is
// configured; the visor uses this for in-process lookups. Returns nil only if
// the committed blob fails to decompress (build/repo corruption), which callers
// surface as "database not initialized".
func EmbeddedDB() []byte {
	embeddedOnce.Do(func() {
		zr, err := gzip.NewReader(bytes.NewReader(embeddedGz))
		if err != nil {
			return
		}
		defer zr.Close() //nolint:errcheck
		embedded, _ = io.ReadAll(zr)
	})
	return embedded
}

// OpenEmbedded returns a *geoip2.Reader backed by the embedded database.
// Reuse the reader — it's safe for concurrent use.
//
// It calls EmbeddedDB() (not the bare `embedded` var) so the lazy gzip
// decompress is guaranteed to have run: since #3500 the DB is decompressed
// only inside EmbeddedDB()'s sync.Once, and a caller that reached
// OpenEmbedded() first would otherwise get OpenBytes(nil) → no database →
// empty geoip results (the regression that dropped every visor's country
// code from the node list on v1.3.85).
func OpenEmbedded() (*geoip2.Reader, error) {
	return geoip2.OpenBytes(EmbeddedDB())
}
