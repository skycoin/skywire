// Package visor pkg/visor/policy_provider_test.go c3-vis-core
package visor

import (
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/geoip"
	"github.com/skycoin/skywire/pkg/transport/network/addrresolver"
)

// gbIP is a fixed address the embedded GeoLite2 db maps to GB (the
// same fixture pkg/geoip's own tests rely on).
const gbIP = "81.2.69.142"

func TestHostFromAddr(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"81.2.69.142:1234", "81.2.69.142"},
		{"81.2.69.142", "81.2.69.142"}, // no port → unchanged
		{"[2001:db8::1]:443", "2001:db8::1"},
	}
	for _, c := range cases {
		if got := hostFromAddr(c.in); got != c.want {
			t.Errorf("hostFromAddr(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestCountryFromVisorData asserts the AR-record → country path: the
// reachable IP is extracted (v4 preferred, v6 fallback) and looked up
// in the embedded geoip db, and an AR record with no usable IP yields
// "".
func TestCountryFromVisorData(t *testing.T) {
	db, err := geoip.OpenEmbedded()
	if err != nil {
		t.Skipf("embedded geoip db unavailable: %v", err)
	}
	defer db.Close() //nolint:errcheck,gosec
	p := &visorPolicyProvider{geoDB: db}

	if got := p.countryFromVisorData(addrresolver.VisorData{RemoteAddr: gbIP + ":1234"}); got != "GB" {
		t.Errorf("countryFromVisorData(v4) = %q, want GB", got)
	}
	// v6 fallback when v4 is absent.
	if got := p.countryFromVisorData(addrresolver.VisorData{RemoteAddrV6: gbIP + ":1234"}); got != "GB" {
		t.Errorf("countryFromVisorData(v6 fallback) = %q, want GB", got)
	}
	// No usable address → "".
	if got := p.countryFromVisorData(addrresolver.VisorData{}); got != "" {
		t.Errorf("countryFromVisorData(empty) = %q, want \"\"", got)
	}
	// Private / non-public IP → no country.
	if got := p.countryFromVisorData(addrresolver.VisorData{RemoteAddr: "10.0.0.1:1"}); got != "" {
		t.Errorf("countryFromVisorData(private) = %q, want \"\"", got)
	}
}

// TestARGeoForPKCache asserts arGeoForPK serves cached values without
// touching the AR: a fresh positive entry returns its country, a
// negative ("??") entry returns "" (so Geo falls through), and a miss
// with no capability (nil tpM/geoDB) returns "" without spawning a
// resolve.
func TestARGeoForPKCache(t *testing.T) {
	p := &visorPolicyProvider{
		arGeo:      map[string]arGeoEntry{},
		arInFlight: map[string]struct{}{},
	}
	const hit = "aaaa000000000000000000000000000000000000000000000000000000000001"
	const neg = "bbbb000000000000000000000000000000000000000000000000000000000002"
	const miss = "cccc000000000000000000000000000000000000000000000000000000000003"
	p.arGeo[hit] = arGeoEntry{country: "DE", expires: time.Now().Add(time.Minute)}
	p.arGeo[neg] = arGeoEntry{country: "??", expires: time.Now().Add(time.Minute)}

	if got := p.arGeoForPK(cipher.PubKey{}, hit); got != "DE" {
		t.Errorf("arGeoForPK(cached hit) = %q, want DE", got)
	}
	if got := p.arGeoForPK(cipher.PubKey{}, neg); got != "" {
		t.Errorf("arGeoForPK(cached negative) = %q, want \"\"", got)
	}
	// Miss with nil tpM/geoDB: no capability → "" and no in-flight
	// resolve registered (nothing to resolve with).
	if got := p.arGeoForPK(cipher.PubKey{}, miss); got != "" {
		t.Errorf("arGeoForPK(miss, no capability) = %q, want \"\"", got)
	}
	if _, running := p.arInFlight[miss]; running {
		t.Error("arGeoForPK spawned a resolve despite nil tpM/geoDB")
	}
}

// TestARGeoForPKExpired asserts an expired cache entry is treated as a
// miss (not served stale) and, lacking capability, returns "".
func TestARGeoForPKExpired(t *testing.T) {
	p := &visorPolicyProvider{
		arGeo:      map[string]arGeoEntry{},
		arInFlight: map[string]struct{}{},
	}
	const pk = "dddd000000000000000000000000000000000000000000000000000000000004"
	p.arGeo[pk] = arGeoEntry{country: "FR", expires: time.Now().Add(-time.Minute)} // expired
	if got := p.arGeoForPK(cipher.PubKey{}, pk); got != "" {
		t.Errorf("arGeoForPK(expired) = %q, want \"\" (not served stale)", got)
	}
}
