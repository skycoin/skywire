package visor

import (
	"testing"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/deployment/ar/arfeed"
	"github.com/skycoin/skywire/pkg/transport/network/addrresolver"
)

func sudphBinding(public, lan string) *arfeed.PeerBindings {
	d := &addrresolver.VisorData{RemoteAddr: public + ":30000"}
	if lan != "" {
		d.Addresses = []string{lan + ":7777"}
	}
	return &arfeed.PeerBindings{SUDPH: d}
}

// testSite adds n visors behind public with LAN addresses in lan (a /24
// prefix such as "192.168.1") to bindings and returns their keys.
func testSite(bindings map[string]*arfeed.PeerBindings, n int, public, lan string) []cipher.PubKey {
	pks := make([]cipher.PubKey, n)
	for i := range pks {
		pks[i], _ = cipher.GenerateKeyPair()
		addr := ""
		if lan != "" {
			addr = lan + "." + string(rune('2'+i))
		}
		bindings[pks[i].Hex()] = sudphBinding(public, addr)
	}
	return pks
}

func TestBuildSites(t *testing.T) {
	b := map[string]*arfeed.PeerBindings{}
	home := testSite(b, 3, "77.1.2.3", "192.168.1")
	cgnatA := testSite(b, 1, "100.70.0.1", "192.168.1")
	cgnatB := testSite(b, 1, "100.70.0.1", "10.0.0")
	noLAN := testSite(b, 1, "88.1.1.1", "")
	sm := buildSites(b)

	if sm.site[home[0]] != sm.site[home[1]] || sm.site[home[0]] != sm.site[home[2]] {
		t.Error("visors behind one IP on one subnet should be one site")
	}
	if sm.site[cgnatA[0]] == sm.site[cgnatB[0]] {
		t.Error("visors behind one IP on different subnets should be different sites")
	}
	if _, ok := sm.site[noLAN[0]]; ok {
		t.Error("a visor that advertises no LAN address should be in no site")
	}
}

// TestSiteAllowsKPairsAgreedFromBothEnds: between two sites of eight, exactly
// k member pairs may dial, and both ends of a pair reach the same answer.
func TestSiteAllowsKPairsAgreedFromBothEnds(t *testing.T) {
	b := map[string]*arfeed.PeerBindings{}
	s1 := testSite(b, 8, "77.1.2.3", "192.168.1")
	s2 := testSite(b, 8, "88.4.5.6", "192.168.0")
	sm := buildSites(b)
	for _, k := range []int{1, 2, 5} {
		allowed := 0
		for _, a := range s1 {
			for _, c := range s2 {
				ab, ba := sm.siteAllows(a, c, k, nil), sm.siteAllows(c, a, k, nil)
				if ab != ba {
					t.Fatalf("k=%d: the two ends disagree on a pair", k)
				}
				if ab {
					allowed++
				}
			}
		}
		if allowed != k {
			t.Errorf("k=%d: %d pairs allowed, want %d", k, allowed, k)
		}
	}
}

func TestSiteAllowsLeavesUnlimitedCases(t *testing.T) {
	b := map[string]*arfeed.PeerBindings{}
	s1 := testSite(b, 4, "77.1.2.3", "192.168.1")
	s2 := testSite(b, 4, "88.4.5.6", "192.168.0")
	stranger, _ := cipher.GenerateKeyPair()
	sm := buildSites(b)
	if !sm.siteAllows(s1[0], s1[1], 1, nil) {
		t.Error("siblings should never be limited")
	}
	if !sm.siteAllows(s1[0], stranger, 1, nil) {
		t.Error("a peer in no known site should not be limited")
	}
	for _, c := range s2 {
		if !sm.siteAllows(s1[0], c, 0, nil) {
			t.Error("k=0 should turn the limit off")
		}
	}
}

// TestSiteAllowsSkipsIneligibleMembers: members that cannot take the type do
// not use up the site's links.
func TestSiteAllowsSkipsIneligibleMembers(t *testing.T) {
	b := map[string]*arfeed.PeerBindings{}
	s1 := testSite(b, 6, "77.1.2.3", "192.168.1")
	s2 := testSite(b, 6, "88.4.5.6", "192.168.0")
	sm := buildSites(b)
	ok := map[cipher.PubKey]bool{s1[0]: true, s1[1]: true, s2[0]: true, s2[1]: true}
	eligible := func(pk cipher.PubKey) bool { return ok[pk] }
	allowed := 0
	for _, a := range s1[:2] {
		for _, c := range s2[:2] {
			if sm.siteAllows(a, c, 2, eligible) {
				allowed++
			}
		}
	}
	if allowed != 2 {
		t.Errorf("%d pairs allowed among the eligible members, want 2", allowed)
	}
}
