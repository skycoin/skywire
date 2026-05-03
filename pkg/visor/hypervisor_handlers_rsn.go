// Package visor pkg/visor/hypervisor_handlers_rsn.go
//
// Per-RSN remote stats fetch for the hvui's Services Health page.
// Mirrors `skywire-cli route rsn-remote-stats` — for every RSN PK
// in EffectiveRouteSetupNodes the local visor's DmsgHTTP RPC is
// used to GET dmsg://<pk>:80/stats, the response is parsed back
// into a StatsSnapshot and the array is returned to the UI.
package visor

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/router/setupmetrics"
)

// RSNRemoteStat is the per-RSN entry returned by /api/route-setup-nodes/stats.
// Either Snapshot or Error is set; both being absent means the RSN
// returned a non-2xx status with no body.
type RSNRemoteStat struct {
	PK       cipher.PubKey               `json:"pk"`
	Snapshot *setupmetrics.StatsSnapshot `json:"snapshot,omitempty"`
	Error    string                      `json:"error,omitempty"`
	// Status from the RSN's HTTP response (only set when Snapshot is
	// nil and Error is empty — i.e. unexpected 2xx body that didn't
	// parse, or non-2xx with no error message).
	Status int `json:"status,omitempty"`
}

// rsnRemoteStatsTimeout is the per-RSN deadline for the dmsg-http
// fetch. The fan-out is parallel, so the total wall-clock is
// max(per-rsn) rather than sum.
const rsnRemoteStatsTimeout = 8 * time.Second

func (hv *Hypervisor) getRSNRemoteStats() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if hv.visor == nil {
			httputil.WriteJSON(w, r, http.StatusServiceUnavailable, []RSNRemoteStat{})
			return
		}
		pks := hv.visor.conf.EffectiveRouteSetupNodes()
		out := make([]RSNRemoteStat, len(pks))

		var wg sync.WaitGroup
		for i, pk := range pks {
			wg.Add(1)
			go func(i int, pk cipher.PubKey) {
				defer wg.Done()
				out[i] = hv.fetchOneRSNStat(pk)
			}(i, pk)
		}

		// Bound the parallel fan-out by the per-RSN timeout — the
		// goroutines don't share a context so the wait is just an
		// upper bound on the slowest fetch.
		done := make(chan struct{})
		go func() { wg.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(rsnRemoteStatsTimeout + 2*time.Second):
		}

		httputil.WriteJSON(w, r, http.StatusOK, out)
	}
}

func (hv *Hypervisor) fetchOneRSNStat(pk cipher.PubKey) RSNRemoteStat {
	entry := RSNRemoteStat{PK: pk}
	url := fmt.Sprintf("dmsg://%s:80/stats", pk.Hex())
	resp, err := hv.visor.DmsgHTTP(DmsgHTTPRequest{
		URL:    url,
		Method: "GET",
	})
	if err != nil {
		entry.Error = err.Error()
		return entry
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		entry.Status = resp.StatusCode
		entry.Error = fmt.Sprintf("HTTP %d", resp.StatusCode)
		return entry
	}
	var snap setupmetrics.StatsSnapshot
	if err := json.Unmarshal(resp.Body, &snap); err != nil {
		entry.Status = resp.StatusCode
		entry.Error = "unparseable response: " + err.Error()
		return entry
	}
	entry.Snapshot = &snap
	return entry
}
