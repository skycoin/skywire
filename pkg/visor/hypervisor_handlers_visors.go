// Package visor pkg/visor/hypervisor.go
package visor

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/httputil"
	types "github.com/skycoin/skywire/pkg/transport/types"
	"github.com/skycoin/skywire/pkg/visor/dmsgtracker"
	"github.com/skycoin/skywire/rewards"
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

// VisorTreeSection is one hypervisor's section in the tree response.
// Same shape as HVVisorTreeNode in the visor RPC, but rehydrates each
// HVVisorEntry into the richer Summary the UI's table already knows
// how to render. Visor entries replicate across sections by design;
// sections themselves dedup.
type VisorTreeSection struct {
	HypervisorPK cipher.PubKey   `json:"hypervisor_pk"`
	ViaChain     []cipher.PubKey `json:"via_chain,omitempty"`
	Visors       []Summary       `json:"visors"`
	SubError     string          `json:"sub_error,omitempty"`
}

// VisorTreeResponse wraps the tree sections for the UI's main node
// list endpoint.
type VisorTreeResponse struct {
	Sections []VisorTreeSection `json:"sections"`
}

// getVisorsTreeSummary returns the structured tree of hypervisor
// sections for the UI's main node list. The local hypervisor's
// section is built using the existing getAllVisorsSummary path (with
// the summary cache + dead-visor cleanup) so the local table renders
// identically to today. Sub-hypervisor sections query each direct
// remote hypervisor for its own direct visors via RPC.
//
// Visor entries replicate across sections when a visor is connected
// to multiple hypervisors (intentional — every table shows every
// visor directly connected to that hypervisor). Sections themselves
// dedup by hypervisor PK.
func (hv *Hypervisor) getVisorsTreeSummary() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		localPK := hv.visor.conf.PK

		// Build the local section by reusing the same Summary
		// machinery as /visors-summary — same fields, same cache,
		// same offline-row semantics.
		localVisors := hv.collectLocalVisorSummaries()
		sections := []VisorTreeSection{{
			HypervisorPK: localPK,
			ViaChain:     nil,
			Visors:       localVisors,
		}}

		// Snapshot direct remotes for the sub-hypervisor walk. Each
		// remote that's itself a hypervisor contributes a section;
		// non-hypervisors are silently skipped (every visor in a
		// deployment would otherwise show as a "this isn't a
		// hypervisor" placeholder).
		hv.mu.RLock()
		type remote struct {
			pk   cipher.PubKey
			conn Conn
		}
		remotes := make([]remote, 0, len(hv.remoteVisors))
		for pk, c := range hv.remoteVisors {
			remotes = append(remotes, remote{pk, c})
		}
		hv.mu.RUnlock()

		type subResult struct {
			hyperPK cipher.PubKey
			entries []HVVisorEntry
			err     error
		}
		results := make([]subResult, len(remotes))
		var wg sync.WaitGroup
		wg.Add(len(remotes))
		for i, e := range remotes {
			go func(idx int, pk cipher.PubKey, api API) {
				defer wg.Done()
				done := make(chan struct {
					vs  []HVVisorEntry
					err error
				}, 1)
				go func() {
					vs, err := api.HVListDirectVisors()
					done <- struct {
						vs  []HVVisorEntry
						err error
					}{vs, err}
				}()
				select {
				case r := <-done:
					results[idx] = subResult{pk, r.vs, r.err}
				case <-time.After(10 * time.Second):
					results[idx] = subResult{pk, nil, fmt.Errorf("timeout")}
				}
			}(i, e.pk, e.conn.API)
		}
		wg.Wait()

		rendered := map[cipher.PubKey]bool{localPK: true}
		// subHyperPKs collects every hypervisor PK that gets its own
		// section below the local one. Used to scrub the local
		// section's visor list — a hypervisor that has its own
		// section shouldn't ALSO appear as a row in the local
		// section's table, because the UI tabs render each section
		// independently and the operator perceives the duplicate
		// hypervisor row as a bug ("the visor shows twice").
		subHyperPKs := make(map[cipher.PubKey]struct{}, len(results))
		for _, sr := range results {
			if rendered[sr.hyperPK] {
				continue
			}
			if sr.err != nil {
				// Skip remotes that simply aren't hypervisors (the
				// most common case — every plain visor in the
				// deployment would otherwise show as a no-op error
				// row in the tree). Mirrors isNotHypervisorErr() in
				// rpc_hypervisor_proxy.go — kept in sync there.
				if isNotHypervisorErr(sr.err) {
					continue
				}
				es := sr.err.Error()
				rendered[sr.hyperPK] = true
				subHyperPKs[sr.hyperPK] = struct{}{}
				sections = append(sections, VisorTreeSection{
					HypervisorPK: sr.hyperPK,
					ViaChain:     []cipher.PubKey{localPK},
					SubError:     es,
				})
				continue
			}
			rendered[sr.hyperPK] = true
			subHyperPKs[sr.hyperPK] = struct{}{}
			// Project HVVisorEntry → Summary so the UI's existing
			// table renderer works identically across all sections.
			// Sub-hypervisor visors don't carry the full Summary
			// shape (health, dmsg stats, etc.) — those fields stay
			// zero/empty. The basic fields the table cares about
			// (PK, version, uptime, transports, apps, IP, country)
			// come through populateEntryFromSummary on the remote
			// side, here projected back.
			visors := make([]Summary, 0, len(sr.entries))
			for _, e := range sr.entries {
				visors = append(visors, projectEntryToSummary(e))
			}
			sections = append(sections, VisorTreeSection{
				HypervisorPK: sr.hyperPK,
				ViaChain:     []cipher.PubKey{localPK},
				Visors:       visors,
			})
		}

		// Scrub the local section: drop any visor row whose PK has
		// its own subsequent sub-hypervisor section. Without this,
		// a hypervisor that's both connected to AND served by this
		// visor shows up in both the local section (as a plain
		// connected visor) and its own section (as the section's
		// hypervisor) — duplicate rows from the operator's POV.
		// Doesn't touch the local hypervisor's own row at index 0
		// (it's hv.visor.conf.PK, never in subHyperPKs).
		if len(subHyperPKs) > 0 && len(sections) > 0 {
			scrubbed := make([]Summary, 0, len(sections[0].Visors))
			for _, v := range sections[0].Visors {
				if v.Overview == nil {
					scrubbed = append(scrubbed, v)
					continue
				}
				if _, isSub := subHyperPKs[v.Overview.PubKey]; isSub {
					continue
				}
				scrubbed = append(scrubbed, v)
			}
			sections[0].Visors = scrubbed
		}

		httputil.WriteJSON(w, r, http.StatusOK, VisorTreeResponse{Sections: sections})
	}
}

// collectLocalVisorSummaries returns the Summary slice this
// hypervisor's main node list shows, reusing the existing fetch /
// cache / dead-visor cleanup logic. Extracted from
// getAllVisorsSummary so the tree endpoint can build the local
// section without duplicating that wiring.
func (hv *Hypervisor) collectLocalVisorSummaries() []Summary {
	// Reuse the existing handler's wire by serving it to a discard
	// writer — keeps the cleanup + caching invariants intact. The
	// summary set is small enough that the extra encode/decode is
	// not a concern; alternative is a bigger refactor pulling the
	// fetch logic into a shared helper.
	rec := newBufferingResponseRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/api/visors-summary", nil) //nolint:errcheck
	hv.getAllVisorsSummary()(rec, req)
	var out []Summary
	if err := rec.decode(&out); err != nil {
		hv.logger.WithError(err).Warn("getVisorsTreeSummary: failed to decode local section")
		return nil
	}
	return out
}

// projectEntryTransports returns the per-transport detail the
// node-list's Transports column iterates. Prefers the full
// TransportSummaries field populated post-#2789. Falls back to a slice
// of placeholder TransportSummary entries sized to the count field
// when the remote sub-hypervisor predates #2789 and only sent the
// count — the per-type breakdown is unknown but the operator at
// least sees the right total instead of a "-" dash.
func projectEntryTransports(e HVVisorEntry) []*TransportSummary {
	if len(e.TransportSummaries) > 0 {
		return e.TransportSummaries
	}
	if e.Transports <= 0 {
		return nil
	}
	out := make([]*TransportSummary, e.Transports)
	for i := range out {
		out[i] = &TransportSummary{Type: types.Type("?")}
	}
	return out
}

// projectEntryToSummary fills the Summary fields the main node list's
// table consumes from an HVVisorEntry. Fields the table doesn't
// render (health, dmsg stats, route group info, etc.) stay zero —
// the UI's per-row code already guards on field presence (see
// node.service.ts comments around "Offline rows from the hypervisor
// cache still carry the visor's last-known fields").
func projectEntryToSummary(e HVVisorEntry) Summary {
	overview := &Overview{
		PubKey:         e.PK,
		LocalIP:        e.LocalIP,
		PublicIP:       e.PublicIP,
		CountryCode:    e.CountryCode,
		IsSymmetricNAT: e.IsSymmetricNAT,
		BuildInfo: &buildinfo.Info{
			Version: e.Version,
		},
		Transports: projectEntryTransports(e),
	}
	// Health is partial here — only ServicesHealth carries through
	// the HVVisorEntry round-trip from the remote hypervisor. That's
	// enough for nodeStatusClass to choose between dot-green
	// (healthy), dot-yellow (unhealthy), and dot-outline-gray
	// (unknown).
	//
	// Defaulting: when the remote reports Online=true but doesn't
	// populate ServicesHealth, we synthesize "healthy" here. Two
	// scenarios this covers:
	//
	//   1. Mixed-version deploy where the remote sub-hypervisor
	//      predates #2784 — its HVVisorEntry has no ServicesHealth
	//      field. Without this default the operator would see an
	//      indefinite gray-outline-circle "unknown" dot across the
	//      sub-section until every hypervisor in the deployment
	//      updates.
	//   2. Future paths where Summary.Health is nil on the remote
	//      side (e.g. a visor still mid-startup whose health probes
	//      haven't run yet). "Online + healthy" is a closer
	//      approximation than "Online + unknown" — the remote
	//      hypervisor's Online=true means it CAN talk to the visor.
	//
	// Offline (Online=false) keeps the empty Health so the UI's red
	// dot path triggers.
	var health *HealthInfo
	if e.ServicesHealth != "" {
		health = &HealthInfo{ServicesHealth: e.ServicesHealth}
	} else if e.Online {
		health = &HealthInfo{ServicesHealth: "healthy"}
	}
	// IsHypervisor: the only sub-section row that's actually its
	// section's hypervisor is the one whose PK matches the
	// sub-hypervisor itself — flagged with IsLocal on the remote's
	// HVListDirectVisors response. Setting this drives the ★ icon on
	// the sub-hypervisor's own row in its own section. Other rows
	// (regular visors connected to that sub-hypervisor) stay false.
	return Summary{
		Overview:      overview,
		Health:        health,
		BuildTag:      e.BuildTag,
		Uptime:        e.Uptime,
		Online:        e.Online,
		IsHypervisor:  e.IsLocal,
		RewardAddress: e.RewardAddress,
		ConfigVersion: e.ConfigVersion,
	}
}

// bufferingResponseRecorder is a minimal http.ResponseWriter that
// captures the body so collectLocalVisorSummaries can decode it.
// Avoids pulling in net/http/httptest just for one internal call.
type bufferingResponseRecorder struct {
	hdr  http.Header
	code int
	body []byte
}

func newBufferingResponseRecorder() *bufferingResponseRecorder {
	return &bufferingResponseRecorder{hdr: http.Header{}}
}

func (r *bufferingResponseRecorder) Header() http.Header  { return r.hdr }
func (r *bufferingResponseRecorder) WriteHeader(code int) { r.code = code }
func (r *bufferingResponseRecorder) Write(b []byte) (int, error) {
	r.body = append(r.body, b...)
	return len(b), nil
}
func (r *bufferingResponseRecorder) decode(v interface{}) error {
	if len(r.body) == 0 {
		return fmt.Errorf("empty body")
	}
	return json.Unmarshal(r.body, v)
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
// view, not per-visor); cached on the visor side for 5 minutes.
// Pass ?refresh=true to force the visor to re-aggregate before
// responding (used by the UI's manual-refresh button).
func (hv *Hypervisor) getNetworkView() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var (
			resp *NetworkViewResponse
			err  error
		)
		if r.URL.Query().Get("refresh") == "true" {
			resp, err = hv.visor.NetworkViewRefresh()
		} else {
			resp, err = hv.visor.NetworkView()
		}
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
			return
		}
		httputil.WriteJSON(w, r, http.StatusOK, resp)
	}
}

// getRewardRules serves the embedded mainnet_rules.md text. Used
// by the hypervisor UI's reward-address area to surface the binary's
// own copy of the rules instead of an external link.
func (hv *Hypervisor) getRewardRules() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(rewards.MainnetRules)) //nolint:errcheck
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

		// Remote visor summaries with per-visor timeout. Successful
		// fetches are cached so a later RPC failure can still surface
		// the visor's last-known fields (version, IP, etc) instead of
		// dropping the row from the response and forcing the UI into
		// its empty-hyphens state.
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

				live := false
				select {
				case <-done:
					if rpcErr != nil {
						hv.logger.WithError(rpcErr).WithField("pk", pk).Warn("Failed to obtain summary via RPC")
						mu.Lock()
						deadVisors = append(deadVisors, pk)
						mu.Unlock()
					} else {
						live = true
					}
				case <-time.After(5 * time.Second):
					hv.logger.WithField("pk", pk).Warn("Remote visor summary RPC timed out (5s)")
					mu.Lock()
					deadVisors = append(deadVisors, pk)
					mu.Unlock()
				}

				if live {
					now := time.Now().UTC()
					// Cache a copy so later mutations can't bleed in.
					cached := *sum
					hv.summaryCacheMx.Lock()
					hv.summaryCache[pk] = cachedSummary{sum: &cached, seenAt: now}
					hv.summaryCacheMx.Unlock()
					resp := makeSummaryResp(true, false, sum)
					resp.LastSeenAt = &now
					mu.Lock()
					summaries = append(summaries, resp)
					mu.Unlock()
					return
				}

				// Live fetch failed — try the cache.
				hv.summaryCacheMx.RLock()
				cached, ok := hv.summaryCache[pk]
				hv.summaryCacheMx.RUnlock()
				if !ok {
					return
				}
				stale := *cached.sum
				offlineSince := time.Now().UTC()
				resp := makeSummaryResp(false, false, &stale)
				seenAt := cached.seenAt
				resp.LastSeenAt = &seenAt
				resp.OfflineSince = &offlineSince
				mu.Lock()
				summaries = append(summaries, resp)
				mu.Unlock()
			}(entry.pk, entry.conn)
		}
		wg.Wait()

		// Remove dead visors under write lock (safe — no goroutines accessing the map).
		// Cache entries are NOT dropped here; they keep serving stale rows
		// until the visor reconnects and we get a fresh summary, which
		// overwrites the cache entry.
		if len(deadVisors) > 0 {
			hv.mu.Lock()
			for _, pk := range deadVisors {
				delete(hv.remoteVisors, pk)
			}
			hv.mu.Unlock()
		}

		// Surface stale cache rows for PKs that didn't make it into
		// summaries this round. The above loop only consults the cache
		// for visors still present in remoteVisors at fetch time, so
		// once a visor's been cleaned up after its first transient RPC
		// failure, subsequent refreshes would drop the row entirely —
		// operators saw nodes disappearing from the list rather than
		// staying as offline. Sweeping the cache here makes the offline
		// state durable across cleanups; rows reappear as live when the
		// visor reconnects and the cache is overwritten with fresh data.
		rendered := make(map[cipher.PubKey]struct{}, len(summaries))
		for _, s := range summaries {
			if s.Overview != nil {
				rendered[s.Overview.PubKey] = struct{}{}
			}
		}
		hv.summaryCacheMx.RLock()
		for pk, cached := range hv.summaryCache {
			if _, already := rendered[pk]; already {
				continue
			}
			stale := *cached.sum
			offlineSince := time.Now().UTC()
			resp := makeSummaryResp(false, false, &stale)
			seenAt := cached.seenAt
			resp.LastSeenAt = &seenAt
			resp.OfflineSince = &offlineSince
			summaries = append(summaries, resp)
		}
		hv.summaryCacheMx.RUnlock()

		// Attach DMSG stats
		for i := 0; i < len(summaries); i++ {
			if stat, ok := dmsgStats[summaries[i].Overview.PubKey.String()]; ok {
				summaries[i].DmsgStats = &stat
			}
		}

		httputil.WriteJSON(w, r, http.StatusOK, summaries)
	}
}
