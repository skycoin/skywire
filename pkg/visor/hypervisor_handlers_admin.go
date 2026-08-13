// Package visor pkg/visor/hypervisor_handlers_admin.go c3-vis-core
package visor

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/visor/rewardconfig"
	"github.com/skycoin/skywire/pkg/visor/usermanager"
)

func (hv *Hypervisor) shutdown() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		if err := ctx.API.Shutdown(); err != nil {
			httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
			return
		}
		httputil.WriteJSON(w, r, http.StatusOK, true)
	})
}

// restart restarts a whole visor through its existing Reload RPC: close the
// module stack, re-read the config from disk, run it again — the process
// stays, everything inside it is rebuilt.
//
// Fired and not waited on, because Reload cannot answer: it tears down the
// very RPC transport the call is riding, so the caller's outcome is an EOF
// whether the restart succeeded or the visor died. `cli visor reload` has
// always worked this way (goroutine + a fixed pause, then "Visor reloaded").
// So this route reports 202 — dispatched, not completed — and the caller
// learns the real outcome the honest way: the visor drops out of
// /api/visors-summary and comes back.
//
// The visor being connected at all is already established before we get
// here: visorCtx answers 404/503 for a PK this hypervisor cannot reach.
func (hv *Hypervisor) restart() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		api := ctx.API
		addr := ctx.Addr.PK
		go func() {
			// Expected on success (connection closed under the call) —
			// logged for the case where it is something else entirely.
			if err := api.Reload(); err != nil {
				hv.logger.WithError(err).WithField("visor_pk", addr).
					Debug("Reload RPC returned (an error is expected: the visor closes the connection).")
			}
		}()
		httputil.WriteJSON(w, r, http.StatusAccepted, restartResp{Restarting: true})
	})
}

// restartResp is the 202 body of the restart route: the request was
// dispatched, the visor's state is what the next summary poll says.
type restartResp struct {
	Restarting bool `json:"restarting"`
}

func (hv *Hypervisor) getRuntimeLogs() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		// Diff-streaming mode: caller passes ?since=N to receive
		// only entries with log_line > N. Returns the
		// RuntimeLogsDelta JSON shape ({entries, latest, dropped}).
		// When ?since is absent, the legacy full-buffer array shape
		// is returned for back-compat with existing callers (curl,
		// CLI clients without the cursor wired).
		if sinceStr := r.URL.Query().Get("since"); sinceStr != "" {
			since, parseErr := strconv.ParseInt(sinceStr, 10, 64)
			if parseErr != nil {
				httputil.WriteJSON(w, r, http.StatusBadRequest, fmt.Errorf("invalid since: %w", parseErr))
				return
			}
			delta, err := ctx.API.RuntimeLogsSince(since)
			if err != nil {
				httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
				return
			}
			httputil.WriteJSON(w, r, http.StatusOK, delta)
			return
		}

		logs, err := ctx.API.RuntimeLogs()
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, err = w.Write([]byte(logs))
		if err != nil {
			hv.visor.log.Errorf("Cannot write response: %s", err)
		}
	})
}

func (hv *Hypervisor) getRewardAddress() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		pts, err := ctx.API.GetRewardAddress()
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
			return
		}
		httputil.WriteJSON(w, r, http.StatusOK, pts)
	})
}

func (hv *Hypervisor) putRewardAddress() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		var reqBody *rewardconfig.Reward

		if err := httputil.ReadJSON(r, &reqBody); err != nil {
			if err != io.EOF {
				hv.log(r).Warnf("putRewardAddress request: %v", err)
			}
			httputil.WriteJSON(w, r, http.StatusBadRequest, usermanager.ErrMalformedRequest)
			return
		}

		canonical, _, err := rewardconfig.ValidateRewardAddress(reqBody.RewardAddress)
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest, err)
			return
		}
		pConf, err := ctx.API.SetRewardAddress(canonical)
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
			return
		}
		httputil.WriteJSON(w, r, http.StatusOK, pConf)
	})
}

func (hv *Hypervisor) deleteRewardAddress() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		err := ctx.API.DeleteRewardAddress()
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
			return
		}
		httputil.WriteJSON(w, r, http.StatusOK, struct{}{})
	})
}

func (hv *Hypervisor) proxyRewardSystem() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := chi.URLParam(r, "*")
		if path == "" {
			httputil.WriteJSON(w, r, http.StatusBadRequest, "missing reward API path")
			return
		}

		// Reward system is reached over dmsg only — plain HTTP to deployment
		// services is no longer supported. The dmsg URL may live in either config
		// field (the dmsg-only default stores it in the "http" field).
		rewardDmsg := ""
		if hv.visor != nil && hv.visor.conf != nil {
			rewardDmsg = hv.visor.conf.RewardSystemDmsg
			if !strings.HasPrefix(rewardDmsg, "dmsg://") {
				rewardDmsg = hv.visor.conf.RewardSystem
			}
		}
		if !strings.HasPrefix(rewardDmsg, "dmsg://") {
			rewardDmsg = deployment.Prod.RewardSystemDmsg
		}
		log := hv.visor.MasterLogger().PackageLogger("reward_proxy")

		if !strings.HasPrefix(rewardDmsg, "dmsg://") || hv.visor == nil {
			httputil.WriteJSON(w, r, http.StatusServiceUnavailable, "no reward system dmsg URL configured")
			return
		}
		dmsgURL := rewardDmsg + "/" + path
		log.Debugf("Fetching reward data via DMSG: %s", dmsgURL)
		resp, err := hv.visor.DmsgHTTP(DmsgHTTPRequest{
			URL:    dmsgURL,
			Method: "GET",
		})
		if err == nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(resp.StatusCode)
			w.Write(resp.Body) //nolint:errcheck,gosec
			return
		}
		if err != nil {
			log.WithError(err).Warn("reward system DMSG fetch failed")
			httputil.WriteJSON(w, r, http.StatusBadGateway, fmt.Errorf("reward system unreachable over dmsg: %w", err))
			return
		}
		log.Warnf("reward system DMSG fetch returned status %d", resp.StatusCode)
		httputil.WriteJSON(w, r, http.StatusBadGateway, "reward system returned non-success over dmsg")
	}
}

func (hv *Hypervisor) putIsPublic() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		var req isPublicResp
		if err := httputil.ReadJSON(r, &req); err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest, err)
			return
		}
		if err := ctx.API.SetIsPublic(req.IsPublic); err != nil {
			httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
			return
		}
		httputil.WriteJSON(w, r, http.StatusOK, req)
	})
}

func (hv *Hypervisor) getIsPublic() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		httputil.WriteJSON(w, r, http.StatusOK, isPublicResp{ctx.API.GetIsPublic()})
	})
}

// getRuntimeStats — Go-runtime metrics for the visor process.
// Polled by the hypervisor UI's Resource Monitor "Process" tab.
func (hv *Hypervisor) getRuntimeStats() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		stats, err := ctx.API.RuntimeStats()
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
			return
		}
		httputil.WriteJSON(w, r, http.StatusOK, stats)
	})
}

// getHostStats — psutil-style host metrics (CPU%, mem, disk, net,
// visor process). Polled by the Resource Monitor "Host" tab.
func (hv *Hypervisor) getHostStats() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		stats, err := ctx.API.HostStats()
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
			return
		}
		httputil.WriteJSON(w, r, http.StatusOK, stats)
	})
}

func (hv *Hypervisor) getRuntimeConfig() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		configJSON, err := ctx.API.GetRuntimeConfig()
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(configJSON) //nolint:errcheck,gosec
	})
}

// getLocalTransportStats returns the visor's locally-tracked
// per-transport bandwidth + latency rollup. Source of truth is
// the visor's bbolt stats store; no TPD round-trip.
func (hv *Hypervisor) getLocalTransportStats() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		resp, err := ctx.API.LocalTransportStats()
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		httputil.WriteJSON(w, r, http.StatusOK, resp)
	})
}

// getLocalUptimeStats returns the visor's locally-tracked tier
// uptime bitmaps (process / dmsg / skynet) over a window. Source
// of truth is the same bbolt stats store /stats/uptime serves on
// the logserver; this handler bridges it through the hvui's
// existing per-visor proxy chain. Window comes from `?since=` and
// `?until=` (RFC3339); both default — no since means seven days
// before until, no until means now.
func (hv *Hypervisor) getLocalUptimeStats() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		var args LocalUptimeArgs
		if s := r.URL.Query().Get("since"); s != "" {
			t, err := time.Parse(time.RFC3339, s)
			if err != nil {
				httputil.WriteJSON(w, r, http.StatusBadRequest, map[string]string{"error": "since: " + err.Error()})
				return
			}
			args.Since = t
		}
		if u := r.URL.Query().Get("until"); u != "" {
			t, err := time.Parse(time.RFC3339, u)
			if err != nil {
				httputil.WriteJSON(w, r, http.StatusBadRequest, map[string]string{"error": "until: " + err.Error()})
				return
			}
			args.Until = t
		}
		resp, err := ctx.API.LocalUptimeStats(args)
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		httputil.WriteJSON(w, r, http.StatusOK, resp)
	})
}

// putRuntimeConfig accepts a raw JSON body and forwards it to
// SetRuntimeConfig on the target visor. The body is validated
// server-side (strict JSON decode + SK/PK consistency); the visor
// is NOT hot-reloaded — the caller must restart it for the change
// to take effect. The response includes a "restart_required" flag
// so the hvui can surface that to the operator.
func (hv *Hypervisor) putRuntimeConfig() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if len(bytes.TrimSpace(body)) == 0 {
			httputil.WriteJSON(w, r, http.StatusBadRequest, map[string]string{"error": "empty body"})
			return
		}
		if err := ctx.API.SetRuntimeConfig(body); err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		httputil.WriteJSON(w, r, http.StatusOK, map[string]any{
			"saved":            true,
			"restart_required": true,
			"restart_hint":     "Visor must be restarted for the new config to take effect.",
		})
	})
}
