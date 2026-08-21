// Package clitp cmd/skywire-cli/commands/tp/tp-visor_test.go c4-vis-cli
package clitp

import (
	"testing"

	"github.com/bitfield/script"
)

// TestVisorOnlineJQFilter_PortStrip is the regression guard for the
// "tp v returns 0" bug: the service-discovery visor address carries a
// ":port" suffix while the uptime .pk is the bare public key, so the
// join must strip the port before matching. Without the strip the online
// set never intersects the SD addresses and every visor is dropped.
func TestVisorOnlineJQFilter_PortStrip(t *testing.T) {
	// pkOnline is registered in SD as pk:port and is online in UT.
	// pkOffline is registered but not online. pkPortless exercises an
	// address with no port (defensive: split(":")[0] is a no-op).
	const (
		pkOnline   = "027eac7508598d959e17d4d474d001f0e795e1758f68759eeb74fdb4131c9f9039"
		pkOffline  = "03160010f0b606918129708b9727a11b10282d99b5475f41c0568b49ade3feda9e"
		pkPortless = "03dc3488e49ded9250f3cca2827ab16f6331b7ccefdad614353c785a5fc76c1b13"
	)
	joined := `{
		"sd": [
			{"address": "` + pkOnline + `:9651", "geo": {"country": "US"}, "version": "v1.3.92"},
			{"address": "` + pkOffline + `:7771", "geo": {"country": "SG"}, "version": "v1.3.91"},
			{"address": "` + pkPortless + `", "geo": {"country": "IT"}, "version": "v1.3.91"}
		],
		"ut": [
			{"pk": "` + pkOnline + `", "on": true},
			{"pk": "` + pkOffline + `", "on": false},
			{"pk": "` + pkPortless + `", "on": true}
		]
	}`

	out, err := script.Echo(joined).JQ(visorOnlineJQFilter("", "")).Replace(`"`, "").String()
	if err != nil {
		t.Fatalf("jq filter failed: %v", err)
	}

	if !contains(out, pkOnline) {
		t.Errorf("online visor with :port address was filtered out (port not stripped); got:\n%s", out)
	}
	if !contains(out, pkPortless) {
		t.Errorf("online visor with portless address was filtered out; got:\n%s", out)
	}
	if contains(out, pkOffline) {
		t.Errorf("offline visor leaked into the online-filtered output; got:\n%s", out)
	}
}

// TestVisorOnlineJQFilter_CountryVersion checks the optional narrowing
// conditions compose with the port-stripped join.
func TestVisorOnlineJQFilter_CountryVersion(t *testing.T) {
	const (
		pkUS = "027eac7508598d959e17d4d474d001f0e795e1758f68759eeb74fdb4131c9f9039"
		pkDE = "03160010f0b606918129708b9727a11b10282d99b5475f41c0568b49ade3feda9e"
	)
	joined := `{
		"sd": [
			{"address": "` + pkUS + `:9651", "geo": {"country": "US"}, "version": "v1.3.92"},
			{"address": "` + pkDE + `:7771", "geo": {"country": "DE"}, "version": "v1.3.92"}
		],
		"ut": [
			{"pk": "` + pkUS + `", "on": true},
			{"pk": "` + pkDE + `", "on": true}
		]
	}`

	out, err := script.Echo(joined).JQ(visorOnlineJQFilter("DE", "")).Replace(`"`, "").String()
	if err != nil {
		t.Fatalf("jq filter failed: %v", err)
	}
	if contains(out, pkUS) || !contains(out, pkDE) {
		t.Errorf("country filter did not narrow correctly; got:\n%s", out)
	}
}

// TestOnlineDataUsable covers the fail-open predicate that prevents an
// empty / all-offline uptime payload from silently zeroing the visor list.
func TestOnlineDataUsable(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"empty string", "", false},
		{"empty array", "[]", false},
		{"malformed json", "not json", false},
		{"all offline", `[{"pk":"027eac7508598d959e17d4d474d001f0e795e1758f68759eeb74fdb4131c9f9039","on":false}]`, false},
		{"one online", `[{"pk":"027eac7508598d959e17d4d474d001f0e795e1758f68759eeb74fdb4131c9f9039","on":false},{"pk":"03160010f0b606918129708b9727a11b10282d99b5475f41c0568b49ade3feda9e","on":true}]`, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := onlineDataUsable(c.in); got != c.want {
				t.Errorf("onlineDataUsable(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
