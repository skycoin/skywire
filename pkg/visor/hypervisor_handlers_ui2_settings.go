// Package visor pkg/visor/hypervisor_handlers_ui2_settings.go c3-vis-core
package visor

import (
	"net/http"
	"strings"

	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/skywireconfig/skyenvform"
	"github.com/skycoin/skywire/pkg/visor/ui2"
)

// The settings tab of the server rendered UI: the visor's skywire.conf
// through its skyenv API, as `skywire cli visor skyenv` edits it.

func (hv *Hypervisor) ui2SettingsRoutes(r *httputil.Router) {
	r.Get("/visors/{pk}/settings", hv.ui2Settings(ui2SettingsPage))
	r.Get("/visors/{pk}/settings/panel", hv.ui2Settings(ui2SettingsForm))
	r.Post("/visors/{pk}/settings/panel", hv.ui2Settings(ui2SettingsForm))
	r.Post("/visors/{pk}/settings/review", hv.ui2Settings(ui2SettingsReview))
	r.Put("/visors/{pk}/settings", hv.ui2Settings(ui2SettingsSave))
}

type ui2SettingsStep int

const (
	ui2SettingsPage ui2SettingsStep = iota
	ui2SettingsForm
	ui2SettingsReview
	ui2SettingsSave
)

func (hv *Hypervisor) ui2Settings(step ui2SettingsStep) http.HandlerFunc {
	return hv.withCtx(hv.ui2VisorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		s := ui2.Settings{PK: r.PathValue("pk")}
		st, err := ctx.API.Skyenv()
		if err != nil {
			s.Err = err.Error()
			if step == ui2SettingsPage {
				hv.render(w, r, ui2.SettingsPage(s))
			} else {
				hv.render(w, r, ui2.ErrorBox(s.Err))
			}
			return
		}
		s.Path, s.Exists, s.Writable = st.Path, st.Exists, st.Writable
		rows := skyenvform.Rows(st)
		drafts := map[string]string{}
		if r.Method != http.MethodGet {
			r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
			if err := r.ParseForm(); err != nil {
				httputil.WriteJSON(w, r, http.StatusBadRequest, err)
				return
			}
			for k, v := range r.PostForm {
				if key, ok := strings.CutPrefix(k, "v."); ok && len(v) > 0 {
					drafts[key] = v[0]
				}
			}
		}
		fields, bad := ui2.FieldsOf(rows, drafts)
		s.Fields = fields
		if step == ui2SettingsPage {
			hv.render(w, r, ui2.SettingsPage(s))
			return
		}
		if step == ui2SettingsForm || bad > 0 {
			hv.render(w, r, ui2.SettingsPanel(s))
			return
		}
		form := map[string]skyenvform.Input{}
		for _, f := range fields {
			if f.Format == "bool" && f.Draft == "" {
				form[f.Key] = skyenvform.Input{Unset: true}
				continue
			}
			form[f.Key] = skyenvform.Input{Value: f.Draft}
		}
		edits, changes, err := skyenvform.Edits(rows, form)
		if err != nil {
			s.Err = err.Error()
			hv.render(w, r, ui2.ErrorBox(s.Err), ui2.SettingsPanel(s))
			return
		}
		s.Reviewing, s.Changes = true, changes
		if !s.Writable {
			s.Command = skyenvform.Command(rows, edits)
		}
		if step == ui2SettingsSave && len(changes) > 0 {
			if _, err := ctx.API.SetSkyenv(edits); err != nil {
				hv.render(w, r, ui2.SettingsPanel(s), ui2.Toast(err.Error(), true))
				return
			}
			s.Saved = true
		}
		hv.render(w, r, ui2.SettingsPanel(s))
	})
}
