//go:build js && wasm

package xterm

import (
	"strings"
	"syscall/js"

	"github.com/0magnet/xterm-go/vt"
)

// Port of src/browser/input/CompositionHelper.ts — handles
// compositionstart/update/end so IME input (CJK, dead keys, speech
// input) shows the in-progress composition at the cursor and sends
// only the final composed string to the pty.

type compositionHelper struct {
	term *Terminal

	textarea        js.Value
	compositionView js.Value

	// whether input composition is currently happening
	isComposing bool
	// position within the textarea value of the current composition
	posStart, posEnd int
	// whether a composition is in the process of being sent; setting
	// this to false cancels any in-progress send
	isSendingComposition bool
	// data already sent due to a keydown event
	dataAlreadySent string
}

func newCompositionHelper(term *Terminal, textarea, compositionView js.Value) *compositionHelper {
	return &compositionHelper{
		term:            term,
		textarea:        textarea,
		compositionView: compositionView,
	}
}

func (c *compositionHelper) value() string {
	return c.textarea.Get("value").String()
}

// setTimeout schedules f on the JS event loop for the next tick (releasing
// the callback after one shot).
func setTimeout(f func()) {
	var cb js.Func
	cb = js.FuncOf(func(js.Value, []js.Value) any {
		f()
		cb.Release()
		return nil
	})
	window.Call("setTimeout", cb, 0)
}

// CompositionStart handles the compositionstart event, activating the
// composition view.
func (c *compositionHelper) CompositionStart() {
	c.isComposing = true
	c.posStart = len(utf16UnitsOf(c.value()))
	c.compositionView.Set("textContent", "")
	c.dataAlreadySent = ""
	c.compositionView.Get("classList").Call("add", "active")
}

// CompositionUpdate handles the compositionupdate event, updating the
// composition view.
func (c *compositionHelper) CompositionUpdate(data string) {
	c.compositionView.Set("textContent", data)
	c.UpdateCompositionElements(false)
	setTimeout(func() {
		c.posEnd = len(utf16UnitsOf(c.value()))
	})
}

// CompositionEnd handles the compositionend event, hiding the view and
// sending the composition to the pty.
func (c *compositionHelper) CompositionEnd() {
	c.finalizeComposition(true)
}

// Keydown routes a keydown event through the composition logic.
// Returns whether the terminal should continue processing it.
func (c *compositionHelper) Keydown(keyCode int) bool {
	if c.isComposing || c.isSendingComposition {
		if keyCode == 20 || keyCode == 229 {
			// 20 is CapsLock, 229 is the "composition character"
			return false
		}
		if keyCode == 16 || keyCode == 17 || keyCode == 18 {
			// continue composing on modifier keys
			return false
		}
		// finish composition immediately, mainly for the case where
		// enter is pressed and the data must be sent before the
		// command executes
		c.finalizeComposition(false)
	}

	if keyCode == 229 {
		// a non-composition character (numbers, punctuation) was
		// pressed while the IME is active
		c.handleAnyTextareaChanges()
		return false
	}

	return true
}

func (c *compositionHelper) finalizeComposition(waitForPropagation bool) {
	c.compositionView.Get("classList").Call("remove", "active")
	c.isComposing = false

	if !waitForPropagation {
		// cancel any delayed sends and send the input immediately
		c.isSendingComposition = false
		input := utf16Slice(c.value(), c.posStart, c.posEnd)
		c.term.Core.Input(input, true)
		return
	}

	// deep copy: a new compositionstart may fire before the timeout
	curStart, curEnd := c.posStart, c.posEnd
	_ = curEnd

	// composition* events happen before the textarea changes on most
	// browsers; a 0ms timeout lets the native compositionend complete
	// so the correct character is retrieved
	c.isSendingComposition = true
	setTimeout(func() {
		if !c.isSendingComposition {
			return
		}
		c.isSendingComposition = false
		var input string
		// account for data already sent by keydown so characters are
		// not duplicated (xterm.js #3191)
		curStart += len(utf16UnitsOf(c.dataAlreadySent))
		if c.isComposing {
			// a new composition started: read up to its start
			input = utf16Slice(c.value(), curStart, c.posStart)
		} else {
			// no end position: pick up characters typed right after
			// the composition finished
			input = utf16Slice(c.value(), curStart, -1)
		}
		if len(input) > 0 {
			c.term.Core.Input(input, true)
		}
	})
}

// handleAnyTextareaChanges applies textarea changes after the current
// event chain completes — lets non-composition text through while an
// IME is active.
func (c *compositionHelper) handleAnyTextareaChanges() {
	oldValue := c.value()
	setTimeout(func() {
		// ignore if a composition has started since the timeout
		if c.isComposing {
			return
		}
		newValue := c.value()
		diff := strings.Replace(newValue, oldValue, "", 1)
		c.dataAlreadySent = diff
		if len(newValue) > len(oldValue) {
			c.term.Core.Input(diff, true)
		} else if len(newValue) < len(oldValue) {
			c.term.Core.Input("\x7f", true) // DEL
		} else if newValue != oldValue {
			c.term.Core.Input(newValue, true)
		}
	})
}

// UpdateCompositionElements positions the composition view on top of
// the cursor and the textarea just below it so the IME helper dialog
// appears in the right place.
func (c *compositionHelper) UpdateCompositionElements(dontRecurse bool) {
	if !c.isComposing {
		return
	}

	b := c.term.Core.Buffer()
	if b.IsCursorInViewport() {
		cursorX := b.X
		if max := c.term.Core.Cols() - 1; cursorX > max {
			cursorX = max
		}
		cellHeight := c.term.cellH
		cursorTop := float64(b.Y) * cellHeight
		cursorLeft := float64(cursorX) * c.term.cellW

		vs := c.compositionView.Get("style")
		vs.Set("left", jsPx(cursorLeft))
		vs.Set("top", jsPx(cursorTop))
		vs.Set("height", jsPx(cellHeight))
		vs.Set("lineHeight", jsPx(cellHeight))
		vs.Set("fontFamily", c.term.Core.Options.FontFamily)
		vs.Set("fontSize", jsPx(c.term.Core.Options.FontSize))

		// sync the textarea to the composition view position so the
		// IME knows where the text is
		bounds := c.compositionView.Call("getBoundingClientRect")
		ts := c.textarea.Get("style")
		ts.Set("left", jsPx(cursorLeft))
		ts.Set("top", jsPx(cursorTop))
		// at least 1x1 or certain IMEs may break
		w := bounds.Get("width").Float()
		if w < 1 {
			w = 1
		}
		h := bounds.Get("height").Float()
		if h < 1 {
			h = 1
		}
		ts.Set("width", jsPx(w))
		ts.Set("height", jsPx(h))
		ts.Set("lineHeight", jsPx(h))
	}

	if !dontRecurse {
		setTimeout(func() { c.UpdateCompositionElements(true) })
	}
}

// utf16UnitsOf mirrors JS string.length semantics.
func utf16UnitsOf(s string) []uint16 {
	return vt.Utf16Units(s)
}

// utf16Slice mirrors JS substring on UTF-16 indices (end < 0 = to the
// end of the string).
func utf16Slice(s string, start, end int) string {
	units := vt.Utf16Units(s)
	if start < 0 {
		start = 0
	}
	if end < 0 || end > len(units) {
		end = len(units)
	}
	if start > len(units) {
		start = len(units)
	}
	if start > end {
		start, end = end, start
	}
	return vt.Utf16ToString(units[start:end])
}
