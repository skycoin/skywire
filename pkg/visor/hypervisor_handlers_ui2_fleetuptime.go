// Package visor pkg/visor/hypervisor_handlers_ui2_fleetuptime.go c3-vis-core
package visor

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/visor/ui2"
)

// The uptime of every visor, read from /api/network/visor-uptime.

func (hv *Hypervisor) ui2FleetUptimeRoutes(r *httputil.Router) {
	r.Get("/uptime", hv.ui2FleetUptime(false))
	r.Get("/uptime/body", hv.ui2FleetUptime(true))
}

func (hv *Hypervisor) ui2FleetUptime(body bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(50 * time.Second)) //nolint:errcheck
		days := 7
		switch d, _ := strconv.Atoi(r.URL.Query().Get("days")); d { //nolint:errcheck
		case 1, 30:
			days = d
		}
		all := r.URL.Query().Get("all") != ""
		r2 := r.Clone(r.Context())
		q := r2.URL.Query()
		q.Set("days", strconv.Itoa(days))
		r2.URL.RawQuery = q.Encode()
		cw := &captureWriter{h: http.Header{}, code: http.StatusOK}
		hv.getNetworkVisorUptime().ServeHTTP(cw, r2)
		var sums []ui2.FleetSummary
		var errMsg string
		if cw.code != http.StatusOK {
			errMsg = cw.errText()
		} else if err := json.Unmarshal(cw.body.Bytes(), &sums); err != nil {
			errMsg = err.Error()
		}
		managed := map[string]bool{}
		for _, s := range hv.collectLocalVisorSummaries() {
			if s.Overview != nil {
				managed[s.Overview.PubKey.Hex()] = true
			}
		}
		f := ui2.FleetUptimeOf(sums, managed, all, days, time.Now())
		f.Err, f.Fetched = errMsg, time.Now().Format(time.TimeOnly)
		if body {
			hv.render(w, r, ui2.FleetUptimeBody(f))
			return
		}
		hv.render(w, r, ui2.FleetUptimePage(f))
	}
}
