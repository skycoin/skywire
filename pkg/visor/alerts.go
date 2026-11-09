// Package visor pkg/visor/alerts.go c3-vis-core
package visor

import (
	"context"
	"errors"
	"net/http"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/alerts"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/util/rpcutil"
)

// alertFreshWindow is how recently an attached visor must have answered
// the hypervisor's 30s summary poll to count as online for alerts.
const alertFreshWindow = 75 * time.Second

// alertStartGrace gives attached visors time to dial back in after this
// visor restarts before their offline delay starts counting.
const alertStartGrace = 2 * time.Minute

var errAlertsOff = errors.New("alerts are not running on this visor")

// initAlerts starts the offline alerts. The browser visor keeps the
// settings API but does not watch, it is not always on and cannot dial
// SMTP.
func initAlerts(ctx context.Context, v *Visor, log *logging.Logger) error {
	host, _ := os.Hostname() //nolint:errcheck
	sender := &alerts.Sender{Watcher: v.conf.PK, Hostname: host}
	svc, err := alerts.NewService(v.conf.LocalPath, v.conf.Alerts, sender)
	if err != nil {
		log.WithError(err).Warn("Reading saved alert settings")
	}
	svc.Targets = v.alertTargets
	svc.Logf = log.Warnf
	v.alertsMu.Lock()
	v.alertSvc = svc
	v.alertsMu.Unlock()
	if runtime.GOOS != "js" {
		go svc.Run(ctx, alerts.CheckInterval, alertStartGrace)
	}
	return nil
}

func (v *Visor) alertService() (*alerts.Service, error) {
	v.alertsMu.RLock()
	defer v.alertsMu.RUnlock()
	if v.alertSvc == nil {
		return nil, errAlertsOff
	}
	return v.alertSvc, nil
}

// alertTargets is the liveness of every watched visor: the visors this
// visor's hypervisor manages, known ones included, and the watch list,
// whose liveness is a current dmsg discovery client entry.
func (v *Visor) alertTargets(ctx context.Context, set alerts.Settings) []alerts.Target {
	out := map[cipher.PubKey]alerts.Target{}
	if hv := v.hvInstance; hv != nil && hv.IsEnabled() {
		for _, t := range hv.alertTargets(time.Now()) {
			out[t.PK] = t
		}
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	for _, pk := range set.Watch {
		if _, ok := out[pk]; ok || pk == v.conf.PK || pk.Null() {
			continue
		}
		out[pk] = alerts.Target{PK: pk, Source: "watch"}
		if v.dmsgC == nil {
			continue
		}
		wg.Add(1)
		go func(pk cipher.PubKey) {
			defer wg.Done()
			cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			_, err := v.dmsgC.DiscEntry(cctx, pk)
			if err != nil {
				return
			}
			mu.Lock()
			out[pk] = alerts.Target{PK: pk, Source: "watch", Online: true, LastSeen: time.Now()}
			mu.Unlock()
		}(pk)
	}
	wg.Wait()
	list := make([]alerts.Target, 0, len(out))
	for _, t := range out {
		list = append(list, t)
	}
	return list
}

// alertTargets lists every visor this hypervisor manages or remembers.
func (hv *Hypervisor) alertTargets(now time.Time) []alerts.Target {
	hv.mu.RLock()
	connected := make(map[cipher.PubKey]bool, len(hv.remoteVisors))
	for pk := range hv.remoteVisors {
		connected[pk] = true
	}
	hv.mu.RUnlock()

	hv.summaryCacheMx.RLock()
	defer hv.summaryCacheMx.RUnlock()
	out := make([]alerts.Target, 0, len(hv.summaryCache)+len(connected))
	for pk, c := range hv.summaryCache {
		t := alerts.Target{PK: pk, Source: "hypervisor", LastSeen: c.seenAt,
			Online: connected[pk] && now.Sub(c.seenAt) < alertFreshWindow}
		if c.sum != nil && c.sum.Overview != nil {
			t.Hostname, t.PublicIP = c.sum.Overview.Hostname, c.sum.Overview.PublicIP
		}
		out = append(out, t)
		delete(connected, pk)
	}
	for pk := range connected {
		out = append(out, alerts.Target{PK: pk, Source: "hypervisor", Online: true, LastSeen: now})
	}
	return out
}

// AlertsStatus implements visorapi.Alerts.
func (v *Visor) AlertsStatus() (*alerts.Status, error) {
	svc, err := v.alertService()
	if err != nil {
		return nil, err
	}
	st := svc.Status()
	return &st, nil
}

// AlertsSet implements visorapi.Alerts.
func (v *Visor) AlertsSet(s alerts.Settings) error {
	svc, err := v.alertService()
	if err != nil {
		return err
	}
	return svc.SetSettings(s)
}

// AlertsTest implements visorapi.Alerts.
func (v *Visor) AlertsTest() ([]alerts.Result, error) {
	svc, err := v.alertService()
	if err != nil {
		return nil, err
	}
	// Inside the hypervisor's 15s remote visor timeout.
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	return svc.Test(ctx)
}

// AlertsStatus returns the alert settings and watch state.
func (r *RPC) AlertsStatus(_ *struct{}, out *alerts.Status) (err error) {
	defer rpcutil.LogCall(r.log, "AlertsStatus", nil)(nil, &err)
	st, err := r.visor.AlertsStatus()
	if st != nil {
		*out = *st
	}
	return err
}

// AlertsSet replaces the alert settings. The argument is not logged, it
// can hold the relay password.
func (r *RPC) AlertsSet(in *alerts.Settings, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "AlertsSet", nil)(nil, &err)
	return r.visor.AlertsSet(*in)
}

// AlertsTest sends a test alert.
func (r *RPC) AlertsTest(_ *struct{}, out *[]alerts.Result) (err error) {
	defer rpcutil.LogCall(r.log, "AlertsTest", nil)(out, &err)
	res, err := r.visor.AlertsTest()
	*out = res
	return err
}

// alertsRoutes registers the per-visor alert routes.
func (hv *Hypervisor) alertsRoutes(r *httputil.Router) {
	r.Get("/visors/{pk}/alerts", hv.getAlerts())
	r.Put("/visors/{pk}/alerts", hv.putAlerts())
	r.Post("/visors/{pk}/alerts/test", hv.postAlertsTest())
}

func (hv *Hypervisor) getAlerts() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		st, err := ctx.API.AlertsStatus()
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
			return
		}
		httputil.WriteJSON(w, r, http.StatusOK, st)
	})
}

func (hv *Hypervisor) putAlerts() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		var s alerts.Settings
		if err := httputil.ReadJSON(r, &s); err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest, err)
			return
		}
		if err := ctx.API.AlertsSet(s); err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest, err)
			return
		}
		st, err := ctx.API.AlertsStatus()
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
			return
		}
		httputil.WriteJSON(w, r, http.StatusOK, st)
	})
}

func (hv *Hypervisor) postAlertsTest() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		res, err := ctx.API.AlertsTest()
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest, err)
			return
		}
		if res == nil {
			res = []alerts.Result{}
		}
		httputil.WriteJSON(w, r, http.StatusOK, res)
	})
}
