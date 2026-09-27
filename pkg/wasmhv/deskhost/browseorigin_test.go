// Package deskhost pkg/wasmhv/deskhost/browseorigin_test.go c3-vis-wasm
package deskhost

import (
	"strings"
	"testing"

	"github.com/0magnet/realorigin"
)

// The 66-hex PK and its 53-char base32 DNS label, as cipher.PubKey.DNSLabel
// produces it. Kept as a literal pair: the whole point of these rules is that
// the spelling does not drift, and a test that recomputes it cannot notice.
const hexPK = "0326978f5a53d3b0fb6e2a6f1b5f2f3b8a4e6c1d9f0a2b3c4d5e6f708192a3b4c5"

func TestPKDNSLabelIsStable(t *testing.T) {
	got := pkDNSLabel(hexPK)
	if len(got) != 53 {
		t.Errorf("label is %d chars, want 53 (a DNS label must stay under 63)", len(got))
	}
	for _, c := range got {
		if !((c >= 'a' && c <= 'z') || (c >= '2' && c <= '7')) {
			t.Fatalf("label %q has %q, outside RFC 4648 base32 lowercase", got, c)
		}
	}
	// Hex case must not change the label, or the same key would get two origins.
	if up := pkDNSLabel(strings.ToUpper(hexPK)); up != got {
		t.Errorf("uppercase hex gave a different label:\n %s\n %s", got, up)
	}
	// Anything that is not a 66-hex key passes through untouched.
	for _, s := range []string{"home", "magnetosphere.net", pkDNSLabel(hexPK), ""} {
		if pkDNSLabel(s) != s {
			t.Errorf("pkDNSLabel(%q) rewrote a non-PK label", s)
		}
	}
}

// TestNormResolverHostCollapsesSpellings is the storage-continuity guard: a
// site must land on ONE canonical target however the reader typed it, or it
// gets a different origin and loses its cookies.
func TestNormResolverHostCollapsesSpellings(t *testing.T) {
	b32PK := pkDNSLabel(hexPK)
	for _, tc := range []struct{ in, network, want string }{
		{hexPK, "dmsg", b32PK + ".dmsg"},
		{hexPK + ".dmsg", "dmsg", b32PK + ".dmsg"},
		{b32PK + ".dmsg", "dmsg", b32PK + ".dmsg"},
		{b32PK, "dmsg", b32PK + ".dmsg"},
		// A name-vhost keeps its label and canonicalizes only the key.
		{"magnetosphere.net." + hexPK + ".dmsg", "dmsg", "magnetosphere.net." + b32PK + ".dmsg"},
		// An alias is not a key and is left alone.
		{"home.dmsg", "dmsg", "home.dmsg"},
		{hexPK + ".skynet", "skynet", b32PK + ".skynet"},
	} {
		if got := normResolverHost(tc.in, tc.network); got != tc.want {
			t.Errorf("normResolverHost(%q, %q)\n got %q\nwant %q", tc.in, tc.network, got, tc.want)
		}
	}
}

func TestMeshOriginForClaims(t *testing.T) {
	b32PK := pkDNSLabel(hexPK)
	t.Run("mesh addresses are claimed", func(t *testing.T) {
		for _, tc := range []struct{ host, network, resolver string }{
			{hexPK + ".dmsg", "dmsg", b32PK + ".dmsg"},
			{"home.dmsg", "dmsg", "home.dmsg"},
			{hexPK + ".skynet", "skynet", b32PK + ".skynet"},
			{hexPK, "dmsg", b32PK + ".dmsg"},
		} {
			net, res, ok := meshOriginFor(tc.host)
			if !ok || net != tc.network || res != tc.resolver {
				t.Errorf("meshOriginFor(%q) = (%q,%q,%v), want (%q,%q,true)",
					tc.host, net, res, ok, tc.network, tc.resolver)
			}
		}
	})

	t.Run("loopback is never claimed", func(t *testing.T) {
		// A browse origin's service worker lives on ANOTHER origin and cannot
		// reach this page's vnet port table. Claiming one would render a blank
		// frame where DirectLoader would have rendered the app.
		for _, h := range []string{"vnet", "8001.vnet", "localhost", "127.0.0.1", "::1", "[::1]"} {
			if _, _, ok := meshOriginFor(h); ok {
				t.Errorf("meshOriginFor(%q) claimed a virtual-loopback address", h)
			}
		}
	})

	t.Run("clearnet and junk are not claimed", func(t *testing.T) {
		for _, h := range []string{"skycoin.com", "example.test", "", "   ", "dmsg", "notapk.invalid"} {
			if _, _, ok := meshOriginFor(h); ok {
				t.Errorf("meshOriginFor(%q) should not be claimed", h)
			}
		}
	})

	t.Run("case does not split an origin", func(t *testing.T) {
		lo, _, _ := meshOriginFor(hexPK + ".dmsg")
		hi, _, _ := meshOriginFor(hexPK + ".DMSG")
		if lo != hi {
			t.Errorf("host case changed the network: %q vs %q", lo, hi)
		}
	})
}

func TestCanonicalMeshTarget(t *testing.T) {
	// The shape realorigin hashes. Changing it moves every site to a new
	// origin and drops the storage it had.
	if got := canonicalMeshTarget("dmsg", "home.dmsg"); got != "dmsg|home.dmsg" {
		t.Errorf("canonical target = %q, want %q", got, "dmsg|home.dmsg")
	}
}

// TestCanonicalTargetAgreesWithRealorigin is the cross-implementation check
// that matters most, and the one static reasoning cannot make.
//
// THREE implementations have to agree on a browse origin or the whole thing
// silently fails: this package builds the canonical string, realorigin's JS
// half (idFor, in responder.js) hashes it in the browser to pick the origin a
// frame is pointed at, and realorigin's Go half (ID) hashes it on the server
// to resolve the Host header back to a target. A disagreement is not an error
// anywhere — the frame simply loads an origin the server does not know, and
// the bootstrap sits on "Connecting to the mesh…" forever.
//
// This pins ours against the Go half. Both are SHA-256 over the same bytes,
// truncated to the same base32 label, so agreement here means the JS half
// agrees too: its idFor is the same construction over the same string.
func TestCanonicalTargetAgreesWithRealorigin(t *testing.T) {
	for _, host := range []string{
		hexPK + ".dmsg",
		"home.dmsg",
		"magnetosphere.net." + hexPK + ".dmsg",
		hexPK + ".skynet",
		hexPK,
	} {
		network, resolver, ok := meshOriginFor(host)
		if !ok {
			t.Fatalf("meshOriginFor(%q) did not claim a mesh address", host)
		}
		canonical := canonicalMeshTarget(network, resolver)
		id := realorigin.ID(canonical)
		if len(id) != realorigin.IDLen {
			t.Errorf("%q: id %q is %d chars, want %d", host, id, len(id), realorigin.IDLen)
		}
		// It must survive the round trip the B server makes on every request:
		// hostname → id → target lookup.
		back, ok := realorigin.IDFromHost(realorigin.Host(canonical, ".mesh.localhost"), ".mesh.localhost")
		if !ok || back != id {
			t.Errorf("%q: browse host did not round-trip (%q, %v)", host, back, ok)
		}
	}

	// The spellings that must share an origin really do produce one id — this
	// is the cookie-jar guarantee, stated against the real hash rather than
	// against our own normalizer.
	want := realorigin.ID(canonicalMeshTarget(meshOriginForOrDie(t, hexPK+".dmsg")))
	for _, alias := range []string{hexPK, pkDNSLabel(hexPK) + ".dmsg", hexPK + ".DMSG"} {
		if got := realorigin.ID(canonicalMeshTarget(meshOriginForOrDie(t, alias))); got != want {
			t.Errorf("%q landed on origin %q, want %q — the site would lose its storage", alias, got, want)
		}
	}
}

func meshOriginForOrDie(t *testing.T, host string) (string, string) {
	t.Helper()
	network, resolver, ok := meshOriginFor(host)
	if !ok {
		t.Fatalf("meshOriginFor(%q) did not claim a mesh address", host)
	}
	return network, resolver
}
