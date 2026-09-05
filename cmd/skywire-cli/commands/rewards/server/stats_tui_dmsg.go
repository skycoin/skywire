// Package clirewardsserver cmd/skywire-cli/commands/rewards/server/stats_tui_dmsg.go c5-reward-server
//
// The dmsg layer, in the terminal panel.
//
// Nothing on the reward site showed dmsg at all: the transport and uptime
// views describe what the network carries, and none of it describes the
// substrate carrying it. A saturated dmsg server is invisible in every
// existing chart, yet it silently pushes clients elsewhere.
package clirewardsserver

import (
	"fmt"
	"sort"
	"strings"

	"github.com/skycoin/skywire/deployment"
)

// dmsgServerRow is one registered dmsg server as discovery describes it.
type dmsgServerRow struct {
	PK      string
	Address string
	Version string
	// Available is REMAINING capacity, not sessions in use. Discovery excludes
	// a server at zero from available_servers by design, so a server reading 0
	// here is FULL, not idle — the inversion that made a saturated server look
	// like a dead one.
	Available int
	// Listed reports whether the server appears in available_servers.
	Listed bool
}

type dmsgStats struct {
	Servers []dmsgServerRow
	// Entries is the number of client entries registered in discovery.
	Entries    int
	EntriesErr string
	ServersErr string
}

// discServerEntry is the shape dmsg-discovery returns per server.
type discServerEntry struct {
	Static string `json:"static"`
	// Version carries the build string. Before #4521 every server advertised a
	// bare release tag, so a blank or tag-only value here means the server has
	// not restarted onto a build that reports its commit.
	Version string `json:"version"`
	Server  struct {
		Address           string `json:"address"`
		AvailableSessions int    `json:"availableSessions"`
	} `json:"server"`
}

// gatherDmsgStats reads the dmsg-discovery views the panel draws. As with the
// other sources, a failure is recorded rather than returned.
func gatherDmsgStats() dmsgStats {
	var d dmsgStats
	base := strings.TrimSuffix(deployment.Prod.DmsgDiscovery, "/")

	var all, avail []discServerEntry
	if err := statsGetJSON(base+"/dmsg-discovery/all_servers", &all); err != nil {
		d.ServersErr = err.Error()
	} else {
		listed := make(map[string]bool)
		if err := statsGetJSON(base+"/dmsg-discovery/available_servers", &avail); err == nil {
			for _, e := range avail {
				listed[e.Static] = true
			}
		}
		for _, e := range all {
			d.Servers = append(d.Servers, dmsgServerRow{
				PK: e.Static, Address: e.Server.Address, Version: e.Version,
				Available: e.Server.AvailableSessions, Listed: listed[e.Static],
			})
		}
		// Fullest first: a server at zero spare is the one worth seeing.
		sort.Slice(d.Servers, func(i, j int) bool {
			if d.Servers[i].Available != d.Servers[j].Available {
				return d.Servers[i].Available < d.Servers[j].Available
			}
			return d.Servers[i].PK < d.Servers[j].PK
		})
	}

	var entries []string
	if err := statsGetJSON(base+"/dmsg-discovery/entries", &entries); err != nil {
		d.EntriesErr = err.Error()
	} else {
		d.Entries = len(entries)
	}
	return d
}

// renderDmsgPanelANSI draws the dmsg substrate panel.
func renderDmsgPanelANSI(d dmsgStats) string {
	width := tuiWidth
	var b strings.Builder

	if d.ServersErr != "" {
		return tuiMissing("DMSG SERVERS", d.ServersErr) + "\n"
	}
	if len(d.Servers) == 0 {
		return tuiMissing("DMSG SERVERS", "no data returned") + "\n"
	}

	b.WriteString(tuiRule("DMSG SERVERS — spare capacity", width))

	// Capacity remaining, NOT load. Labeled explicitly because reading these
	// as sessions-in-use inverts the meaning: it makes the fullest server look
	// like the emptiest.
	maxAvail := 0
	for _, s := range d.Servers {
		if s.Available > maxAvail {
			maxAvail = s.Available
		}
	}
	full := 0
	for _, s := range d.Servers {
		frac := 0.0
		if maxAvail > 0 {
			frac = float64(s.Available) / float64(maxAvail)
		}
		col := aGreen
		state := aDim + "accepting" + aReset
		if !s.Listed || s.Available <= 0 {
			col = aRed
			state = aRed + "FULL" + aReset
			full++
		} else if frac < 0.25 {
			col = aYellow
		}
		ver := s.Version
		if ver == "" {
			ver = "—"
		}
		b.WriteString(fmt.Sprintf("  %s%-21s%s %s%6d%s %s  %s\n",
			aDim, s.Address, aReset, aBold, s.Available, aReset,
			tuiBar(frac, 20, col), state))
		b.WriteString(fmt.Sprintf("  %s%s  %s%s\n", aDim, s.PK, ver, aReset))
	}
	b.WriteString(fmt.Sprintf("  %s%d servers · %d at capacity · figures are spare slots, not load%s\n",
		aDim, len(d.Servers), full, aReset))
	b.WriteString(tuiClose(width))
	b.WriteString("\n")

	// Client entries.
	if d.EntriesErr != "" {
		b.WriteString(tuiMissing("DMSG CLIENT ENTRIES", d.EntriesErr) + "\n")
	} else {
		b.WriteString(tuiRule("DMSG CLIENT ENTRIES", width))
		b.WriteString(fmt.Sprintf("  %sregistered in discovery%s %s%d%s\n",
			aDim, aReset, aBold, d.Entries, aReset))
		b.WriteString(tuiClose(width))
		b.WriteString("\n")
	}
	return b.String()
}
