//go:build !mobile && !js

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

var (
	sharedOnce sync.Once
	shared     *geoip2.Reader
	sharedErr  error
)

// Embedded reports whether this build carries the database at all, without
// decompressing it.
func Embedded() bool { return len(embeddedGz) > 0 }

// Shared returns the process-wide reader over the embedded database, opening
// it on first use. Callers keep this lazy: a visor that never answers a geo
// query never pays for it. The reader is memory-mapped over a cached inflated
// copy (see geoip_mapped.go) so the ~60 MB database stays out of the Go heap;
// only if that cache cannot be written does it fall back to inflating into
// memory. The reader is safe for concurrent use and is never closed.
func Shared() (*geoip2.Reader, error) {
	sharedOnce.Do(func() {
		if r, err := openMapped(mappedDir()); err == nil {
			shared = r
			return
		}
		shared, sharedErr = OpenEmbedded()
	})
	return shared, sharedErr
}

func bytesReader(b []byte) io.Reader { return bytes.NewReader(b) }
