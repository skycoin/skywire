// Package geoip embeds the MaxMind GeoLite2-City database and exposes
// a lookup function. Used by the visor (for service-discovery
// geolocation tagging without an HTTP round-trip to the geoip service)
// and by `cmd/svc/geoip` (the standalone HTTP geoip service).
package geoip

import (
	_ "embed"
	"errors"
	"fmt"
	"net/netip"

	"github.com/oschwald/geoip2-golang/v2"
)

//go:embed GeoLite2-City.mmdb
var embedded []byte

// EmbeddedDB returns the raw bytes of the embedded GeoLite2-City database.
// The standalone geoip service uses this when no external `--db` path is
// configured; the visor uses this for in-process lookups.
func EmbeddedDB() []byte {
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
func OpenEmbedded() (*geoip2.Reader, error) {
	return geoip2.OpenBytes(embedded)
}
