// Package visor pkg/visor/hypervisor.go
package visor

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/skycoin/skywire/pkg/app/appcommon"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/visor/usermanager"
)

// returns app summaries of a given node of pk
func (hv *Hypervisor) getApps() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		apps, err := ctx.API.Apps()
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
			return
		}

		httputil.WriteJSON(w, r, http.StatusOK, apps)
	})
}

// returns an app summary of a given visor's pk and app name
func (hv *Hypervisor) getApp() http.HandlerFunc {
	return hv.withCtx(hv.appCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		httputil.WriteJSON(w, r, http.StatusOK, ctx.App)
	})
}

func (hv *Hypervisor) getAppStats() http.HandlerFunc {
	return hv.withCtx(hv.appCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		stats, err := ctx.API.GetAppStats(ctx.App.Name)
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
			return
		}

		httputil.WriteJSON(w, r, http.StatusOK, &stats)
	})
}

// nolint: funlen,gocognit,godox
// nolint: gocyclo
//
//gocyclo:ignore
func (hv *Hypervisor) putApp() http.HandlerFunc {
	return hv.withCtx(hv.appCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		type req struct {
			AutoStart     *bool          `json:"autostart,omitempty"`
			Killswitch    *bool          `json:"killswitch,omitempty"`
			Secure        *bool          `json:"secure,omitempty"`
			Address       *string        `json:"Address,omitempty"`
			Status        *int           `json:"status,omitempty"`
			Whitelist     *string        `json:"whitelist,omitempty"`
			NetIfc        *string        `json:"netifc,omitempty"`
			DNSAddr       *string        `json:"dns,omitempty"`
			PK            *cipher.PubKey `json:"pk,omitempty"`
			CustomSetting map[string]any `json:"custom_setting,omitempty"`
		}

		shouldRestartApp := func(r req) bool {
			// we restart the app if one of these fields was changed
			return r.Killswitch != nil || r.Secure != nil || r.Address != nil || r.Whitelist != nil ||
				r.PK != nil || r.NetIfc != nil || r.CustomSetting != nil
		}

		var reqBody req
		if err := httputil.ReadJSON(r, &reqBody); err != nil {
			if err != io.EOF {
				hv.log(r).Warnf("putApp request: %v", err)
			}

			httputil.WriteJSON(w, r, http.StatusBadRequest, usermanager.ErrMalformedRequest)

			return
		}

		if reqBody.AutoStart != nil {
			if *reqBody.AutoStart != ctx.App.AutoStart {
				if err := ctx.API.SetAutoStart(ctx.App.Name, *reqBody.AutoStart); err != nil {
					httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
					return
				}
			}
		}

		if reqBody.Whitelist != nil {
			if err := ctx.API.SetAppWhitelist(ctx.App.Name, *reqBody.Whitelist); err != nil {
				httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
				return
			}
		}

		if reqBody.PK != nil {
			if err := ctx.API.SetAppPK(ctx.App.Name, *reqBody.PK); err != nil {
				httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
				return
			}
		}

		if reqBody.Killswitch != nil {
			if err := ctx.API.SetAppKillswitch(ctx.App.Name, *reqBody.Killswitch); err != nil {
				httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
				return
			}
		}

		if reqBody.Secure != nil {
			if err := ctx.API.SetAppSecure(ctx.App.Name, *reqBody.Secure); err != nil {
				httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
				return
			}
		}

		if reqBody.Address != nil {
			if err := ctx.API.SetAppAddress(ctx.App.Name, *reqBody.Address); err != nil {
				httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
				return
			}
		}

		if reqBody.NetIfc != nil {
			if err := ctx.API.SetAppNetworkInterface(ctx.App.Name, *reqBody.NetIfc); err != nil {
				httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
				return
			}
		}

		if reqBody.DNSAddr != nil {
			if err := ctx.API.SetAppDNS(ctx.App.Name, *reqBody.DNSAddr); err != nil {
				httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
				return
			}
		}

		if reqBody.CustomSetting != nil {
			if err := ctx.API.DoCustomSetting(ctx.App.Name, reqBody.CustomSetting); err != nil {
				httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
				return
			}
		}

		if shouldRestartApp(reqBody) {
			if err := ctx.API.RestartApp(ctx.App.Name); err != nil {
				httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
				return
			}
		}

		if reqBody.Status != nil {
			switch *reqBody.Status {
			case statusStop:
				if err := ctx.API.StopApp(ctx.App.Name); err != nil {
					httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
					return
				}
			case statusStart:
				if err := ctx.API.StartApp(ctx.App.Name); err != nil {
					httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
					return
				}
				appStatus := appserver.AppDetailedStatusStarting
				if ctx.App.Name == skyenv.VPNClientName {
					appStatus = appserver.AppDetailedStatusVPNConnecting
				}
				if err := ctx.API.SetAppDetailedStatus(ctx.App.Name, appStatus); err != nil {
					httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
					return
				}
			default:
				errMsg := fmt.Errorf("value of 'status' field is %d when expecting 0 or 1", *reqBody.Status)
				httputil.WriteJSON(w, r, http.StatusBadRequest, errMsg)
				return
			}
		}

		// get the latest AppState of the app after changes
		app, err := ctx.API.App(ctx.App.Name)
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
			return
		}

		httputil.WriteJSON(w, r, http.StatusOK, app)
	})
}

func (hv *Hypervisor) appLogsSince() http.HandlerFunc {
	return hv.withCtx(hv.appCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		since := r.URL.Query().Get("since")
		since = strings.Replace(since, " ", "+", 1) // we need to put '+' again that was replaced in the query string

		// if time is not parsable or empty default to return all logs
		t, err := time.Parse(time.RFC3339Nano, since)
		if err != nil {
			t = time.Unix(0, 0)
		}

		logs, err := ctx.API.LogsSince(t, ctx.App.Name)
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
			return
		}

		if len(logs) == 0 {
			httputil.WriteJSON(w, r, http.StatusInternalServerError, fmt.Errorf("no new available logs"))
			return
		}

		httputil.WriteJSON(w, r, http.StatusOK, &LogsRes{
			LastLogTimestamp: appcommon.TimestampFromLog(logs[len(logs)-1]),
			Logs:             logs,
		})
	})
}

func (hv *Hypervisor) appConnections() http.HandlerFunc {
	return hv.withCtx(hv.appCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		cSummary, err := ctx.API.GetAppConnectionsSummary(ctx.App.Name)
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
			return
		}

		httputil.WriteJSON(w, r, http.StatusOK, &cSummary)
	})
}
