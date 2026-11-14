// Package ui2 pkg/visor/ui2/tabs.go c3-vis-core
package ui2

import (
	"context"
	"strconv"
)

// Tab is one entry of the per-visor navigation.
type Tab struct {
	Seg   string
	Label string
	Icon  string
}

// VisorTabs follows the Angular UI's node tabs
// (static/skywire-manager-src/src/app/utils/node-tabs.ts), with the DMSG tab
// that Angular reaches by route. A tab without its own ui2 route renders a
// link to the classic one.
var VisorTabs = []Tab{
	{"info", "node.tabs.info", "info"},
	{"routing", "node.tabs.routing", "shuffle"},
	{"transports", "node.tabs.transports", "swap_horiz"},
	{"bandwidth", "node.tabs.bandwidth", "equalizer"},
	{"uptime", "node.tabs.uptime", "schedule"},
	{"apps", "node.tabs.apps", "apps"},
	{"chat", "node.tabs.chat", "forum"},
	{"mail", "node.tabs.mail", "mail"},
	{"contacts", "node.tabs.contacts", "contacts"},
	{"rewards", "node.tabs.rewards", "monetization_on"},
	{"dmsg", "node.tabs.dmsg", "device_hub"},
	{"skynet", "node.tabs.skynet", "public"},
	{"web-proxy", "node.tabs.web-proxy", "language"},
	{"vpn", "node.tabs.vpn", "vpn_lock"},
	{"resources", "node.tabs.resources", "memory"},
	{"terminal", "node.tabs.terminal", "code"},
	{"wallet", "node.tabs.wallet", "account_balance_wallet"},
	{"logs", "node.tabs.logs", "description"},
	{"settings", "node.tabs.settings", "tune"},
}

// TabOf is the tab with segment seg.
func TabOf(seg string) (Tab, bool) {
	for _, t := range VisorTabs {
		if t.Seg == seg {
			return t, true
		}
	}
	return Tab{}, false
}

// VisorURL is the ui2 URL of a visor tab, relative to the page's <base>.
func VisorURL(pk, seg string) string { return "visors/" + pk + "/" + seg }

// ClassicURL is the Angular UI's route for the same visor tab.
func ClassicURL(pk, seg string) string {
	if pk == "" {
		return "#/nodes"
	}
	return "#/nodes/" + pk + "/" + seg
}

func tabTitle(ctx context.Context, seg string) string {
	if t, ok := TabOf(seg); ok {
		return T(ctx, t.Label)
	}
	return seg
}

func itoa(n int) string { return strconv.Itoa(n) }
