//go:build js && wasm

package xterm

import (
	"syscall/js"
)

// The right-click menu.
//
// A browser's own Copy acts on the document's selection, and the terminal's is
// not one: it is a range of buffer cells that the renderer draws, with no DOM
// text behind it for the browser to have selected. So the browser offers no
// Copy over a terminal, however much is highlighted — the keyboard shortcut
// worked and the obvious gesture did not.
//
// The alternative to a menu of our own was to mirror the selection into the
// hidden textarea and slide it under the pointer, so that the browser's menu
// would be operating on a real editable field. That works until it does not:
// the textarea has to cover the click, which means covering the terminal, and
// there is no event for the menu closing again to uncover it.
//
// So the terminal draws its own, which is what every terminal emulator does
// anyway. It also gets to offer Paste, which a browser will not do for a
// non-editable element at all.

// contextMenu is the popup and what it is currently offering.
type contextMenu struct {
	t *Terminal

	el      js.Value // the menu, parented to the body
	onClose js.Func  // dismisses it: a click anywhere, a scroll, a key
	open    bool
}

// dismissCapturing are the events that close the menu wherever they happen.
var dismissCapturing = []string{"mousedown", "wheel", "keydown", "resize"}

// contextMenuCSS styles the menu against the terminal's own theme rather than
// the page's, so it belongs to the terminal it pops out of.
const contextMenuCSS = `
.xterm-menu {
  position: fixed; z-index: 2147483000; min-width: 148px; padding: 4px;
  border: 1px solid #3a4150; border-radius: 6px; background: #1b1f27;
  box-shadow: 0 6px 24px rgba(0,0,0,.45); user-select: none;
  font: 13px/1.4 ui-monospace, SFMono-Regular, Menlo, monospace;
}
.xterm-menu button {
  display: flex; justify-content: space-between; gap: 18px; width: 100%;
  padding: 5px 9px; border: 0; border-radius: 4px; background: transparent;
  color: #d3d7cf; font: inherit; text-align: left; cursor: default;
}
.xterm-menu button:hover:not(:disabled) { background: #2c333f; }
.xterm-menu button:disabled { color: #5b626e; }
.xterm-menu button span { color: #6b7280; }
.xterm-menu hr { margin: 4px 2px; border: 0; border-top: 1px solid #2c333f; }
`

// wireContextMenu attaches the right-click handler.
func (t *Terminal) wireContextMenu() {
	t.menu = &contextMenu{t: t}
	t.element.Call("addEventListener", "contextmenu", t.fn(func(_ js.Value, args []js.Value) any {
		if t.NoContextMenu {
			return nil
		}
		ev := args[0]
		// An application that asked for mouse reporting may well want the
		// right button; shift takes it back, as it does for selecting.
		if t.Core.MouseService().AreMouseEventsActive() && !ev.Get("shiftKey").Bool() {
			return nil
		}
		ev.Call("preventDefault")
		t.menu.show(ev.Get("clientX").Float(), ev.Get("clientY").Float())
		return nil
	}))

	// If a copy does reach us by some other route, it should carry the
	// terminal's selection rather than whatever the page thinks is selected.
	t.element.Call("addEventListener", "copy", t.fn(func(_ js.Value, args []js.Value) any {
		if !t.HasSelection() {
			return nil
		}
		ev := args[0]
		if cd := ev.Get("clipboardData"); cd.Truthy() {
			cd.Call("setData", "text/plain", t.Selection())
			ev.Call("preventDefault")
		}
		return nil
	}))
}

// show pops the menu up at a point in the viewport, nudged back inside it if it
// would hang off the right or the bottom.
func (m *contextMenu) show(x, y float64) {
	m.hide()
	ensureStylesheet()

	m.el = document.Call("createElement", "div")
	m.el.Set("className", "xterm-menu")

	has := m.t.HasSelection()
	m.item("Copy", "Ctrl+Shift+C", has, func() { m.t.CopySelection() })
	m.item("Paste", "Ctrl+Shift+V", true, func() { m.t.PasteFromClipboard() })
	m.el.Call("appendChild", document.Call("createElement", "hr"))
	m.item("Select All", "Ctrl+Shift+A", true, func() { m.t.SelectAll() })

	document.Get("body").Call("appendChild", m.el)

	// Measured after it is in the document, because until then it has no size.
	rect := m.el.Call("getBoundingClientRect")
	w, h := rect.Get("width").Float(), rect.Get("height").Float()
	vw := window.Get("innerWidth").Float()
	vh := window.Get("innerHeight").Float()
	if x+w > vw-4 {
		x = max(vw-w-4, 4)
	}
	if y+h > vh-4 {
		y = max(vh-h-4, 4)
	}
	m.el.Get("style").Set("left", jsPx(x))
	m.el.Get("style").Set("top", jsPx(y))

	// Anything else dismisses it, which is what a menu should do. The listeners
	// capture so that dismissing does not also press whatever is underneath.
	m.onClose = js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) > 0 && args[0].Truthy() {
			if target := args[0].Get("target"); target.Truthy() &&
				m.el.Truthy() && m.el.Call("contains", target).Bool() {
				return nil // a click on the menu is the menu's own business
			}
		}
		m.hide()
		return nil
	})
	for _, e := range dismissCapturing {
		window.Call("addEventListener", e, m.onClose, true)
	}
	// The page losing focus should close the menu; the textarea losing it to a
	// menu button should not, and blur does not bubble — so this one is not
	// capturing, or pressing an item would dismiss the menu before the press
	// became a click and nothing would ever be chosen.
	window.Call("addEventListener", "blur", m.onClose)
	m.open = true
}

// item adds a row. A disabled row is shown rather than hidden, so the menu does
// not change shape depending on whether anything is selected.
func (m *contextMenu) item(label, accel string, enabled bool, do func()) {
	b := document.Call("createElement", "button")
	b.Set("type", "button")
	b.Set("disabled", !enabled)

	name := document.Call("createElement", "span")
	name.Set("textContent", label)
	name.Get("style").Set("color", "inherit")
	key := document.Call("createElement", "span")
	key.Set("textContent", accel)
	b.Call("appendChild", name)
	b.Call("appendChild", key)

	if enabled {
		// Pressing a button would otherwise focus it, taking focus off the
		// terminal for the moment it takes to choose something.
		var down js.Func
		down = js.FuncOf(func(_ js.Value, args []js.Value) any {
			if len(args) > 0 {
				args[0].Call("preventDefault")
			}
			down.Release()
			return nil
		})
		b.Call("addEventListener", "mousedown", down)

		var fn js.Func
		fn = js.FuncOf(func(_ js.Value, args []js.Value) any {
			if len(args) > 0 {
				args[0].Call("preventDefault")
				args[0].Call("stopPropagation")
			}
			m.hide()
			do()
			fn.Release()
			return nil
		})
		b.Call("addEventListener", "click", fn)
	}
	m.el.Call("appendChild", b)
}

// hide takes the menu down and gives back everything it was holding.
func (m *contextMenu) hide() {
	if m == nil || !m.open {
		return
	}
	m.open = false
	for _, e := range dismissCapturing {
		window.Call("removeEventListener", e, m.onClose, true)
	}
	window.Call("removeEventListener", "blur", m.onClose)
	m.onClose.Release()
	if m.el.Truthy() {
		m.el.Call("remove")
		m.el = js.Value{}
	}
	m.t.Focus()
}

// PasteFromClipboard reads the system clipboard and sends it to the terminal.
//
// Ctrl+Shift+V and the browser's own paste arrive as a paste event with the
// data attached, which needs no permission. Asking for the clipboard rather
// than being handed it does, so this may prompt, and does nothing if refused.
func (t *Terminal) PasteFromClipboard() {
	clipboard := window.Get("navigator").Get("clipboard")
	if !clipboard.Truthy() || clipboard.Get("readText").Type() != js.TypeFunction {
		return
	}
	promise := clipboard.Call("readText")
	if !promise.Truthy() || promise.Get("then").Type() != js.TypeFunction {
		return
	}
	var ok, failed js.Func
	release := func() {
		ok.Release()
		failed.Release()
	}
	ok = js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) > 0 && args[0].Type() == js.TypeString {
			t.Paste(args[0].String())
		}
		release()
		return nil
	})
	failed = js.FuncOf(func(js.Value, []js.Value) any {
		release()
		return nil
	})
	promise.Call("then", ok, failed)
}
