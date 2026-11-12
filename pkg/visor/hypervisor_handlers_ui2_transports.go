// Package visor pkg/visor/hypervisor_handlers_ui2_transports.go c3-vis-core
package visor

import (
	"encoding/json"
	"net/http"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/transport"
	tptypes "github.com/skycoin/skywire/pkg/transport/types"
	"github.com/skycoin/skywire/pkg/visor/ui2"
)

// The transports tab of the server rendered UI. Writes go through the JSON
// transport handlers.

func (hv *Hypervisor) ui2TransportsRoutes(r *httputil.Router) {
	r.Get("/visors/{pk}/transports", hv.ui2Transports(false))
	r.Get("/visors/{pk}/transports/body", hv.ui2Transports(true))
	r.Get("/visors/{pk}/transports/{tid}/details", hv.withCtx(hv.ui2VisorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		t := ui2LoadTransports(ctx, r)
		for _, x := range t.List {
			if x.ID == r.PathValue("tid") {
				hv.render(w, r, ui2.TransportDetails(t, x))
				return
			}
		}
		http.NotFound(w, r)
	}))
	r.Post("/visors/{pk}/transports", hv.ui2TransportAdd())
	r.Delete("/visors/{pk}/transports/{tid}", hv.ui2TransportWrite(func(hv *Hypervisor, w http.ResponseWriter, r *http.Request, _ *httpCtx) bool {
		return ui2CallAPI(w, r, hv.deleteTransport(), nil)
	}, "transports.deleted"))
	r.Delete("/visors/{pk}/transports", hv.ui2TransportWrite(ui2RemoveAllTransports, "transports.deleted"))
	r.Put("/visors/{pk}/transports/autoconnect", hv.ui2TransportWrite(func(hv *Hypervisor, w http.ResponseWriter, r *http.Request, _ *httpCtx) bool {
		return ui2CallAPI(w, r, hv.putPublicAutoconnect(), publicAutoconnectReq{PublicAutoconnect: r.FormValue("on") != ""})
	}, "transports.changes-made"))
	r.Put("/visors/{pk}/transports/public", hv.ui2TransportWrite(func(hv *Hypervisor, w http.ResponseWriter, r *http.Request, _ *httpCtx) bool {
		return ui2CallAPI(w, r, hv.putIsPublic(), isPublicResp{IsPublic: r.FormValue("on") != ""})
	}, "transports.changes-made"))
	r.Put("/visors/{pk}/transports/persistent", hv.ui2TransportWrite(ui2SetPersistent, "transports.changes-made"))
}

func ui2LoadTransports(ctx *httpCtx, r *http.Request) ui2.Transports {
	pk := r.PathValue("pk")
	var errs []string
	tps, err := ctx.API.Transports(nil, nil, true)
	if err != nil {
		errs = append(errs, err.Error())
	}
	pts, err := ctx.API.GetPersistentTransports()
	if err != nil {
		errs = append(errs, err.Error())
	}
	t := ui2.TransportsOf(pk, tps, pts)
	if types, err := ctx.API.TransportTypes(); err == nil {
		t.Types = types
	}
	t.IsPublic = ctx.API.GetIsPublic()
	// The autoconnect setting is the config value, as the Angular tab reads
	// it from the summary.
	if raw, err := ctx.API.GetRuntimeConfig(); err == nil {
		var c struct {
			Transport struct {
				PublicAutoconnect bool `json:"public_autoconnect"`
			} `json:"transport"`
		}
		if json.Unmarshal(raw, &c) == nil {
			t.Autoconnect = c.Transport.PublicAutoconnect
		}
	}
	if len(errs) > 0 {
		t.Err = errs[0]
	}
	t.Select(ui2.ParseListQuery(r.FormValue))
	return t
}

func (hv *Hypervisor) ui2Transports(body bool) http.HandlerFunc {
	return hv.withCtx(hv.ui2VisorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		t := ui2LoadTransports(ctx, r)
		if body {
			hv.render(w, r, ui2.TransportsBody(t), ui2.TransportsSummary(t, true))
			return
		}
		hv.render(w, r, ui2.TransportsPage(t))
	})
}

type ui2TransportOp func(hv *Hypervisor, w http.ResponseWriter, r *http.Request, ctx *httpCtx) bool

// ui2TransportWrite runs op and answers with the refreshed tab body and a
// toast. op writes its own error.
func (hv *Hypervisor) ui2TransportWrite(op ui2TransportOp, done string) http.HandlerFunc {
	return hv.withCtx(hv.ui2VisorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		if !op(hv, w, r, ctx) {
			return
		}
		t := ui2LoadTransports(ctx, r)
		hv.render(w, r, ui2.TransportsBody(t), ui2.TransportsSummary(t, true), ui2.Toast(hv.ui2T(r, done), false))
	})
}

// ui2TransportAdd adds a transport and, when asked, makes it persistent.
func (hv *Hypervisor) ui2TransportAdd() http.HandlerFunc {
	return hv.ui2TransportWrite(func(hv *Hypervisor, w http.ResponseWriter, r *http.Request, ctx *httpCtx) bool {
		var remote cipher.PubKey
		if err := remote.Set(r.FormValue("remote")); err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest, err)
			return false
		}
		tpType := r.FormValue("type")
		if r.FormValue("persistent") != "" {
			if !ui2EditPersistent(w, r, ctx, hv, remote, tpType, true) {
				return false
			}
		}
		body := struct {
			TpType string        `json:"transport_type"`
			Remote cipher.PubKey `json:"remote_pk"`
		}{tpType, remote}
		return ui2CallAPI(w, r, hv.postTransport(), body)
	}, "transports.dialog.success")
}

func ui2RemoveAllTransports(hv *Hypervisor, w http.ResponseWriter, r *http.Request, ctx *httpCtx) bool {
	tps, err := ctx.API.Transports(nil, nil, false)
	if err != nil {
		httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
		return false
	}
	ids := make([]string, 0, len(tps))
	for _, t := range tps {
		if t != nil {
			ids = append(ids, t.ID.String())
		}
	}
	return ui2CallAPI(w, r, hv.deleteTransports(), ids)
}

func ui2SetPersistent(hv *Hypervisor, w http.ResponseWriter, r *http.Request, ctx *httpCtx) bool {
	var pk cipher.PubKey
	if err := pk.Set(r.FormValue("pk")); err != nil {
		httputil.WriteJSON(w, r, http.StatusBadRequest, err)
		return false
	}
	return ui2EditPersistent(w, r, ctx, hv, pk, r.FormValue("type"), r.FormValue("on") != "")
}

// ui2EditPersistent adds the transport to the persistent list or takes it
// off, and saves the list through the JSON handler.
func ui2EditPersistent(w http.ResponseWriter, r *http.Request, ctx *httpCtx, hv *Hypervisor, pk cipher.PubKey, tpType string, on bool) bool {
	cur, err := ctx.API.GetPersistentTransports()
	if err != nil {
		httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
		return false
	}
	next := make([]transport.PersistentTransports, 0, len(cur)+1)
	for _, p := range cur {
		if p.PK != pk || string(p.NetType) != tpType {
			next = append(next, p)
		}
	}
	if on {
		next = append(next, transport.PersistentTransports{PK: pk, NetType: tptypes.Type(tpType)})
	}
	return ui2CallAPI(w, r, hv.putPersistentTransports(), next)
}
