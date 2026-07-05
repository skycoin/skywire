package geoip

import (
	_ "embed"
	"testing"

	"github.com/oschwald/geoip2-golang/v2"
)

// nonCityDB is a minimal GeoLite2-ASN database (a valid MaxMind DB that is
// not a City database). Opening it succeeds, but Lookup's db.City() call
// returns an InvalidMethodError, exercising the lookup error branch.
//
//go:embed testdata/non-city.mmdb
var nonCityDB []byte

func TestEmbeddedDB(t *testing.T) {
	b := EmbeddedDB()
	if len(b) == 0 {
		t.Fatal("EmbeddedDB returned empty bytes")
	}
	// Returned slice should alias the same backing array on each call.
	if &EmbeddedDB()[0] != &b[0] {
		t.Error("EmbeddedDB returned a different backing array on second call")
	}
}

func TestOpenEmbedded(t *testing.T) {
	db, err := OpenEmbedded()
	if err != nil {
		t.Fatalf("OpenEmbedded: %v", err)
	}
	defer db.Close() //nolint:errcheck,gosec

	if md := db.Metadata(); md.DatabaseType == "" {
		t.Error("expected a non-empty database type in metadata")
	}
}

func TestLookup_Errors(t *testing.T) {
	db, err := OpenEmbedded()
	if err != nil {
		t.Fatalf("OpenEmbedded: %v", err)
	}
	defer db.Close() //nolint:errcheck,gosec

	tests := []struct {
		name  string
		db    *geoip2.Reader
		ipStr string
	}{
		{name: "nil db", db: nil, ipStr: "8.8.8.8"},
		{name: "empty IP", db: db, ipStr: ""},
		{name: "invalid IP", db: db, ipStr: "not-an-ip"},
		{name: "hostname not IP", db: db, ipStr: "example.com"},
		{name: "IP with port", db: db, ipStr: "8.8.8.8:80"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res, err := Lookup(tc.db, tc.ipStr)
			if err == nil {
				t.Fatalf("expected error, got result %+v", res)
			}
			if res != nil {
				t.Errorf("expected nil result on error, got %+v", res)
			}
		})
	}
}

// A reader backed by a non-City database opens fine but fails the City()
// lookup, so Lookup must surface that error rather than a Result.
func TestLookup_NonCityDB(t *testing.T) {
	db, err := geoip2.OpenBytes(nonCityDB)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	defer db.Close() //nolint:errcheck,gosec

	res, err := Lookup(db, "1.0.0.1")
	if err == nil {
		t.Fatalf("expected error from non-City database, got result %+v", res)
	}
	if res != nil {
		t.Errorf("expected nil result on error, got %+v", res)
	}
}

// 81.2.69.142 is MaxMind's documented sample IP and resolves to a fully
// populated City record, exercising every field of Result including the
// latitude/longitude pointers and the first subdivision.
func TestLookup_FullRecord(t *testing.T) {
	db, err := OpenEmbedded()
	if err != nil {
		t.Fatalf("OpenEmbedded: %v", err)
	}
	defer db.Close() //nolint:errcheck,gosec

	res, err := Lookup(db, "81.2.69.142")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}

	if res.IP != "81.2.69.142" {
		t.Errorf("IP = %q, want 81.2.69.142", res.IP)
	}
	if res.ContinentCode != "EU" {
		t.Errorf("ContinentCode = %q, want EU", res.ContinentCode)
	}
	if res.ContinentName != "Europe" {
		t.Errorf("ContinentName = %q, want Europe", res.ContinentName)
	}
	if res.CountryCode != "GB" {
		t.Errorf("CountryCode = %q, want GB", res.CountryCode)
	}
	if res.CountryName != "United Kingdom" {
		t.Errorf("CountryName = %q, want United Kingdom", res.CountryName)
	}
	if res.RegionCode != "ENG" {
		t.Errorf("RegionCode = %q, want ENG", res.RegionCode)
	}
	if res.RegionName != "England" {
		t.Errorf("RegionName = %q, want England", res.RegionName)
	}
	if res.CityName != "Windsor" {
		t.Errorf("CityName = %q, want Windsor", res.CityName)
	}
	if res.PostalCode != "SL4" {
		t.Errorf("PostalCode = %q, want SL4", res.PostalCode)
	}
	if res.Timezone != "Europe/London" {
		t.Errorf("Timezone = %q, want Europe/London", res.Timezone)
	}
	if res.Latitude == nil || res.Longitude == nil {
		t.Fatalf("expected non-nil Latitude/Longitude, got lat=%v lon=%v", res.Latitude, res.Longitude)
	}
}

// An IP that is present in the database but lacks city/region data must still
// succeed and populate only the fields that are available.
func TestLookup_PartialRecord(t *testing.T) {
	db, err := OpenEmbedded()
	if err != nil {
		t.Fatalf("OpenEmbedded: %v", err)
	}
	defer db.Close() //nolint:errcheck,gosec

	res, err := Lookup(db, "8.8.8.8")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if res.CountryCode != "US" {
		t.Errorf("CountryCode = %q, want US", res.CountryCode)
	}
	if res.CityName != "" {
		t.Errorf("CityName = %q, want empty", res.CityName)
	}
	if res.RegionCode != "" {
		t.Errorf("RegionCode = %q, want empty", res.RegionCode)
	}
}

// IPv6 addresses are valid input and resolve through the same code path.
func TestLookup_IPv6(t *testing.T) {
	db, err := OpenEmbedded()
	if err != nil {
		t.Fatalf("OpenEmbedded: %v", err)
	}
	defer db.Close() //nolint:errcheck,gosec

	res, err := Lookup(db, "2001:4860:4860::8888")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if res.CountryCode != "US" {
		t.Errorf("CountryCode = %q, want US", res.CountryCode)
	}
}

// An IP that is not present in the database is not an error: Lookup returns a
// Result whose IP echoes the input while the geo fields stay empty.
func TestLookup_NotFound(t *testing.T) {
	db, err := OpenEmbedded()
	if err != nil {
		t.Fatalf("OpenEmbedded: %v", err)
	}
	defer db.Close() //nolint:errcheck,gosec

	res, err := Lookup(db, "1.1.1.1")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if res.IP != "1.1.1.1" {
		t.Errorf("IP = %q, want 1.1.1.1", res.IP)
	}
	if res.CountryCode != "" || res.Latitude != nil || res.Longitude != nil {
		t.Errorf("expected empty geo fields for unlisted IP, got %+v", res)
	}
}
