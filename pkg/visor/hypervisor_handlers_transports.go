// Package visor pkg/visor/hypervisor.go
package visor

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/httputil"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/visor/usermanager"
)

func (hv *Hypervisor) getTransportTypes() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		types, err := ctx.API.TransportTypes()
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
			return
		}

		httputil.WriteJSON(w, r, http.StatusOK, types)
	})
}

func (hv *Hypervisor) getTransports() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		qTypes := strSliceFromQuery(r, "type", nil)

		qPKs, err := pkSliceFromQuery(r, "pk", nil)
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest, err)
			return
		}

		qLogs, err := httputil.BoolFromQuery(r, "logs", true)
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest, err)
			return
		}

		transports, err := ctx.API.Transports(qTypes, qPKs, qLogs)
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
			return
		}
		httputil.WriteJSON(w, r, http.StatusOK, transports)
	})
}

func (hv *Hypervisor) postTransport() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		var reqBody struct {
			TpType     string        `json:"transport_type"`
			Remote     cipher.PubKey `json:"remote_pk"`
			Label      string        `json:"label,omitempty"`       // "user" or "skycoin" (default: "skycoin")
			NoRegister bool          `json:"no_register,omitempty"` // skip transport discovery (only for "user" label)
		}

		if err := httputil.ReadJSON(r, &reqBody); err != nil {
			if err != io.EOF {
				hv.log(r).Warnf("postTransport request: %v", err)
			}

			httputil.WriteJSON(w, r, http.StatusBadRequest, usermanager.ErrMalformedRequest)

			return
		}

		const timeout = 30 * time.Second
		tSummary, err := ctx.API.AddTransport(reqBody.Remote, reqBody.TpType, timeout, reqBody.Label, reqBody.NoRegister, false)
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
			return
		}

		httputil.WriteJSON(w, r, http.StatusOK, tSummary)
	})
}

func (hv *Hypervisor) getTransport() http.HandlerFunc {
	return hv.withCtx(hv.tpCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		httputil.WriteJSON(w, r, http.StatusOK, ctx.Tp)
	})
}

func (hv *Hypervisor) deleteTransport() http.HandlerFunc {
	return hv.withCtx(hv.tpCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		if err := ctx.API.RemoveTransport(ctx.Tp.ID); err != nil {
			httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
			return
		}

		httputil.WriteJSON(w, r, http.StatusOK, true)
	})
}

func (hv *Hypervisor) deleteTransports() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		var transports []string
		response := make(map[string]elementResponse)
		err := json.NewDecoder(r.Body).Decode(&transports)
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest, err)
			return
		}
		for _, transport := range transports {
			transportBoxed, err := uuid.Parse(transport)
			if err != nil {
				response[transport] = elementResponse{
					Success: false,
					Error:   err.Error(),
				}
				continue
			}
			_, err = ctx.API.Transport(transportBoxed)
			if err != nil {
				if err.Error() == ErrNotFound.Error() {
					errMsg := fmt.Errorf("transport of ID %s is not found", transportBoxed)
					response[transport] = elementResponse{
						Success: false,
						Error:   errMsg.Error(),
					}
					continue
				}
			}

			if err := ctx.API.RemoveTransport(transportBoxed); err != nil {
				response[transport] = elementResponse{
					Success: false,
					Error:   err.Error(),
				}
				continue
			}
			response[transport] = elementResponse{
				Success: true,
			}
		}
		httputil.WriteJSON(w, r, http.StatusOK, response)
	})
}

func (hv *Hypervisor) putPublicAutoconnect() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		var reqBody publicAutoconnectReq

		if err := httputil.ReadJSON(r, &reqBody); err != nil {
			if err != io.EOF {
				hv.log(r).Warnf("putPublicAutoconnect request: %v", err)
			}
			httputil.WriteJSON(w, r, http.StatusBadRequest, usermanager.ErrMalformedRequest)
			return
		}

		if err := ctx.API.SetPublicAutoconnect(reqBody.PublicAutoconnect); err != nil {
			httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
			return
		}
		httputil.WriteJSON(w, r, http.StatusOK, struct{}{})
	})
}

func (hv *Hypervisor) putPersistentTransports() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		var reqBody []transport.PersistentTransports

		if err := httputil.ReadJSON(r, &reqBody); err != nil {
			if err != io.EOF {
				hv.log(r).Warnf("putPersistentTransports request: %v", err)
			}
			httputil.WriteJSON(w, r, http.StatusBadRequest, usermanager.ErrMalformedRequest)
			return
		}

		if err := ctx.API.SetPersistentTransports(reqBody); err != nil {
			httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
			return
		}
		httputil.WriteJSON(w, r, http.StatusOK, struct{}{})
	})
}

func (hv *Hypervisor) getPersistentTransports() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		pts, err := ctx.API.GetPersistentTransports()
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
			return
		}
		httputil.WriteJSON(w, r, http.StatusOK, pts)
	})
}
