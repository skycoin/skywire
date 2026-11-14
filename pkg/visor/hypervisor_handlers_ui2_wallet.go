// Package visor pkg/visor/hypervisor_handlers_ui2_wallet.go c3-vis-core
package visor

// The wallet tab hosts skycoin-web's own server rendered wallet, which the
// hypervisor forwards at wallet/web/, and the coin node settings page.

import (
	"net/http"

	"github.com/skycoin/skywire/pkg/app/launcher"
	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/visor/ui2"
)

func (hv *Hypervisor) ui2WalletRoutes(r *httputil.Router) {
	r.Get("/visors/{pk}/wallet", hv.withCtx(hv.ui2VisorCtx, func(w http.ResponseWriter, r *http.Request, _ *httpCtx) {
		hv.render(w, r, ui2.WalletPage(r.PathValue("pk"), hv.ui2WalletState()))
	}))
}

// ui2WalletState is whether the hypervisor's own skycoin-web app serves the
// wallet: running, stopped, or missing from its launcher config.
func (hv *Hypervisor) ui2WalletState() string {
	if launcher.GetHTTPHandler(skyenv.SkycoinWebName) != nil {
		return ui2.WalletRunning
	}
	if hv.selfConn.API == nil {
		return ui2.WalletMissing
	}
	states, err := hv.selfConn.API.Apps()
	if err != nil {
		return ui2.WalletMissing
	}
	for _, s := range states {
		if s != nil && s.Name == skyenv.SkycoinWebName {
			return ui2.WalletStopped
		}
	}
	return ui2.WalletMissing
}
