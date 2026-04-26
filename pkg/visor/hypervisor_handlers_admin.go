// Package visor pkg/visor/hypervisor.go
package visor

import (
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/visor/rewardconfig"
	"github.com/skycoin/skywire/pkg/visor/usermanager"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
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

func (hv *Hypervisor) getRuntimeLogs() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
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

func (hv *Hypervisor) putLogRotationInterval() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		var reqBody struct {
			LogRotationInterval visorconfig.Duration `json:"log_rotation_interval"`
		}

		if err := httputil.ReadJSON(r, &reqBody); err != nil {
			if err != io.EOF {
				hv.log(r).Warnf("putLogRotationInterval request: %v", err)
			}
			httputil.WriteJSON(w, r, http.StatusBadRequest, usermanager.ErrMalformedRequest)
			return
		}

		if err := ctx.API.SetLogRotationInterval(reqBody.LogRotationInterval); err != nil {
			httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
			return
		}
		httputil.WriteJSON(w, r, http.StatusOK, struct{}{})
	})
}

func (hv *Hypervisor) getLogRotationInterval() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		pts, err := ctx.API.GetLogRotationInterval()
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
			return
		}
		httputil.WriteJSON(w, r, http.StatusOK, pts)
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

		// Read from visor config (configurable), fall back to deployment defaults
		rewardDmsg := ""
		rewardHTTP := ""
		if hv.visor != nil && hv.visor.conf != nil {
			rewardDmsg = hv.visor.conf.RewardSystemDmsg
			rewardHTTP = hv.visor.conf.RewardSystem
		}
		if rewardDmsg == "" {
			rewardDmsg = deployment.Prod.RewardSystemDmsg
		}
		if rewardHTTP == "" {
			rewardHTTP = deployment.Prod.RewardSystem
		}
		log := hv.visor.MasterLogger().PackageLogger("reward_proxy")
		var fetchErr error

		// Try DMSG first via the visor's DmsgHTTP
		if rewardDmsg != "" && hv.visor != nil {
			dmsgURL := rewardDmsg + "/" + path
			log.Debugf("Fetching reward data via DMSG: %s", dmsgURL)
			resp, err := hv.visor.DmsgHTTP(DmsgHTTPRequest{
				URL:    dmsgURL,
				Method: "GET",
			})
			if err == nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
				log.Debugf("DMSG fetch succeeded: %d bytes, status %d", len(resp.Body), resp.StatusCode)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(resp.StatusCode)
				w.Write(resp.Body) //nolint:errcheck,gosec
				return
			}
			if err != nil {
				log.WithError(err).Warn("DMSG fetch failed, falling back to HTTP")
				fetchErr = err
			} else {
				log.Warnf("DMSG fetch returned non-success status: %d", resp.StatusCode)
			}
		}

		// Fall back to plain HTTP
		if rewardHTTP != "" {
			httpURL := rewardHTTP + "/" + path
			log.Debugf("Fetching reward data via HTTP: %s", httpURL)
			client := &http.Client{Timeout: 15 * time.Second}
			resp, err := client.Get(httpURL) //nolint:gosec
			if err == nil {
				defer resp.Body.Close()          //nolint:errcheck
				body, _ := io.ReadAll(resp.Body) //nolint:errcheck
				log.Debugf("HTTP fetch succeeded: %d bytes, status %d", len(body), resp.StatusCode)
				w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
				w.WriteHeader(resp.StatusCode)
				w.Write(body) //nolint:errcheck,gosec
				return
			}
			log.WithError(err).Warn("HTTP fetch also failed")
			fetchErr = err
		}

		if fetchErr != nil {
			httputil.WriteJSON(w, r, http.StatusBadGateway, fmt.Errorf("reward system unreachable: %w", fetchErr))
		} else {
			httputil.WriteJSON(w, r, http.StatusServiceUnavailable, "no reward system URL configured")
		}
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
