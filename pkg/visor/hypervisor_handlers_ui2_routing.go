// Package visor pkg/visor/hypervisor_handlers_ui2_routing.go c3-vis-core
package visor

import (
	"encoding/json"
	"net/http"
	"slices"
	"strconv"

	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/visor/ui2"
)

// The routing tab of the server rendered UI. Writes and the route queries go
// through the JSON routing handlers.

func (hv *Hypervisor) ui2RoutingRoutes(r *httputil.Router) {
	r.Get("/visors/{pk}/routing", hv.ui2Routing())
	r.Get("/visors/{pk}/routing/min-hops", hv.ui2MinHops())
	r.Post("/visors/{pk}/routing/min-hops", hv.ui2MinHopsSave())
	r.Get("/visors/{pk}/routing/rules", hv.ui2Rules())
	r.Delete("/visors/{pk}/routing/rules/{rid}", hv.ui2RuleDelete())
	r.Get("/visors/{pk}/routing/groups", hv.ui2RouteGroups())
	r.Get("/visors/{pk}/routing/policies", hv.ui2RoutingPolicies())
	r.Get("/visors/{pk}/routing/find", hv.ui2PathForm(false))
	r.Get("/visors/{pk}/routing/calc", hv.ui2PathForm(true))
	r.Post("/visors/{pk}/routing/find", hv.ui2PathRun(false))
	r.Post("/visors/{pk}/routing/calc", hv.ui2PathRun(true))
}

func ui2MinHopsOf(ctx *httpCtx) (string, string) {
	n, err := ctx.API.GetMinHops()
	if err != nil {
		return "", err.Error()
	}
	return strconv.Itoa(int(n)), ""
}

func (hv *Hypervisor) ui2Routing() http.HandlerFunc {
	return hv.withCtx(hv.ui2VisorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		view := r.URL.Query().Get("view")
		if !slices.Contains(ui2.RoutingViews, view) {
			view = ui2.RoutingViews[0]
		}
		n, _ := ui2MinHopsOf(ctx)
		hv.render(w, r, ui2.RoutingPage(r.PathValue("pk"), n, view))
	})
}

func (hv *Hypervisor) ui2MinHops() http.HandlerFunc {
	return hv.withCtx(hv.ui2VisorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		n, msg := ui2MinHopsOf(ctx)
		hv.render(w, r, ui2.MinHops(r.PathValue("pk"), n, r.URL.Query().Get("edit") != "", msg))
	})
}

func (hv *Hypervisor) ui2MinHopsSave() http.HandlerFunc {
	return hv.withCtx(hv.ui2VisorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		pk := r.PathValue("pk")
		n, err := strconv.ParseUint(r.FormValue("min"), 10, 16)
		if err != nil {
			hv.render(w, r, ui2.MinHops(pk, r.FormValue("min"), true, err.Error()))
			return
		}
		cw := callJSON(w, r, hv.postMinHops(), struct {
			MinHops uint16 `json:"min_hops"`
		}{uint16(n)})
		if cw.code != http.StatusOK {
			hv.render(w, r, ui2.MinHops(pk, r.FormValue("min"), true, cw.errText()))
			return
		}
		cur, msg := ui2MinHopsOf(ctx)
		hv.render(w, r, ui2.MinHops(pk, cur, false, msg), ui2.Toast(hv.ui2T(r, "common.save"), false))
	})
}

func (hv *Hypervisor) ui2RulesView(w http.ResponseWriter, r *http.Request, ctx *httpCtx, toast string) {
	rules, err := ctx.API.RoutingRules()
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	rs := ui2.SelectRules(r.PathValue("pk"), ui2.RulesOf(rules), ui2.ParseListQuery(r.FormValue), msg)
	view := ui2.RulesView(rs)
	if r.FormValue("list") != "" || toast != "" {
		view = ui2.RulesList(rs)
	}
	if toast != "" {
		hv.render(w, r, view, ui2.Toast(toast, false))
		return
	}
	hv.render(w, r, view)
}

func (hv *Hypervisor) ui2Rules() http.HandlerFunc {
	return hv.withCtx(hv.ui2VisorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		hv.ui2RulesView(w, r, ctx, "")
	})
}

func (hv *Hypervisor) ui2RuleDelete() http.HandlerFunc {
	return hv.withCtx(hv.ui2VisorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		if !ui2CallAPI(w, r, hv.deleteRoute(), nil) {
			return
		}
		hv.ui2RulesView(w, r, ctx, hv.ui2T(r, "routes.deleted"))
	})
}

func (hv *Hypervisor) ui2RouteGroups() http.HandlerFunc {
	return hv.withCtx(hv.ui2VisorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		gs, err := ctx.API.RouteGroups()
		msg := ""
		if err != nil {
			msg = err.Error()
		}
		hv.render(w, r, ui2.GroupsView(ui2.GroupsOf(gs), msg))
	})
}

func (hv *Hypervisor) ui2RoutingPolicies() http.HandlerFunc {
	return hv.withCtx(hv.ui2VisorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		s, err := ctx.API.RoutingPolicies()
		msg := ""
		if err != nil {
			msg = err.Error()
		}
		hv.render(w, r, ui2.PoliciesView(ui2.PoliciesOf(s), msg))
	})
}

// ui2PathDefaults are the Angular form defaults (routing.component.ts).
func ui2PathDefaults(pk string, calc bool) ui2.PathQuery {
	q := ui2.PathQuery{PK: pk, Calc: calc, MinHops: "1", MaxHops: "5"}
	if calc {
		q.MinHops, q.Count = "0", "50"
	}
	return q
}

func (hv *Hypervisor) ui2PathForm(calc bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hv.render(w, r, ui2.PathView(ui2PathDefaults(r.PathValue("pk"), calc)))
	}
}

// ui2PathRun asks the route finder, or calculates routes over the visor's
// view of the transport graph, from the visor of the page.
func (hv *Hypervisor) ui2PathRun(calc bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := ui2PathDefaults(r.PathValue("pk"), calc)
		q.Ran = true
		q.Dst = r.FormValue("dst")
		atoi := func(field string, into *string) int {
			if v := r.FormValue(field); v != "" {
				*into = v
			}
			n, _ := strconv.Atoi(*into) //nolint:errcheck
			return n
		}
		minH, maxH := atoi("min", &q.MinHops), atoi("max", &q.MaxHops)
		var cw *captureWriter
		if calc {
			cw = callJSON(w, r, hv.postRouteCalc(), routeCalcReq{SrcPK: q.PK, DstPK: q.Dst, MinHops: minH, MaxHops: maxH, Count: atoi("count", &q.Count)})
		} else {
			cw = callJSON(w, r, hv.postRouteFind(), routeFindReq{SrcPK: q.PK, DstPK: q.Dst, MinHops: uint16(minH), MaxHops: uint16(maxH)}) //nolint:gosec
		}
		if cw.code != http.StatusOK {
			q.Err = cw.errText()
			hv.render(w, r, ui2.PathView(q))
			return
		}
		var resp routeFindResp
		if err := json.Unmarshal(cw.body.Bytes(), &resp); err != nil {
			q.Err = err.Error()
		}
		for _, rt := range resp.Routes {
			p := ui2.Path{Dst: rt.DstPK}
			for _, h := range rt.Hops {
				p.Hops = append(p.Hops, h.From)
			}
			q.Paths = append(q.Paths, p)
		}
		hv.render(w, r, ui2.PathView(q))
	}
}
