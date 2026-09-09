// Package visor pkg/visor/hypervisor_handlers_pair.go c3-vis-api
//
// Pre-auth pairing endpoints (#4484 stage 5). A desk tab this hypervisor
// serves has no session yet; it learns its own standing from GET /pair/status
// and, as the fallback to `skywire cli visor hv pair`, redeems a one-time code
// the operator made on the board with POST /pair. Both sit beside /tp/ws,
// outside the session-auth group; the code is the credential, single-use and
// revoked after a few wrong attempts.
package visor

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/httputil"
)

// getPairStatus — GET /pair/status?pk=<hex>: whether pk is paired (trusted)
// or pending, plus its fingerprint for the operator to match on the board.
func (hv *Hypervisor) getPairStatus() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if hv.visor == nil {
			httputil.WriteJSON(w, r, http.StatusServiceUnavailable, "no visor behind this hypervisor")
			return
		}
		var pk cipher.PubKey
		if err := pk.Set(r.URL.Query().Get("pk")); err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest, "pk: "+err.Error())
			return
		}
		httputil.WriteJSON(w, r, http.StatusOK, hv.visor.PairStatusOf(pk))
	}
}

type pairRequest struct {
	PK   cipher.PubKey `json:"pk"`
	Code string        `json:"code"`
}

// postPair — POST /pair {"pk","code"}: approve pk if code is an outstanding
// one-time code. Any failure is 403 with one message, so a guess learns
// nothing about which codes exist.
func (hv *Hypervisor) postPair() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if hv.visor == nil {
			httputil.WriteJSON(w, r, http.StatusServiceUnavailable, "no visor behind this hypervisor")
			return
		}
		var req pairRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest, "body: "+err.Error())
			return
		}
		if err := hv.visor.PairWithCode(req.PK, req.Code); err != nil {
			if errors.Is(err, ErrPairCodeInvalid) {
				httputil.WriteJSON(w, r, http.StatusForbidden, ErrPairCodeInvalid.Error())
				return
			}
			httputil.WriteJSON(w, r, http.StatusInternalServerError, err.Error())
			return
		}
		httputil.WriteJSON(w, r, http.StatusOK, hv.visor.PairStatusOf(req.PK))
	}
}
