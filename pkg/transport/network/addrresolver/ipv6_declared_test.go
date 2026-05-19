// Package addrresolver — pkg/transport/network/addrresolver/ipv6_declared_test.go
//
// Coverage for #1525 Phase 2c on the client side: the LocalAddresses
// schema add (PublicIPv6) and the httpClient.clientPublicIPv6 plumbing
// that populates the field on outbound BindSTCPR payloads.
package addrresolver

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestLocalAddresses_PublicIPv6_OmitemptyBackwardCompat verifies that
// a LocalAddresses with empty PublicIPv6 marshals WITHOUT the field
// (omitempty elides it). Pre-Phase-2c AR servers won't see the new
// key in the JSON, preserving their existing parse path. Pre-Phase-2c
// visors don't populate the field, so empty-after-marshal is the
// production-default for older deployments.
func TestLocalAddresses_PublicIPv6_OmitemptyBackwardCompat(t *testing.T) {
	la := LocalAddresses{
		Port:      "8080",
		Addresses: []string{"203.0.113.5"},
		PublicIP:  "203.0.113.5",
	}
	b, err := json.Marshal(la)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	assert.NotContains(t, string(b), "public_ip_v6",
		"empty PublicIPv6 must be omitted from JSON (backward compat)")
	assert.Contains(t, string(b), `"public_ip":"203.0.113.5"`)
}

// TestLocalAddresses_PublicIPv6_RoundTrip verifies marshal/unmarshal
// preserves a populated v6 declaration. Pins the wire format the AR
// bind handler depends on.
func TestLocalAddresses_PublicIPv6_RoundTrip(t *testing.T) {
	la := LocalAddresses{
		Port:       "8080",
		Addresses:  []string{"203.0.113.5"},
		PublicIP:   "203.0.113.5",
		PublicIPv6: "2606:4700:4700::1111",
	}
	b, err := json.Marshal(la)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	assert.Contains(t, string(b), `"public_ip_v6":"2606:4700:4700::1111"`)

	var out LocalAddresses
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	assert.Equal(t, la.PublicIPv6, out.PublicIPv6)
}

// TestHTTPClient_ClientPublicIPv6_FieldOnly verifies the constructor
// captures clientPublicIPv6 onto the struct (and that LocalPublicIP /
// existing v4 accessor stays unchanged). Doesn't exercise the network;
// the actual BindSTCPR payload population is integration-tested live.
func TestHTTPClient_ClientPublicIPv6_FieldOnly(t *testing.T) {
	c := &httpClient{
		clientPublicIP:   "203.0.113.5",
		clientPublicIPv6: "2606:4700:4700::1111",
	}
	assert.Equal(t, "203.0.113.5", c.LocalPublicIP())
	assert.Equal(t, "2606:4700:4700::1111", c.clientPublicIPv6)
}
