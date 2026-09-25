//go:build !tinygo || (js && wasm)

// Package router pkg/router/router_dial_filter.go c2-net-routing
package router

import (
	"errors"
	"net"
	"strings"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/router/routersettings"
	"github.com/skycoin/skywire/pkg/routing"
)

// filterDisjointFirstHop returns the candidate paths whose FIRST hop leaves
// over a transport NOT in excludeTpIDs. It is the route-finder-path counterpart
// to the local-calc's direct-transport exclusion (calculateLocalRoutes skips an
// excluded direct tp): the finder ranks purely by latency and ignores
// ExcludeTransportIDs, so this is what actually keeps a multi-tunnel diversify
// dial off a first-hop transport a sibling tunnel already holds. Returns the
// input unchanged when there is nothing to exclude; may return an EMPTY slice
// when every candidate shares an excluded first hop — the caller decides whether
// to fall back (local calc / shared path) rather than failing here.
func filterDisjointFirstHop(cands [][]routing.Hop, excludeTpIDs []uuid.UUID) [][]routing.Hop {
	if len(cands) == 0 || len(excludeTpIDs) == 0 {
		return cands
	}
	excl := make(map[uuid.UUID]struct{}, len(excludeTpIDs))
	for _, id := range excludeTpIDs {
		excl[id] = struct{}{}
	}
	out := make([][]routing.Hop, 0, len(cands))
	for _, hops := range cands {
		if len(hops) == 0 {
			continue
		}
		if _, bad := excl[hops[0].TpID]; bad {
			continue
		}
		out = append(out, hops)
	}
	return out
}

// filterDisjointFirstHopPeer is filterDisjointFirstHop's PEER-level companion:
// it drops the candidates whose first hop goes to a visor (or, where the local
// transport exposes one, an IP) a sibling tunnel already leaves over. One peer
// answers on several transports — this visor holds stcpr, squicr and sudph
// transports to the same exit host — so a transport-ID exclusion alone leaves
// the twins looking free, and a diversify dial that takes one rides the very
// link it was supposed to leave (measured 2026-09-17: the second tunnel took
// the squicr twin of the sibling's stcpr first hop and the pair split one link).
//
// Returns the input unchanged when there is nothing to exclude; may return an
// EMPTY slice when every candidate shares a peer — the caller decides whether to
// fall back, exactly as with the transport-ID filter.
func (r *router) filterDisjointFirstHopPeer(cands [][]routing.Hop, peers []cipher.PubKey, ips []string) [][]routing.Hop {
	if len(cands) == 0 || (len(peers) == 0 && len(ips) == 0) {
		return cands
	}
	exclPK := make(map[cipher.PubKey]struct{}, len(peers))
	for _, pk := range peers {
		exclPK[pk] = struct{}{}
	}
	exclIP := make(map[string]struct{}, len(ips))
	for _, ip := range ips {
		exclIP[ip] = struct{}{}
	}
	out := make([][]routing.Hop, 0, len(cands))
	for _, hops := range cands {
		if len(hops) == 0 {
			continue
		}
		if _, bad := exclPK[hops[0].To]; bad {
			continue
		}
		if len(exclIP) > 0 && r.tm != nil {
			if tp := r.tm.Transport(hops[0].TpID); tp != nil {
				if ip := tp.RemoteIP(); ip != "" {
					if _, bad := exclIP[ip]; bad {
						continue
					}
				}
			}
		}
		out = append(out, hops)
	}
	return out
}

// filterLANFirstHops drops the candidates whose FIRST HOP peer sits on our own
// local network — a private/RFC1918 (or CGNAT, loopback, link-local) endpoint,
// or a public address in the same /24 as one of our own interfaces or our own
// public IP (the NAT-hairpin case). It returns the survivors and the dropped
// candidates, so the dial trail can name what it refused.
//
// A LAN neighbor is not diversity: it shares our uplink, so a second tunnel
// through it contends with the first for the very bottleneck the tunnels were
// split to escape, while its own onward hop to the exit is a link we know
// nothing about. Ranking gave it the top slot precisely because it is close —
// measured 2026-09-18, a 1ms LAN peer was chosen over every real intermediate
// and delivered 3.85 MB/s on 50 MB down (2.46 on 10 MB), worse than the 470ms
// hop the ranking was introduced to avoid.
//
// This is the first-hop counterpart of the same-LAN INTERMEDIATE reject (#4253,
// r.sameLANExcludedPKs / Manager.SameLANPeers), reusing that check and its
// routing.exclude_same_lan_hops switch so an operator who turns it off gets the
// old behavior on both.
func (r *router) filterLANFirstHops(cands [][]routing.Hop) (keep, dropped [][]routing.Hop) {
	if len(cands) == 0 {
		return cands, nil
	}
	lan := r.sameLANExcludedPKs()
	if len(lan) == 0 {
		return cands, nil
	}
	excl := make(map[cipher.PubKey]struct{}, len(lan))
	for _, pk := range lan {
		excl[pk] = struct{}{}
	}
	keep = make([][]routing.Hop, 0, len(cands))
	for _, hops := range cands {
		if len(hops) == 0 {
			continue
		}
		if _, bad := excl[hops[0].To]; bad {
			dropped = append(dropped, hops)
			continue
		}
		keep = append(keep, hops)
	}
	return keep, dropped
}

// distinctPKs counts the DISTINCT keys in an exclusion list.
//
// The sibling-exclusion lists are built by appending one entry per sibling
// route group PER TRANSPORT (siblingRouteGroupExclusions), so a pool whose
// tunnels have collapsed onto one first hop reports that hop once per tunnel.
// Counting the SLICE there makes a degenerate pool look diverse — the very
// duplicates that prove the collapse are what push the count past
// setup.first_hop_filter_max and unlatch the filter that would have refused
// them. Measured live 2026-09-18: 32 pool tunnels reporting "32 first-hop
// peer(s)" over 4 distinct transports, 29 of them the same one.
func distinctPKs(pks []cipher.PubKey) int {
	if len(pks) == 0 {
		return 0
	}
	seen := make(map[cipher.PubKey]struct{}, len(pks))
	for _, pk := range pks {
		seen[pk] = struct{}{}
	}
	return len(seen)
}

// pathIntermediates returns the PKs strictly between a candidate path's source
// and its destination — the nodes that make one path to an exit different from
// another to the same exit. A 1-hop direct path has none.
func pathIntermediates(hops []routing.Hop) []cipher.PubKey {
	if len(hops) < 2 {
		return nil
	}
	out := make([]cipher.PubKey, 0, len(hops)-1)
	for _, h := range hops[:len(hops)-1] {
		out = append(out, h.To)
	}
	return out
}

// filterDistinctIntermediate keeps the candidates that reach the destination
// through at least one intermediate no sibling already occupies.
//
// This is the other half of the relaxation below. Letting a deep pool reuse a
// first hop is only sound while the reused hop still leads somewhere new; the
// --standby-pool contract states it exactly — "a reused first hop WITH A
// DISTINCT INTERMEDIATE is allowed". A 1-hop direct route has no intermediate
// at all, so reusing ITS first hop reuses the entire path: the new tunnel is
// the old tunnel, and it aggregates nothing. Such a candidate is dropped here
// rather than offered, which is what lets the pool settle at its real disjoint
// bound instead of filling to the ceiling with clones of its own best route.
func filterDistinctIntermediate(cands [][]routing.Hop, held []cipher.PubKey) [][]routing.Hop {
	if len(cands) == 0 {
		return cands
	}
	heldSet := make(map[cipher.PubKey]struct{}, len(held))
	for _, pk := range held {
		heldSet[pk] = struct{}{}
	}
	keep := make([][]routing.Hop, 0, len(cands))
	for _, hops := range cands {
		fresh := false
		for _, pk := range pathIntermediates(hops) {
			if _, taken := heldSet[pk]; !taken {
				fresh = true
				break
			}
		}
		if fresh {
			keep = append(keep, hops)
		}
	}
	return keep
}

// freeFirstHops is THE first-hop admission test for a diversify dial, shared by
// every candidate selection that can win one (the route-finder path, the
// K-candidate race, the RSN oracle and the hook-race direct route) so they
// cannot disagree about which first hops are taken. A first hop is free when its
// transport is not one a sibling holds AND its peer/remote-IP is not one a
// sibling already leaves over.
func (r *router) freeFirstHops(cands [][]routing.Hop, opts *DialOptions) [][]routing.Hop {
	if opts == nil {
		return cands
	}
	// A route that died within seconds of its last dial without carrying a byte
	// is not a free first hop, it is a known-dead one. Without this the pool fill
	// re-picked the same dead route on every consecutive dial (dead_route_cache.go).
	// This filter is always hard: a dead route is not a worse route, it is a
	// route that does not work.
	cands = r.filterDeadRoutes(cands, opts)

	free := filterDisjointFirstHop(cands, opts.ExcludeTransportIDs)
	free = r.filterDisjointFirstHopPeer(free, opts.ExcludeFirstHopPeers, opts.ExcludeFirstHopIPs)

	// By DEFAULT there is no "beyond": two tunnels to one exit never share a
	// first-hop transport, however deep the pool is. When no candidate in the
	// window has a free first hop the pool SETTLES — that is the answer, and
	// dial.diversify_candidates is the knob that decides how much of the
	// topology the window covered before we believe it. The live failure this
	// closes: the window was 20 rank-ordered routes while the client held 750
	// stcpr transports to intermediates, so "9 held first hops, relaxing" meant
	// "free first hops exist, just outside the window" and the pool put two
	// tunnels on one transport.
	//
	// pool.allow_duplicate_route is the explicit opt-in to sharing, and only
	// with it on does setup.first_hop_filter_max mean anything:
	//
	// Beyond setup.first_hop_filter_max held first hops, diversity stops being a
	// FILTER and becomes a RANKING TERM: the free hops still come first, but a
	// candidate over an already-used first hop is offered rather than refused.
	//
	// As a hard filter it is what settled the pool at "no disjoint first hop
	// left", which is the right answer for the first handful of tunnels — those
	// are the ones whose whole purpose is to escape a shared bottleneck. It is
	// the wrong answer for the twentieth: with 274 shared intermediates to the
	// exit and every one of them requiring a DISTINCT intermediate path anyway,
	// refusing the twentieth tunnel because its first hop is the same transport
	// as the third's throws away a genuinely different path for a constraint
	// that has already been satisfied nineteen times over.
	//
	// DISTINCT held hops, not the length of a list that carries one entry per
	// sibling transport: counting duplicates lets a pool that has collapsed onto
	// a single first hop unlatch its own guard (see distinctPKs).
	held := distinctPKs(opts.ExcludeFirstHopPeers)
	if len(free) > 0 || held < SetupFirstHopFilterMax() {
		return free
	}
	if !routersettings.PoolAllowDuplicateRoute.Bool() {
		opts.note("no free first hop among %d candidate(s) with %d held; settling (pool.allow_duplicate_route is off)", len(cands), held)
		return nil
	}

	// Relaxed — but only as far as the documented contract, "a reused first hop
	// WITH A DISTINCT INTERMEDIATE". A candidate that reuses a held first hop
	// and adds no new intermediate is not a different path to the exit, it is
	// the same path again; offering it is how the pool filled to its ceiling
	// with clones of its own lowest-latency route.
	reused := filterDistinctIntermediate(cands, opts.ExcludeIntermediatePKs)
	if len(reused) == 0 {
		opts.note("first-hop diversity relaxed: %d held first hops, but no reused-hop candidate adds a distinct intermediate; settling", held)
		return nil
	}
	opts.note("first-hop diversity relaxed: %d held first hops, offering %d reused one(s) with a distinct intermediate", held, len(reused))
	return reused
}

// firstHopExcluded reports whether a single candidate path's first hop is taken
// — by transport ID or by peer/IP. The single-path form of freeFirstHops, used
// to confirm a local-calc or direct-route fallback is genuinely disjoint before
// returning it for a diversify dial.
func (r *router) firstHopExcluded(hops []routing.Hop, opts *DialOptions) bool {
	if len(hops) == 0 || opts == nil {
		return false
	}
	return len(r.freeFirstHops([][]routing.Hop{hops}, opts)) == 0
}

// firstHopTransportExcluded reports whether the path's first hop leaves over one
// of excludeTpIDs. Used to confirm a local-calc fallback route is genuinely
// disjoint before returning it for a diversify dial.
func firstHopTransportExcluded(hops []routing.Hop, excludeTpIDs []uuid.UUID) bool {
	if len(hops) == 0 {
		return false
	}
	for _, id := range excludeTpIDs {
		if hops[0].TpID == id {
			return true
		}
	}
	return false
}

// rejectDMSGMultihop returns paths with any multihop candidate
// containing a DMSG hop removed. Single-hop DMSG candidates are
// kept (direct DMSG dials are fine — both endpoints know who
// they're talking to). Stable order — survivors retain their
// original index, so downstream rank/pick logic isn't perturbed.
//
// Returns the input slice unchanged when nothing was filtered, to
// avoid an allocation in the common case.
// rejectExcludedTypes drops any candidate route (single- OR multi-hop) that
// traverses a transport type in exclude (case-insensitive). Unlike rejectDMSGMultihop
// (which spares a direct hop of the type), this removes the type ENTIRELY — the
// operator asked for it not to be used at all — so even a 1-hop leg of that type is
// dropped. Empty exclude / nil typeFor is a no-op returning paths unchanged.
func rejectExcludedTypes(paths [][]routing.Hop, typeFor func(uuid.UUID) string, exclude []string) [][]routing.Hop {
	if len(paths) == 0 || typeFor == nil || len(exclude) == 0 {
		return paths
	}
	excl := make(map[string]struct{}, len(exclude))
	for _, e := range exclude {
		if t := strings.ToLower(strings.TrimSpace(e)); t != "" {
			excl[t] = struct{}{}
		}
	}
	if len(excl) == 0 {
		return paths
	}
	out := make([][]routing.Hop, 0, len(paths))
	for _, p := range paths {
		drop := false
		for _, h := range p {
			if _, bad := excl[strings.ToLower(typeFor(h.TpID))]; bad {
				drop = true
				break
			}
		}
		if !drop {
			out = append(out, p)
		}
	}
	return out
}

func rejectDMSGMultihop(paths [][]routing.Hop, typeFor func(uuid.UUID) string) [][]routing.Hop {
	if len(paths) == 0 || typeFor == nil {
		return paths
	}
	anyDropped := false
	for _, p := range paths {
		if len(p) <= 1 {
			continue
		}
		for _, h := range p {
			if typeFor(h.TpID) == "dmsg" {
				anyDropped = true
				break
			}
		}
		if anyDropped {
			break
		}
	}
	if !anyDropped {
		return paths
	}
	out := make([][]routing.Hop, 0, len(paths))
	for _, p := range paths {
		if len(p) > 1 {
			drop := false
			for _, h := range p {
				if typeFor(h.TpID) == "dmsg" {
					drop = true
					break
				}
			}
			if drop {
				continue
			}
		}
		out = append(out, p)
	}
	return out
}

// pathTouchesIntermediate reports whether route hops contain any PK
// in the exclude set, considering only INTERMEDIATE hops — the first
// hop's From is the source and the last hop's To is the destination;
// both are endpoints, not intermediates.
func pathTouchesIntermediate(path []routing.Hop, exclude map[cipher.PubKey]struct{}) bool {
	if len(path) == 0 {
		return false
	}
	// Intermediate PKs are the "To" of every hop except the last,
	// which is the destination endpoint. Equivalently: the "From" of
	// every hop except the first (which is the source endpoint).
	for i := 0; i < len(path)-1; i++ {
		if _, hit := exclude[path[i].To]; hit {
			return true
		}
	}
	return false
}

// intermediatesOfHops extracts the set of intermediate-visor PKs
// from a forward hop sequence, excluding source (lPK) and destination
// (rPK). For a 1-hop path src->dst the returned slice is empty (no
// intermediates). For a 2-hop path src->X->dst the returned slice is
// [X]. Helpers below operate on either a raw []routing.Hop or a
// constructed *NoiseRouteGroup.
func intermediatesOfHops(hops []routing.Hop, src, dst cipher.PubKey) []cipher.PubKey {
	if len(hops) <= 1 {
		return nil
	}
	out := make([]cipher.PubKey, 0, len(hops)-1)
	for i := 0; i < len(hops)-1; i++ {
		pk := hops[i].To
		if pk == src || pk == dst {
			continue
		}
		out = append(out, pk)
	}
	return out
}

// intermediatesOfRouteGroup pulls intermediate-visor PKs from the
// transports currently registered in a NoiseRouteGroup. Used to seed
// the DisjointMux exclude set with the primary route's intermediates
// before the auxiliary routes are dialed.
//
// The primary route may already be appended by the time we reach the
// mux loop (it's the route_group's first transport); inspecting the
// transport-manager records gives us the same hop information that
// the original fetchBestRoutes call returned.
func intermediatesOfRouteGroup(nrg *NoiseRouteGroup, src, dst cipher.PubKey) []cipher.PubKey {
	if nrg == nil {
		return nil
	}
	nrg.rg.mu.Lock()
	defer nrg.rg.mu.Unlock()
	var out []cipher.PubKey
	for _, tp := range nrg.rg.tps {
		if tp == nil {
			continue
		}
		// Both edges of a transport are PKs; one is us, the other is
		// the next-hop visor. Add the non-self edge if it's not the
		// final destination.
		for _, pk := range tp.Entry.Edges {
			if pk == src || pk == dst {
				continue
			}
			out = append(out, pk)
		}
	}
	return out
}

// siblingRouteGroupExclusions scans this visor's live route groups for
// ones that already terminate at the same destination (rPK:rPort) as an
// about-to-be-dialed tunnel, and returns the first-hop transport IDs and
// intermediate PKs they occupy. Feeding these into a new dial's
// ExcludeTransportIDs / ExcludeIntermediatePKs makes the new tunnel leave
// the source over a DIFFERENT first-hop transport / disjoint intermediate —
// so N tunnels to one exit aggregate bandwidth instead of splitting a single
// link (docs/mux_aggregation_rfc.md step 3).
//
// rgsNs is keyed by the RECEIVE-side descriptor (Src = remote peer, Dst =
// this visor), so a sibling group to (rPK, rPort) matches on SrcPK()==rPK &&
// SrcPort()==rPort; the differing DstPort() is each tunnel's own ephemeral
// local port. count is the number of matching sibling groups — 0 means this
// is the first tunnel to that dst and the caller must add NO exclusions
// (byte-identical to a normal single dial).
//
// Locking: the matching *NoiseRouteGroup values are snapshotted under r.mx,
// then their transports are read WITHOUT r.mx held (each via the group's own
// rg.mu), so we never hold r.mx while taking a route-group lock.
func (r *router) siblingRouteGroupExclusions(lPK, rPK cipher.PubKey, rPort routing.Port) (ids []uuid.UUID, pks []cipher.PubKey, peers []cipher.PubKey, ips []string, count int) {
	r.mx.Lock()
	var siblings []*NoiseRouteGroup
	for desc, nrg := range r.rgsNs {
		if nrg == nil {
			continue
		}
		if desc.SrcPK() == rPK && desc.SrcPort() == rPort {
			siblings = append(siblings, nrg)
		}
	}
	r.mx.Unlock()

	count = len(siblings)
	for _, nrg := range siblings {
		nrg.rg.mu.Lock()
		for _, tp := range nrg.rg.tps {
			if tp == nil {
				continue
			}
			ids = append(ids, tp.Entry.ID)
			// The first hop's PEER, not just its transport: one peer usually
			// answers on several transports (stcpr + squicr + sudph to the same
			// exit host), and a tunnel over any of them rides the same link as
			// this sibling. Its remote IP too where the transport exposes one,
			// so the same host behind a second PK is caught as well.
			if peer := tp.Entry.RemoteEdge(lPK); peer != lPK {
				peers = append(peers, peer)
			}
			if ip := tp.RemoteIP(); ip != "" {
				ips = append(ips, ip)
			}
		}
		nrg.rg.mu.Unlock()
		pks = append(pks, intermediatesOfRouteGroup(nrg, lPK, rPK)...)
	}
	return ids, pks, peers, ips, count
}

// extractFailedIntermediatePK walks an error chain looking for a
// router.DialError and returns its PK if and only if the PK is an
// INTERMEDIATE (i.e., neither the local source nor the remote
// destination). Returns (PK, true) on a real intermediate failure;
// (zero, false) otherwise.
//
// Used by the DialRoutes / PingRoute retry loops to remember which
// intermediates have just failed during id_reservation so the next
// fetchBestRoutes call can route around them via the existing
// ExcludeIntermediatePKs filter (#2746) without a second route-finder
// candidate-selection algorithm. Composes with #2750's per-intermediate
// breaker — that fix tracks repeated failures across calls; this fix
// handles fast within-call fallback.
func extractFailedIntermediatePK(err error, src, dst cipher.PubKey) (cipher.PubKey, bool) {
	if err == nil {
		return cipher.PubKey{}, false
	}
	var de *DialError
	if !errors.As(err, &de) {
		return cipher.PubKey{}, false
	}
	failed := de.PK
	if failed == src || failed == dst {
		return cipher.PubKey{}, false
	}
	return failed, true
}

// appendExcludeIntermediate returns a (potentially new) DialOptions
// with pk added to ExcludeIntermediatePKs. Returns opts unchanged when
// pk is already in the slice (idempotent: re-failure of the same
// intermediate within a single retry cycle won't compound).
//
// MUTATES opts when opts is non-nil — ExcludeIntermediatePKs is grown
// in place via append. Callers that need to preserve the original opts
// must clone before calling. The current call sites (DialRoutes /
// PingRoute retry loops) operate on a single opts value across
// iterations and intentionally want the in-place mutation: carrying
// excluded PKs forward to the next iteration IS the point.
//
// opts is guaranteed non-nil on return so the caller can pass it
// straight back into fetchBestRoutes without a nil check.
func appendExcludeIntermediate(opts *DialOptions, pk cipher.PubKey) *DialOptions {
	if opts == nil {
		opts = DefaultDialOptions()
	}
	for _, existing := range opts.ExcludeIntermediatePKs {
		if existing == pk {
			return opts
		}
	}
	opts.ExcludeIntermediatePKs = append(opts.ExcludeIntermediatePKs, pk)
	return opts
}

// sameLANExcludedPKs returns the PKs of directly-connected peers that sit on
// this visor's own local network, to be excluded as routing INTERMEDIATES when
// routing.exclude_same_lan_hops is enabled (Config.ExcludeSameLANHops). Returns
// nil when the feature is off, the transport manager is absent, or no peer is
// same-LAN. See transport.Manager.SameLANPeers for the same-LAN definition. The
// visor's own public IP (for the NAT-hairpin case) is read from the SelfPublicIP
// getter; a nil getter or nil result still catches private-endpoint peers.
func (r *router) sameLANExcludedPKs() []cipher.PubKey {
	if r.sameLANPeersFn != nil {
		return r.sameLANPeersFn()
	}
	if r.conf == nil || !r.conf.ExcludeSameLANHops || r.tm == nil {
		return nil
	}
	var self net.IP
	if r.conf.SelfPublicIP != nil {
		self = r.conf.SelfPublicIP()
	}
	return r.tm.SameLANPeers(self)
}

// isSameLANDest reports whether dst is a peer on this visor's own local network
// (private-endpoint or NAT-hairpin, per transport.Manager.SameLANPeers). Unlike
// sameLANExcludedPKs (the INTERMEDIATE exclusion, gated on ExcludeSameLANHops),
// this is used for the DESTINATION mux decision and applies regardless of that
// config: a box on our LAN is always best reached over its single direct
// transport, never a warm-standby mux through remote intermediates.
func (r *router) isSameLANDest(dst cipher.PubKey) bool {
	if r.tm == nil {
		return false
	}
	var self net.IP
	if r.conf != nil && r.conf.SelfPublicIP != nil {
		self = r.conf.SelfPublicIP()
	}
	for _, pk := range r.tm.SameLANPeers(self) {
		if pk == dst {
			return true
		}
	}
	return false
}

// appendUniquePKs appends every pk in add that is not already in base,
// preserving order. Nil-safe on both arguments.
func appendUniquePKs(base, add []cipher.PubKey) []cipher.PubKey {
	if len(add) == 0 {
		return base
	}
	seen := make(map[cipher.PubKey]struct{}, len(base))
	for _, pk := range base {
		seen[pk] = struct{}{}
	}
	for _, pk := range add {
		if _, ok := seen[pk]; !ok {
			base = append(base, pk)
			seen[pk] = struct{}{}
		}
	}
	return base
}

// intermediatePKsOfPath returns the distinct intermediate-visor PKs of a
// forward path: every hop.To that is neither the source nor the destination.
// For a direct route (src->dst) it returns nil. Used to exclude a dead
// route's intermediates from the next route-finder pick when a route sets up
// (rules installed on every hop) but its data plane never carries the
// handshake — i.e. one of the intermediates ACKs install but won't forward.
func intermediatePKsOfPath(hops []routing.Hop, src, dst cipher.PubKey) []cipher.PubKey {
	seen := make(map[cipher.PubKey]struct{}, len(hops))
	var out []cipher.PubKey
	for _, h := range hops {
		pk := h.To
		if pk == src || pk == dst {
			continue
		}
		if _, ok := seen[pk]; ok {
			continue
		}
		seen[pk] = struct{}{}
		out = append(out, pk)
	}
	return out
}

// routeIntermediates returns the de-duplicated set of intermediate-visor PKs
// of a hop sequence: every visor appearing as a hop endpoint (From or To)
// other than this visor (src) and the destination (dst). For a direct 1-hop
// route src->dst it returns nil (no intermediates). Unlike
// intermediatePKsOfPath it inspects BOTH edges of every hop, so an
// intermediate that a malformed candidate lists only as a From (never a To)
// is still reported — the disjointness guarantee must not depend on the
// route being well-formed.
func routeIntermediates(fwd []routing.Hop, src, dst cipher.PubKey) []cipher.PubKey {
	seen := make(map[cipher.PubKey]struct{}, 2*len(fwd))
	var out []cipher.PubKey
	add := func(pk cipher.PubKey) {
		if pk == src || pk == dst || pk == (cipher.PubKey{}) {
			return
		}
		if _, ok := seen[pk]; ok {
			return
		}
		seen[pk] = struct{}{}
		out = append(out, pk)
	}
	for _, h := range fwd {
		add(h.From)
		add(h.To)
	}
	return out
}

// hasLoop reports whether a forward hop chain passes through the same visor
// more than once (an intra-route loop). The visor visit order is the first
// hop's From followed by every hop's To; a repeat anywhere in that sequence
// is a loop. A route that loops through a visor twice can send traffic in a
// circle with no way for the endpoints to detect it.
func hasLoop(fwd []routing.Hop) bool {
	if len(fwd) == 0 {
		return false
	}
	seen := make(map[cipher.PubKey]struct{}, len(fwd)+1)
	seen[fwd[0].From] = struct{}{}
	for _, h := range fwd {
		if _, ok := seen[h.To]; ok {
			return true
		}
		seen[h.To] = struct{}{}
	}
	return false
}

// disjointFrom reports whether cand shares no PK with used (the set
// intersection is empty). An empty cand — a direct, zero-intermediate leg —
// is trivially disjoint and always accepted.
func disjointFrom(cand, used []cipher.PubKey) bool {
	if len(cand) == 0 || len(used) == 0 {
		return true
	}
	set := make(map[cipher.PubKey]struct{}, len(used))
	for _, pk := range used {
		set[pk] = struct{}{}
	}
	for _, pk := range cand {
		if _, ok := set[pk]; ok {
			return false
		}
	}
	return true
}
