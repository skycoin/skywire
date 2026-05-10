// Package visor pkg/visor/hypervisor.go
package visor

import (
	"net/http"
	"time"

	"github.com/skycoin/skywire/pkg/httputil"
)

// getServiceHealth returns the health status of all configured deployment
// services (TPD, DMSG discovery, AR, RF, UT, SD). Each entry has name, URL,
// status, latency and version. Fetched via the local visor's DMSG/HTTP
// client so the UI sees the same view the visor does.
func (hv *Hypervisor) getServiceHealth() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if hv.visor == nil {
			httputil.WriteJSON(w, r, http.StatusServiceUnavailable, []ServiceHealthEntry{})
			return
		}
		entries, err := hv.visor.ServiceHealth()
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		httputil.WriteJSON(w, r, http.StatusOK, entries)
	}
}

// getSvcFetch is a generic proxy over the visor's existing
// FetchServiceData(service, path) method. UI uses this for two
// distinct purposes:
//
//  1. Service-health drill-down — fetch a deeper endpoint on a
//     specific deployment service (e.g. ?service=ar&path=/transports
//     to get AR's full transports JSON, not just /health).
//  2. Service-integrated UT — fetch the per-service "seen-online"
//     list (tpd /uptimes, sd /uptimes, mdisc /uptimes) for the
//     Uptime tab's services side-by-side row.
//
// Service must be one of: tpd, ut, sd, ar, rf, dmsgd. Path is
// passed through verbatim (validated by FetchServiceData against
// the configured base URL). Response body is returned as-is with
// Content-Type application/octet-stream so the caller can parse
// the upstream's payload format itself; UI parses the JSON.
func (hv *Hypervisor) getSvcFetch() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if hv.visor == nil {
			httputil.WriteJSON(w, r, http.StatusServiceUnavailable,
				map[string]string{"error": "visor unavailable"})
			return
		}
		service := r.URL.Query().Get("service")
		path := r.URL.Query().Get("path")
		if service == "" || path == "" {
			httputil.WriteJSON(w, r, http.StatusBadRequest,
				map[string]string{"error": "service and path query params required"})
			return
		}
		body, err := hv.visor.FetchServiceData(service, path)
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusBadGateway,
				map[string]string{"error": err.Error()})
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write(body); err != nil {
			hv.log(r).WithError(err).Warn("getSvcFetch: failed to write response body")
		}
	}
}

// provides summary of health information for every visor
func (hv *Hypervisor) getHealth() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		vh := &Health{}

		type healthRes struct {
			h   *HealthInfo
			err error
		}

		resCh := make(chan healthRes)
		tCh := time.After(HealthTimeout)

		go func() {
			hi, err := ctx.API.Health()
			resCh <- healthRes{hi, err}
		}()

		select {
		case res := <-resCh:
			if res.err != nil {
				vh.Status = http.StatusInternalServerError
			} else {
				vh.HealthInfo = res.h
				vh.Status = http.StatusOK
			}

			httputil.WriteJSON(w, r, http.StatusOK, vh)
		case <-tCh:
			httputil.WriteJSON(w, r, http.StatusRequestTimeout, &Health{Status: http.StatusRequestTimeout})
		}
	})
}

// getUptime gets given visor's uptime
func (hv *Hypervisor) getUptime() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		u, err := ctx.API.Uptime()
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
			return
		}

		httputil.WriteJSON(w, r, http.StatusOK, u)
	})
}
