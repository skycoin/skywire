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
//		                         <this visor / source PK>
//		                         │
//		R[0] ● 143ms 1.6K↑ 1.5K↓ ┬── <exit PK>          [stcpr]  <tpid>  143ms
//		R[1] ● 47ms  0.5K↑ 0.4K↓ ┴── <hop1 PK>          [sudph]  <tpid>  47ms
//		                          └── <exit PK>          [stcpr]  <tpid>  88ms
//
//	  - Root = this visor (the source PK, taken from any leg's first hop).
//	  - Each ACTIVE route is a top-level right child; DEAD legs are pruned.
//	  - The LEFT block of a route is its per-route summary: R[n], a state glyph
//	    (● active / ○ standby — the caller colors it, no state words), the
//	    end-to-end ROUTE rtt, and bandwidth X↑ Y↓.
//	  - The RIGHT branch of a route is its hop chain; each hop node's label is the
//	    peer PK (never truncated) and its trailing columns are [<tp-type>],
//	    <tpid>, <transport-rtt> — the per-hop TRANSPORT rtt (distinct from the
//	    route rtt on the left).
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

// RouteTree builds the bilateral route-group model for a Snapshot. Dead legs
// are pruned; the surviving legs are ordered direct-active first, then active
// multihop, then standby, matching the page's reading order. The returned root
// is nil-safe to render (bitree.Render handles a childless root).
func RouteTree(snap Snapshot) *bitree.Node {
	src := "this visor"
	for _, l := range snap.Legs {
		if len(l.Hops) > 0 && l.Hops[0].From != "" {
			src = l.Hops[0].From
			break
		}
	}
	root := &bitree.Node{Label: src}

	legs := make([]Leg, 0, len(snap.Legs))
	for _, l := range snap.Legs {
		if !l.Alive { // prune dead routes/legs
			continue
		}
		legs = append(legs, l)
	}
	sort.SliceStable(legs, func(i, j int) bool {
		if ri, rj := legRank(legs[i]), legRank(legs[j]); ri != rj {
			return ri < rj
		}
		return legs[i].Index < legs[j].Index
	})

	w := legSumWidths(legs)
	for _, l := range legs {
		root.Right = append(root.Right, routeToNode(l, w))
	}
	return root
}

// sumWidths holds the per-field display widths used to pad every route's left
// summary into fixed columns, so R[n], the route-rtt and the ↑/↓ bandwidth line
// up vertically across all routes instead of being ragged (only the whole block
// was right-justified before).
type sumWidths struct{ idx, rtt, up, down int }

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
)

// legSumWidths measures the widest R[n] identity across the legs (that count is
// structural — it only changes when a route is added or dropped, not on a live
// value push) and pins the mutable numeric fields to their fixed column widths so
// they never reflow as the values update. legSummary pads every field to these.
func legSumWidths(legs []Leg) sumWidths {
	w := sumWidths{rtt: rttColWidth, up: bwColWidth, down: bwColWidth}
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
	return "R[n] · state · route-rtt · bw ↑↓", "peer-pk", []string{"[type]", "tp-id", "tp-rtt"}
}

// routeToNode turns one leg into a hop-chain right-branch carrying its left
// summary on the head (spine) row.
func routeToNode(l Leg, w sumWidths) *bitree.Node {
	left := &bitree.Node{Label: legSummary(l, w)}

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
// end-to-end route rtt, and bandwidth. No state word — the glyph (colored by
// the surface's StyleCell) carries active vs standby. Each field is padded to a
// common width (w) so the fields line up in fixed columns across all routes: the
// R[n] identity left-justified, the numeric route-rtt and ↑/↓ bandwidth
// right-justified.
func legSummary(l Leg, w sumWidths) string {
	g := GlyphActive
	if l.Standby {
		g = GlyphStandby
	}
	idx := padRightRunes(fmt.Sprintf("R[%d]", l.Index), w.idx)
	rtt := padLeftRunes(routeRTTCompact(l.RouteLatencyMS), w.rtt)
	up := padLeftRunes(compactBytes(l.SentBytes)+"↑", w.up)
	down := padLeftRunes(compactBytes(l.RecvBytes)+"↓", w.down)
	return idx + " " + g + " " + rtt + " " + up + " " + down
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
