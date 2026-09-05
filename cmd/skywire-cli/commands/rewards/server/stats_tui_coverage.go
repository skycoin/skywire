// Package clirewardsserver cmd/skywire-cli/commands/rewards/server/stats_tui_coverage.go c5-reward-server
//
// Service reachability across the dmsg servers.
//
// The deployment services deliberately do NOT register client entries in
// dmsg-discovery — an entry lookup for TPD or SD returns 404. That is the
// design: a service should not cost every caller a discovery round-trip, it
// should be equally reachable through whichever dmsg server the caller already
// holds a session with. Callers reach them through seeded delegated-server
// entries instead.
//
// That design has a failure mode nothing was checking. If a service is
// connected to only some of the dmsg servers, it stays perfectly reachable for
// clients on those servers and quietly unreachable for the rest — no error
// anywhere, just a subset of the network unable to resolve it. Since the
// service publishes no entry, discovery cannot report the gap either.
//
// Each service's /health lists the dmsg servers it currently holds sessions
// with. Comparing that against the registered server set turns an invisible
// partial connection into a number.
package clirewardsserver

import (
	"fmt"
	"sort"
	"strings"

	"github.com/skycoin/skywire/deployment"
)

// serviceCoverage is one service measured against the dmsg server set.
type serviceCoverage struct {
	Name string
	PK   string
	// Connected is how many of the registered servers the service reports
	// sessions with; Total is how many are registered.
	Connected int
	Total     int
	// Missing lists the servers it is NOT connected to, which is the
	// actionable part: those are the servers whose clients cannot reach it.
	Missing []string
	Err     string
}

type coverageStats struct {
	Services []serviceCoverage
	Err      string
}

// healthDoc is the slice of a service's /health that matters here.
type healthDoc struct {
	DmsgServers []string `json:"dmsg_servers"`
}

// gatherServiceCoverage asks each dmsg-addressed service which servers it is
// connected to and compares against the registered set.
func gatherServiceCoverage() coverageStats {
	var c coverageStats

	var all []discServerEntry
	base := strings.TrimSuffix(deployment.Prod.DmsgDiscovery, "/")
	if err := statsGetJSON(base+"/dmsg-discovery/all_servers", &all); err != nil {
		c.Err = err.Error()
		return c
	}
	registered := make([]string, 0, len(all))
	for _, e := range all {
		registered = append(registered, e.Static)
	}
	sort.Strings(registered)

	// Only the dmsg-addressed services: a clearnet service has no dmsg
	// sessions to account for.
	//
	// The uptime tracker is NOT listed: it is TPD-integrated (CXO-backed v3), so
	// there is no standalone tracker to probe. deployment.Prod.UptimeTrackerDmsg
	// still names the retired standalone service, and probing it reported
	// "unreachable ... entry is not found in discovery" on every page load — an
	// alarming red row for a service that is not supposed to exist. Uptime
	// reachability is covered by the transport-discovery entry above.
	for _, s := range []struct{ name, url string }{
		{"transport-discovery", deployment.Prod.TransportDiscoveryDmsg},
		{"service-discovery", deployment.Prod.ServiceDiscoveryDmsg},
		{"address-resolver", deployment.Prod.AddressResolverDmsg},
		{"route-finder", deployment.Prod.RouteFinderDmsg},
		{"dmsg-discovery", deployment.Prod.DmsgDiscoveryDmsg},
	} {
		if s.url == "" {
			continue
		}
		cov := serviceCoverage{Name: s.name, PK: pkFromDmsgAddr(s.url), Total: len(registered)}
		var h healthDoc
		if err := statsGetJSON(strings.TrimSuffix(s.url, "/")+"/health", &h); err != nil {
			cov.Err = err.Error()
			c.Services = append(c.Services, cov)
			continue
		}
		have := make(map[string]bool, len(h.DmsgServers))
		for _, pk := range h.DmsgServers {
			have[pk] = true
		}
		for _, pk := range registered {
			if have[pk] {
				cov.Connected++
			} else {
				cov.Missing = append(cov.Missing, pk)
			}
		}
		c.Services = append(c.Services, cov)
	}
	return c
}

// pkFromDmsgAddr pulls the public key out of a dmsg://<pk>:<port> address.
func pkFromDmsgAddr(u string) string {
	s := strings.TrimPrefix(u, "dmsg://")
	if i := strings.IndexByte(s, ':'); i > 0 {
		s = s[:i]
	}
	if i := strings.IndexByte(s, '/'); i > 0 {
		s = s[:i]
	}
	return s
}

// renderCoveragePanelANSI draws the service-reachability panel.
func renderCoveragePanelANSI(c coverageStats) string {
	width := tuiWidth
	if c.Err != "" {
		return tuiMissing("SERVICE REACHABILITY", c.Err) + "\n"
	}
	if len(c.Services) == 0 {
		return tuiMissing("SERVICE REACHABILITY", "no dmsg-addressed services configured") + "\n"
	}

	var b strings.Builder
	b.WriteString(tuiRule("SERVICE REACHABILITY — dmsg servers connected", width))

	gaps := 0
	for _, s := range c.Services {
		if s.Err != "" {
			b.WriteString(fmt.Sprintf("  %s%-20s%s %sunreachable%s %s%s%s\n",
				aDim, s.Name, aReset, aRed, aReset, aDim, s.Err, aReset))
			gaps++
			continue
		}
		frac := 0.0
		if s.Total > 0 {
			frac = float64(s.Connected) / float64(s.Total)
		}
		col := aGreen
		if s.Connected < s.Total {
			col = aRed
			gaps++
		}
		b.WriteString(fmt.Sprintf("  %s%-20s%s %s%2d/%-2d%s %s\n",
			aDim, s.Name, aReset, aBold, s.Connected, s.Total, aReset, tuiBar(frac, 24, col)))
		b.WriteString(fmt.Sprintf("  %s%s%s\n", aDim, s.PK, aReset))
		// Name the servers it cannot be reached through: those are precisely
		// the clients that cannot resolve it, and nothing else reports them.
		for _, pk := range s.Missing {
			b.WriteString(fmt.Sprintf("    %sunreachable via %s%s\n", aRed, pk, aReset))
		}
	}
	if gaps == 0 {
		b.WriteString(fmt.Sprintf("  %severy service reachable through every dmsg server%s\n", aDim, aReset))
	} else {
		b.WriteString(fmt.Sprintf("  %s%d service(s) not reachable through every server — clients on the "+
			"missing servers cannot resolve them%s\n", aRed, gaps, aReset))
	}
	b.WriteString(tuiClose(width))
	b.WriteString("\n")
	return b.String()
}
