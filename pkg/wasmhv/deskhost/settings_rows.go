// Package deskhost pkg/wasmhv/deskhost/settings_rows.go c4-wasm-desk
package deskhost

import (
	"strings"

	"github.com/skycoin/skywire/pkg/skywireconfig/skyenvform"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

// deskOwned are the variables desk-boot.js sets on every load, so an edit
// would last one boot. The hypervisor serves the desk's browser; the attach
// pair is chosen per load.
var deskOwned = map[string]bool{"ISHYPERVISOR": true, "DISABLEPUBLICAUTOCONN": true, "WSPEERS": true}

// hostOnly reports variables that do nothing in a browser tab, which cannot
// listen on a port, open a TUN device or run a separate daemon.
func hostOnly(key string) bool {
	switch key {
	case "BINPATH", "STCPRPORT", "SUDPHPORT", "TRANSPORTPORT", "LANDMSGPORT", "LANDMSGPUBLIC",
		"DMSGSERVER", "DMSGSERVERCONF", "DMSGSERVERPUBLIC", "DMSGSERVERWSTLS",
		"VPNSERVER", "VPNSEVERNETIFC", "VPNSEVERSECURE", "VPNSERVERWL", "SHUTDOWNTIMEOUT":
		return true
	}
	return strings.HasPrefix(key, "VPNROUTER") || strings.HasPrefix(key, "SKYCOIND")
}

// settingRows is the settings window's rows, the desk's own variables locked.
func settingRows(st visorapi.SkyenvState) []skyenvform.Row {
	rows := skyenvform.Rows(st)
	for i := range rows {
		rows[i].Locked = deskOwned[rows[i].Key]
	}
	return rows
}
