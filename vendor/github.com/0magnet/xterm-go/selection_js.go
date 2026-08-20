//go:build js && wasm

package xterm

import (
	"syscall/js"

	"github.com/0magnet/xterm-go/vt"
)

// The browser side of the selection: turning pointer events into a range,
// keeping a drag alive past the edge of the terminal, and getting the result
// onto the system clipboard.

// selectionAutoScrollMS is how often a drag held past the top or bottom edge
// scrolls by one line. Slow enough to aim with, fast enough to get somewhere.
const selectionAutoScrollMS = 50

// selectionInput wires the pointer to the selection.
type selectionInput struct {
	t *Terminal

	// A drag continues while the button is held, wherever the pointer goes, so
	// move and up are watched on the document rather than on the terminal.
	moveFn, upFn js.Func
	wired        bool

	scrollTimer js.Value
	scrollBy    int
}

// mouseDown starts, extends or ignores a selection, and reports whether it took
// the event.
//
// An application that has asked for mouse reporting gets the mouse, because it
// asked; holding shift takes it back, which is the convention every terminal
// emulator uses for exactly this conflict.
func (t *Terminal) selectionMouseDown(ev js.Value) bool {
	if ev.Get("button").Int() != 0 {
		return false
	}
	if t.Core.MouseService().AreMouseEventsActive() && !ev.Get("shiftKey").Bool() {
		return false
	}

	p := t.selectionPos(ev)

	// Shift-clicking extends the selection rather than starting a new one.
	if ev.Get("shiftKey").Bool() && t.sel.valid() {
		t.sel.active = true
		t.sel.extend(p)
	} else {
		// The browser counts the clicks for us: detail is 1, 2, 3 and then on
		// past 3, so a fourth click starts over at a character.
		mode := selectChar
		switch (ev.Get("detail").Int() - 1) % 3 {
		case 1:
			mode = selectWord
		case 2:
			mode = selectLine
		}
		t.sel.begin(p, mode)
	}

	// Without this the browser starts its own drag — of the page's selection,
	// or of an image — over the top of ours.
	ev.Call("preventDefault")
	t.Focus()
	t.scheduleRender(false)
	return true
}

func (t *Terminal) selectionMouseMove(ev js.Value) {
	if !t.sel.active {
		return
	}
	t.sel.extend(t.selectionPos(ev))
	t.selectionAutoScroll(ev)
	t.scheduleRender(false)
}

func (t *Terminal) selectionMouseUp() {
	if !t.sel.active {
		return
	}
	t.selectionStopAutoScroll()
	t.sel.finish()
	if t.CopyOnSelect && t.sel.valid() {
		t.CopySelection()
	}
	t.scheduleRender(false)
}

// selectionPos is the cell under the pointer, in buffer coordinates.
//
// The row is clamped into the viewport rather than dropped, so a drag that has
// left the terminal still selects up to the edge it left by; the column is
// clamped the same way, which is what makes dragging off the right-hand side
// select to the end of the row.
func (t *Terminal) selectionPos(ev js.Value) pos {
	col, row, _, _ := t.mouseCell(ev)
	col = clampInt(col, t.Core.Cols()-1)
	row = clampInt(row, t.Core.Rows()-1)
	b := t.Core.Buffer()
	return pos{col: col, line: clampInt(b.YDisp+row, b.Lines.Length()-1)}
}

// selectionAutoScroll keeps a drag going once it reaches the top or bottom of
// the terminal, which is the only way to select more than a screenful.
func (t *Terminal) selectionAutoScroll(ev js.Value) {
	rect := t.screen.Call("getBoundingClientRect")
	y := ev.Get("clientY").Float()
	switch {
	case y < rect.Get("top").Float():
		t.startAutoScroll(-1)
	case y > rect.Get("bottom").Float():
		t.startAutoScroll(1)
	default:
		t.selectionStopAutoScroll()
	}
}

func (t *Terminal) startAutoScroll(by int) {
	if t.selInput.scrollBy == by && t.selInput.scrollTimer.Truthy() {
		return
	}
	t.selectionStopAutoScroll()
	t.selInput.scrollBy = by
	tick := t.fn(func(js.Value, []js.Value) any {
		if !t.sel.active {
			t.selectionStopAutoScroll()
			return nil
		}
		before := t.Core.Buffer().YDisp
		t.Core.ScrollLines(by)
		b := t.Core.Buffer()
		if b.YDisp == before {
			return nil // already at the end of the scrollback
		}
		// The pointer has not moved; the text under it has. Following the edge
		// it is held against is what makes the selection grow.
		row := 0
		if by > 0 {
			row = t.Core.Rows() - 1
		}
		t.sel.extend(pos{col: t.sel.focus.col, line: clampInt(b.YDisp+row, b.Lines.Length()-1)})
		t.scheduleRender(false)
		return nil
	})
	t.selInput.scrollTimer = window.Call("setInterval", tick, selectionAutoScrollMS)
}

func (t *Terminal) selectionStopAutoScroll() {
	if t.selInput.scrollTimer.Truthy() {
		window.Call("clearInterval", t.selInput.scrollTimer)
		t.selInput.scrollTimer = js.Value{}
	}
	t.selInput.scrollBy = 0
}

// wireSelection attaches the pointer handlers. Move and up go on the document
// so that a drag survives leaving the terminal; they are removed by Dispose,
// which is why they are kept rather than passed to fn and forgotten.
func (t *Terminal) wireSelection() {
	if t.selInput.wired {
		return
	}
	t.selInput.wired = true
	t.selInput.moveFn = js.FuncOf(func(_ js.Value, args []js.Value) any {
		t.selectionMouseMove(args[0])
		return nil
	})
	t.selInput.upFn = js.FuncOf(func(js.Value, []js.Value) any {
		t.selectionMouseUp()
		return nil
	})
	document.Call("addEventListener", "mousemove", t.selInput.moveFn)
	document.Call("addEventListener", "mouseup", t.selInput.upFn)
}

func (t *Terminal) unwireSelection() {
	if !t.selInput.wired {
		return
	}
	t.selInput.wired = false
	t.selectionStopAutoScroll()
	document.Call("removeEventListener", "mousemove", t.selInput.moveFn)
	document.Call("removeEventListener", "mouseup", t.selInput.upFn)
	t.selInput.moveFn.Release()
	t.selInput.upFn.Release()
}

// selectionKeydown handles the keys that act on a selection, and reports
// whether it took the event.
//
// Ctrl+Shift+C rather than Ctrl+C, because Ctrl+C has to keep interrupting the
// program: that is the whole reason terminals moved copy onto the shifted key.
// On a Mac the command key is free, so Cmd+C means copy there and nothing has
// to be given up.
func (t *Terminal) selectionKeydown(ev js.Value) bool {
	key := ev.Get("key").String()
	if len(key) != 1 {
		return false
	}
	lower := key
	if key[0] >= 'A' && key[0] <= 'Z' {
		lower = string(rune(key[0] - 'A' + 'a'))
	}
	ctrlShift := ev.Get("ctrlKey").Bool() && ev.Get("shiftKey").Bool()
	meta := ev.Get("metaKey").Bool() && !ev.Get("ctrlKey").Bool()

	switch lower {
	case "c":
		if (ctrlShift || meta) && t.HasSelection() {
			t.CopySelection()
			ev.Call("preventDefault")
			ev.Call("stopPropagation")
			return true
		}
	case "a":
		if ctrlShift || meta {
			t.SelectAll()
			t.scheduleRender(false)
			ev.Call("preventDefault")
			ev.Call("stopPropagation")
			return true
		}
	}
	return false
}

// HasSelection reports whether any text is selected.
func (t *Terminal) HasSelection() bool { return t.sel.valid() }

// Selection is the selected text, or "" if there is none.
func (t *Terminal) Selection() string {
	if !t.sel.valid() {
		return ""
	}
	return t.sel.text()
}

// ClearSelection deselects.
func (t *Terminal) ClearSelection() {
	t.sel.drop()
	t.scheduleRender(false)
}

// SelectAll selects the whole buffer, scrollback included.
func (t *Terminal) SelectAll() {
	t.sel.selectAll()
	t.scheduleRender(false)
}

// CopySelection puts the selected text on the system clipboard.
func (t *Terminal) CopySelection() {
	text := t.Selection()
	if text == "" {
		return
	}
	t.writeClipboard(text)
}

// writeClipboard prefers the asynchronous clipboard API and falls back to the
// old hidden-textarea trick, which needs no permission and works in the
// insecure contexts where navigator.clipboard is simply absent.
func (t *Terminal) writeClipboard(text string) {
	clipboard := window.Get("navigator").Get("clipboard")
	if !clipboard.Truthy() || clipboard.Get("writeText").Type() != js.TypeFunction {
		t.copyViaTextarea(text)
		return
	}
	promise := clipboard.Call("writeText", text)
	if !promise.Truthy() || promise.Get("then").Type() != js.TypeFunction {
		return
	}
	// A rejected promise nobody is listening to is an unhandled rejection in
	// the console, and the rejection is the interesting case: it means the page
	// lacks permission and the fallback is the only way through.
	var ok, failed js.Func
	release := func() {
		ok.Release()
		failed.Release()
	}
	ok = js.FuncOf(func(js.Value, []js.Value) any {
		release()
		return nil
	})
	failed = js.FuncOf(func(js.Value, []js.Value) any {
		t.copyViaTextarea(text)
		release()
		return nil
	})
	promise.Call("then", ok, failed)
}

func (t *Terminal) copyViaTextarea(text string) {
	ta := document.Call("createElement", "textarea")
	ta.Set("value", text)
	style := ta.Get("style")
	style.Set("position", "fixed")
	style.Set("top", "0")
	style.Set("left", "-9999px")
	style.Set("opacity", "0")
	document.Get("body").Call("appendChild", ta)
	ta.Call("focus")
	ta.Call("select")
	document.Call("execCommand", "copy")
	ta.Call("remove")
	// execCommand needed the focus; the terminal needs it back.
	t.Focus()
}

// markSelectionDirty redraws the viewport rows a range covers. A selection in
// the scrollback that is not on screen costs nothing.
func (t *Terminal) markSelectionDirty(start, end pos) {
	b := t.Core.Buffer()
	first := start.line - b.YDisp
	last := end.line - b.YDisp
	if last < 0 || first > t.Core.Rows()-1 {
		return
	}
	t.markDirty(clampInt(first, t.Core.Rows()-1), clampInt(last, t.Core.Rows()-1))
}

func (t *Terminal) notifySelection() {
	if t.OnSelectionChange != nil {
		t.OnSelectionChange()
	}
}

// selectionColors substitutes the colors of a selected cell. The background is
// the theme's selection color already flattened onto the terminal background;
// the foreground is left alone unless the theme names one, which keeps colored
// output readable through a selection.
func (t *Terminal) selectionColors(fg, bg uint32) (uint32, uint32) {
	// Only the color mode and the color itself are replaced; bold, italic and
	// the link to the extended attributes are the cell's own and survive being
	// selected.
	setRGB := func(attr, rgb uint32) uint32 {
		return (attr &^ (vt.AttrCMMask | vt.AttrRGBMask)) | vt.AttrCMRGB | (rgb & vt.AttrRGBMask)
	}
	bg = setRGB(bg, cssToRGB(t.colors.SelectionBgOpaque))
	if t.colors.SelectionFg != "" {
		fg = setRGB(fg, cssToRGB(t.colors.SelectionFg))
	}
	// Inverse video would swap the two back again, and the cell is already
	// being given the colors it should end up with.
	fg &^= vt.FgInverse
	return fg, bg
}
