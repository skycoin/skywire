//go:build js

// Package deskhost pkg/wasmhv/deskhost/desk_install_js.go c5-wasm-desk
//
// The PWA install offer as an Applications entry.
//
// The page shell used to draw this as a fixed button at the bottom right,
// which is exactly where the desk panel is — so the one affordance for
// installing the desk was obscured by the desk. The shell now publishes the
// offer on window.__skywireInstall and this registers it in the launcher,
// beside the other things a user can start.
package deskhost

import (
	"fmt"
	"syscall/js"

	"github.com/0magnet/desk"
)

// installAppName is the launcher entry's name; also what `Launch("install")`
// takes, so the desk's own command surface can trigger it.
const installAppName = "install"

// registerInstallApp publishes the install entry once the browser has offered
// one, and not before: an entry that silently does nothing is worse than no
// entry, and beforeinstallprompt does not fire when the app is already
// installed, when the page is not a secure context, or on a browser without
// PWA install at all.
//
// It checks the CURRENT state first and only then listens. The event fires
// about a second after load, while this module is inside a ~170 MB wasm binary
// that takes far longer to boot — so in practice the offer has always already
// happened and the listener is just the cheap safety net for the reverse order.
func registerInstallApp() {
	offer := js.Global().Get("__skywireInstall")
	if !offer.Truthy() {
		return
	}
	if avail := offer.Get("available"); avail.Truthy() {
		if ok := avail.Invoke(); ok.Truthy() {
			addInstallApp()
			return
		}
	}
	js.Global().Get("window").Call("addEventListener", "skywire-install-available",
		js.FuncOf(func(js.Value, []js.Value) any {
			addInstallApp()
			return nil
		}))
}

// addInstallApp registers the launcher entry. Run rather than Open: installing
// is a browser prompt, not a window the desk owns, and desk.App.Run exists for
// exactly that — no pane is built and no window opened.
func addInstallApp() {
	desk.Register(desk.App{
		Name:  installAppName,
		Title: "Install Skywire",
		Help:  "install this desk as an app: it opens on its own and keeps working when this address is out of reach",
		Run: func([]string) error {
			offer := js.Global().Get("__skywireInstall")
			if !offer.Truthy() {
				return fmt.Errorf("install: the page offered no install prompt")
			}
			offer.Call("prompt")
			return nil
		},
	})
}
