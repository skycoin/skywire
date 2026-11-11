// Package visor pkg/visor/hypervisor_handlers_ui2_nodes.go c3-vis-core
package visor

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/visor/ui2"
)

// Actions on the visor list. Labels live in the browser, as in the Angular
// UI, so only forgetting an offline visor reaches the server.

func (hv *Hypervisor) ui2NodesRoutes(r *httputil.Router) {
	r.Delete("/ui2/nodes/{pk}", hv.ui2ForgetNodes(false))
	r.Delete("/ui2/nodes", hv.ui2ForgetNodes(true))
}

// ui2ForgetNodes forgets one offline visor, or with all set every offline
// visor this hypervisor manages directly, and answers with the new table.
func (hv *Hypervisor) ui2ForgetNodes(all bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if hv.useCsrf() {
			if err := verifyCSRFToken(r.Header.Get(CSRFHeaderName)); err != nil {
				httputil.WriteJSON(w, r, http.StatusForbidden, err)
				return
			}
		}
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(45 * time.Second)) //nolint:errcheck
		var pks []cipher.PubKey
		if !all {
			pk, err := pkFromParam(r, "pk")
			if err != nil {
				httputil.WriteJSON(w, r, http.StatusBadRequest, err)
				return
			}
			pks = []cipher.PubKey{pk}
		}
		n, errs := hv.ui2Forget(pks)
		if !all && len(errs) > 0 {
			httputil.WriteJSON(w, r, http.StatusConflict, errs[0])
			return
		}
		msg := hv.ui2T(r, "nodes.deleted")
		if all {
			msg = hv.ui2T(r, "nodes.deleted-plural", "number", strconv.Itoa(n))
			if n == 1 {
				msg = hv.ui2T(r, "nodes.deleted-singular")
			}
		}
		ns := ui2NodesOf(hv.visorTree())
		hv.render(w, r, ui2.NodeRows(ns, ui2ErrText(errors.Join(errs...)), true), ui2.Toast(msg, false))
	}
}

func ui2ErrText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// ui2Forget forgets the visors pks, or with none given every remembered
// visor that is offline. It returns how many it forgot.
func (hv *Hypervisor) ui2Forget(pks []cipher.PubKey) (int, []error) {
	if len(pks) == 0 {
		connected := map[cipher.PubKey]bool{}
		hv.mu.RLock()
		for pk := range hv.remoteVisors {
			connected[pk] = true
		}
		hv.mu.RUnlock()
		for _, e := range hv.offlineKnownEntries(connected) {
			if !e.Online {
				pks = append(pks, e.PK)
			}
		}
	}
	var errs []error
	for _, pk := range pks {
		if err := hv.ForgetVisor(pk); err != nil {
			errs = append(errs, err)
		}
	}
	return len(pks) - len(errs), errs
}
