// Package bitree renders a generic bilateral (two-sided) tree as monospace
// plain text using box-drawing glyphs.
//
// A bilateral tree has a single root whose children extend RIGHTWARD (the
// normal topology). In addition, any node may carry a mirror subtree that
// extends LEFTWARD, hanging at the same rows as the right-side branches. The
// renderer auto-justifies with whitespace so the central spine aligns in one
// column, left content is right-justified up to the spine, and right content
// flows out from the spine. Optional trailing columns are aligned in a block
// regardless of tree depth (like `skywire cli tp tree`).
//
// Layout and styling are separable: the geometry is computed from the plain
// (unstyled) text, and an optional StyleCell hook decorates cells afterwards
// so a color wrapper or HTML renderer can restyle without disturbing columns.
package bitree

import (
	"strings"
	"unicode/utf8"
)

// Node is a bilateral tree node.
//
//   - Right holds rightward children (the topology): the node's label sits on
//     its own row and each right child hangs below it, nesting arbitrarily.
//   - Left holds leftward children (annotations), themselves a full nestable
//     subtree that mirrors the right connector logic. The left block hangs at
//     the same rows as the right branches.
//   - Cols holds optional trailing columns for the node's row; they are padded
//     into an aligned block across every right-side line.
type Node struct {
	Label string
	Right []*Node
	Left  []*Node
	Cols  []string
}

// CellKind identifies the semantic role of a cell passed to StyleCell.
type CellKind int

const (
	// CellRoot is the root label.
	CellRoot CellKind = iota
	// CellLabel is a right-side node label.
	CellLabel
	// CellColumn is a trailing aligned column cell.
	CellColumn
	// CellLeft is a left-side (annotation) block line. It carries the whole
	// left field for a row (right-justified, connectors included), so a styler
	// may recolor specific glyphs within it. Styling is applied after layout, so
	// it must preserve display width.
	CellLeft
)

// GlyphSet is the set of box-drawing glyphs used by the renderer. The zero
// value is unusable; use DefaultGlyphs (or Options with a nil GlyphSet, which
// falls back to DefaultGlyphs).
type GlyphSet struct {
	Horizontal string // ─
	Vertical   string // │
	TeeRight   string // ├  (up+down+right)
	TeeLeft    string // ┤  (up+down+left)
	TeeDown    string // ┬  (down+left+right)
	TeeUp      string // ┴  (up+left+right)
	Cross      string // ┼  (up+down+left+right)
	CornerUL   string // ┘  (up+left)
	CornerDR   string // ┌  (down+right)
	CornerUR   string // └  (up+right)
}

// DefaultGlyphs returns the light box-drawing glyph set.
func DefaultGlyphs() GlyphSet {
	return GlyphSet{
		Horizontal: "─",
		Vertical:   "│",
		TeeRight:   "├",
		TeeLeft:    "┤",
		TeeDown:    "┬",
		TeeUp:      "┴",
		Cross:      "┼",
		CornerUL:   "┘",
		CornerDR:   "┌",
		CornerUR:   "└",
	}
}

// Options controls rendering. The zero value is valid and renders with sane
// defaults (light glyphs, two-space column separator).
type Options struct {
	// Glyphs is the box-drawing glyph set; empty fields fall back to
	// DefaultGlyphs.
	Glyphs GlyphSet
	// LeftGutter is the number of spaces printed at the very start of every
	// line (before the left block).
	LeftGutter int
	// ColSep separates trailing columns. Defaults to two spaces.
	ColSep string
	// AlignColumns, if > 0, is the minimum width the right-side tree portion
	// is padded to before trailing columns are appended. When the tree portion
	// is wider than this the columns shift right (acceptable). When 0 the pad
	// is the natural max tree width.
	AlignColumns int
	// StyleCell, if non-nil, post-processes each cell's text. It must not
	// change the cell's display width if column alignment is to be preserved
	// (ANSI color codes are fine — they have zero display width). Layout is
	// computed from the un-styled text, so styling is purely cosmetic.
	StyleCell func(text string, kind CellKind) string
}

func (o Options) glyphs() GlyphSet {
	g := o.Glyphs
	d := DefaultGlyphs()
	if g.Horizontal == "" {
		g.Horizontal = d.Horizontal
	}
	if g.Vertical == "" {
		g.Vertical = d.Vertical
	}
	if g.TeeRight == "" {
		g.TeeRight = d.TeeRight
	}
	if g.TeeLeft == "" {
		g.TeeLeft = d.TeeLeft
	}
	if g.TeeDown == "" {
		g.TeeDown = d.TeeDown
	}
	if g.TeeUp == "" {
		g.TeeUp = d.TeeUp
	}
	if g.Cross == "" {
		g.Cross = d.Cross
	}
	if g.CornerUL == "" {
		g.CornerUL = d.CornerUL
	}
	if g.CornerDR == "" {
		g.CornerDR = d.CornerDR
	}
	if g.CornerUR == "" {
		g.CornerUR = d.CornerUR
	}
	return g
}

func (o Options) colSep() string {
	if o.ColSep == "" {
		return "  "
	}
	return o.ColSep
}

func (o Options) style(text string, kind CellKind) string {
	if o.StyleCell == nil {
		return text
	}
	return o.StyleCell(text, kind)
}

// dispWidth returns the monospace display width of s. All glyphs used here
// (box-drawing, arrows, the horizontal ellipsis) are single-width runes, so a
// rune count is correct; callers should avoid wide CJK/emoji in cells.
func dispWidth(s string) int { return utf8.RuneCountInString(s) }

func padLeft(s string, w int) string {
	if n := w - dispWidth(s); n > 0 {
		return strings.Repeat(" ", n) + s
	}
	return s
}

// rline is one rendered right-side line: a plain connector prefix, a plain
// label, and the node's trailing columns. Keeping the label separate lets the
// StyleCell hook decorate it without corrupting width math.
type rline struct {
	prefix string
	label  string
	cols   []string
}

func (r rline) treeWidth() int { return dispWidth(r.prefix) + dispWidth(r.label) }

// rightLayout renders node as a standard downward tree flowing rightward. The
// node's label is row 0; each right child hangs below with ├──/└── connectors.
func rightLayout(node *Node, g GlyphSet) []rline {
	out := []rline{{prefix: "", label: node.Label, cols: node.Cols}}
	h := g.Horizontal
	for i, child := range node.Right {
		last := i == len(node.Right)-1
		var head, cont string
		if last {
			head = g.CornerUR + h + h + " " // "└── "
			cont = "    "
		} else {
			head = g.TeeRight + h + h + " " // "├── "
			cont = g.Vertical + "   "       // "│   "
		}
		sub := rightLayout(child, g)
		for j, s := range sub {
			p := cont
			if j == 0 {
				p = head
			}
			out = append(out, rline{prefix: p + s.prefix, label: s.label, cols: s.cols})
		}
	}
	return out
}

// leftLayout renders node as a mirror tree flowing leftward, returned as a
// rectangle of width w whose rightmost column (w-1) is the connection point on
// row 0 (the node's label's last cell). Child subtrees hang below-left with
// mirrored connectors reaching the junction column.
func leftLayout(node *Node, g GlyphSet) (lines []string, w int) {
	labelW := dispWidth(node.Label)
	if len(node.Left) == 0 {
		return []string{node.Label}, labelW
	}
	h := g.Horizontal
	type cg struct {
		lines []string
		w     int
	}
	children := make([]cg, len(node.Left))
	childW := 0
	for i, c := range node.Left {
		cl, cw := leftLayout(c, g)
		children[i] = cg{cl, cw}
		if cw > childW {
			childW = cw
		}
	}
	// Junction sits at the block's right edge. A child connects via a 4-cell
	// segment " ──<glyph>" to its left, so a child's right edge lands at w-5.
	w = labelW
	if childW+4 > w {
		w = childW + 4
	}
	lines = append(lines, padLeft(node.Label, w)) // label flush right; junction = last cell
	for i, c := range children {
		last := i == len(node.Left)-1
		for r, cl := range c.lines {
			body := padLeft(cl, w-4) // child right edge at col w-5
			var conn string
			switch {
			case r == 0 && last:
				conn = " " + h + h + g.CornerUL // " ──┘"
			case r == 0:
				conn = " " + h + h + g.TeeLeft // " ──┤"
			case last:
				conn = "    "
			default:
				conn = "   " + g.Vertical // "   │"
			}
			lines = append(lines, body+conn)
		}
	}
	return lines, w
}

// spineGlyph returns the junction glyph for a top-level route on the spine.
// right is always true.
func spineGlyph(g GlyphSet, up, down, left bool) string {
	switch {
	case up && down:
		if left {
			return g.Cross
		}
		return g.TeeRight
	case up:
		if left {
			return g.TeeUp
		}
		return g.CornerUR
	case down:
		if left {
			return g.TeeDown
		}
		return g.CornerDR
	default:
		return g.Horizontal
	}
}

// Render renders the bilateral tree rooted at root and returns monospace
// plain text (no trailing newline on the final line). root.Right are the
// top-level "routes" laid out as a vertical spine; each route's Left is its
// mirrored annotation subtree.
func Render(root *Node, opts Options) string {
	if root == nil {
		return ""
	}
	g := opts.glyphs()
	h := g.Horizontal

	// Build each route (top-level right child) into a set of rows.
	type row struct {
		left     string // left block line (plain), un-justified
		hasLeft  bool
		anchor   bool   // spine junction row
		spine    string // spine column glyph for this row
		right    rline  // right-side tree line
		hasRight bool
	}
	var (
		routes    [][]row
		leftWidth int // max width of any left block line, across all routes
	)
	nRoutes := len(root.Right)
	for i, route := range root.Right {
		rlines := rightLayout(route, g)
		var llines []string
		if len(route.Left) > 0 {
			// A route may carry a forest of left children; stack them as a
			// single mirror block by wrapping in a synthetic parent whose own
			// label is empty is awkward, so render each and concatenate with
			// the first as the spine anchor. In practice the common case is a
			// single left child (possibly deeply nested).
			for _, lc := range route.Left {
				sub, _ := leftLayout(lc, g)
				llines = append(llines, sub...)
			}
		}
		for _, l := range llines {
			if dw := dispWidth(l); dw > leftWidth {
				leftWidth = dw
			}
		}
		// Routes join through a central vertical spine. Every route connects
		// UP (route 0 to the root descender above the spine, later routes to
		// the previous route); every route but the last connects DOWN to the
		// next. The junction glyph reflects up/down plus whether the route
		// carries a left-side summary block.
		up := true
		down := i < nRoutes-1
		n := len(rlines)
		if len(llines) > n {
			n = len(llines)
		}
		rows := make([]row, n)
		for r := 0; r < n; r++ {
			var rw row
			if r < len(llines) {
				rw.left = llines[r]
				rw.hasLeft = len(route.Left) > 0 && r == 0
			}
			if r < len(rlines) {
				rw.right = rlines[r]
				rw.hasRight = true
			}
			if r == 0 {
				rw.anchor = true
				rw.spine = spineGlyph(g, up, down, len(route.Left) > 0)
			} else if down {
				rw.spine = g.Vertical
			} else {
				rw.spine = " "
			}
			rows[r] = rw
		}
		routes = append(routes, rows)
	}

	// Column alignment: max tree width and per-column widths across all
	// right-side lines.
	treeWidth := opts.AlignColumns
	var colW []int
	for _, rows := range routes {
		for _, rw := range rows {
			if !rw.hasRight {
				continue
			}
			if tw := rw.right.treeWidth(); tw > treeWidth {
				treeWidth = tw
			}
			for ci, c := range rw.right.cols {
				for len(colW) <= ci {
					colW = append(colW, 0)
				}
				if cw := dispWidth(c); cw > colW[ci] {
					colW[ci] = cw
				}
			}
		}
	}

	gutter := strings.Repeat(" ", opts.LeftGutter)
	sep := opts.colSep()

	// Assemble every line.
	var b strings.Builder
	writeLine := func(s string) {
		b.WriteString(strings.TrimRight(s, " "))
		b.WriteByte('\n')
	}

	// Root label anchored at the central spine column, with a box-drawing
	// descender dropping into the top of the spine, so the source PK reads as
	// connected to the spine rather than floating off over the right-side hops.
	// spineCol is the 0-based column of the spine glyph: gutter + left field +
	// the joining space + the 2-cell arm.
	spineCol := opts.LeftGutter + leftWidth + 1 + 2
	rootCol := spineCol - dispWidth(root.Label)/2
	if rootCol < 0 {
		rootCol = 0
	}
	writeLine(strings.Repeat(" ", rootCol) + opts.style(root.Label, CellRoot))
	writeLine(strings.Repeat(" ", spineCol) + g.Vertical)

	for _, rows := range routes {
		for _, rw := range rows {
			// Left field (right-justified to leftWidth). Styled after layout so a
			// color/HTML wrapper may decorate glyphs within it without disturbing
			// column math (StyleCell must preserve display width).
			left := opts.style(padLeft(rw.left, leftWidth), CellLeft)
			// Middle connector: gutter + left field + space + arm(2) + spine.
			var arm string
			if rw.anchor && rw.hasLeft {
				arm = h + h
			} else {
				arm = "  "
			}
			mid := gutter + left + " " + arm + rw.spine

			// Right region.
			var rightText string
			if rw.hasRight {
				var rprefix string
				if rw.anchor {
					rprefix = h + h + " " // right arm "── "
				} else {
					rprefix = "   "
				}
				tree := rprefix + rw.right.prefix + opts.style(rw.right.label, CellLabel)
				// pad tree to treeWidth+3 (prefix adds 3) using plain width.
				plainW := 3 + rw.right.treeWidth()
				if pad := (treeWidth + 3) - plainW; pad > 0 {
					tree += strings.Repeat(" ", pad)
				}
				rightText = tree
				if len(rw.right.cols) > 0 {
					for ci, c := range rw.right.cols {
						rightText += sep
						cell := opts.style(c, CellColumn)
						// pad by plain width
						if pad := colW[ci] - dispWidth(c); pad > 0 {
							cell += strings.Repeat(" ", pad)
						}
						rightText += cell
					}
				}
			} else {
				rightText = "   "
			}
			writeLine(mid + rightText)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}
