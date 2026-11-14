// Package visor pkg/visor/hypervisor_handlers_ui2_terminal.go c3-vis-core
package visor

// The terminal tab hosts the existing dmsgpty page.

import (
	"net/http"

	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/visor/ui2"
)

func (hv *Hypervisor) ui2TerminalRoutes(r *httputil.Router) {
	r.Get("/visors/{pk}/terminal", hv.withCtx(hv.ui2VisorCtx, func(w http.ResponseWriter, r *http.Request, _ *httpCtx) {
		hv.render(w, r, ui2.TerminalPage(r.PathValue("pk")))
	}))
}
