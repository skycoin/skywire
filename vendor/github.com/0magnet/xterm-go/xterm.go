//go:build js && wasm

// Package xterm is a Go/wasm port of xterm.js 6.0.0 — a terminal
// emulator for the browser. The VT core (parser, buffers, input
// handler) lives in the pure-Go subpackage vt; this package adds the
// browser layer: a DOM renderer, keyboard handling, a scrollback
// viewport and equivalents of the fit and attach addons.
package xterm

import (
	"errors"
	"strconv"
	"strings"
	"syscall/js"

	"github.com/0magnet/xterm-go/vt"
)

// renderer abstracts over the DOM and WebGL renderers.
type renderer interface {
	// renderRows redraws viewport rows start..end (inclusive).
	renderRows(start, end int)
	// onResize is called after the terminal dimensions changed.
	onResize()
	// onColorsChanged is called when the palette/theme changed.
	onColorsChanged()
	dispose()
}

var (
	document = js.Global().Get("document")
	window   = js.Global()
)

// Terminal is a terminal rendered into a DOM element.
type Terminal struct {
	Core *vt.Terminal

	// OnTitleChange fires when the application sets the window title.
	OnTitleChange func(title string)

	// OnResize fires after the grid changed size, with the new dimensions.
	//
	// USE THIS RATHER THAN Core.OnResize. That field looks equally available —
	// Attach assigns Core.OnData, so assigning Core.OnResize beside it reads as
	// the same kind of thing — but Open sets it, and ITS handler is what tells
	// the renderer to reallocate for the new grid. Overwriting it leaves the
	// render model sized for the old grid while the renderer reads the new
	// columns and rows, and the next frame indexes past the end of it:
	//
	//	panic: index out of range [6889] with length 6888
	//	  xterm-go.(*rectangleRenderer).updateBackgrounds
	//
	// In wasm that panic ends the Go program, so the symptom is not a
	// misdrawn terminal but every terminal on the page freezing at once. This
	// field is fanned out from that handler and cannot displace it.
	//
	// It is the hook a pty bridge wants: a resize here is what should become a
	// TIOCSWINSZ at the far end.
	OnResize func(cols, rows int)
	// OnBell fires on BEL.
	OnBell func()
	// OnSelectionChange fires when the selected text changes, including when
	// it is cleared.
	OnSelectionChange func()

	// CopyOnSelect puts the selection on the clipboard as soon as the mouse is
	// released, the way X11 fills its primary selection. Off by default: it
	// overwrites whatever the clipboard was holding, which is not a decision
	// a terminal should make for everyone.
	CopyOnSelect bool

	// NoContextMenu leaves the right button to the browser. The terminal draws
	// its own menu by default, because a browser's Copy acts on the document's
	// selection and the terminal's is not one — so the browser offers no Copy
	// over a terminal however much is highlighted.
	NoContextMenu bool

	colors *ColorSet

	element         js.Value // the .xterm root
	viewport        js.Value // scrollbar area
	scrollArea      js.Value // spacer inside viewport
	screen          js.Value // holds the rows
	rowsEl          js.Value
	textarea        js.Value // hidden input capture
	compositionView js.Value // in-progress IME composition overlay
	rowEls          []js.Value

	composition    *compositionHelper
	renderer       renderer
	resizeObserver js.Value // set by AutoFit

	sel      *selection
	selInput selectionInput
	menu     *contextMenu

	cellW, cellH float64

	keyDownHandled       bool
	renderQueued         bool
	fitQueued            bool
	allDirty             bool
	dirtyStart, dirtyEnd int
	syncingScroll        bool
	blinkVisible         bool
	opened               bool

	funcs []js.Func

	// blinkTimer is the window.setInterval handle for the cursor-blink tick; it
	// must be cleared in Dispose, otherwise the global interval keeps firing the
	// (now-released) blink js.Func → "call to released function" every 600ms after
	// the terminal is torn down (e.g. its window closed).
	blinkTimer js.Value
}

// New creates a terminal (nil options = defaults).
func New(options *vt.Options) *Terminal {
	t := &Terminal{
		Core:         vt.NewTerminal(options),
		blinkVisible: true,
	}
	t.colors = NewColorSet(t.Core.Options.Theme)
	t.sel = newSelection(t.Core.Cols, t.Core.Buffer)
	t.sel.dirty = t.markSelectionDirty
	t.sel.changed = t.notifySelection
	t.selInput.t = t
	return t
}

func (t *Terminal) fn(f func(js.Value, []js.Value) any) js.Func {
	jf := js.FuncOf(f)
	t.funcs = append(t.funcs, jf)
	return jf
}

// Open attaches the terminal to a DOM element.
func (t *Terminal) Open(parent js.Value) {
	if t.opened {
		return
	}
	t.opened = true
	ensureStylesheet()

	opts := t.Core.Options

	t.element = document.Call("createElement", "div")
	t.element.Set("className", "xterm")
	style := t.element.Get("style")
	style.Set("backgroundColor", t.colors.Background)
	style.Set("fontFamily", opts.FontFamily)
	style.Set("fontSize", jsPx(opts.FontSize))

	t.viewport = document.Call("createElement", "div")
	t.viewport.Set("className", "xterm-viewport")
	t.scrollArea = document.Call("createElement", "div")
	t.scrollArea.Set("className", "xterm-scroll-area")
	t.viewport.Call("appendChild", t.scrollArea)

	t.screen = document.Call("createElement", "div")
	t.screen.Set("className", "xterm-screen")
	t.rowsEl = document.Call("createElement", "div")
	t.rowsEl.Set("className", "xterm-rows")
	t.rowsEl.Get("style").Set("color", t.colors.Foreground)
	t.screen.Call("appendChild", t.rowsEl)

	t.textarea = document.Call("createElement", "textarea")
	t.textarea.Set("className", "xterm-helper-textarea")
	t.textarea.Set("autocapitalize", "off")
	t.textarea.Set("autocomplete", "off")
	t.textarea.Set("spellcheck", false)

	t.compositionView = document.Call("createElement", "div")
	t.compositionView.Set("className", "xterm-composition-view")
	t.composition = newCompositionHelper(t, t.textarea, t.compositionView)

	t.element.Call("appendChild", t.textarea)
	t.element.Call("appendChild", t.viewport)
	t.element.Call("appendChild", t.screen)
	t.screen.Call("appendChild", t.compositionView)
	parent.Call("appendChild", t.element)

	t.measureCharSize()
	t.refreshRowEls()
	t.updateScrollArea()
	t.renderer = &domRenderer{t}
	t.wireCoreEvents()
	t.wireDomEvents()
	t.scheduleRender(true)
	t.Focus()

	// cursor blink timer (CSS animation handles the fade; we only need
	// re-render on option flips, so a simple interval keeps it honest)
	blink := t.fn(func(js.Value, []js.Value) any {
		if t.cursorBlinkEnabled() {
			t.blinkVisible = !t.blinkVisible
			t.markCursorRowDirty()
			t.scheduleRender(false)
		} else if !t.blinkVisible {
			t.blinkVisible = true
			t.markCursorRowDirty()
			t.scheduleRender(false)
		}
		return nil
	})
	t.blinkTimer = window.Call("setInterval", blink, 600)
}

func (t *Terminal) cursorBlinkEnabled() bool {
	if b := t.Core.CoreService().DecPrivateModes.CursorBlink; b != nil {
		return *b
	}
	return t.Core.Options.CursorBlink
}

func (t *Terminal) cursorStyle() string {
	if s := t.Core.CoreService().DecPrivateModes.CursorStyle; s != nil {
		return *s
	}
	if t.Core.Options.CursorStyle != "" {
		return t.Core.Options.CursorStyle
	}
	return "block"
}

func (t *Terminal) measureCharSize() {
	probe := document.Call("createElement", "div")
	probe.Set("className", "xterm-rows")
	ps := probe.Get("style")
	ps.Set("position", "absolute")
	ps.Set("visibility", "hidden")
	ps.Set("fontFamily", t.Core.Options.FontFamily)
	ps.Set("fontSize", jsPx(t.Core.Options.FontSize))
	probe.Set("textContent", strings.Repeat("W", 32))
	t.element.Call("appendChild", probe)
	rect := probe.Call("getBoundingClientRect")
	t.cellW = rect.Get("width").Float() / 32
	t.cellH = rect.Get("height").Float()
	if lh := t.Core.Options.LineHeight; lh > 1 {
		t.cellH *= lh
	}
	t.element.Call("removeChild", probe)
}

func (t *Terminal) refreshRowEls() {
	rows := t.Core.Rows()
	for len(t.rowEls) < rows {
		row := document.Call("createElement", "div")
		row.Get("style").Set("height", jsPx(t.cellH))
		t.rowsEl.Call("appendChild", row)
		t.rowEls = append(t.rowEls, row)
	}
	for len(t.rowEls) > rows {
		last := t.rowEls[len(t.rowEls)-1]
		t.rowsEl.Call("removeChild", last)
		t.rowEls = t.rowEls[:len(t.rowEls)-1]
	}
	t.screen.Get("style").Set("width", jsPx(t.cellW*float64(t.Core.Cols())))
	t.screen.Get("style").Set("height", jsPx(t.cellH*float64(rows)))
}

func (t *Terminal) updateScrollArea() {
	total := t.Core.Buffer().Lines.Length()
	t.scrollArea.Get("style").Set("height", jsPx(t.cellH*float64(total)))
	t.syncScrollTop()
}

func (t *Terminal) syncScrollTop() {
	t.syncingScroll = true
	t.viewport.Set("scrollTop", float64(t.Core.Buffer().YDisp)*t.cellH)
	t.syncingScroll = false
}

func (t *Terminal) wireCoreEvents() {
	t.Core.OnRefreshRows = func(start, end int) {
		t.markDirty(start, end)
		t.scheduleRender(false)
	}
	t.Core.OnScroll = func(int) {
		// Once the scrollback is full each new line evicts the oldest, and
		// every line index below it shifts by one — including the ones the
		// selection is pinned to. Rather than track the drift, drop the
		// selection at the point it stops meaning what it says.
		if t.Core.Buffer().Lines.IsFull() {
			t.sel.drop()
		}
		t.allDirty = true
		t.updateScrollArea()
		t.scheduleRender(false)
	}
	t.Core.OnResize = func(cols, rows int) {
		// Reflow rewraps the buffer, so the cells a selection names are no
		// longer the cells it meant.
		t.sel.drop()
		t.refreshRowEls()
		t.updateScrollArea()
		t.renderer.onResize()
		t.scheduleRender(true)
		// Last, and after the renderer: a consumer told about the new size
		// before the renderer has been reallocated for it may act on the new
		// grid while the next frame is still drawn against the old one.
		if t.OnResize != nil {
			t.OnResize(cols, rows)
		}
	}
	t.Core.OnCursorMove = func() {
		t.markCursorRowDirty()
		t.scheduleRender(false)
	}
	t.Core.OnTitleChange = func(title string) {
		if t.OnTitleChange != nil {
			t.OnTitleChange(title)
		}
	}
	t.Core.OnBell = func() {
		if t.OnBell != nil {
			t.OnBell()
		}
	}
	t.Core.OnColor = func(events []vt.ColorEvent) {
		changed := false
		for _, e := range events {
			switch e.Type {
			case vt.ColorRequestSet:
				css := rgbCSS(e.Color)
				switch e.Index {
				case vt.SpecialColorForeground:
					t.colors.Foreground = css
					t.rowsEl.Get("style").Set("color", css)
				case vt.SpecialColorBackground:
					t.colors.Background = css
					t.element.Get("style").Set("backgroundColor", css)
				case vt.SpecialColorCursor:
					t.colors.Cursor = css
				default:
					if e.Index >= 0 && e.Index < 256 {
						t.colors.Ansi[e.Index] = css
					}
				}
				changed = true
			case vt.ColorRequestRestore:
				fresh := NewColorSet(t.Core.Options.Theme)
				switch e.Index {
				case -1:
					t.colors.Ansi = fresh.Ansi
				case vt.SpecialColorForeground:
					t.colors.Foreground = fresh.Foreground
					t.rowsEl.Get("style").Set("color", fresh.Foreground)
				case vt.SpecialColorBackground:
					t.colors.Background = fresh.Background
					t.element.Get("style").Set("backgroundColor", fresh.Background)
				case vt.SpecialColorCursor:
					t.colors.Cursor = fresh.Cursor
				default:
					if e.Index >= 0 && e.Index < 256 {
						t.colors.Ansi[e.Index] = fresh.Ansi[e.Index]
					}
				}
				changed = true
			}
		}
		if changed {
			t.renderer.onColorsChanged()
			t.scheduleRender(true)
		}
	}
}

func (t *Terminal) wireDomEvents() {
	// IME composition
	t.textarea.Call("addEventListener", "compositionstart", t.fn(func(js.Value, []js.Value) any {
		t.composition.CompositionStart()
		return nil
	}))
	t.textarea.Call("addEventListener", "compositionupdate", t.fn(func(_ js.Value, args []js.Value) any {
		t.composition.CompositionUpdate(args[0].Get("data").String())
		return nil
	}))
	t.textarea.Call("addEventListener", "compositionend", t.fn(func(js.Value, []js.Value) any {
		t.composition.CompositionEnd()
		return nil
	}))

	// keyboard
	t.textarea.Call("addEventListener", "keydown", t.fn(func(_ js.Value, args []js.Value) any {
		ev := args[0]
		// copy and select-all first: they act on the selection rather than
		// on the program, and must not be forwarded as input
		if t.selectionKeydown(ev) {
			return nil
		}
		// route through the composition helper first; while composing,
		// keys must reach the textarea/IME untouched
		if !t.composition.Keydown(ev.Get("keyCode").Int()) {
			b := t.Core.Buffer()
			if b.YBase != b.YDisp {
				t.Core.ScrollToBottom()
			}
			return nil
		}
		kev := &vt.KeyboardEvent{
			AltKey:   ev.Get("altKey").Bool(),
			CtrlKey:  ev.Get("ctrlKey").Bool(),
			ShiftKey: ev.Get("shiftKey").Bool(),
			MetaKey:  ev.Get("metaKey").Bool(),
			KeyCode:  ev.Get("keyCode").Int(),
			Key:      ev.Get("key").String(),
			Code:     ev.Get("code").String(),
		}
		isMac := strings.Contains(window.Get("navigator").Get("platform").String(), "Mac")
		result := vt.EvaluateKeyboardEvent(kev, t.Core.CoreService().DecPrivateModes.ApplicationCursorKeys, isMac, false)
		switch result.Type {
		case vt.KeyPageUp:
			t.Core.ScrollPages(-1)
			ev.Call("preventDefault")
			return nil
		case vt.KeyPageDown:
			t.Core.ScrollPages(1)
			ev.Call("preventDefault")
			return nil
		case vt.KeySelectAll:
			t.SelectAll()
			ev.Call("preventDefault")
			return nil
		}
		t.keyDownHandled = false
		if result.Key != "" {
			// clear the textarea when ctrl+c or enter is sent so
			// composition offsets don't drift (and screen readers
			// announce deletions, like the original)
			if result.Key == "\x03" || result.Key == "\r" {
				t.textarea.Set("value", "")
			}
			// don't swallow browser shortcuts that produced no data
			ev.Call("preventDefault")
			ev.Call("stopPropagation")
			t.keyDownHandled = true
			// Typing is the end of caring about what was selected.
			t.sel.drop()
			t.Core.Input(result.Key, true)
		} else if result.Cancel {
			ev.Call("preventDefault")
		}
		return nil
	}))

	// keypress: keys evaluateKeyboardEvent leaves alone (space and
	// third-level-shift characters) arrive here, like the _keyPress
	// path of the original
	t.textarea.Call("addEventListener", "keypress", t.fn(func(_ js.Value, args []js.Value) any {
		ev := args[0]
		if t.keyDownHandled {
			return nil
		}
		key := ev.Get("key").String()
		if ev.Get("ctrlKey").Bool() || ev.Get("metaKey").Bool() {
			return nil
		}
		if len(vt.Utf16Units(key)) == 1 {
			ev.Call("preventDefault")
			ev.Call("stopPropagation")
			t.sel.drop()
			t.Core.Input(key, true)
		}
		return nil
	}))

	// paste (with bracketed paste mode)
	t.textarea.Call("addEventListener", "paste", t.fn(func(_ js.Value, args []js.Value) any {
		ev := args[0]
		ev.Call("preventDefault")
		data := ev.Get("clipboardData").Call("getData", "text/plain").String()
		t.Paste(data)
		return nil
	}))

	// focus terminal on click, and start a selection unless the application
	// has asked for the mouse itself
	t.element.Call("addEventListener", "mousedown", t.fn(func(_ js.Value, args []js.Value) any {
		if t.selectionMouseDown(args[0]) {
			return nil
		}
		t.reportMouse(args[0], vt.MouseActionDown)
		return nil
	}))
	t.element.Call("addEventListener", "mouseup", t.fn(func(_ js.Value, args []js.Value) any {
		t.reportMouse(args[0], vt.MouseActionUp)
		t.Focus()
		return nil
	}))
	t.element.Call("addEventListener", "mousemove", t.fn(func(_ js.Value, args []js.Value) any {
		if t.Core.MouseService().AreMouseEventsActive() {
			t.reportMouse(args[0], vt.MouseActionMove)
		}
		return nil
	}))
	// A drag carries on wherever the pointer goes, so the rest of it is
	// watched on the document.
	t.wireSelection()
	t.wireContextMenu()

	// wheel: scroll our own viewport (or report to the app)
	t.element.Call("addEventListener", "wheel", t.fn(func(_ js.Value, args []js.Value) any {
		ev := args[0]
		if t.Core.MouseService().AreMouseEventsActive() {
			t.reportWheel(ev)
			ev.Call("preventDefault")
			return nil
		}
		dpr := window.Get("devicePixelRatio").Float()
		amount := t.Core.MouseService().ConsumeWheelEvent(
			ev.Get("deltaY").Float(), ev.Get("deltaMode").Int(),
			ev.Get("shiftKey").Bool(), ev.Get("altKey").Bool(), ev.Get("ctrlKey").Bool(),
			t.cellH*dpr, dpr,
		)
		if amount != 0 {
			t.Core.ScrollLines(amount)
			ev.Call("preventDefault")
		}
		return nil
	}), map[string]any{"passive": false})

	// scrollbar dragging
	t.viewport.Call("addEventListener", "scroll", t.fn(func(js.Value, []js.Value) any {
		if t.syncingScroll {
			return nil
		}
		newDisp := int(t.viewport.Get("scrollTop").Float()/t.cellH + 0.5)
		diff := newDisp - t.Core.Buffer().YDisp
		if diff != 0 {
			t.Core.ScrollLines(diff)
		}
		return nil
	}))
}

func (t *Terminal) mouseCell(ev js.Value) (col, row, x, y int) {
	rect := t.screen.Call("getBoundingClientRect")
	x = int(ev.Get("clientX").Float() - rect.Get("left").Float())
	y = int(ev.Get("clientY").Float() - rect.Get("top").Float())
	col = int(float64(x) / t.cellW)
	row = int(float64(y) / t.cellH)
	return col, row, x, y
}

func (t *Terminal) reportMouse(ev js.Value, action int) {
	if !t.Core.MouseService().AreMouseEventsActive() {
		return
	}
	col, row, x, y := t.mouseCell(ev)
	button := ev.Get("button").Int()
	if action == vt.MouseActionMove {
		if ev.Get("buttons").Int() == 0 {
			button = vt.MouseButtonNone
		}
	}
	t.Core.MouseService().TriggerMouseEvent(&vt.MouseEvent{
		Col: col, Row: row, X: x, Y: y,
		Button: button,
		Action: action,
		Ctrl:   ev.Get("ctrlKey").Bool(),
		Alt:    ev.Get("altKey").Bool(),
		Shift:  ev.Get("shiftKey").Bool(),
	})
}

func (t *Terminal) reportWheel(ev js.Value) {
	col, row, x, y := t.mouseCell(ev)
	action := vt.MouseActionUp // wheel up
	if ev.Get("deltaY").Float() > 0 {
		action = vt.MouseActionDown
	}
	t.Core.MouseService().TriggerMouseEvent(&vt.MouseEvent{
		Col: col, Row: row, X: x, Y: y,
		Button: vt.MouseButtonWheel,
		Action: action,
		Ctrl:   ev.Get("ctrlKey").Bool(),
		Alt:    ev.Get("altKey").Bool(),
		Shift:  ev.Get("shiftKey").Bool(),
	})
}

// Focus focuses the terminal input.
func (t *Terminal) Focus() {
	t.textarea.Call("focus")
}

// Paste sends pasted data, honoring bracketed paste mode.
func (t *Terminal) Paste(data string) {
	data = strings.ReplaceAll(data, "\r\n", "\r")
	data = strings.ReplaceAll(data, "\n", "\r")
	if t.Core.CoreService().DecPrivateModes.BracketedPasteMode {
		data = "\x1b[200~" + data + "\x1b[201~"
	}
	t.Core.Input(data, true)
}

// Write feeds pty output (UTF-8 bytes) into the terminal.
func (t *Terminal) Write(data []byte) {
	t.Core.Write(data)
}

// WriteString feeds pty output into the terminal.
func (t *Terminal) WriteString(data string) {
	t.Core.WriteString(data)
}

// dirty tracking + rAF scheduling

// dirty range: dirtyEnd < dirtyStart means "empty".
func (t *Terminal) markDirty(start, end int) {
	if start < 0 || end < 0 {
		t.allDirty = true
		return
	}
	if t.dirtyEnd < t.dirtyStart {
		t.dirtyStart, t.dirtyEnd = start, end
		return
	}
	if start < t.dirtyStart {
		t.dirtyStart = start
	}
	if end > t.dirtyEnd {
		t.dirtyEnd = end
	}
}

func (t *Terminal) markCursorRowDirty() {
	b := t.Core.Buffer()
	y := b.YBase + b.Y - b.YDisp
	if y >= 0 && y < t.Core.Rows() {
		t.markDirty(y, y)
	}
}

// scheduleFit refits on the next frame rather than inside the notification.
//
// A ResizeObserver fires for every step of a layout, so a window being dragged
// or a desktop arranging itself produces a burst of them, and refitting inside
// each one reflows the buffer that many times to reach one final size.
//
// It also keeps Fit out of the callback itself. Compiled with TinyGo, calling
// into JavaScript from inside a ResizeObserver notification returned an
// address the shim would not store to —
//
//	RangeError: Offset is outside the bounds of the DataView
//	  at storeValue -> syscall/js.valueCall -> Terminal.Fit -> AutoFit
//
// — thrown once per notification, which under a window manager is constant.
// Deferring to an animation frame does the same work outside that reentrancy.
func (t *Terminal) scheduleFit() {
	if t.fitQueued || !t.opened {
		return
	}
	t.fitQueued = true
	var raf js.Func
	raf = js.FuncOf(func(js.Value, []js.Value) any {
		t.fitQueued = false
		t.Fit()
		raf.Release()
		return nil
	})
	window.Call("requestAnimationFrame", raf)
}

func (t *Terminal) scheduleRender(all bool) {
	if all {
		t.allDirty = true
	}
	if t.renderQueued || !t.opened {
		return
	}
	t.renderQueued = true
	var raf js.Func
	raf = js.FuncOf(func(js.Value, []js.Value) any {
		t.renderQueued = false
		t.render()
		raf.Release()
		return nil
	})
	window.Call("requestAnimationFrame", raf)
}

func (t *Terminal) render() {
	rows := t.Core.Rows()
	start, end := t.dirtyStart, t.dirtyEnd
	if t.allDirty {
		start, end = 0, rows-1
	}
	t.allDirty = false
	t.dirtyStart, t.dirtyEnd = 0, -1 // empty
	if end >= rows {
		end = rows - 1
	}
	if start < 0 {
		start = 0
	}
	if end < start {
		return
	}
	t.renderer.renderRows(start, end)
}

// domRenderer is the default renderer: rows as divs of styled spans.
type domRenderer struct{ t *Terminal }

func (d *domRenderer) renderRows(start, end int) {
	t := d.t
	b := t.Core.Buffer()
	cursorRow := -1
	cursorVisible := !t.Core.CoreService().IsCursorHidden && t.blinkVisible && b.IsCursorInViewport()
	if cursorVisible {
		cursorRow = b.YBase + b.Y - b.YDisp
	}
	for y := start; y <= end && y < len(t.rowEls); y++ {
		lineIdx := b.YDisp + y
		if lineIdx >= b.Lines.Length() {
			t.rowEls[y].Set("innerHTML", "")
			continue
		}
		cursorCol := -1
		if y == cursorRow {
			cursorCol = min(b.X, t.Core.Cols()-1)
		}
		t.rowEls[y].Set("innerHTML", t.rowHTML(b.Lines.Get(lineIdx), lineIdx, cursorCol))
	}
}

func (d *domRenderer) onResize()        {}
func (d *domRenderer) onColorsChanged() {}
func (d *domRenderer) dispose()         {}

// EnableWebGL switches to the WebGL renderer (the addon-webgl
// equivalent). On failure (no WebGL2 context) the DOM renderer stays
// active and an error is returned.
func (t *Terminal) EnableWebGL() error {
	if !t.opened {
		return errors.New("terminal is not opened")
	}
	if _, ok := t.renderer.(*webglRenderer); ok {
		return nil
	}
	r, err := newWebglRenderer(t)
	if err != nil {
		return err
	}
	t.renderer.dispose()
	t.rowsEl.Get("style").Set("display", "none")
	t.renderer = r
	t.scheduleRender(true)
	return nil
}

// DisableWebGL switches back to the DOM renderer.
func (t *Terminal) DisableWebGL() {
	wr, ok := t.renderer.(*webglRenderer)
	if !ok {
		return
	}
	wr.dispose()
	t.rowsEl.Get("style").Set("display", "")
	t.renderer = &domRenderer{t}
	t.scheduleRender(true)
}

// rowHTML builds a row's spans, splitting runs on attribute changes
// (the DomRendererRowFactory equivalent).
func (t *Terminal) rowHTML(l *vt.BufferLine, lineIdx, cursorCol int) string {
	var sb strings.Builder
	cols := t.Core.Cols()
	cell := vt.NewCellData()
	selected := t.sel.valid()

	var runText strings.Builder
	var runStyle, runClass string
	flush := func() {
		if runText.Len() == 0 {
			return
		}
		sb.WriteString("<span")
		if runClass != "" {
			sb.WriteString(` class="` + runClass + `"`)
		}
		if runStyle != "" {
			sb.WriteString(` style="` + runStyle + `"`)
		}
		sb.WriteString(">")
		sb.WriteString(escapeHTML(runText.String()))
		sb.WriteString("</span>")
		runText.Reset()
	}

	for x := 0; x < cols && x < l.Length; x++ {
		l.LoadCell(x, cell)
		if cell.GetWidth() == 0 {
			// trailing half of a wide char
			continue
		}
		chars := cell.GetChars()
		if chars == "" {
			chars = " "
		}
		style, class := t.cellStyle(&cell.AttributeData, x == cursorCol,
			selected && t.sel.contains(x, lineIdx))
		if style != runStyle || class != runClass {
			flush()
			runStyle, runClass = style, class
		}
		runText.WriteString(chars)
	}
	flush()
	return sb.String()
}

func (t *Terminal) cellStyle(attr *vt.AttributeData, isCursor, isSelected bool) (style, class string) {
	fg, bg := t.colors.ResolveCellColors(attr)
	var s strings.Builder
	var classes []string

	// The selection is drawn the same way here as in the WebGL renderer:
	// by recoloring the cell rather than by overlaying anything, so the two
	// renderers cannot disagree about what is selected.
	if isSelected {
		bg = t.colors.SelectionBgOpaque
		if t.colors.SelectionFg != "" {
			fg = t.colors.SelectionFg
		} else if fg == "" {
			fg = t.colors.Foreground
		}
	}

	if isCursor {
		switch t.cursorStyle() {
		case "underline":
			s.WriteString("border-bottom:2px solid " + t.colors.Cursor + ";")
		case "bar":
			s.WriteString("border-left:2px solid " + t.colors.Cursor + ";margin-left:-2px;")
		default: // block
			bg = t.colors.Cursor
			fg = t.colors.CursorAccent
		}
	}

	if fg != "" {
		s.WriteString("color:" + fg + ";")
	}
	if bg != "" {
		s.WriteString("background-color:" + bg + ";")
	}
	if attr.IsBold() {
		classes = append(classes, "xb")
	}
	if attr.IsItalic() {
		classes = append(classes, "xi")
	}
	if attr.IsDim() {
		classes = append(classes, "xd")
	}
	if attr.IsInvisible() {
		classes = append(classes, "xh")
	}
	var deco []string
	if attr.IsUnderline() {
		deco = append(deco, "underline")
		switch attr.GetUnderlineStyle() {
		case vt.UnderlineDouble:
			s.WriteString("text-decoration-style:double;")
		case vt.UnderlineCurly:
			s.WriteString("text-decoration-style:wavy;")
		case vt.UnderlineDotted:
			s.WriteString("text-decoration-style:dotted;")
		case vt.UnderlineDashed:
			s.WriteString("text-decoration-style:dashed;")
		}
		if uc := t.colors.UnderlineColor(attr); uc != "" {
			s.WriteString("text-decoration-color:" + uc + ";")
		}
	}
	if attr.IsStrikethrough() {
		deco = append(deco, "line-through")
	}
	if attr.IsOverline() {
		deco = append(deco, "overline")
	}
	if len(deco) > 0 {
		s.WriteString("text-decoration-line:" + strings.Join(deco, " ") + ";")
	}
	return s.String(), strings.Join(classes, " ")
}

// Fit resizes the terminal to fill its parent element (the fit addon
// equivalent).
func (t *Terminal) Fit() {
	if !t.opened {
		return
	}
	parent := t.element.Get("parentElement")
	if parent.IsNull() || parent.IsUndefined() {
		return
	}
	rect := parent.Call("getBoundingClientRect")
	scrollbarW := 16.0
	cols := int((rect.Get("width").Float() - scrollbarW) / t.cellW)
	rows := int(rect.Get("height").Float() / t.cellH)
	if cols < vt.MinimumCols {
		cols = vt.MinimumCols
	}
	if rows < vt.MinimumRows {
		rows = vt.MinimumRows
	}
	if cols != t.Core.Cols() || rows != t.Core.Rows() {
		t.Core.Resize(cols, rows)
	}
}

// AutoFit keeps the terminal fitted to its parent for as long as it is open,
// by observing the parent element rather than the window.
//
// Listening for the window's resize event is not enough, and the difference
// matters as soon as the terminal is not the whole page. A terminal inside a
// draggable window, a split pane, or a collapsing sidebar is resized by its
// container changing size while the window never does — no resize event is
// fired, Fit is never called, and the terminal keeps the dimensions it had when
// it was opened, overflowing or under-filling whatever now holds it.
//
// Calling it more than once is harmless. The observer is released by Dispose.
func (t *Terminal) AutoFit() {
	if !t.opened || t.resizeObserver.Truthy() {
		return
	}
	parent := t.element.Get("parentElement")
	if parent.IsNull() || parent.IsUndefined() {
		return
	}
	ctor := js.Global().Get("ResizeObserver")
	if !ctor.Truthy() {
		// Every browser with WebAssembly has had ResizeObserver for years,
		// but falling back to the window keeps this from being worse than
		// not calling it at all.
		js.Global().Call("addEventListener", "resize", t.fn(func(js.Value, []js.Value) any {
			t.scheduleFit()
			return nil
		}))
		return
	}
	t.resizeObserver = ctor.New(t.fn(func(js.Value, []js.Value) any {
		t.scheduleFit()
		return nil
	}))
	t.resizeObserver.Call("observe", parent)
	t.Fit()
}

// Attach connects the terminal to a WebSocket carrying pty data (the
// attach addon equivalent). bidirectional=false makes it read-only.
func (t *Terminal) Attach(ws js.Value, bidirectional bool) {
	ws.Set("binaryType", "arraybuffer")
	ws.Call("addEventListener", "message", t.fn(func(_ js.Value, args []js.Value) any {
		data := args[0].Get("data")
		if data.Type() == js.TypeString {
			t.WriteString(data.String())
		} else {
			u8 := js.Global().Get("Uint8Array").New(data)
			buf := make([]byte, u8.Get("length").Int())
			js.CopyBytesToGo(buf, u8)
			t.Write(buf)
		}
		return nil
	}))
	if bidirectional {
		send := func(data string) {
			if ws.Get("readyState").Int() == 1 { // OPEN
				ws.Call("send", data)
			}
		}
		t.Core.OnData = send
		t.Core.OnBinary = func(data string) {
			// binary is passed as latin-1 bytes
			buf := make([]byte, len(data))
			for i := 0; i < len(data); i++ {
				buf[i] = data[i]
			}
			u8 := js.Global().Get("Uint8Array").New(len(buf))
			js.CopyBytesToJS(u8, buf)
			if ws.Get("readyState").Int() == 1 {
				ws.Call("send", u8)
			}
		}
	}
}

// Dispose releases all registered JS callbacks and removes the DOM.
func (t *Terminal) Dispose() {
	// Stop the blink interval BEFORE releasing funcs — the global setInterval
	// keeps firing regardless of DOM removal, and firing a released js.Func throws
	// "call to released function". (Element event listeners die with removeChild;
	// this window-scoped timer does not.)
	if t.blinkTimer.Truthy() {
		window.Call("clearInterval", t.blinkTimer)
		t.blinkTimer = js.Undefined()
	}
	t.menu.hide()
	t.unwireSelection()
	if t.resizeObserver.Truthy() {
		t.resizeObserver.Call("disconnect")
		t.resizeObserver = js.Value{}
	}
	if t.opened && !t.element.IsUndefined() {
		parent := t.element.Get("parentElement")
		if !parent.IsNull() && !parent.IsUndefined() {
			parent.Call("removeChild", t.element)
		}
	}
	for _, f := range t.funcs {
		f.Release()
	}
	t.funcs = nil
	t.opened = false
}

var stylesheetInstalled = false

func ensureStylesheet() {
	if stylesheetInstalled {
		return
	}
	stylesheetInstalled = true
	css := `
.xterm { position: relative; user-select: none; padding: 2px; }
.xterm .xterm-helper-textarea {
  position: absolute; opacity: 0; left: -9999em; top: 0;
  width: 0; height: 0; z-index: -5; white-space: nowrap; overflow: hidden; resize: none;
}
.xterm .xterm-viewport { position: absolute; top: 0; right: 0; bottom: 0; left: 0; overflow-y: scroll; cursor: default; }
/* The viewport scrolls, so it carries the browser's default scrollbar: a light
   strip down the right-hand side of a terminal that is usually dark. Nothing
   downstream can reach it without knowing this class name, so it is styled
   here, where the element is created. */
.xterm .xterm-viewport { scrollbar-width: thin; scrollbar-color: #4a4f57 transparent; }
.xterm .xterm-viewport::-webkit-scrollbar { width: 10px; }
.xterm .xterm-viewport::-webkit-scrollbar-track { background: transparent; }
.xterm .xterm-viewport::-webkit-scrollbar-thumb { background: #4a4f57; border-radius: 5px; }
.xterm .xterm-viewport::-webkit-scrollbar-thumb:hover { background: #6b727c; }
.xterm .xterm-viewport::-webkit-scrollbar-corner { background: transparent; }
.xterm .xterm-screen { position: relative; z-index: 1; cursor: text; }
/* The terminal draws its own selection, as a range of buffer cells, so that
   selecting works the same under both renderers — the WebGL one hides these
   rows entirely and paints to a canvas, where there is no text for the browser
   to select. Leaving the browser's selection enabled as well would give two
   selections that disagree with each other. */
.xterm .xterm-rows { line-height: normal; letter-spacing: 0; white-space: pre; user-select: none; }
.xterm .xterm-rows > div { overflow: hidden; }
.xterm .xterm-rows span { display: inline-block; }
.xterm .xb { font-weight: bold; }
.xterm .xi { font-style: italic; }
.xterm .xd { opacity: 0.5; }
.xterm .xh { visibility: hidden; }
.xterm .xterm-composition-view {
  background: #000; color: #fff; border: 1px solid #555;
  display: none; position: absolute; white-space: nowrap; z-index: 5;
}
.xterm .xterm-composition-view.active { display: block; }
` + contextMenuCSS
	styleEl := document.Call("createElement", "style")
	styleEl.Set("textContent", css)
	document.Get("head").Call("appendChild", styleEl)
}

func jsPx(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64) + "px"
}

func escapeHTML(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}

// RefreshGlyphs rebuilds the renderer's glyph cache.
//
// Rasterised glyphs are cached, so a change to an option that affects how one
// is DRAWN rather than which one is drawn — Options.MirrorGlyph is the only one
// today — is not picked up by itself: the cache still holds the glyphs as they
// were rasterised before. Everything else that invalidates the cache does so as
// a side effect of resizing or of the colors changing, which is why this is the
// only place that needs saying out loud.
//
// A no-op on the DOM renderer, which rasterises nothing.
func (t *Terminal) RefreshGlyphs() {
	if r, ok := t.renderer.(*webglRenderer); ok {
		r.refreshCharAtlas()
		r.model.clear()
		r.glyphs.clear()
	}
}
