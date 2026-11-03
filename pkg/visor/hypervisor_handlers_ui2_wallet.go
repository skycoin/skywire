// Package visor pkg/visor/hypervisor_handlers_ui2_wallet.go c3-vis-core
package visor

// The wallet tab links to the dashboard wallet and hosts its node settings.

import (
	"net/http"

	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/visor/ui2"
)

func (hv *Hypervisor) ui2WalletRoutes(r *httputil.Router) {
	r.Get("/ui2/visors/{pk}/wallet", hv.withCtx(hv.ui2VisorCtx, func(w http.ResponseWriter, r *http.Request, _ *httpCtx) {
		hv.render(w, r, ui2.WalletPage(r.PathValue("pk")))
	}))
}
