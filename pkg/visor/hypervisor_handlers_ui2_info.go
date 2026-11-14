// Package visor pkg/visor/hypervisor_handlers_ui2_info.go c3-vis-core
package visor

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/visor/ui2"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

// The info tab of the server rendered UI.

func (hv *Hypervisor) ui2InfoRoutes(r *httputil.Router) {
	r.Get("/visors/{pk}/info", hv.ui2Info(false))
	r.Get("/visors/{pk}/info/live", hv.ui2Info(true))
	r.Put("/visors/{pk}/info/router", hv.ui2RouterSave())
	r.Get("/visors/{pk}/info/config", hv.ui2Config(false))
	r.Get("/visors/{pk}/info/config/edit", hv.ui2Config(true))
	r.Put("/visors/{pk}/info/config", hv.ui2ConfigSave())
	r.Post("/visors/{pk}/info/restart", hv.ui2Restart())
	r.Post("/visors/{pk}/info/shutdown", hv.ui2Shutdown())
}

// ui2Summary is the visor's summary as getVisorSummary answers it.
func (hv *Hypervisor) ui2Summary(ctx *httpCtx) (visorapi.Summary, error) {
	s, err := ctx.API.Summary()
	if err != nil || s == nil || s.Overview == nil {
		if err == nil {
			err = errUI2NoSummary
		}
		return visorapi.Summary{}, err
	}
	for _, d := range hv.getDmsgSummary() {
		if d.PK == s.Overview.PubKey {
			d := d
			s.DmsgStats = &d
		}
	}
	s.Hypervisor = s.Overview.PubKey == hv.c.PK
	return *s, nil
}

type ui2Err string

func (e ui2Err) Error() string { return string(e) }

const errUI2NoSummary = ui2Err("the visor sent no summary")

// ui2Info serves the info tab, or with live set only its polled part.
func (hv *Hypervisor) ui2Info(live bool) http.HandlerFunc {
	return hv.withCtx(hv.ui2VisorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		i := ui2.Info{PK: r.PathValue("pk")}
		s, err := hv.ui2Summary(ctx)
		if err != nil {
			i.Err = err.Error()
		}
		i.Sum = s
		if live {
			hv.render(w, r, ui2.InfoLive(i))
			return
		}
		if ports, err := ctx.API.Ports(); err == nil {
			i.Ports = ui2.PortRows(ports)
		}
		if rs, err := ctx.API.GetRouterSettings(); err == nil {
			i.Router = &rs
		} else {
			i.RouterErr = err.Error()
		}
		hv.render(w, r, ui2.InfoPage(i))
	})
}

func (hv *Hypervisor) ui2RouterSave() http.HandlerFunc {
	return hv.withCtx(hv.ui2VisorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		pk := r.PathValue("pk")
		if err := r.ParseForm(); err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest, err)
			return
		}
		hops, err := strconv.ParseUint(r.PostForm.Get("min_hops"), 10, 16)
		if err != nil {
			cur, _ := ctx.API.GetRouterSettings() //nolint:errcheck
			hv.render(w, r, ui2.RouterForm(pk, &cur, err.Error()))
			return
		}
		// TransportPreference stays nil, which leaves the order unchanged.
		s := visorapi.RouterSettings{
			ForceLocalRoutes: r.PostForm.Get("force_local_routes") != "",
			ExistingTPOnly:   r.PostForm.Get("existing_tp_only") != "",
			MinHops:          uint16(hops),
		}
		if err := ctx.API.SetRouterSettings(s); err != nil {
			hv.render(w, r, ui2.RouterForm(pk, &s, err.Error()))
			return
		}
		out, err := ctx.API.GetRouterSettings()
		if err != nil {
			out = s
		}
		hv.render(w, r, ui2.RouterForm(pk, &out, ""), ui2.Toast(hv.ui2T(r, "rewards-address-config.done"), false))
	})
}

// ui2Config serves the runtime config read only, or with edit set in a form.
func (hv *Hypervisor) ui2Config(edit bool) http.HandlerFunc {
	return hv.withCtx(hv.ui2VisorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		pk := r.PathValue("pk")
		raw, err := ctx.API.GetRuntimeConfig()
		if err != nil {
			hv.render(w, r, ui2.ErrorBox(err.Error()))
			return
		}
		var pretty bytes.Buffer
		if json.Indent(&pretty, raw, "", "  ") == nil {
			raw = pretty.Bytes()
		}
		if edit {
			hv.render(w, r, ui2.ConfigEdit(pk, string(raw), ""))
			return
		}
		hv.render(w, r, ui2.ConfigView(pk, string(raw), ""))
	})
}

// ui2ConfigSave writes the runtime config, as putRuntimeConfig does. It takes
// effect when the visor restarts.
func (hv *Hypervisor) ui2ConfigSave() http.HandlerFunc {
	return hv.withCtx(hv.ui2VisorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		pk := r.PathValue("pk")
		r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
		if err := r.ParseForm(); err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest, err)
			return
		}
		raw := r.PostForm.Get("config")
		if !json.Valid([]byte(raw)) {
			hv.render(w, r, ui2.ConfigEdit(pk, raw, "invalid JSON"))
			return
		}
		if err := ctx.API.SetRuntimeConfig([]byte(raw)); err != nil {
			hv.render(w, r, ui2.ConfigEdit(pk, raw, err.Error()))
			return
		}
		hv.render(w, r, ui2.ConfigView(pk, raw, hv.ui2T(r, "node.details.config.restart-hint")))
	})
}

// ui2Restart reloads the visor without waiting, as restart does: the reload
// closes the connection the call rides.
func (hv *Hypervisor) ui2Restart() http.HandlerFunc {
	return hv.withCtx(hv.ui2VisorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		api, pk := ctx.API, r.PathValue("pk")
		go func() {
			if err := api.Reload(); err != nil {
				hv.logger.WithError(err).WithField("visor_pk", pk).Debug("ui2: reload returned (expected: the visor closes the connection)")
			}
		}()
		hv.render(w, r, ui2.Toast(hv.ui2T(r, "nodes.bulk.restarting", "name", pk), false))
	})
}

func (hv *Hypervisor) ui2Shutdown() http.HandlerFunc {
	return hv.withCtx(hv.ui2VisorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		if err := ctx.API.Shutdown(); err != nil {
			hv.render(w, r, ui2.Toast(err.Error(), true))
			return
		}
		hv.render(w, r, ui2.Toast(hv.ui2T(r, "actions.turn-off.done"), false))
	})
}
