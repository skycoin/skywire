//go:build js && wasm

package xterm

import (
	"strconv"
	"syscall/js"
	"time"
)

// Screen reader mode: a port of xterm.js's AccessibilityManager.
//
// The rows a renderer draws are no use to a screen reader — under WebGL they
// are pixels, and under the DOM renderer they are spans split wherever a color
// changes. So beside the screen there is a second copy of the viewport made
// for reading: one element per row, plain text, in a list a screen reader can
// walk, kept up to date as rows are drawn and the view scrolls. It is laid
// over the screen with transparent text, so it takes up no room and shows
// nothing, but a screen reader's highlight lands on the cells it is reading.
//
// Output as it arrives goes to a live region as well, so a person hears what
// a command printed without having to go and look for it. Focus stays on the
// input textarea throughout: the live region announces without being focused,
// and the rows are only focusable from a screen reader's own navigation.

// a11yDebounce is how often, at most, the tree is rebuilt while output
// streams (TimeBasedDebouncer's threshold in xterm.js). Each rebuild measures
// every row, and a screen reader re-reads what changes; once a second is
// enough to keep up and few enough to stay out of the way.
const a11yDebounce = time.Second

type accessibility struct {
	t *Terminal

	container js.Value // .xterm-accessibility
	tree      js.Value // .xterm-accessibility-tree, role=list
	live      js.Value // .live-region, aria-live
	rows      []js.Value
	// cols are each row's columns, from a11yRowText, for turning a selection
	// made in the tree back into cells.
	cols [][]int

	announce a11yAnnouncer

	// The debouncer's state: the rows waiting to be refreshed, when the last
	// refresh was, and whether one is already scheduled.
	pendStart, pendEnd int
	pending            bool
	last               time.Time
	timer              js.Value
	timerFn            js.Func

	topFocus, bottomFocus js.Func
	selChange             js.Func
}

// SetScreenReaderMode turns screen reader support on or off. The terminal
// starts with it as Options.ScreenReaderMode says.
func (t *Terminal) SetScreenReaderMode(on bool) {
	t.Core.Options.ScreenReaderMode = on
	if !t.opened {
		return
	}
	if on && t.a11y == nil {
		t.a11y = newAccessibility(t)
	} else if !on && t.a11y != nil {
		t.a11y.dispose()
		t.a11y = nil
	}
}

// ScreenReaderMode reports whether screen reader support is on.
func (t *Terminal) ScreenReaderMode() bool { return t.Core.Options.ScreenReaderMode }

func newAccessibility(t *Terminal) *accessibility {
	a := &accessibility{t: t}
	a.container = document.Call("createElement", "div")
	a.container.Set("className", "xterm-accessibility")
	a.tree = document.Call("createElement", "div")
	a.tree.Call("setAttribute", "role", "list")
	a.tree.Set("className", "xterm-accessibility-tree")
	a.container.Call("appendChild", a.tree)
	a.live = document.Call("createElement", "div")
	a.live.Set("className", "live-region")
	a.live.Call("setAttribute", "aria-live", "assertive")
	a.container.Call("appendChild", a.live)

	a.topFocus = js.FuncOf(func(_ js.Value, args []js.Value) any {
		a.boundaryFocus(args[0], true)
		return nil
	})
	a.bottomFocus = js.FuncOf(func(_ js.Value, args []js.Value) any {
		a.boundaryFocus(args[0], false)
		return nil
	})
	a.timerFn = js.FuncOf(func(js.Value, []js.Value) any {
		a.timer = js.Value{}
		a.last = time.Now()
		a.flush()
		return nil
	})
	a.selChange = js.FuncOf(func(js.Value, []js.Value) any {
		a.selectionChange()
		return nil
	})
	document.Call("addEventListener", "selectionchange", a.selChange)

	for range t.Core.Rows() {
		a.appendRow()
	}
	a.watchBoundaries()
	// Over the screen rather than at the top of the element, as xterm.js
	// has it: the element here has padding the screen does not, and the
	// tree's rows have to sit on the rows they read for a screen reader's
	// highlight to land in the right place.
	t.screen.Call("insertAdjacentElement", "afterbegin", a.container)
	a.refreshDimensions()
	a.refresh(0, t.Core.Rows()-1)
	return a
}

func (a *accessibility) newRow() js.Value {
	el := document.Call("createElement", "div")
	el.Call("setAttribute", "role", "listitem")
	el.Set("tabIndex", -1)
	el.Get("style").Set("height", jsPx(a.t.cellH))
	return el
}

func (a *accessibility) appendRow() {
	el := a.newRow()
	a.tree.Call("appendChild", el)
	a.rows = append(a.rows, el)
	a.cols = append(a.cols, nil)
}

// watchBoundaries listens for focus on the first and last rows: a screen
// reader moving past either edge of the tree is how it asks to scroll.
func (a *accessibility) watchBoundaries() {
	if len(a.rows) == 0 {
		return
	}
	a.rows[0].Call("addEventListener", "focus", a.topFocus)
	a.rows[len(a.rows)-1].Call("addEventListener", "focus", a.bottomFocus)
}

func (a *accessibility) unwatchBoundaries() {
	if len(a.rows) == 0 {
		return
	}
	a.rows[0].Call("removeEventListener", "focus", a.topFocus)
	a.rows[len(a.rows)-1].Call("removeEventListener", "focus", a.bottomFocus)
}

func (a *accessibility) dispose() {
	if a.timer.Truthy() {
		window.Call("clearTimeout", a.timer)
		a.timer = js.Value{}
	}
	a.unwatchBoundaries()
	document.Call("removeEventListener", "selectionchange", a.selChange)
	a.container.Call("remove")
	a.rows, a.cols = nil, nil
	a.topFocus.Release()
	a.bottomFocus.Release()
	a.timerFn.Release()
	a.selChange.Release()
}

// refresh asks for viewport rows start..end to be read again. The first
// request after a quiet second is done at once, so a keystroke's echo is
// heard straight away; requests after that are gathered up and done when
// the second is over.
func (a *accessibility) refresh(start, end int) {
	if a.pending {
		a.pendStart = min(a.pendStart, start)
		a.pendEnd = max(a.pendEnd, end)
	} else {
		a.pendStart, a.pendEnd, a.pending = start, end, true
	}
	now := time.Now()
	if since := now.Sub(a.last); since >= a11yDebounce {
		a.last = now
		a.flush()
	} else if !a.timer.Truthy() {
		a.timer = window.Call("setTimeout", a.timerFn, (a11yDebounce - since).Milliseconds())
	}
}

func (a *accessibility) flush() {
	if !a.pending {
		a.announceChars()
		return
	}
	a.pending = false
	rows := a.t.Core.Rows()
	a.renderRows(max(a.pendStart, 0), min(a.pendEnd, rows-1))
}

func (a *accessibility) renderRows(start, end int) {
	b := a.t.Core.Buffer()
	setSize := strconv.Itoa(b.Lines.Length())
	for i := start; i <= end && i < len(a.rows); i++ {
		el := a.rows[i]
		line := b.YDisp + i
		text, cols := "", []int{0}
		if line < b.Lines.Length() {
			text, cols = a11yRowText(b.Lines.Get(line))
		}
		if text == "" {
			text, cols = " ", []int{0, 1}
		}
		a.cols[i] = cols
		// Only when it changed: the cursor blinking redraws its row twice a
		// second, and replacing a row's text with the same text is still a
		// change a screen reader may read out again.
		if el.Get("textContent").String() != text {
			el.Set("textContent", text)
		}
		el.Call("setAttribute", "aria-posinset", strconv.Itoa(line+1))
		el.Call("setAttribute", "aria-setsize", setSize)
		a.alignRowWidth(i)
	}
	a.announceChars()
}

func (a *accessibility) announceChars() {
	if s := a.announce.take(); s != "" {
		a.live.Set("textContent", a.live.Get("textContent").String()+s)
	}
}

func (a *accessibility) clearLiveRegion() {
	a.live.Set("textContent", "")
	a.announce.reset()
}

// key is told what a key press sent to the program.
func (a *accessibility) key(k string) {
	a.clearLiveRegion()
	a.announce.key(k)
}

// resize grows or shrinks the tree to the terminal's rows.
func (a *accessibility) resize() {
	a.unwatchBoundaries()
	rows := a.t.Core.Rows()
	for len(a.rows) < rows {
		a.appendRow()
	}
	for len(a.rows) > rows {
		a.rows[len(a.rows)-1].Call("remove")
		a.rows = a.rows[:len(a.rows)-1]
		a.cols = a.cols[:len(a.cols)-1]
	}
	a.watchBoundaries()
	a.refreshDimensions()
	a.refresh(0, rows-1)
}

// refreshDimensions sizes the tree and its rows to the cells, after the
// font or the grid changed.
func (a *accessibility) refreshDimensions() {
	t := a.t
	if t.cellH == 0 {
		return
	}
	st := a.container.Get("style")
	st.Set("width", jsPx(t.cellW*float64(t.Core.Cols())))
	st.Set("fontSize", jsPx(t.Core.Options.FontSize))
	if len(a.rows) != t.Core.Rows() {
		a.resize()
		return
	}
	for i, el := range a.rows {
		el.Get("style").Set("height", jsPx(t.cellH))
		a.alignRowWidth(i)
	}
}

// alignRowWidth stretches a row so each character sits over its cell. The
// tree is drawn in the browser's monospace, which need not be the terminal's
// font and has no reason to give wide characters exactly two cells; scaling
// the row to the width of its cells is what puts a screen reader's outline
// around the text it is reading rather than beside it.
func (a *accessibility) alignRowWidth(i int) {
	el := a.rows[i]
	st := el.Get("style")
	st.Set("transform", "")
	cols := a.cols[i]
	if len(cols) == 0 || cols[len(cols)-1] == 0 {
		return
	}
	width := el.Call("getBoundingClientRect").Get("width").Float()
	if width == 0 {
		return
	}
	target := float64(cols[len(cols)-1]) * a.t.cellW
	st.Set("transform", "scaleX("+strconv.FormatFloat(target/width, 'f', -1, 64)+")")
}

// boundaryFocus scrolls when a screen reader steps off the first or last row
// of the tree, by recycling that edge's row to the other end, so that reading
// line by line carries on into the scrollback rather than stopping at the
// edge of the screen.
func (a *accessibility) boundaryFocus(ev js.Value, top bool) {
	if len(a.rows) < 2 {
		return
	}
	boundary := ev.Get("target")
	before := a.rows[len(a.rows)-2]
	lastPos := strconv.Itoa(a.t.Core.Buffer().Lines.Length())
	if top {
		before = a.rows[1]
		lastPos = "1"
	}
	// Nothing further that way.
	if boundary.Call("getAttribute", "aria-posinset").String() == lastPos {
		return
	}
	// Only when the focus came from the row next to the edge, which is
	// reading towards it rather than away.
	if !ev.Get("relatedTarget").Equal(before) {
		return
	}
	a.unwatchBoundaries()
	if top {
		last := a.rows[len(a.rows)-1]
		last.Call("remove")
		a.rows = append([]js.Value{a.newRow()}, a.rows[:len(a.rows)-1]...)
		a.cols = append([][]int{nil}, a.cols[:len(a.cols)-1]...)
		a.tree.Call("insertAdjacentElement", "afterbegin", a.rows[0])
	} else {
		a.rows[0].Call("remove")
		a.rows = append(a.rows[1:], a.newRow())
		a.cols = append(a.cols[1:], nil)
		a.tree.Call("appendChild", a.rows[len(a.rows)-1])
	}
	a.watchBoundaries()
	if top {
		a.t.Core.ScrollLines(-1)
		a.rows[1].Call("focus")
	} else {
		a.t.Core.ScrollLines(1)
		a.rows[len(a.rows)-2].Call("focus")
	}
	ev.Call("preventDefault")
	ev.Call("stopImmediatePropagation")
}

// selectionChange makes a selection a screen reader makes in the tree the
// terminal's selection, so that copying it copies what the terminal holds.
func (a *accessibility) selectionChange() {
	if len(a.rows) == 0 {
		return
	}
	sel := document.Call("getSelection")
	if !sel.Truthy() {
		return
	}
	if sel.Get("isCollapsed").Bool() {
		// As with the mouse, a click somewhere else on the page leaves the
		// selection alone; only one inside the tree clears it.
		if a.tree.Call("contains", sel.Get("anchorNode")).Bool() {
			a.t.ClearSelection()
		}
		return
	}
	if !sel.Get("anchorNode").Truthy() || !sel.Get("focusNode").Truthy() {
		return
	}
	const (
		docPreceding   = 0x02
		docFollowing   = 0x04
		docContainedBy = 0x10
	)
	beginNode, beginOff := sel.Get("anchorNode"), sel.Get("anchorOffset").Int()
	endNode, endOff := sel.Get("focusNode"), sel.Get("focusOffset").Int()
	if beginNode.Call("compareDocumentPosition", endNode).Int()&docPreceding != 0 ||
		(beginNode.Equal(endNode) && beginOff > endOff) {
		beginNode, endNode = endNode, beginNode
		beginOff, endOff = endOff, beginOff
	}
	first, last := a.rows[0], a.rows[len(a.rows)-1]
	if beginNode.Call("compareDocumentPosition", first).Int()&(docContainedBy|docFollowing) != 0 {
		beginNode, beginOff = first.Get("childNodes").Index(0), 0
	}
	if !a.tree.Call("contains", beginNode).Bool() {
		return
	}
	if endNode.Call("compareDocumentPosition", last).Int()&(docContainedBy|docPreceding) != 0 {
		endNode, endOff = last, len(last.Get("textContent").String())
	}
	if !a.tree.Call("contains", endNode).Bool() {
		return
	}
	begin, ok1 := a.toCell(beginNode, beginOff)
	end, ok2 := a.toCell(endNode, endOff)
	if !ok1 || !ok2 || !begin.before(end) {
		return
	}
	a.t.sel.selectRange(begin, end)
	a.t.scheduleRender(false)
}

// toCell turns a point in the tree into the cell it is over.
func (a *accessibility) toCell(node js.Value, offset int) (pos, bool) {
	row := node
	if node.Get("nodeType").Int() == 3 { // a text node
		row = node.Get("parentNode")
	}
	idx := -1
	for i, el := range a.rows {
		if el.Equal(row) {
			idx = i
			break
		}
	}
	if idx < 0 || len(a.cols[idx]) == 0 {
		return pos{}, false
	}
	line, err := strconv.Atoi(row.Call("getAttribute", "aria-posinset").String())
	if err != nil {
		return pos{}, false
	}
	line--
	cols := a.cols[idx]
	col := cols[len(cols)-1] + 1
	if offset < len(cols) {
		col = cols[offset]
	}
	if col >= a.t.Core.Cols() {
		line++
		col = 0
	}
	return pos{col, line}, true
}
