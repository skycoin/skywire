// Package visor pkg/visor/hypervisor_handlers_ui2_resources.go c3-vis-core
package visor

import (
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/visor/ui2"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

// The host resources pages: every visor's host stats, and one visor's
// host and runtime stats. Network rates come from the byte counters of the
// previous sample this hypervisor took for the visor.

func (hv *Hypervisor) ui2ResourcesRoutes(r *httputil.Router) {
	r.Get("/resources", hv.ui2Resources(false))
	r.Get("/resources/rows", hv.ui2Resources(true))
	r.Get("/visors/{pk}/resources", hv.ui2VisorResources(false))
	r.Get("/visors/{pk}/resources/live", hv.ui2VisorResources(true))
}

type ui2NetSample struct {
	sent, recv uint64
	at         time.Time
	tx, rx     float64
}

var ui2NetRates = struct {
	sync.Mutex
	m map[string]ui2NetSample
}{m: map[string]ui2NetSample{}}

// ui2Rates is the send and receive rate of pk since its previous sample,
// or -1 before there is one. A sample closer than a second to the previous
// one keeps the rates already worked out.
func ui2Rates(pk string, s *visorapi.HostStatsInfo, now time.Time) (float64, float64) {
	ui2NetRates.Lock()
	defer ui2NetRates.Unlock()
	prev, ok := ui2NetRates.m[pk]
	if ok && now.Sub(prev.at) < time.Second {
		return prev.tx, prev.rx
	}
	next := ui2NetSample{sent: s.NetBytesSent, recv: s.NetBytesRecv, at: now, tx: -1, rx: -1}
	if ok {
		dt := now.Sub(prev.at).Seconds()
		next.tx, next.rx = ui2Delta(prev.sent, s.NetBytesSent)/dt, ui2Delta(prev.recv, s.NetBytesRecv)/dt
	}
	ui2NetRates.m[pk] = next
	return next.tx, next.rx
}

// ui2HostStats asks one visor for its host stats within the remote visor
// timeout.
func ui2HostStats(api visorapi.API) (*visorapi.HostStatsInfo, error) {
	type res struct {
		s   *visorapi.HostStatsInfo
		err error
	}
	ch := make(chan res, 1)
	go func() {
		s, err := api.HostStats()
		ch <- res{s, err}
	}()
	select {
	case r := <-ch:
		return r.s, r.err
	case <-time.After(remoteVisorTimeout):
		return nil, errVisorTimeout
	}
}

var errVisorTimeout = errors.New("the visor did not answer in time")

func (hv *Hypervisor) ui2Resources(rows bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(remoteVisorTimeout + 10*time.Second)) //nolint:errcheck
		sums := hv.collectLocalVisorSummaries()
		out := make([]ui2.ResRow, len(sums))
		var wg sync.WaitGroup
		for i, s := range sums {
			out[i] = ui2.ResRow{Node: ui2.NodeOf(s), TxRate: -1, RxRate: -1}
			if !out[i].Online {
				continue
			}
			var pk cipher.PubKey
			if pk.Set(out[i].PK) != nil {
				continue
			}
			api := hv.selfConn.API
			if pk != hv.c.PK {
				c, ok := hv.visorConn(pk)
				if !ok {
					continue
				}
				api = c.API
			}
			wg.Add(1)
			go func(row *ui2.ResRow) {
				defer wg.Done()
				st, err := ui2HostStats(api)
				if err != nil {
					row.Err = err.Error()
					return
				}
				row.Stats = st
				row.TxRate, row.RxRate = ui2Rates(row.PK, st, time.Now())
			}(&out[i])
		}
		wg.Wait()
		sortResRows(out)
		sampled := time.Now().Format(time.TimeOnly)
		if rows {
			hv.render(w, r, ui2.ResourceRows(out, sampled))
			return
		}
		hv.render(w, r, ui2.ResourcesPage(out, sampled))
	}
}

// sortResRows orders the rows as the visor list does.
func sortResRows(rs []ui2.ResRow) {
	ns := make([]ui2.Node, len(rs))
	idx := map[string]ui2.ResRow{}
	for i, r := range rs {
		ns[i] = r.Node
		idx[r.PK] = r
	}
	ui2.SortNodes(ns)
	for i, n := range ns {
		rs[i] = idx[n.PK]
	}
}

func (hv *Hypervisor) ui2VisorResources(live bool) http.HandlerFunc {
	return hv.withCtx(hv.ui2VisorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		v := ui2.VisorRes{PK: r.PathValue("pk"), TxRate: -1, RxRate: -1, Sampled: time.Now().Format(time.TimeOnly)}
		if h, err := ctx.API.HostStats(); err != nil {
			v.HostErr = err.Error()
		} else {
			v.Host = h
			v.TxRate, v.RxRate = ui2Rates(v.PK, h, time.Now())
		}
		if rt, err := ctx.API.RuntimeStats(); err != nil {
			v.RunErr = err.Error()
		} else {
			v.Runtime = rt
		}
		if live {
			hv.render(w, r, ui2.VisorResLive(v))
			return
		}
		hv.render(w, r, ui2.VisorResourcesPage(v))
	})
}

// ui2Delta is how far a byte counter moved, 0 when it went back (a reset).
func ui2Delta(prev, cur uint64) float64 {
	if cur < prev {
		return 0
	}
	return float64(cur - prev)
}
