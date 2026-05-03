// Package visor pkg/visor/hypervisor.go
package visor

import (
	"net/http"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/visor/dmsgtracker"
)

// provides overview of all visors.
func (hv *Hypervisor) getVisors() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Snapshot remote visors under lock, then release immediately
		hv.mu.RLock()
		type visorEntry struct {
			pk   cipher.PubKey
			conn Conn
		}
		remotes := make([]visorEntry, 0, len(hv.remoteVisors))
		for pk, c := range hv.remoteVisors {
			remotes = append(remotes, visorEntry{pk, c})
		}
		hv.mu.RUnlock()

		i := 0
		if hv.visor != nil {
			i++
		}
		overviews := make([]Overview, len(remotes)+i)

		if hv.visor != nil {
			overview, err := hv.visor.Overview()
			if err != nil {
				hv.logger.WithError(err).Warn("Failed to obtain overview of this visor.")
				overview = &Overview{PubKey: hv.visor.conf.PK}
			}
			overviews[0] = *overview
		}

		wg := new(sync.WaitGroup)
		wg.Add(len(remotes))
		for _, entry := range remotes {
			go func(pk cipher.PubKey, c Conn, idx int) {
				defer wg.Done()
				// Per-visor timeout prevents one dead visor from blocking everything
				done := make(chan struct{})
				var overview *Overview
				go func() {
					var err error
					overview, err = c.API.Overview()
					if err != nil {
						hv.logger.WithError(err).WithField("pk", pk).Warn("Failed to obtain overview via RPC")
						overview = &Overview{PubKey: pk}
					}
					close(done)
				}()
				select {
				case <-done:
					overviews[idx] = *overview
				case <-time.After(5 * time.Second):
					hv.logger.WithField("pk", pk).Warn("Remote visor RPC timed out (5s)")
					overviews[idx] = Overview{PubKey: pk}
				}
			}(entry.pk, entry.conn, i)
			i++
		}
		wg.Wait()

		httputil.WriteJSON(w, r, http.StatusOK, overviews)
	}
}

// provides overview of single visor.
func (hv *Hypervisor) getVisor() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		overview, err := ctx.API.Overview()
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
			return
		}

		httputil.WriteJSON(w, r, http.StatusOK, overview)
	})
}

// provides extra summary of single visor.
func (hv *Hypervisor) getVisorSummary() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		summary, err := ctx.API.Summary()
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
			return
		}

		dmsgStats := make(map[string]dmsgtracker.DmsgClientSummary)
		dSummary := hv.getDmsgSummary()
		for _, stat := range dSummary {
			dmsgStats[stat.PK.String()] = stat
		}

		if stat, ok := dmsgStats[summary.Overview.PubKey.String()]; ok {
			summary.DmsgStats = &stat
		}
		// If stats not found, leave DmsgStats as nil (don't create empty struct with 0ms latency)

		// Check if this is the local visor (hypervisor)
		summary.IsHypervisor = summary.Overview.PubKey == hv.visor.conf.PK

		httputil.WriteJSON(w, r, http.StatusOK, summary)
	})
}

// getNetworkView surfaces the SD/TPD/UT-aggregated network table
// the `cli sd` command prints. Hypervisor-scope (network-wide
// view, not per-visor); cached on the visor side for 30s.
func (hv *Hypervisor) getNetworkView() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		resp, err := hv.visor.NetworkView()
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
			return
		}
		httputil.WriteJSON(w, r, http.StatusOK, resp)
	}
}

func (hv *Hypervisor) getAllVisorsSummary() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Get DMSG stats first (uses its own lock internally)
		dmsgStats := make(map[string]dmsgtracker.DmsgClientSummary)
		for _, stat := range hv.getDmsgSummary() {
			dmsgStats[stat.PK.String()] = stat
		}

		// Snapshot remote visors under lock, then release immediately
		hv.mu.RLock()
		type visorEntry struct {
			pk   cipher.PubKey
			conn Conn
		}
		remotes := make([]visorEntry, 0, len(hv.remoteVisors))
		for pk, c := range hv.remoteVisors {
			remotes = append(remotes, visorEntry{pk, c})
		}
		hv.mu.RUnlock()

		summaries := make([]Summary, 0, len(remotes)+1)

		// Local visor summary
		summary, err := hv.visor.Summary()
		if err != nil {
			hv.logger.WithError(err).Warn("Failed to obtain summary of this visor.")
			summary = &Summary{
				Overview: &Overview{PubKey: hv.visor.conf.PK},
				Health:   &HealthInfo{},
			}
		}
		summaries = append(summaries, makeSummaryResp(err == nil, true, summary))

		// Remote visor summaries with per-visor timeout
		var deadVisors []cipher.PubKey
		var mu sync.Mutex
		wg := new(sync.WaitGroup)
		wg.Add(len(remotes))

		for _, entry := range remotes {
			go func(pk cipher.PubKey, c Conn) {
				defer wg.Done()

				done := make(chan struct{})
				var sum *Summary
				var rpcErr error
				go func() {
					sum, rpcErr = c.API.Summary()
					close(done)
				}()

				select {
				case <-done:
					if rpcErr != nil {
						hv.logger.WithError(rpcErr).WithField("pk", pk).Warn("Failed to obtain summary via RPC")
						mu.Lock()
						deadVisors = append(deadVisors, pk)
						mu.Unlock()
					} else {
						resp := makeSummaryResp(true, false, sum)
						mu.Lock()
						summaries = append(summaries, resp)
						mu.Unlock()
					}
				case <-time.After(5 * time.Second):
					hv.logger.WithField("pk", pk).Warn("Remote visor summary RPC timed out (5s)")
					mu.Lock()
					deadVisors = append(deadVisors, pk)
					mu.Unlock()
				}
			}(entry.pk, entry.conn)
		}
		wg.Wait()

		// Remove dead visors under write lock (safe — no goroutines accessing the map)
		if len(deadVisors) > 0 {
			hv.mu.Lock()
			for _, pk := range deadVisors {
				delete(hv.remoteVisors, pk)
			}
			hv.mu.Unlock()
		}

		// Attach DMSG stats
		for i := 0; i < len(summaries); i++ {
			if stat, ok := dmsgStats[summaries[i].Overview.PubKey.String()]; ok {
				summaries[i].DmsgStats = &stat
			}
		}

		httputil.WriteJSON(w, r, http.StatusOK, summaries)
	}
}
