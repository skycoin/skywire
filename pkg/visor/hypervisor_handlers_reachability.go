// Package visor pkg/visor/hypervisor_handlers_reachability.go
//
// HTTP handlers for the per-visor Reachability tab: skywire-route
// ping (DialPing + Ping + StopPing), dmsg ping (DialDmsgPing +
// DmsgPing + StopDmsgPing), and a remote /health fetch over dmsg.
// All three wrap RPC methods that already exist on Visor; this file
// just exposes them to the UI's HTTP API.
package visor

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsghttp"
	"github.com/skycoin/skywire/pkg/httputil"
)

// pingTargetReq is the JSON body the UI sends to start a one-shot
// ping. Default tries=5 / size=1KB packet matches the CLI defaults.
type pingTargetReq struct {
	Target string `json:"target"`
	Tries  int    `json:"tries"`
	Size   int    `json:"size"`
}

// pingTargetResp wraps the latency array + computed summary stats
// the UI renders. Mean/min/max are computed server-side so the UI
// stays a thin viewer.
type pingTargetResp struct {
	Target       string  `json:"target"`
	LatenciesMs  []int64 `json:"latencies_ms"`
	MinMs        int64   `json:"min_ms"`
	MaxMs        int64   `json:"max_ms"`
	MeanMs       int64   `json:"mean_ms"`
	SuccessCount int     `json:"success_count"`
}

// postPingTarget handles POST /visors/{pk}/ping-target. Atomically
// dials, runs the ping protocol, and tears down the ping connection
// — three RPC calls that have to happen in order with cleanup, so
// the HTTP wrapper is the right place to orchestrate.
func (hv *Hypervisor) postPingTarget() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		var req pingTargetReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest,
				map[string]string{"error": "bad json: " + err.Error()})
			return
		}
		var pk cipher.PubKey
		if err := pk.Set(req.Target); err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest,
				map[string]string{"error": "bad target pk: " + err.Error()})
			return
		}
		if req.Tries <= 0 {
			req.Tries = 5
		}
		if req.Size <= 0 {
			req.Size = 1
		}
		conf := PingConfig{
			PK:       pk,
			Tries:    req.Tries,
			PcktSize: req.Size,
		}
		// DialPing → Ping → StopPing. Each step is bounded internally
		// (DialPing has a 30s ctx; Ping bounds itself by tries). On
		// any error the StopPing tear-down still runs via the defer.
		if err := ctx.API.DialPing(conf); err != nil {
			httputil.WriteJSON(w, r, http.StatusBadGateway,
				map[string]string{"error": "DialPing: " + err.Error()})
			return
		}
		defer func() {
			if err := ctx.API.StopPing(pk); err != nil {
				hv.log(r).WithError(err).Warn("postPingTarget: StopPing failed")
			}
		}()
		latencies, err := ctx.API.Ping(conf)
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusBadGateway,
				map[string]string{"error": "Ping: " + err.Error()})
			return
		}
		httputil.WriteJSON(w, r, http.StatusOK, summarizeLatencies(req.Target, latencies))
	})
}

// postDmsgPingTarget handles POST /visors/{pk}/dmsg-ping-target.
// Same shape as postPingTarget but rides on the dmsg ping protocol
// instead of skywire routing — useful when the operator wants to
// confirm the visor is reachable on the dmsg layer specifically.
func (hv *Hypervisor) postDmsgPingTarget() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		var req pingTargetReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest,
				map[string]string{"error": "bad json: " + err.Error()})
			return
		}
		var pk cipher.PubKey
		if err := pk.Set(req.Target); err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest,
				map[string]string{"error": "bad target pk: " + err.Error()})
			return
		}
		if req.Tries <= 0 {
			req.Tries = 5
		}
		if req.Size <= 0 {
			req.Size = 1
		}
		conf := PingConfig{PK: pk, Tries: req.Tries, PcktSize: req.Size}
		if err := ctx.API.DialDmsgPing(pk); err != nil {
			httputil.WriteJSON(w, r, http.StatusBadGateway,
				map[string]string{"error": "DialDmsgPing: " + err.Error()})
			return
		}
		defer func() {
			if err := ctx.API.StopDmsgPing(pk); err != nil {
				hv.log(r).WithError(err).Warn("postDmsgPingTarget: StopDmsgPing failed")
			}
		}()
		latencies, err := ctx.API.DmsgPing(conf)
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusBadGateway,
				map[string]string{"error": "DmsgPing: " + err.Error()})
			return
		}
		httputil.WriteJSON(w, r, http.StatusOK, summarizeLatencies(req.Target, latencies))
	})
}

// summarizeLatencies folds a []time.Duration into the wire shape the
// UI expects. Successful tries = entries with non-zero duration; the
// CLI-side ping protocol fills 0 for failures, so this preserves the
// same convention.
func summarizeLatencies(target string, ds []time.Duration) pingTargetResp {
	out := pingTargetResp{
		Target:      target,
		LatenciesMs: make([]int64, 0, len(ds)),
	}
	if len(ds) == 0 {
		return out
	}
	var sum, mn, mx int64
	mn = -1
	for _, d := range ds {
		ms := d.Milliseconds()
		out.LatenciesMs = append(out.LatenciesMs, ms)
		if d > 0 {
			out.SuccessCount++
			sum += ms
			if mn < 0 || ms < mn {
				mn = ms
			}
			if ms > mx {
				mx = ms
			}
		}
	}
	if out.SuccessCount > 0 {
		out.MinMs = mn
		out.MaxMs = mx
		out.MeanMs = sum / int64(out.SuccessCount)
	}
	return out
}

// healthFetchResp is the response wrapper for a remote /health fetch.
// Status is the HTTP status the upstream returned; Body is the raw
// response (typically a JSON health blob the UI parses inline).
// LatencyMs is wall time from request dispatch to body close.
type healthFetchResp struct {
	Target    string `json:"target"`
	StatusURL string `json:"status_url"`
	Status    int    `json:"status"`
	LatencyMs int64  `json:"latency_ms"`
	Body      string `json:"body"`
	Error     string `json:"error,omitempty"`
}

// getHealthFetch handles GET /visors/{pk}/health-fetch?target=<pk>.
// Issues a dmsg-routed GET to dmsg://<target>:80/health using the
// visor's existing dmsghttp client — same plumbing the visor uses
// for `svc health`, just pointed at a peer's landing-page handler
// instead of a deployment service.
func (hv *Hypervisor) getHealthFetch() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		targetStr := r.URL.Query().Get("target")
		if targetStr == "" {
			httputil.WriteJSON(w, r, http.StatusBadRequest,
				map[string]string{"error": "target query param required"})
			return
		}
		var pk cipher.PubKey
		if err := pk.Set(targetStr); err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest,
				map[string]string{"error": "bad target pk: " + err.Error()})
			return
		}
		// Hypervisor doesn't get direct access to the visor's dmsg
		// client through the API interface, but the local visor
		// (hv.visor) does. Use dmsghttp's HTTPTransport with the
		// visor's dmsg client so this rides the existing session
		// pool — same model as the dmsg-side service-health probe.
		if hv.visor == nil || hv.visor.dmsgC == nil {
			httputil.WriteJSON(w, r, http.StatusServiceUnavailable,
				map[string]string{"error": "visor or dmsg client unavailable"})
			return
		}
		port := uint16(80)
		if portStr := r.URL.Query().Get("port"); portStr != "" {
			if p, err := strconv.ParseUint(portStr, 10, 16); err == nil {
				port = uint16(p)
			}
		}
		path := r.URL.Query().Get("path")
		if path == "" {
			path = "/health"
		}
		client := &http.Client{
			Timeout:   10 * time.Second,
			Transport: dmsghttp.MakeHTTPTransport(r.Context(), hv.visor.dmsgC),
		}
		statusURL := fmt.Sprintf("dmsg://%s:%d%s", pk.Hex(), port, path)
		started := time.Now()
		resp, err := client.Get(statusURL) //nolint:gosec
		out := healthFetchResp{Target: targetStr, StatusURL: statusURL}
		if err != nil {
			out.Error = err.Error()
			out.LatencyMs = time.Since(started).Milliseconds()
			httputil.WriteJSON(w, r, http.StatusOK, out)
			return
		}
		defer resp.Body.Close() //nolint:errcheck,gosec
		body, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			out.Error = "read body: " + readErr.Error()
			out.Status = resp.StatusCode
			out.LatencyMs = time.Since(started).Milliseconds()
			httputil.WriteJSON(w, r, http.StatusOK, out)
			return
		}
		out.Status = resp.StatusCode
		out.LatencyMs = time.Since(started).Milliseconds()
		out.Body = string(body)
		httputil.WriteJSON(w, r, http.StatusOK, out)
	})
}
