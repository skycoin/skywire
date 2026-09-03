//go:build js

package visor

import "syscall/js"

// wsPageAllowed reports whether plain-ws dials are usable from this page. An
// HTTPS origin blocks ws:// as mixed content — every WS-transport dial would
// throw a SecurityError instantly — so the autoconnect's WS phase is pointless
// there (peer visors serve plain ws on their stcpr port, not wss). An http
// origin (the local desk, a LAN deploy) dials ws freely. Non-browser js
// runtimes (no location) default to allowed.
func wsPageAllowed() bool {
	loc := js.Global().Get("location")
	if !loc.Truthy() {
		return true
	}
	return loc.Get("protocol").String() != "https:"
}
