// Package visor pkg/visor/hypervisor_handlers_ui2_bulk.go c3-vis-core
package visor

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/visor/rewardconfig"
	"github.com/skycoin/skywire/pkg/visor/ui2"
)

// The bulk actions of the ui2 visor list. Each visor's write goes through
// the same JSON handler the Angular dialogs call.

func (hv *Hypervisor) ui2BulkRoutes(r *httputil.Router) {
	r.Get("/nodes/bulk", hv.ui2Bulk(false))
	r.Post("/nodes/bulk", hv.ui2Bulk(true))
}

// ui2BulkForm reads the panel's form into a plan for the selected visors.
func (hv *Hypervisor) ui2BulkForm(r *http.Request) (ui2.Bulk, bool) {
	b := ui2.Bulk{
		Action:     r.FormValue("action"),
		App:        r.FormValue("app"),
		Address:    r.FormValue("address"),
		AcceptRisk: r.FormValue("accept_risk") != "",
		StartAfter: r.FormValue("start_after") != "",
	}
	if !ui2.BulkAction(b.Action) {
		return b, false
	}
	var catalog []string
	if b.Action == ui2.BulkAdd && hv.visor != nil {
		if cs, err := hv.visor.AppCatalog(); err == nil {
			for _, c := range cs {
				catalog = append(catalog, c.Name)
			}
		}
	}
	return ui2.PlanBulk(b, ui2NodesOf(hv.visorTree()), r.Form["pk"], catalog), true
}

func (hv *Hypervisor) ui2Bulk(run bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(90 * time.Second)) //nolint:errcheck
		if err := r.ParseForm(); err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest, err)
			return
		}
		b, ok := hv.ui2BulkForm(r)
		if !ok {
			httputil.WriteJSON(w, r, http.StatusBadRequest, errors.New("unknown bulk action"))
			return
		}
		if !run {
			hv.render(w, r, ui2.BulkPanel(b))
			return
		}
		var body any
		switch b.Action {
		case ui2.BulkReward:
			if _, _, err := rewardconfig.ValidateRewardSetting(b.Address, b.AcceptRisk); err != nil {
				b.Err = hv.ui2T(r, "rewards-address-config.address-error")
				if errors.Is(err, rewardconfig.ErrSingleAddress) {
					b.Err = hv.ui2T(r, "rewards-address-config.single-needs-opt-in")
				}
				hv.render(w, r, ui2.BulkPanel(b))
				return
			}
			body = rewardconfig.Reward{RewardAddress: b.Address, AcceptRisk: b.AcceptRisk}
		case ui2.BulkAdd, ui2.BulkStart, ui2.BulkStop:
			if b.App == "" {
				httputil.WriteJSON(w, r, http.StatusBadRequest, errors.New("no app chosen"))
				return
			}
		}
		var wg sync.WaitGroup
		for i := range b.Rows {
			row := &b.Rows[i]
			if row.Skip != "" {
				continue
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				call := func(h http.Handler, v any) bool {
					if err := ui2CallFor(r, row.PK, b.App, h, v); err != nil {
						row.Err = err.Error()
						return false
					}
					return true
				}
				switch b.Action {
				case ui2.BulkRestart:
					row.Done = call(hv.restart(), nil)
				case ui2.BulkReward:
					row.Done = call(hv.putRewardAddress(), body)
				case ui2.BulkStop:
					row.Done = call(hv.putApp(), map[string]any{"status": statusStop})
				case ui2.BulkStart:
					row.Done = call(hv.putApp(), map[string]any{"status": statusStart})
				case ui2.BulkAdd:
					ok := !row.AddFirst || call(hv.postApp(), map[string]string{"name": b.App, "binary": b.App})
					if ok && b.StartAfter {
						ok = call(hv.putApp(), map[string]any{"status": statusStart})
					}
					row.Done = ok
				}
			}()
		}
		wg.Wait()
		b.Ran = true
		hv.render(w, r, ui2.BulkPanel(b))
	}
}

// ui2CallFor runs a JSON handler for visor pk, and app if set, with v as
// the body. The handler sees the request's session and CSRF headers.
func ui2CallFor(r *http.Request, pk, app string, h http.Handler, v any) error {
	var b []byte
	if v != nil {
		b, _ = json.Marshal(v) //nolint:errcheck
	}
	r2 := r.Clone(r.Context())
	r2.SetPathValue("pk", pk)
	r2.SetPathValue("app", app)
	r2.Body = io.NopCloser(bytes.NewReader(b))
	r2.ContentLength = int64(len(b))
	r2.Header.Set("Content-Type", "application/json")
	cw := &captureWriter{h: http.Header{}, code: http.StatusOK}
	h.ServeHTTP(cw, r2)
	if cw.code/100 != 2 {
		return errors.New(cw.errText())
	}
	return nil
}
