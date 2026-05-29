// Package visor pkg/visor/hypervisor.go
package visor

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/visor/usermanager"
)

func (hv *Hypervisor) getRoutes() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		qSummary, err := httputil.BoolFromQuery(r, "summary", false)
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest, err)
			return
		}

		rules, err := ctx.API.RoutingRules()
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
			return
		}

		resp := make([]routingRuleResp, len(rules))
		for i, rule := range rules {
			resp[i] = makeRoutingRuleResp(rule.KeyRouteID(), rule, qSummary)
		}

		httputil.WriteJSON(w, r, http.StatusOK, resp)
	})
}

func (hv *Hypervisor) postRoute() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		var summary routing.RuleSummary
		if err := httputil.ReadJSON(r, &summary); err != nil {
			if err != io.EOF {
				hv.log(r).Warnf("postRoute request: %v", err)
			}

			httputil.WriteJSON(w, r, http.StatusBadRequest, usermanager.ErrMalformedRequest)

			return
		}

		rule, err := summary.ToRule()
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest, err)
			return
		}

		if err := ctx.API.SaveRoutingRule(rule); err != nil {
			httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
			return
		}

		httputil.WriteJSON(w, r, http.StatusOK, makeRoutingRuleResp(rule.KeyRouteID(), rule, true))
	})
}

func (hv *Hypervisor) getRoute() http.HandlerFunc {
	return hv.withCtx(hv.routeCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		qSummary, err := httputil.BoolFromQuery(r, "summary", true)
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest, err)
			return
		}

		rule, err := ctx.API.RoutingRule(ctx.RtKey)
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusNotFound, err)
			return
		}

		httputil.WriteJSON(w, r, http.StatusOK, makeRoutingRuleResp(ctx.RtKey, rule, qSummary))
	})
}

func (hv *Hypervisor) putRoute() http.HandlerFunc {
	return hv.withCtx(hv.routeCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		var rSummary routing.RuleSummary
		if err := httputil.ReadJSON(r, &rSummary); err != nil {
			if err != io.EOF {
				hv.log(r).Warnf("putRoute request: %v", err)
			}

			httputil.WriteJSON(w, r, http.StatusBadRequest, usermanager.ErrMalformedRequest)

			return
		}

		rule, err := rSummary.ToRule()
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest, err)
			return
		}

		if err := ctx.API.SaveRoutingRule(rule); err != nil {
			httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
			return
		}

		httputil.WriteJSON(w, r, http.StatusOK, makeRoutingRuleResp(ctx.RtKey, rule, true))
	})
}

func (hv *Hypervisor) deleteRoute() http.HandlerFunc {
	return hv.withCtx(hv.routeCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		if err := ctx.API.RemoveRoutingRule(ctx.RtKey); err != nil {
			httputil.WriteJSON(w, r, http.StatusNotFound, err)
			return
		}

		httputil.WriteJSON(w, r, http.StatusOK, true)
	})
}

func (hv *Hypervisor) deleteRoutes() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		var rids []string
		response := make(map[string]elementResponse)
		err := json.NewDecoder(r.Body).Decode(&rids)
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusNotFound, err)
			return
		}
		rules, err := ctx.API.RoutingRules()
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusNotFound, err)
			return
		}
		for _, rid := range rids {
			ridUint64, err := strconv.ParseUint(rid, 10, 32)
			if err != nil {
				response[rid] = elementResponse{
					Success: false,
					Error:   err.Error(),
				}
				continue
			}
			routeID := routing.RouteID(ridUint64)
			contains := false
			for _, rule := range rules {
				if rule.KeyRouteID() == routeID {
					contains = true
				}
			}
			if !contains {
				errMsg := fmt.Errorf("route of ID %s is not found", rid)
				response[rid] = elementResponse{
					Success: false,
					Error:   errMsg.Error(),
				}
				continue
			}

			if err := ctx.API.RemoveRoutingRule(routeID); err != nil {
				response[rid] = elementResponse{
					Success: false,
					Error:   err.Error(),
				}
				continue
			}
			response[rid] = elementResponse{
				Success: true,
			}
		}
		httputil.WriteJSON(w, r, http.StatusOK, response)
	})
}

func (hv *Hypervisor) getRouteGroups() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		routegroups, err := ctx.API.RouteGroups()
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
			return
		}

		resp := make([]routeGroupResp, len(routegroups))
		for i, l := range routegroups {
			resp[i] = makeRouteGroupResp(l)
		}

		httputil.WriteJSON(w, r, http.StatusOK, resp)
	})
}

// getRoutingPolicies surfaces the installed routing-policy state
// for the hypervisor UI's routing tab. Returns
// {default?, per_app: {appName: PolicyInfo}}.
func (hv *Hypervisor) getRoutingPolicies() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		summary, err := ctx.API.RoutingPolicies()
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
			return
		}
		if summary == nil {
			summary = &RoutingPoliciesSummary{PerApp: map[string]*RoutingPolicyInfo{}}
		}
		httputil.WriteJSON(w, r, http.StatusOK, summary)
	})
}

func (hv *Hypervisor) postMinHops() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		var reqBody struct {
			MinHops uint16 `json:"min_hops"`
		}

		if err := httputil.ReadJSON(r, &reqBody); err != nil {
			if err != io.EOF {
				hv.log(r).Warnf("postMinHops request: %v", err)
			}
			httputil.WriteJSON(w, r, http.StatusBadRequest, usermanager.ErrMalformedRequest)
			return
		}

		if err := ctx.API.SetMinHops(reqBody.MinHops); err != nil {
			httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
			return
		}
		httputil.WriteJSON(w, r, http.StatusOK, struct{}{})
	})
}
