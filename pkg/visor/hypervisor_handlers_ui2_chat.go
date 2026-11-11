// Package visor pkg/visor/hypervisor_handlers_ui2_chat.go c3-vis-core
package visor

// The chat tab hosts the Angular chat tab in its embed mode. Chat and
// voice are not reimplemented here.

import (
	"net/http"

	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/visor/ui2"
)

func (hv *Hypervisor) ui2ChatRoutes(r *httputil.Router) {
	r.Get("/ui2/visors/{pk}/chat", hv.withCtx(hv.ui2VisorCtx, func(w http.ResponseWriter, r *http.Request, _ *httpCtx) {
		hv.render(w, r, ui2.ChatPage(r.PathValue("pk")))
	}))
}
