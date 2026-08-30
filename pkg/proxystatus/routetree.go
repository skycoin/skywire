// Package proxystatus pkg/proxystatus/routetree.go c4-app-web
//
// RouteTree is the single shared adapter that projects a surface Snapshot's
// live route-group state into a bilateral tree model (pkg/bitree). BOTH the
// status.skysocks page (+ interstitial) and `skywire cli proxy tree` render
// from this one model, so the terminal and the page always show the same shape.
//
// Layout (rendered by bitree, styled by each surface's own StyleCell). The root
// (source PK) is right-anchored over the spine descender, so ignoring the left
// summary branches it reads as a normal downward tree with the source PK at top
// in the same PK column as the hops:
//
//		                                         <this visor / source PK>
//		                                         │
//		R[0] ● 143ms  1.6K↑ 8.1K/s ███░  1.5K↓ 12K/s ██░░ ┬── <exit PK>  [stcpr]  <tpid>  143ms
//		R[1] ● 47ms   0.5K↑ 2.0K/s █░░░  0.4K↓  3K/s █░░░ ┴── <hop1 PK>  [sudph]  <tpid>  47ms
//		                                                   └── <exit PK>  [stcpr]  <tpid>  88ms
//
//	  - Root = this visor (the source PK, taken from any leg's first hop).
//	  - Each ACTIVE route is a top-level right child; DEAD legs are pruned.
//	  - The LEFT block of a route is its per-route summary: R[n], a state glyph
//	    (● active / ○ standby — the caller colors it, no state words), the
//	    end-to-end ROUTE rtt, then per direction the cumulative bytes (X↑ / Y↓),
//	    the live goodput RATE (send-rate / recv-rate) and a SHARE bar — this
//	    route's fraction of the group's aggregate up / down goodput.
//	  - The RIGHT branch of a route is its hop chain; each hop node's label is the
//	    peer PK (never truncated) and its trailing columns are [<tp-type>],
//	    <tpid>, <transport-rtt> — the per-hop TRANSPORT rtt. For a DIRECT (1-hop)
//	    leg this equals the left ROUTE rtt (same physical link, same live
//	    measurement); on a multihop leg the near-edge transport rtt and the
//	    whole-path route rtt differ.
package proxystatus

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/skycoin/skywire/pkg/bitree"
)

// Route-state glyphs. The caller supplies them into the left annotation; a
// surface's StyleCell colors active vs standby by detecting which glyph is
// present, so no "active"/"standby" word ever appears in the tree.
const (
	// GlyphActive marks an alive, in-rotation route.
	GlyphActive = "●"
	// GlyphStandby marks an alive but standby (warm spare) route.
	GlyphStandby = "○"
)

// The two route-multiplexing LAYERS are made visually distinct in the tree:
//
//   - StreamBandGlyph is the per-STREAM accent band prefixed onto every leg summary
//     ("▏s0"), so a leg reads which stream (route group) it belongs to at a
//     glance; the page colors it per-stream, the terminal shows it monochrome.
//   - StreamHeaderGlyph marks a STREAM-boundary node on the spine — a different
//     KIND of node than a leg/hop — so the stream (packet-group) layer reads
//     clearly apart from the leg (packet-striping) layer beneath it.
//
// streamAccentCount is how many distinct per-stream accents the palette cycles
// before wrapping (stream index N → N % streamAccentCount); the page's CSS
// defines --stream0…--streamN to match.
const (
	// StreamBandGlyph leads a leg's per-stream accent band ("▏sN"); StreamHeaderGlyph
	// marks a stream-boundary node. Exported so the CLI renderer (a different
	// package) can detect them to style the two layers, as it does GlyphActive.
	StreamBandGlyph   = "▏"
	StreamHeaderGlyph = "▚"
	streamAccentCount = 5
)

// RouteTree builds the bilateral route-group model for a Snapshot. Dead legs
// are pruned; the surviving legs are ordered direct-active first, then active
// multihop, then standby, matching the page's reading order. The returned root
// is nil-safe to render (bitree.Render handles a childless root).
//
// The two multiplexing LAYERS stack: STREAM level (--tunnels, disjoint route
// groups) over PACKET level (the mux legs striping one stream's packets). Every
// stream gets a stream-boundary header node on the spine followed by its own
// legs. CRUCIALLY the legs are SIBLING spine routes of the header (top-level),
// not children nested under it — bitree draws the rich left annotation block
// (R[n], ●/○ state, route-rtt, per-direction ↑/↓ bandwidth + rate + share bars)
// ONLY for top-level spine routes, so nesting the legs would drop every leg's
// summary (the #4313 regression this restores).
//
// With MORE THAN ONE stream each leg summary additionally carries that stream's
// accent band ("▏sN") so which leg belongs to which stream is legible. With a
// single stream there is nothing to distinguish, so the legs render UNBANDED —
// the tree reads essentially like the original flat per-leg tree, plus a thin
// "stream 0 · N legs" header. The per-leg summaries are always present.
func RouteTree(snap Snapshot) *bitree.Node {
	root := &bitree.Node{Label: treeSrc(snap)}
	// One stream-boundary header per tunnel, then that tunnel's legs as SIBLING
	// spine routes (top-level, so their rich left summary renders). Legs are
	// banded with the stream accent only when there is more than one stream to
	// tell apart; a lone stream renders its legs clean.
	if len(snap.Tunnels) >= 1 {
		multi := len(snap.Tunnels) > 1
		for _, t := range snap.Tunnels {
			streamIdx := -1 // unbanded: a single stream has nothing to distinguish
			if multi {
				streamIdx = t.Index
			}
			legNodes := legNodesForStream(t.Legs, streamIdx)
			if len(legNodes) == 0 {
				continue
			}
			root.Right = append(root.Right, streamHeaderNode(t, len(legNodes)))
			root.Right = append(root.Right, legNodes...)
		}
		return root
	}
	// Flat fallback: a caller that projects a single route group (Tunnels unset).
	root.Right = legNodesFor(snap.Legs)
	return root
}

// treeSrc finds the source PK to label the tree root — this visor, taken from any
// leg's first hop, across all tunnels (or the flat Legs list).
func treeSrc(snap Snapshot) string {
	pick := func(legs []Leg) string {
		for _, l := range legs {
			if len(l.Hops) > 0 && l.Hops[0].From != "" {
				return l.Hops[0].From
			}
		}
		return ""
	}
	for _, t := range snap.Tunnels {
		if s := pick(t.Legs); s != "" {
			return s
		}
	}
	if s := pick(snap.Legs); s != "" {
		return s
	}
	// No leg recorded its forward hops (common for auto/standby legs) — fall back to
	// the visor's own PK, carried explicitly on the snapshot, so the root is still
	// the real source rather than a placeholder.
	if snap.SelfPK != "" {
		return snap.SelfPK
	}
	return "this visor"
}

// streamHeaderNode builds a STREAM-boundary node for the spine — a different
// kind of node than a leg/hop. Its label is marked with StreamHeaderGlyph so a
// renderer can style it as a stream header (the page gives it the stream's
// accent + a badge; the terminal shows the glyph) and names the stream index,
// its packet-level leg count, and whether packet mux is on. It carries no left
// summary and no hop chain, so it reads plainly as a layer boundary above the
// legs that follow it.
func streamHeaderNode(t Tunnel, nLegs int) *bitree.Node {
	legWord := "legs"
	if nLegs == 1 {
		legWord = "leg"
	}
	mux := "mux off"
	if t.MuxEnabled {
		mux = "mux on"
	}
	return &bitree.Node{Label: fmt.Sprintf("%s stream %d · %d %s · %s", StreamHeaderGlyph, t.Index, nLegs, legWord, mux)}
}

// streamTag is the per-stream accent band prefixed onto a leg's left summary
// ("▏s0 ") when the leg belongs to a distinguished stream (multi-stream view).
// The page colors it with that stream's accent; the terminal shows it plain. A
// negative index yields no tag (single-stream / flat view).
func streamTag(streamIdx int) string {
	if streamIdx < 0 {
		return ""
	}
	return fmt.Sprintf("%ss%d ", StreamBandGlyph, streamIdx)
}

// legNodesFor builds the per-leg route nodes for a single (undistinguished)
// stream — no per-stream band on the summaries.
func legNodesFor(all []Leg) []*bitree.Node { return legNodesForStream(all, -1) }

// legNodesForStream prunes dead legs, orders them (direct-active first, then
// active multihop, then standby), and builds the per-leg route nodes with each
// leg's share drawn against the aggregate goodput of THIS stream's set. When
// streamIdx >= 0 each leg summary is banded with that stream's accent tag so the
// leg reads as belonging to that stream.
func legNodesForStream(all []Leg, streamIdx int) []*bitree.Node {
	legs := make([]Leg, 0, len(all))
	for _, l := range all {
		if !l.Alive { // prune dead routes/legs
			continue
		}
		legs = append(legs, l)
	}
	if len(legs) == 0 {
		return nil
	}
	sort.SliceStable(legs, func(i, j int) bool {
		if ri, rj := legRank(legs[i]), legRank(legs[j]); ri != rj {
			return ri < rj
		}
		// Stable within a role group: key on the leg's IDENTITY (its intermediate,
		// then its transport), not its Index. The leg set churns as standby routes
		// are added/dropped, which reassigns indices and made the tree branches jump
		// around every ~1s refresh; keying on the peer keeps a given route in the
		// same slot so the tree stays readable while the pool grows.
		return legStableKey(legs[i]) < legStableKey(legs[j])
	})
	// Aggregate per-direction goodput across the surviving legs — the denominator
	// each route's share bar is drawn against.
	var aggUp, aggDown float64
	for _, l := range legs {
		aggUp += l.GoodputUpBps
		aggDown += l.GoodputDownBps
	}
	w := legSumWidths(legs)
	nodes := make([]*bitree.Node, 0, len(legs))
	for _, l := range legs {
		nodes = append(nodes, routeToNode(l, w, aggUp, aggDown, streamIdx))
	}
	return nodes
}

// sumWidths holds the per-field display widths used to pad every route's left
// summary into fixed columns, so R[n], the route-rtt and the ↑/↓ bandwidth line
// up vertically across all routes instead of being ragged (only the whole block
// was right-justified before).
type sumWidths struct{ idx, rtt, up, down, gp int }

// Fixed display widths (monospace cells) for the MUTABLE numeric fields of a
// route's left summary — the route rtt and the ↑/↓ byte counts. These values
// change on every ~1s live WebSocket push, so measuring their width per snapshot
// (as the idx field still is) would let the column — and the box-drawing branch
// and PK that follow it — shift horizontally whenever a digit is added or
// dropped (1ms→342ms→1203ms, 9B→2.4K→15.3M). Pinning them to a constant width
// wide enough for the realistic operating range keeps everything to their right
// column-stable across updates. padLeftRunes only pads (never truncates), so a
// rare out-of-range value still renders whole.
const (
	// rttColWidth fits "—" through "9999ms" (a slower route is pruned as dead).
	rttColWidth = 6
	// bwColWidth fits "0B↑" through "999.9K↑" / "15.3M↑" (compactBytes + arrow).
	bwColWidth = 7
	// gpColWidth fits a per-direction goodput rate cell, "—" through "999.9K/s" /
	// "15.3M/s" (compactBytes + "/s"). Same pin-the-width reasoning as bwColWidth:
	// the rate changes on every live push, so a fixed column keeps the tree that
	// follows it column-stable. Used for BOTH the up-rate and the down-rate.
	gpColWidth = 9
	// shareBarWidth is the cell count of each per-direction bandwidth-SHARE bar
	// (████░ style) — this route's fraction of the route-group's aggregate
	// up-goodput / down-goodput. Constant width by construction, so it never
	// reflows the tree as the shares shift on a live push.
	shareBarWidth = 5
)

// legSumWidths measures the widest R[n] identity across the legs (that count is
// structural — it only changes when a route is added or dropped, not on a live
// value push) and pins the mutable numeric fields to their fixed column widths so
// they never reflow as the values update. legSummary pads every field to these.
func legSumWidths(legs []Leg) sumWidths {
	w := sumWidths{rtt: rttColWidth, up: bwColWidth, down: bwColWidth, gp: gpColWidth}
	for _, l := range legs {
		if n := len(fmt.Sprintf("R[%d]", l.Index)); n > w.idx {
			w.idx = n
		}
	}
	return w
}

// hopColorLevels is how many distinct hop-DEPTH colors the tree cycles through
// before wrapping (hop-l1 … hop-lN). Realistic routes here are 1-hop direct or
// 2-3 hop, so four levels cover the common depth with headroom; deeper hops
// reuse a hue by wrapping (level → ((level-1) % hopColorLevels) + 1).
const hopColorLevels = 4

// hopClass returns the CSS class the tree paints a hop PK with, given its role:
// "hop-exit" for a leg's final destination (the exit), or "hop-lN" for an
// intermediate at hop DEPTH n (level 1 = first intermediate after the source),
// cycling every hopColorLevels. The source PK is not passed here — it keeps its
// own source accent (CellRoot).
func hopClass(exit bool, level int) string {
	if exit {
		return "hop-exit"
	}
	return fmt.Sprintf("hop-l%d", ((level-1)%hopColorLevels)+1)
}

// hopClassMap projects a Snapshot's live legs into a PK→hop-color-class map used
// by the page's StyleCell to color each hop PK by its DEPTH: the EXIT (any leg's
// final destination) red ("hop-exit"), every level-1 intermediate one color,
// every level-2 another, and so on ("hop-lN"). Coloring is by depth across all
// legs, not by leg, so a hop at the same depth in different legs shares a color.
// Exit wins over an intermediate role for the same PK; among intermediate roles
// the SHALLOWEST depth wins. A direct (1-hop) leg has no intermediate: its final
// hop — or, when no hop path is recorded, its RemotePK — is the exit.
func hopClassMap(snap Snapshot) map[string]string {
	exit := make(map[string]bool)
	level := make(map[string]int)
	mark := func(pk string, isExit bool, lvl int) {
		if pk == "" {
			return
		}
		if isExit {
			exit[pk] = true
			return
		}
		if prev, ok := level[pk]; !ok || lvl < prev {
			level[pk] = lvl
		}
	}
	for _, l := range snap.Legs {
		if !l.Alive {
			continue
		}
		if len(l.Hops) == 0 {
			mark(l.RemotePK, true, 0) // no recorded path: the remote IS the exit
			continue
		}
		n := len(l.Hops)
		for i, h := range l.Hops {
			mark(h.To, i == n-1, i+1) // last hop → exit; else intermediate depth i+1
		}
	}
	out := make(map[string]string, len(exit)+len(level))
	for pk, lvl := range level {
		out[pk] = hopClass(false, lvl)
	}
	for pk := range exit { // exit precedence: overwrite any intermediate role
		out[pk] = hopClass(true, 0)
	}
	return out
}

// TreeHeader returns the label-header row fields (left group, peer label,
// trailing-column labels) for a route tree, shaped so bitree can render them as
// a template row that lines up with the columns of the tree beneath. Only the
// page uses it; `proxy tree` renders headerless.
func TreeHeader() (left, label string, cols []string) {
	return "R[n] · state · route-rtt · ↑bytes rate share · ↓bytes rate share", "peer-pk", []string{"[type]", "tp-id", "tp-rtt"}
}

// routeToNode turns one leg into a hop-chain right-branch carrying its left
// summary on the head (spine) row. aggUp/aggDown are the route-group's
// aggregate up/down goodput, the denominators for this leg's share bars.
func routeToNode(l Leg, w sumWidths, aggUp, aggDown float64, streamIdx int) *bitree.Node {
	left := &bitree.Node{Label: legSummary(l, w, aggUp, aggDown, streamIdx)}

	if len(l.Hops) == 0 {
		// No recorded path: a single leaf at the remote PK.
		return &bitree.Node{Label: orDashPK(l.RemotePK), Left: []*bitree.Node{left}}
	}
	head := hopToNode(l.Hops[0])
	head.Left = []*bitree.Node{left}
	cur := head
	for _, h := range l.Hops[1:] {
		n := hopToNode(h)
		cur.Right = append(cur.Right, n)
		cur = n
	}
	return head
}

// hopToNode renders one forward hop: label = the peer PK (full), columns =
// [tp-type], tpid, transport-rtt (aligned like `skywire cli tp tree`).
func hopToNode(h Hop) *bitree.Node {
	return &bitree.Node{
		Label: orDashPK(h.To),
		Cols:  []string{"[" + orDash(h.TpType) + "]", orDash(h.TpID), tpRTT(h.LatencyMS)},
	}
}

// legSummary is the left annotation for a route: identity, state glyph,
// end-to-end route rtt, and per-direction bandwidth. No state word — the glyph
// (colored by the surface's StyleCell) carries active vs standby. Each field is
// padded to a common width (w) so the fields line up in fixed columns across all
// routes. The bandwidth is shown per direction: the cumulative ↑ byte total then
// this route's live send-RATE and its share of the group's up-goodput, then the
// ↓ total, live recv-RATE and its share of the group's down-goodput. aggUp/aggDown
// are the group's aggregate up/down goodput (the share-bar denominators).
func legSummary(l Leg, w sumWidths, aggUp, aggDown float64, streamIdx int) string {
	g := GlyphActive
	if l.Standby {
		g = GlyphStandby
	}
	idx := padRightRunes(fmt.Sprintf("R[%d]", l.Index), w.idx)
	rtt := padLeftRunes(routeRTTCompact(l.RouteLatencyMS), w.rtt)
	up := padLeftRunes(compactBytes(l.SentBytes)+"↑", w.up)
	upRate := padLeftRunes(compactRate(l.GoodputUpBps), w.gp)
	upBar := shareBar(l.GoodputUpBps, aggUp, shareBarWidth)
	down := padLeftRunes(compactBytes(l.RecvBytes)+"↓", w.down)
	downRate := padLeftRunes(compactRate(l.GoodputDownBps), w.gp)
	downBar := shareBar(l.GoodputDownBps, aggDown, shareBarWidth)
	return streamTag(streamIdx) + idx + " " + g + " " + rtt +
		"  " + up + " " + upRate + " " + upBar +
		"  " + down + " " + downRate + " " + downBar
}

// compactRate formats a goodput rate (bytes/sec) for the fixed-width leg cell:
// "12.4K/s" style via compactBytes, or "—" when the rate is zero/unmeasured
// (no second sample yet, or an idle leg). The rate is the leg's recent goodput,
// distinct from the cumulative ↑/↓ byte counters beside it.
func compactRate(bps float64) string {
	if bps <= 0 {
		return "—"
	}
	return compactBytes(uint64(bps)) + "/s"
}

// shareBar renders a fixed-width unicode meter (████░) of val's fraction of
// total — this route's share of the group's aggregate up- or down-goodput. The
// filled cell count rounds val/total*width; an all-empty bar (░░░░░) means no
// share (total is zero, or this leg is idle in that direction). Constant width by
// construction so it never reflows the columns to its right on a live push. Same
// glyph block as the tree's box-drawing, so the system-monospace stack tiles it.
func shareBar(val, total float64, width int) string {
	filled := 0
	if total > 0 && val > 0 {
		filled = int(val/total*float64(width) + 0.5)
		if filled > width {
			filled = width
		}
		if filled == 0 {
			filled = 1 // a nonzero share always shows at least one cell
		}
	}
	return strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
}

// padRightRunes/padLeftRunes pad s to n display columns (rune count), on the
// right (left-justify) and left (right-justify) respectively.
func padRightRunes(s string, n int) string {
	if p := n - utf8.RuneCountInString(s); p > 0 {
		return s + strings.Repeat(" ", p)
	}
	return s
}

func padLeftRunes(s string, n int) string {
	if p := n - utf8.RuneCountInString(s); p > 0 {
		return strings.Repeat(" ", p) + s
	}
	return s
}

// orDashPK returns a full (never truncated) PK or a dash when empty.
func orDashPK(pk string) string {
	if pk == "" {
		return "-"
	}
	return pk
}

// routeRTTCompact formats a route rtt for the compact left cell ("143ms" /
// "—" when unmeasured).
func routeRTTCompact(ms float64) string {
	if ms <= 0 {
		return "—"
	}
	return fmt.Sprintf("%.0fms", ms)
}

// tpRTT formats a per-hop transport rtt for the aligned columns ("143ms" / "-").
func tpRTT(ms float64) string {
	if ms <= 0 {
		return "-"
	}
	return fmt.Sprintf("%.0fms", ms)
}

// compactBytes is a terse byte formatter for the space-constrained left cell
// (1.6K, 2.0M) — distinct from humanBytes' spaced "1.6 KiB" used in prose.
func compactBytes(n uint64) string {
	const (
		k = 1024.0
		m = k * 1024
		g = m * 1024
	)
	switch f := float64(n); {
	case f >= g:
		return fmt.Sprintf("%.1fG", f/g)
	case f >= m:
		return fmt.Sprintf("%.1fM", f/m)
	case f >= k:
		return fmt.Sprintf("%.1fK", f/k)
	default:
		return fmt.Sprintf("%dB", n)
	}
}
