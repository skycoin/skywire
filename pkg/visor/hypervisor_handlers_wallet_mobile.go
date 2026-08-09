//go:build mobile

// Package visor pkg/visor/hypervisor_handlers_wallet_mobile.go c3-vis-core
// Mobile build: the browser wallet is desktop-only. Tagging out the real
// handler file drops the vendored skycoin-web bundle (~11 MB, its only
// importer) from the binary — the phone wallet is native app-side code and
// adds no Go payload.
package visor

import (
	"errors"
	"io/fs"
	"net/http"
)

// WalletUIFS reports the wallet bundle absent on the mobile build (same
// message the desktop handler serves when its embed is missing).
func WalletUIFS() (fs.FS, error) {
	return nil, errors.New("wallet UI not embedded in this build")
}

// walletHandler answers 404 for the whole /wallet/* mount on the mobile build.
func (hv *Hypervisor) walletHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "wallet UI not embedded in this build", http.StatusNotFound)
	}
}
