// Package clirewardsserver cmd/skywire-cli/commands/rewards/server/stats_tui_coverage_test.go c5-reward-server
package clirewardsserver

import (
	"strings"
	"testing"
)

const (
	srvA = "0326978f5a53aff537dbb47fed58b1f123af3b00132d365f1309a14db4168dcff7"
	srvB = "02a49bc0aa1b5b78f638e9189be4ed095bac5d6839c828465a8350f80ac07629c0"
	svc  = "02b307aee5c8ce1666c63891f8af25ad2f0a47a243914c963942b3ba35b9d095ae"
)

// A service connected to every server is the healthy case and must read as
// such without qualification.
func TestCoveragePanelReportsFullReachability(t *testing.T) {
	out := renderCoveragePanelANSI(coverageStats{Services: []serviceCoverage{
		{Name: "transport-discovery", PK: svc, Connected: 9, Total: 9},
	}})
	if !strings.Contains(out, "9/9") {
		t.Error("the connected/total figure is missing")
	}
	if !strings.Contains(out, "every service reachable through every dmsg server") {
		t.Error("full coverage was not stated")
	}
	if strings.Contains(out, "unreachable via") {
		t.Error("a fully-connected service was reported as having gaps")
	}
}

// The failure this panel exists to catch: a service on only some servers stays
// perfectly reachable for clients on those servers and silently unresolvable
// for the rest. Since services publish no discovery entry, nothing else
// reports it — so the missing servers must be named, not merely counted.
func TestCoveragePanelNamesUnreachableServers(t *testing.T) {
	out := renderCoveragePanelANSI(coverageStats{Services: []serviceCoverage{
		{Name: "service-discovery", PK: svc, Connected: 1, Total: 2, Missing: []string{srvB}},
	}})
	if !strings.Contains(out, "1/2") {
		t.Error("the partial figure is missing")
	}
	if !strings.Contains(out, srvB) {
		t.Error("the unreachable server was not named in full")
	}
	if !strings.Contains(out, "cannot resolve them") {
		t.Error("the consequence of the gap was not stated")
	}
}

// A service whose health cannot be fetched is unknown, not healthy: reporting
// it as fully connected would be worse than saying nothing.
func TestCoveragePanelMarksUnreachableService(t *testing.T) {
	out := renderCoveragePanelANSI(coverageStats{Services: []serviceCoverage{
		{Name: "route-finder", PK: svc, Err: "request failed: EOF"},
	}})
	if !strings.Contains(out, "unreachable") || !strings.Contains(out, "EOF") {
		t.Error("a service whose health failed was not reported")
	}
	if strings.Contains(out, "every service reachable") {
		t.Error("a service with no health data was counted as healthy")
	}
}

// Whole keys: an operator matching a server against their own config needs all
// 66 characters.
func TestCoveragePanelPrintsFullKeys(t *testing.T) {
	out := renderCoveragePanelANSI(coverageStats{Services: []serviceCoverage{
		{Name: "transport-discovery", PK: svc, Connected: 1, Total: 2, Missing: []string{srvA}},
	}})
	for _, k := range []string{svc, srvA} {
		if !strings.Contains(out, k) {
			t.Errorf("key %s was not printed in full", k)
		}
	}
}

// A failed server list, or none configured, must say so rather than render an
// empty panel that reads as "nothing wrong".
func TestCoveragePanelNamesItsOwnFailure(t *testing.T) {
	if out := renderCoveragePanelANSI(coverageStats{Err: "status 502"}); !strings.Contains(out, "502") {
		t.Error("the panel did not report why it had no data")
	}
	if out := renderCoveragePanelANSI(coverageStats{}); !strings.Contains(out, "unavailable") {
		t.Error("an empty panel did not report itself absent")
	}
}

func TestPKFromDmsgAddr(t *testing.T) {
	for in, want := range map[string]string{
		"dmsg://" + svc + ":80":  svc,
		"dmsg://" + svc + ":80/": svc,
		"dmsg://" + svc:          svc,
		"":                       "",
	} {
		if got := pkFromDmsgAddr(in); got != want {
			t.Errorf("pkFromDmsgAddr(%q) = %q, want %q", in, got, want)
		}
	}
}
