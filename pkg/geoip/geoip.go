// Package geoip pkg/geoip/geoip.go c0-com-util
// a lookup function. Used by the visor (for service-discovery
// geolocation tagging without an HTTP round-trip to the geoip service)
// and by `cmd/svc/geoip` (the standalone HTTP geoip service).
package geoip

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"sync"

	"github.com/oschwald/geoip2-golang/v2"
)

// The GeoLite2-City database is committed gzipped (~30 MB vs ~62 MB raw) to keep
// the embedding binaries — every visor and dmsg-server — that much smaller. It
// is decompressed once, lazily, on first EmbeddedDB() call and cached.
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

// Result is the schema returned by both the in-process Lookup and the
// HTTP geoip service. The JSON tags match what `cmd/svc/geoip` returns
// over HTTP so callers can swap between the two without conversion.
type Result struct {
	IP            string   `json:"ip_address"`
	Latitude      *float64 `json:"latitude"`
	Longitude     *float64 `json:"longitude"`
	PostalCode    string   `json:"postal_code"`
	ContinentCode string   `json:"continent_code"`
	ContinentName string   `json:"continent_name"`
	CountryCode   string   `json:"country_code"`
	CountryName   string   `json:"country_name"`
	RegionCode    string   `json:"region_code"`
	RegionName    string   `json:"region_name"`
	CityName      string   `json:"city_name"`
	Timezone      string   `json:"timezone"`
}

// Lookup looks up an IP in the given database reader and returns a Result.
func Lookup(db *geoip2.Reader, ipStr string) (*Result, error) {
	if db == nil {
		return nil, errors.New("geoip: database not initialized")
	}
	if ipStr == "" {
		return nil, errors.New("geoip: empty IP")
	}
	addr, err := netip.ParseAddr(ipStr)
	if err != nil {
		return nil, fmt.Errorf("geoip: invalid IP %q: %w", ipStr, err)
	}

	record, err := db.City(addr)
	if err != nil {
		return nil, fmt.Errorf("geoip: lookup: %w", err)
	}

	res := &Result{
		IP:            ipStr,
		PostalCode:    record.Postal.Code,
		ContinentCode: record.Continent.Code,
		ContinentName: record.Continent.Names.English,
		CountryCode:   record.Country.ISOCode,
		CountryName:   record.Country.Names.English,
		CityName:      record.City.Names.English,
		Timezone:      record.Location.TimeZone,
	}
	if record.Location.Latitude != nil && record.Location.Longitude != nil {
		res.Latitude = record.Location.Latitude
		res.Longitude = record.Location.Longitude
	}
	if len(record.Subdivisions) > 0 {
		res.RegionCode = record.Subdivisions[0].ISOCode
		res.RegionName = record.Subdivisions[0].Names.English
	}
	return res, nil
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
