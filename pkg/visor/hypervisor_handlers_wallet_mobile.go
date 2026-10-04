//go:build mobile

// Package visor pkg/visor/hypervisor_handlers_wallet_mobile.go c3-vis-core
// Mobile build: the browser wallet is desktop-only. Tagging out the real
// handler file keeps the wallet's TinyGo cipher out of the binary; the phone
// wallet is native app-side code and adds no Go payload.
package visor

import (
	"net/http"
)

// walletHandler answers 404 for the whole /wallet/* mount on the mobile build.
func (hv *Hypervisor) walletHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "wallet UI not embedded in this build", http.StatusNotFound)
	}
}

// walletCipherHandler answers 404 for the dashboard wallet's cipher on the
// mobile build, which has no browser wallet.
func (hv *Hypervisor) walletCipherHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "wallet cipher not embedded in this build", http.StatusNotFound)
	}
}
