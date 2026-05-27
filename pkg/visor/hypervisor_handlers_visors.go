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
				sections = append(sections, VisorTreeSection{
					HypervisorPK: sr.hyperPK,
					ViaChain:     []cipher.PubKey{localPK},
					SubError:     es,
				})
				continue
			}
			rendered[sr.hyperPK] = true
			// Project HVVisorEntry → Summary so the UI's existing
			// table renderer works identically across all sections.
			// Sub-hypervisor visors don't carry the full Summary
			// shape (health, dmsg stats, etc.) — those fields stay
			// zero/empty. The basic fields the table cares about
			// (PK, version, uptime, transports, apps, IP, country)
			// come through populateEntryFromSummary on the remote
			// side, here projected back.
			visors := make([]Summary, 0, len(sr.entries))
			type fetchTarget struct {
				idx int
				pk  cipher.PubKey
			}
			var fetchTargets []fetchTarget
			for i, e := range sr.entries {
				s := projectEntryToSummary(e)
				// The sub-section's ★ icon should appear on the
				// section's own hypervisor row and nowhere else.
				// Override IsHypervisor (which projectEntryToSummary
				// sets from e.IsLocal) with strict PK-equality
				// against this section's HypervisorPK. Defends
				// against any path that might mistakenly mark a
				// non-local entry with IsLocal=true on the remote
				// side, and matches the semantic the hvui's
				// node-list uses for the local section: "star = this
				// row is the section's hypervisor."
				s.IsHypervisor = e.PK == sr.hyperPK
				visors = append(visors, s)
				// Pre-#2789 sub-hypervisors return HVVisorEntry with
				// only the transport COUNT, no per-transport detail.
				// projectEntryTransports synthesizes typeless "?"
				// placeholders so the count renders; the frontend
				// filters those out for cleanliness — net effect is
				// "Total: N" with no per-type breakdown. Fall back
				// to HVVisorSummary (which routes through the
				// proxy chain and ultimately calls the visor's real
				// Summary()) so the per-type breakdown surfaces
				// regardless of the sub-hypervisor's version.
				if e.Online && len(e.TransportSummaries) == 0 && e.Transports > 0 {
					fetchTargets = append(fetchTargets, fetchTarget{i, e.PK})
				}
			}
			// Fan-out the fallback Summary fetches in parallel,
			// bounded by a per-call timeout so a slow visor doesn't
			// hold the entire tree response. Failures are silently
			// tolerated — the placeholder row is still rendered as
			// "Total: N" via projectEntryTransports, so the
			// per-type breakdown just stays missing if the deeper
			// query times out or errors.
			if len(fetchTargets) > 0 && hv.visor != nil {
				var fwg sync.WaitGroup
				fwg.Add(len(fetchTargets))
				for _, ft := range fetchTargets {
					go func(idx int, pk cipher.PubKey) {
						defer fwg.Done()
						done := make(chan *Summary, 1)
						go func() {
							s, err := hv.visor.HVVisorSummary(pk)
							if err != nil {
								done <- nil
								return
							}
							done <- s
						}()
						select {
						case full := <-done:
							if full == nil || full.Overview == nil || len(full.Overview.Transports) == 0 {
								return
							}
							if visors[idx].Overview == nil {
								return
							}
							visors[idx].Overview.Transports = full.Overview.Transports
						case <-time.After(5 * time.Second):
						}
					}(ft.idx, ft.pk)
				}
				fwg.Wait()
			}

			// Enrich Hostname from the local summaryCache when the
			// sub-hypervisor's HVVisorEntry round-trip didn't carry it
			// — happens when the sub-hypervisor predates the
			// Hostname-on-HVVisorEntry change (PR #2808). The local
			// section already polls Summary for any PK we have a
			// direct conn to, and that Summary's Overview.Hostname is
			// the source of truth. Without this bridge, sub-section
			// rows show LAN IP as the default label until the
			// sub-hypervisor itself updates.
			hv.summaryCacheMx.RLock()
			for i := range visors {
				if visors[i].Overview == nil || visors[i].Overview.Hostname != "" {
					continue
				}
				cached, ok := hv.summaryCache[visors[i].Overview.PubKey]
				if !ok || cached.sum == nil || cached.sum.Overview == nil {
					continue
				}
				visors[i].Overview.Hostname = cached.sum.Overview.Hostname
			}
			hv.summaryCacheMx.RUnlock()

			sections = append(sections, VisorTreeSection{
				HypervisorPK: sr.hyperPK,
				ViaChain:     []cipher.PubKey{localPK},
				Visors:       visors,
			})
		}

		// Intentionally NOT scrubbing the local section. A visor that
		// is both (a) directly connected to this hypervisor and (b)
		// itself a hypervisor with its own section represents two
		// distinct relationships, and both should be visible. The
		// local section's row reflects "this visor is one of my
		// managed visors"; the sub-section's IsLocal row reflects
		// "this visor runs its own hypervisor too." Operators rely on
		// the local-section row to manage the visor as a peer — pty
		// access, transport admin, etc. — independent of its
		// hypervisor role. Filtering it out hid managed visors that
		// happened to also run a hypervisor.
		//
		// Sections still dedup by hypervisor PK (line 173 onward), so
		// no section ever appears twice; only visor *rows* replicate
		// across sections, which matches the doc comment at the top
		// of this function ("Visor entries replicate across sections
		// when a visor is connected to multiple hypervisors").

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
		// Hostname is the hvui's default label fallback. Carrying
		// it through HVVisorEntry → Summary lets sub-section rows
		// render the same label the visor's own overview would show.
		Hostname: e.Hostname,
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

		// Remote visor summaries with per-visor timeout. The 5s outer
		// cap keeps the UI snappy; cache update + eviction live in
		// the inner Summary goroutine so completions that arrive
		// after the outer fired still land — without this, an RPC
		// that consistently took 6–20s would never refresh the cache
		// and the visor would render permanently stale.
		var mu sync.Mutex
		wg := new(sync.WaitGroup)
		wg.Add(len(remotes))

		// Visors whose cache was refreshed within this freshness
		// window count as "live" even if THIS round's Summary RPC
		// timed out at the 5s outer cap — they're slow, not broken.
		// 3 minutes covers the 2-minute dmsg.StreamIdleTimeout
		// (pkg/dmsg/dmsg/types.go) plus a generous slack for the
		// peer-side redial → hypervisor-accept → next-poll cycle.
		// Without this, a peer whose stream just idle-closed shows
		// "last seen 2-3min ago" in the UI for one or two polls
		// before the new conn's first Summary lands — visible
		// flicker that operators flag as "nodes are stale". 30s
		// (the original value) caught only inline RPC slowness
		// and missed the steady-state 2-min idle pattern entirely.
		const cacheFreshWindow = 3 * time.Minute

		for _, entry := range remotes {
			go func(pk cipher.PubKey, c Conn) {
				defer wg.Done()

				type rpcResult struct {
					sum    *Summary
					rpcErr error
				}
				resultCh := make(chan rpcResult, 1)
				go func() {
					s, e := c.API.Summary()
					if e == nil && s != nil {
						// Cache the success here (not in the outer
						// select) so completions that arrive after
						// the 5s outer timeout still refresh the
						// cache for the next poll round.
						now := time.Now().UTC()
						cached := *s
						hv.summaryCacheMx.Lock()
						hv.summaryCache[pk] = cachedSummary{sum: &cached, seenAt: now}
						hv.summaryCacheMx.Unlock()
					} else {
						hv.logger.WithError(e).WithField("pk", pk).Warn("Failed to obtain summary via RPC")
						// Evict on RPC failure so the next accept on
						// a fresh conn isn't stomped by this stale
						// entry. Done inline so it works even when
						// the outer goroutine returned on the 5s
						// timeout path; the next poll round picks up
						// the new conn the peer redials in with.
						hv.mu.Lock()
						delete(hv.remoteVisors, pk)
						hv.mu.Unlock()
					}
					resultCh <- rpcResult{s, e}
				}()

				select {
				case r := <-resultCh:
					if r.rpcErr == nil {
						now := time.Now().UTC()
						resp := makeSummaryResp(true, false, r.sum)
						resp.LastSeenAt = &now
						mu.Lock()
						summaries = append(summaries, resp)
						mu.Unlock()
						return
					}
					// rpcErr already logged + peer evicted in the
					// inner goroutine; fall through to cache.
				case <-time.After(5 * time.Second):
					hv.logger.WithField("pk", pk).
						Warn("Remote visor summary RPC slow (>5s); using cache, background-updating")
				}

				// Live fetch didn't return success in time — try the cache.
				hv.summaryCacheMx.RLock()
				cached, ok := hv.summaryCache[pk]
				hv.summaryCacheMx.RUnlock()
				if !ok {
					return
				}
				stale := *cached.sum
				// A recent cache hit means a previous round (or this
				// round's background completion) just updated us —
				// the visor's RPCs are merely slow, not dead. Surface
				// it as online so the UI doesn't flap on slow peers.
				fresh := time.Since(cached.seenAt) < cacheFreshWindow
				seenAt := cached.seenAt
				resp := makeSummaryResp(fresh, false, &stale)
				resp.LastSeenAt = &seenAt
				if !fresh {
					offlineSince := time.Now().UTC()
					resp.OfflineSince = &offlineSince
				}
				mu.Lock()
				summaries = append(summaries, resp)
				mu.Unlock()
			}(entry.pk, entry.conn)
		}
		wg.Wait()

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
			// Same freshness window as the in-remoteVisors fallback
			// path above: a peer that was just Summary'd N seconds
			// ago (within the dmsg stream-idle cycle) hasn't been
			// disconnected long enough to render as offline. Once
			// outside the window the row keeps showing "last seen
			// at" with offline_since stamped.
			fresh := time.Since(cached.seenAt) < cacheFreshWindow
			seenAt := cached.seenAt
			resp := makeSummaryResp(fresh, false, &stale)
			resp.LastSeenAt = &seenAt
			if !fresh {
				offlineSince := time.Now().UTC()
				resp.OfflineSince = &offlineSince
			}
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
