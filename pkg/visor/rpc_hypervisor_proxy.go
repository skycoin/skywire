// Package visor pkg/visor/rpc_hypervisor_proxy.go c3-vis-core
//
// RPC methods that proxy through the hypervisor's DMSG connections
// to remote visors. These enable CLI/TUI access to remote visor data
// without needing direct transport connections.
package visor

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

// sortHVVisorEntries orders an HVVisorEntry slice deterministically so
// UI tables / CLI lists stay stable between fetches. Without this the
// upstream `range hv.remoteVisors` returns entries in Go's randomized
// map-iteration order, and every refresh shuffles the rows on the
// operator — visible to the eye as rows jumping around for no reason.
//
// Sort key, in priority order:
//  1. IsLocal first (the hypervisor's own row, when present)
//  2. PublicIP matches the local hypervisor's PublicIP — these are
//     "LAN-adjacent" visors (same NAT, same site as the hypervisor).
//     Same WAN IP is a stronger proxy for "same physical site" than
//     LocalIP comparison would be, because separate routers on the
//     same WAN often hand out the same RFC1918 LAN range (e.g.
//     192.168.0.x) but their WAN IPs match only if they share the
//     upstream NAT.
//  3. Has a non-empty PublicIP at all — visors with public-IP info
//     before visors without (we still know "they're somewhere" vs.
//     "we don't know where").
//  4. PK lex order — final tiebreak, deterministic across processes.
//
// localPublicIP is the local hypervisor's WAN IP (empty string is
// fine — disables the bucket-2 priority but the rest still applies).
func sortHVVisorEntries(entries []visorapi.HVVisorEntry, localPublicIP string) {
	bucket := func(e *visorapi.HVVisorEntry) int {
		switch {
		case e.IsLocal:
			return 0
		case localPublicIP != "" && e.PublicIP == localPublicIP:
			return 1
		case e.PublicIP != "":
			return 2
		default:
			return 3
		}
	}
	sort.SliceStable(entries, func(i, j int) bool {
		bi, bj := bucket(&entries[i]), bucket(&entries[j])
		if bi != bj {
			return bi < bj
		}
		return entries[i].PK.Hex() < entries[j].PK.Hex()
	})
}

func populateEntryFromSummary(entry *visorapi.HVVisorEntry, summary *visorapi.Summary) {
	entry.Version = summary.Overview.BuildInfo.Version
	entry.BuildTag = summary.BuildTag
	entry.Uptime = summary.Uptime
	entry.LocalIP = summary.Overview.LocalIP
	entry.PublicIP = summary.Overview.PublicIP
	entry.CountryCode = summary.Overview.CountryCode
	entry.IsSymmetricNAT = summary.Overview.IsSymmetricNAT
	entry.Transports = len(summary.Overview.Transports)
	// Only the type and the direction survive: this entry feeds the
	// node-list table (and `hv ls`, which reads the count alone), and
	// it crosses dmsg on every HVListDirectVisors call. Sending the
	// full per-transport detail put ~2.7 MB on the wire per poll for a
	// three-sub-hypervisor tree. See compactTransportSummaries.
	entry.TransportSummaries = compactTransportSummaries(summary.Overview.Transports)
	entry.Apps = len(summary.Overview.Apps)
	entry.ConfigVersion = summary.ConfigVersion
	entry.RewardAddress = summary.RewardAddress
	entry.Hostname = summary.Overview.Hostname
	entry.Load = summary.Load
	// The visor's own conf.Hypervisors, already carried in the Overview it
	// returns — the hypervisors it answers to, which only it knows. Lifting it
	// onto the entry is what puts it in `hv ls`.
	entry.Hypervisors = summary.Overview.Hypervisors
	if summary.Health != nil {
		entry.ServicesHealth = summary.Health.ServicesHealth
	}
}

// HVListDirectVisors returns summaries of visors DIRECTLY connected to
// this hypervisor — does NOT walk sub-hypervisors. Used by the tree
// builder to query each sub-hypervisor's own direct visors without
// pulling its already-flattened sub-merge.
//
// Local visor is included first (with IsLocal=true) when this visor
// has the hypervisor flag enabled, matching HVListVisors's shape.
func (v *Visor) HVListDirectVisors() ([]visorapi.HVVisorEntry, error) {
	if v.hvInstance == nil {
		return nil, fmt.Errorf("hypervisor not running")
	}
	if !v.hvInstance.IsEnabled() {
		return nil, fmt.Errorf("hypervisor not enabled")
	}

	hv := v.hvInstance
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

	log := logging.MustGetLogger("hv_list_direct_visors")

	results := make([]visorapi.HVVisorEntry, len(remotes))
	var wg sync.WaitGroup
	wg.Add(len(remotes))
	for i, e := range remotes {
		go func(idx int, pk cipher.PubKey, api visorapi.API) {
			defer wg.Done()
			results[idx] = hvVisorEntryFor(hv, pk, api, log, "HVListDirectVisors")
		}(i, e.pk, e.conn.API)
	}
	wg.Wait()

	// Build the local entry first so we can use its PublicIP as the
	// "LAN-adjacent" sort key for the remote entries.
	var localEntry visorapi.HVVisorEntry
	hasLocal := false
	if hv.visor != nil {
		localEntry = visorapi.HVVisorEntry{PK: v.conf.PK, Online: true, IsLocal: true}
		if localSummary, err := v.Summary(); err == nil {
			populateEntryFromSummary(&localEntry, localSummary)
		}
		hasLocal = true
	}
	sortHVVisorEntries(results, localEntry.PublicIP)
	if hasLocal {
		results = append([]visorapi.HVVisorEntry{localEntry}, results...)
	}
	return results, nil
}

// HVListVisors returns summaries of all visors connected to this hypervisor.
func (v *Visor) HVListVisors() ([]visorapi.HVVisorEntry, error) {
	if v.hvInstance == nil {
		return nil, fmt.Errorf("hypervisor not running")
	}
	if !v.hvInstance.IsEnabled() {
		return nil, fmt.Errorf("hypervisor not enabled")
	}

	hv := v.hvInstance
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

	log := logging.MustGetLogger("hv_list_visors")

	// Query each visor in parallel with a per-visor timeout
	results := make([]visorapi.HVVisorEntry, len(remotes))
	var wg sync.WaitGroup
	wg.Add(len(remotes))

	for i, e := range remotes {
		go func(idx int, pk cipher.PubKey, api visorapi.API) {
			defer wg.Done()
			results[idx] = hvVisorEntryFor(hv, pk, api, log, "HVListVisors")
		}(i, e.pk, e.conn.API)
	}
	wg.Wait()

	// Build the local entry; PublicIP anchors LAN-adjacency in the final
	// sort. We append rather than prepend here — the single sortHVVisor-
	// Entries call at the end of this function bubbles IsLocal=true to
	// the top, so position-on-insert doesn't matter.
	var localEntry visorapi.HVVisorEntry
	if hv.visor != nil {
		localEntry = visorapi.HVVisorEntry{PK: v.conf.PK, Online: true, IsLocal: true}
		if localSummary, err := v.Summary(); err == nil {
			populateEntryFromSummary(&localEntry, localSummary)
		}
		results = append(results, localEntry)
	}

	// Merge sub-hypervisor visors: for each direct remote that is itself a
	// hypervisor, query its HVListVisors and add any visors not already
	// represented in our list. Tagged with ProxiedVia so write actions can
	// route through the right hop. Cycle protection: skip any PK that is
	// already in our `results` (covers immediate H2↔H1 mutual loops).
	seen := make(map[cipher.PubKey]bool, len(results))
	for _, e := range results {
		seen[e.PK] = true
	}
	subResults := make([][]visorapi.HVVisorEntry, len(remotes))
	subPKs := make([]cipher.PubKey, len(remotes))
	var subWg sync.WaitGroup
	subWg.Add(len(remotes))
	for i, e := range remotes {
		subPKs[i] = e.pk
		go func(idx int, hyperPK cipher.PubKey, api visorapi.API) {
			defer subWg.Done()
			done := make(chan []visorapi.HVVisorEntry, 1)
			go func() {
				sub, err := api.HVListVisors()
				if err != nil {
					// Errors swallowed silently here previously made
					// "the feature didn't work" un-diagnosable from the
					// caller's side. Most common reason: the remote
					// isn't running as a hypervisor (`hv status:
					// disabled`) and HVListVisors returns "hypervisor
					// not enabled". Log so operators can see why a
					// sub-hypervisor merge yielded nothing.
					log.WithError(err).WithField("pk", hyperPK.String()).
						Debug("HVListVisors: sub-hypervisor query failed (likely not a hypervisor)")
					done <- nil
					return
				}
				done <- sub
			}()
			select {
			case sub := <-done:
				subResults[idx] = sub
			case <-time.After(10 * time.Second):
				log.WithField("pk", hyperPK.String()).Warn("HVListVisors: sub-hypervisor query timed out")
			}
		}(i, e.pk, e.conn.API)
	}
	subWg.Wait()
	for idx, sub := range subResults {
		hyperPK := subPKs[idx]
		for _, entry := range sub {
			if seen[entry.PK] {
				continue
			}
			seen[entry.PK] = true
			entry.IsLocal = false
			pk := hyperPK
			entry.ProxiedVia = &pk
			results = append(results, entry)
		}
	}

	// Final stable sort across the merged direct + sub-hypervisor entries.
	// IsLocal stays at the top via bucket 0; everything else falls into
	// LAN-adjacent → has-PublicIP → no-PublicIP, PK-tiebroken.
	sortHVVisorEntries(results, localEntry.PublicIP)

	return results, nil
}

// HVListVisorsTree builds a tree-shaped response of every hypervisor
// reachable from this one (local + direct sub-hypervisors only, depth
// 2). Each section reports the visors DIRECTLY connected to that
// hypervisor; cross-section dedup is the UI's job (visors with two
// hypervisors appear in two sections by design).
//
// Cycle protection: tables dedup by hypervisor PK — if a sub-
// hypervisor is reachable via two paths (e.g. via mutual hypervisor
// hvVisorEntryFor queries one remote visor's Summary over its cached RPC conn
// with a 10s timeout, building the listing row. A success ALSO refreshes the
// hypervisor's summary cache; a failure falls back to that cache. This is what
// keeps `cli visor hv ls` informative through a fleet-wide restart: peers dial
// IN to the hypervisor, so after they all restart every cached conn is dead
// ("connection is shut down") until each peer re-registers minutes later —
// without the fallback an operator sees a wall of bare error rows where
// last-known state (version, IP, label) is sitting in the cache. The row is
// marked offline (Online=false) with the error kept alongside the cached age,
// so staleness is explicit, never disguised as live state.
func hvVisorEntryFor(hv *Hypervisor, pk cipher.PubKey, api visorapi.API, log *logging.Logger, tag string) visorapi.HVVisorEntry {
	type sumResult struct {
		summary *visorapi.Summary
		err     error
	}
	// Buffered so the Summary goroutine never blocks on send — if we time
	// out, the late result is just gc'd. Also avoids the data race the old
	// shared-entry pattern had (both the Summary goroutine and the timeout
	// branch could write the same struct).
	sumCh := make(chan sumResult, 1)
	go func() {
		summary, err := api.Summary()
		sumCh <- sumResult{summary, err}
	}()

	entry := visorapi.HVVisorEntry{PK: pk}
	select {
	case r := <-sumCh:
		if r.err == nil && r.summary != nil {
			// Online ONLY when we actually got a Summary back —
			// hardcoded Online=true previously rendered ghost rows.
			entry.Online = true
			populateEntryFromSummary(&entry, r.summary)
			now := time.Now().UTC()
			cached := *r.summary
			hv.summaryCacheMx.Lock()
			hv.summaryCache[pk] = cachedSummary{sum: &cached, seenAt: now}
			hv.summaryCacheMx.Unlock()
			return entry
		}
		if r.err != nil {
			entry.Error = r.err.Error()
		}
	case <-time.After(10 * time.Second):
		entry.Error = "timeout (10s)"
		log.WithField("pk", pk.String()).Warnf("%s: visor query timed out", tag)
	}

	// Failure path: surface the last known state from the cache, offline and
	// age-stamped, instead of a bare error row.
	hv.summaryCacheMx.RLock()
	cached, ok := hv.summaryCache[pk]
	hv.summaryCacheMx.RUnlock()
	if ok && cached.sum != nil {
		populateEntryFromSummary(&entry, cached.sum)
		entry.Online = false
		entry.Error = entry.Error + " (cached " + time.Since(cached.seenAt).Truncate(time.Second).String() + " ago)"
	}
	return entry
}

// pairs), its table renders once. Per-section query timeout (10s)
// bounds wall time regardless of depth.
//
// Currently 1 level deep (local + direct sub-hypervisors). Walking
// transitively to depth N would need a request-scoped visited-set
// on the wire so each hop knows what NOT to recurse into;
// out-of-scope for v1.
func (v *Visor) HVListVisorsTree() (*visorapi.HVVisorTree, error) {
	if v.hvInstance == nil {
		return nil, fmt.Errorf("hypervisor not running")
	}
	if !v.hvInstance.IsEnabled() {
		return nil, fmt.Errorf("hypervisor not enabled")
	}

	localPK := v.conf.PK
	localVisors, err := v.HVListDirectVisors()
	if err != nil {
		return nil, err
	}

	sections := []visorapi.HVVisorTreeNode{
		{
			HypervisorPK: localPK,
			ViaChain:     nil,
			Visors:       localVisors,
		},
	}

	hv := v.hvInstance
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

	log := logging.MustGetLogger("hv_list_visors_tree")

	// Query each direct remote's HVListDirectVisors in parallel; only
	// remotes that respond successfully are themselves hypervisors and
	// contribute a section. Remotes that error (not a hypervisor / not
	// reachable) are skipped; the error gets logged for operators.
	type subResult struct {
		hyperPK cipher.PubKey
		visors  []visorapi.HVVisorEntry
		err     error
	}
	results := make([]subResult, len(remotes))
	var wg sync.WaitGroup
	wg.Add(len(remotes))
	for i, e := range remotes {
		go func(idx int, hyperPK cipher.PubKey, api visorapi.API) {
			defer wg.Done()
			done := make(chan struct {
				vs  []visorapi.HVVisorEntry
				err error
			}, 1)
			go func() {
				vs, err := api.HVListDirectVisors()
				done <- struct {
					vs  []visorapi.HVVisorEntry
					err error
				}{vs, err}
			}()
			select {
			case r := <-done:
				results[idx] = subResult{hyperPK, r.vs, r.err}
			case <-time.After(10 * time.Second):
				log.WithField("pk", hyperPK.String()).Warn("HVListVisorsTree: sub-hypervisor query timed out")
				results[idx] = subResult{hyperPK, nil, fmt.Errorf("timeout")}
			}
		}(i, e.pk, e.conn.API)
	}
	wg.Wait()

	// Dedup sub-hypervisors by PK. The local hypervisor is already in
	// `sections[0]`; sub-hypervisor PKs that match the local PK (the
	// mutual-hypervisor case) are skipped — they'd render the same
	// table the local section already has.
	rendered := map[cipher.PubKey]bool{localPK: true}
	for _, r := range results {
		if rendered[r.hyperPK] {
			continue
		}
		if r.err != nil {
			// Sub query failed but we still want to render a section
			// so operators see WHICH sub-hypervisor failed and how.
			// Skip if the remote simply isn't a hypervisor (the most
			// common case — "hypervisor not enabled") to avoid noise.
			if isNotHypervisorErr(r.err) {
				log.WithError(r.err).WithField("pk", r.hyperPK.String()).
					Debug("HVListVisorsTree: skipping non-hypervisor remote")
				continue
			}
			rendered[r.hyperPK] = true
			sections = append(sections, visorapi.HVVisorTreeNode{
				HypervisorPK: r.hyperPK,
				ViaChain:     []cipher.PubKey{localPK},
				SubError:     r.err.Error(),
			})
			continue
		}
		rendered[r.hyperPK] = true
		sections = append(sections, visorapi.HVVisorTreeNode{
			HypervisorPK: r.hyperPK,
			ViaChain:     []cipher.PubKey{localPK},
			Visors:       r.visors,
		})
	}

	// Stabilize sub-hypervisor section order + per-section visor order.
	// sections[0] is the local hypervisor's own section — pin it at the
	// top regardless of sort. Sub-hypervisors (sections[1:]) get ordered
	// by LAN adjacency to the local hypervisor (same PublicIP as our
	// local section's IsLocal entry, when present), then PK lex.
	//
	// For each section's Visors slice, sortHVVisorEntries gives stable
	// order: IsLocal first (each section has its own IsLocal=true entry,
	// for its own hypervisor's row), then LAN-adjacent, then by PK.
	var localPublicIP string
	for _, e := range sections[0].Visors {
		if e.IsLocal {
			localPublicIP = e.PublicIP
			break
		}
	}
	for i := range sections {
		sortHVVisorEntries(sections[i].Visors, localPublicIP)
	}
	if len(sections) > 1 {
		// Bucket each sub-hypervisor by looking up its PK in the local
		// section's visors. If the sub-hypervisor is also one of our
		// local hypervisor's known visors, we already have its
		// PublicIP — use it for LAN-adjacency bucketing. Otherwise it
		// falls into the "unknown" bucket and sorts by PK alone.
		pubIPByPK := make(map[cipher.PubKey]string, len(sections[0].Visors))
		for _, e := range sections[0].Visors {
			if e.PublicIP != "" {
				pubIPByPK[e.PK] = e.PublicIP
			}
		}
		sectionBucket := func(s *visorapi.HVVisorTreeNode) int {
			pip, ok := pubIPByPK[s.HypervisorPK]
			switch {
			case !ok:
				return 2
			case localPublicIP != "" && pip == localPublicIP:
				return 0
			default:
				return 1
			}
		}
		subs := sections[1:]
		sort.SliceStable(subs, func(i, j int) bool {
			bi, bj := sectionBucket(&subs[i]), sectionBucket(&subs[j])
			if bi != bj {
				return bi < bj
			}
			return subs[i].HypervisorPK.Hex() < subs[j].HypervisorPK.Hex()
		})
	}

	return &visorapi.HVVisorTree{Sections: sections}, nil
}

// isNotHypervisorErr identifies the well-known "remote isn't running
// as a hypervisor" errors from HVListDirectVisors, so we can quietly
// skip those remotes from the tree (every visor in a deployment that
// ISN'T also a hypervisor would otherwise show up as a SubError row).
//
// Three distinct cases collapse into "not a hypervisor":
//  1. "hypervisor not running" — remote is fully running but its
//     hypervisor module never initialized (no config section).
//  2. "hypervisor not enabled" — module initialized but the runtime
//     toggle is off (operator disabled via `cli visor hv disable`).
//  3. "rpc: can't find method app-visor.HVListDirectVisors" — remote
//     is on a pre-#2633 binary that predates the new RPC method.
//     Functionally indistinguishable from "not a hypervisor" until
//     they update. Without this match, every plain visor in a
//     deployment running an older binary noisily appears as a
//     SubError row in `hv tree` output until everyone updates.
func isNotHypervisorErr(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	if s == "hypervisor not running" || s == "hypervisor not enabled" {
		return true
	}
	// "rpc: can't find method app-visor.HVListDirectVisors" — pre-#2633
	// binary. The net/rpc layer formats unknown methods this way; match
	// the suffix so any service-name prefix change in the future stays
	// covered.
	return strings.Contains(s, "can't find method HVListDirectVisors") ||
		strings.HasSuffix(s, ".HVListDirectVisors")
}

// HVVisorSummary returns detailed info about a specific remote visor.
func (v *Visor) HVVisorSummary(pk cipher.PubKey) (*visorapi.Summary, error) {
	direct, sub, err := v.hvDispatch(pk)
	if err != nil {
		return nil, err
	}
	if direct != nil {
		if direct == visorapi.API(v) {
			return v.Summary()
		}
		return direct.Summary()
	}
	return sub.HVVisorSummary(pk)
}

// hvDispatch finds how to reach pk from this hypervisor:
//   - direct != nil: pk is local or a directly-connected remote visor; call
//     `direct.SomeMethod(...)` (the regular API method, not HV-prefixed).
//   - sub != nil: pk is reachable through one of our connected sub-hypervisors;
//     call `sub.HVSomeMethod(pk, ...)` (recursing one hop deeper).
//
// Sub-hypervisor lookup queries each direct remote's HVListVisors with a 10s
// timeout. Cycle protection relies on the timeout: a mutual hypervisor pair
// will fail-fast with timeouts rather than recurse forever.
func (v *Visor) hvDispatch(pk cipher.PubKey) (direct visorapi.API, sub visorapi.API, err error) {
	if v.hvInstance == nil {
		return nil, nil, fmt.Errorf("hypervisor not running")
	}
	if pk == v.conf.PK {
		return v, nil, nil
	}
	if conn, ok := v.hvInstance.visorConn(pk); ok {
		return conn.API, nil, nil
	}
	// Walk sub-hypervisors looking for pk.
	v.hvInstance.mu.RLock()
	type remote struct {
		pk   cipher.PubKey
		conn Conn
	}
	remotes := make([]remote, 0, len(v.hvInstance.remoteVisors))
	for rpk, c := range v.hvInstance.remoteVisors {
		remotes = append(remotes, remote{rpk, c})
	}
	v.hvInstance.mu.RUnlock()

	for _, e := range remotes {
		listDone := make(chan []visorapi.HVVisorEntry, 1)
		go func(api visorapi.API) {
			out, err := api.HVListVisors()
			if err != nil {
				listDone <- nil
				return
			}
			listDone <- out
		}(e.conn.API)
		select {
		case sub := <-listDone:
			for _, entry := range sub {
				if entry.PK == pk {
					return nil, e.conn.API, nil
				}
			}
		case <-time.After(10 * time.Second):
			// Skip this sub-hypervisor; it's slow or in a cycle.
		}
	}
	return nil, nil, fmt.Errorf("visor %s not reachable", pk.String())
}

// HVStartApp starts an app on the visor identified by pk.
func (v *Visor) HVStartApp(pk cipher.PubKey, appName string) error {
	direct, sub, err := v.hvDispatch(pk)
	if err != nil {
		return err
	}
	if direct != nil {
		return direct.StartApp(appName)
	}
	return sub.HVStartApp(pk, appName)
}

// HVStopApp stops an app on the visor identified by pk.
func (v *Visor) HVStopApp(pk cipher.PubKey, appName string) error {
	direct, sub, err := v.hvDispatch(pk)
	if err != nil {
		return err
	}
	if direct != nil {
		return direct.StopApp(appName)
	}
	return sub.HVStopApp(pk, appName)
}

// HVSetMinHops sets the routing min_hops on the visor identified by pk.
func (v *Visor) HVSetMinHops(pk cipher.PubKey, hops uint16) error {
	direct, sub, err := v.hvDispatch(pk)
	if err != nil {
		return err
	}
	if direct != nil {
		return direct.SetMinHops(hops)
	}
	return sub.HVSetMinHops(pk, hops)
}

// HVSetRewardAddress sets the reward address on the visor identified by pk.
// Returns the resulting config string from the visor.
func (v *Visor) HVSetRewardAddress(pk cipher.PubKey, addr string) (string, error) {
	direct, sub, err := v.hvDispatch(pk)
	if err != nil {
		return "", err
	}
	if direct != nil {
		return direct.SetRewardAddress(addr)
	}
	return sub.HVSetRewardAddress(pk, addr)
}

// HVRemoveTransport deletes a transport on the visor identified by pk.
func (v *Visor) HVRemoveTransport(pk cipher.PubKey, tid uuid.UUID) error {
	direct, sub, err := v.hvDispatch(pk)
	if err != nil {
		return err
	}
	if direct != nil {
		return direct.RemoveTransport(tid)
	}
	return sub.HVRemoveTransport(pk, tid)
}

// HVRemoveRoutingRule deletes a routing rule on the visor identified by pk.
func (v *Visor) HVRemoveRoutingRule(pk cipher.PubKey, key routing.RouteID) error {
	direct, sub, err := v.hvDispatch(pk)
	if err != nil {
		return err
	}
	if direct != nil {
		return direct.RemoveRoutingRule(key)
	}
	return sub.HVRemoveRoutingRule(pk, key)
}

// HVAddTransport creates a new transport on the visor identified by pk.
func (v *Visor) HVAddTransport(pk, remote cipher.PubKey, tpType, label string, timeout time.Duration) (*visorapi.TransportSummary, error) {
	direct, sub, err := v.hvDispatch(pk)
	if err != nil {
		return nil, err
	}
	if label == "" {
		label = string(transport.LabelUser)
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	if direct != nil {
		return direct.AddTransport(remote, tpType, timeout, label, false, false)
	}
	return sub.HVAddTransport(pk, remote, tpType, label, timeout)
}

// HVSetPublicAutoconnect toggles public_autoconnect on the visor identified by pk.
func (v *Visor) HVSetPublicAutoconnect(pk cipher.PubKey, enable bool) error {
	direct, sub, err := v.hvDispatch(pk)
	if err != nil {
		return err
	}
	if direct != nil {
		return direct.SetPublicAutoconnect(enable)
	}
	return sub.HVSetPublicAutoconnect(pk, enable)
}

// HVSetCalculateRoutes toggles calculate_routes on the visor identified by pk.
func (v *Visor) HVSetCalculateRoutes(pk cipher.PubKey, enable bool) error {
	direct, sub, err := v.hvDispatch(pk)
	if err != nil {
		return err
	}
	if direct != nil {
		return direct.SetCalculateRoutes(enable)
	}
	return sub.HVSetCalculateRoutes(pk, enable)
}

// HVReload reloads the visor identified by pk.
func (v *Visor) HVReload(pk cipher.PubKey) error {
	direct, sub, err := v.hvDispatch(pk)
	if err != nil {
		return err
	}
	if direct != nil {
		return direct.Reload()
	}
	return sub.HVReload(pk)
}

// HVShutdown shuts down the visor identified by pk.
func (v *Visor) HVShutdown(pk cipher.PubKey) error {
	direct, sub, err := v.hvDispatch(pk)
	if err != nil {
		return err
	}
	if direct != nil {
		return direct.Shutdown()
	}
	return sub.HVShutdown(pk)
}

// HVServiceHealth returns deployment service health entries for the visor identified by pk.
func (v *Visor) HVServiceHealth(pk cipher.PubKey) ([]visorapi.ServiceHealthEntry, error) {
	direct, sub, err := v.hvDispatch(pk)
	if err != nil {
		return nil, err
	}
	if direct != nil {
		return direct.ServiceHealth()
	}
	return sub.HVServiceHealth(pk)
}

// HVDmsgSessions returns the per-client dmsg sessions snapshot for
// the visor identified by pk. Locally — short-circuits to the
// in-process visor; remotely — forwards over the hypervisor proxy.
func (v *Visor) HVDmsgSessions(pk cipher.PubKey) (*visorapi.DmsgClientSessions, error) {
	direct, sub, err := v.hvDispatch(pk)
	if err != nil {
		return nil, err
	}
	if direct != nil {
		return direct.DmsgSessions()
	}
	return sub.HVDmsgSessions(pk)
}

// HVDmsgConnectAll triggers a one-shot connect-all on the visor identified by pk.
func (v *Visor) HVDmsgConnectAll(pk cipher.PubKey) (*visorapi.DmsgConnectAllResult, error) {
	direct, sub, err := v.hvDispatch(pk)
	if err != nil {
		return nil, err
	}
	if direct != nil {
		return direct.DmsgConnectAll()
	}
	return sub.HVDmsgConnectAll(pk)
}

// HVSetDmsgSessionsCount persists the dmsg sessions_count and triggers connect-all on the visor identified by pk.
func (v *Visor) HVSetDmsgSessionsCount(pk cipher.PubKey, count int) (*visorapi.DmsgConnectAllResult, error) {
	direct, sub, err := v.hvDispatch(pk)
	if err != nil {
		return nil, err
	}
	if direct != nil {
		return direct.SetDmsgSessionsCount(count)
	}
	return sub.HVSetDmsgSessionsCount(pk, count)
}

// HVLogsSince returns app logs since the given timestamp on the visor identified by pk.
func (v *Visor) HVLogsSince(pk cipher.PubKey, since time.Time, appName string) ([]string, error) {
	direct, sub, err := v.hvDispatch(pk)
	if err != nil {
		return nil, err
	}
	if direct != nil {
		return direct.LogsSince(since, appName)
	}
	return sub.HVLogsSince(pk, since, appName)
}

// HVSetAutoStart toggles autostart for an app on the visor identified by pk.
func (v *Visor) HVSetAutoStart(pk cipher.PubKey, appName string, autostart bool) error {
	direct, sub, err := v.hvDispatch(pk)
	if err != nil {
		return err
	}
	if direct != nil {
		return direct.SetAutoStart(appName, autostart)
	}
	return sub.HVSetAutoStart(pk, appName, autostart)
}

// HVEmbeddedProxies returns embedded resolving proxy status on the visor identified by pk.
func (v *Visor) HVEmbeddedProxies(pk cipher.PubKey) (*visorapi.EmbeddedProxiesStatus, error) {
	direct, sub, err := v.hvDispatch(pk)
	if err != nil {
		return nil, err
	}
	if direct != nil {
		return direct.EmbeddedProxies()
	}
	return sub.HVEmbeddedProxies(pk)
}

// HVSetEmbeddedProxyEnabled enables/disables an embedded proxy ("dmsg" or "skynet") on the visor identified by pk.
func (v *Visor) HVSetEmbeddedProxyEnabled(pk cipher.PubKey, kind string, enable bool) error {
	direct, sub, err := v.hvDispatch(pk)
	if err != nil {
		return err
	}
	if direct != nil {
		return direct.SetEmbeddedProxyEnabled(kind, enable)
	}
	return sub.HVSetEmbeddedProxyEnabled(pk, kind, enable)
}

// HVSetEmbeddedProxyUpstream sets the SOCKS5 upstream for an embedded proxy on the visor identified by pk.
func (v *Visor) HVSetEmbeddedProxyUpstream(pk cipher.PubKey, kind, addr string) error {
	direct, sub, err := v.hvDispatch(pk)
	if err != nil {
		return err
	}
	if direct != nil {
		return direct.SetEmbeddedProxyUpstream(kind, addr)
	}
	return sub.HVSetEmbeddedProxyUpstream(pk, kind, addr)
}

// HVListTCPPorts returns the registered skynet TCP ports on the visor identified by pk.
func (v *Visor) HVListTCPPorts(pk cipher.PubKey) ([]int, error) {
	direct, sub, err := v.hvDispatch(pk)
	if err != nil {
		return nil, err
	}
	if direct != nil {
		return direct.ListTCPPorts()
	}
	return sub.HVListTCPPorts(pk)
}

// HVRegisterTCPPort registers a skynet TCP port on the visor identified by pk.
func (v *Visor) HVRegisterTCPPort(pk cipher.PubKey, port int) error {
	direct, sub, err := v.hvDispatch(pk)
	if err != nil {
		return err
	}
	if direct != nil {
		return direct.RegisterTCPPort(port)
	}
	return sub.HVRegisterTCPPort(pk, port)
}

// HVDeregisterTCPPort deregisters a skynet TCP port on the visor identified by pk.
func (v *Visor) HVDeregisterTCPPort(pk cipher.PubKey, port int) error {
	direct, sub, err := v.hvDispatch(pk)
	if err != nil {
		return err
	}
	if direct != nil {
		return direct.DeregisterTCPPort(port)
	}
	return sub.HVDeregisterTCPPort(pk, port)
}

// HVListForwardedPorts returns the configured forwarded ports on the visor identified by pk.
func (v *Visor) HVListForwardedPorts(pk cipher.PubKey) ([]visorapi.ForwardedPort, error) {
	direct, sub, err := v.hvDispatch(pk)
	if err != nil {
		return nil, err
	}
	if direct != nil {
		return direct.ListForwardedPorts()
	}
	return sub.HVListForwardedPorts(pk)
}

// HVRegisterForwardedPort registers a forwarded port on the visor identified by pk.
func (v *Visor) HVRegisterForwardedPort(pk cipher.PubKey, p visorapi.ForwardedPort) error {
	direct, sub, err := v.hvDispatch(pk)
	if err != nil {
		return err
	}
	if direct != nil {
		return direct.RegisterForwardedPort(p)
	}
	return sub.HVRegisterForwardedPort(pk, p)
}

// HVUpdateForwardedPort updates a forwarded port on the visor identified by pk.
func (v *Visor) HVUpdateForwardedPort(pk cipher.PubKey, p visorapi.ForwardedPort) error {
	direct, sub, err := v.hvDispatch(pk)
	if err != nil {
		return err
	}
	if direct != nil {
		return direct.UpdateForwardedPort(p)
	}
	return sub.HVUpdateForwardedPort(pk, p)
}
