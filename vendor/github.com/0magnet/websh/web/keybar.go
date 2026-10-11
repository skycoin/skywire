//go:build js && wasm

package web

import (
	"strings"
	"syscall/js"

	"github.com/0magnet/xterm-go/vt"
)

// The key bar: on a phone or tablet the on-screen keyboard has no Escape,
// Tab, Ctrl, Alt or arrows, and a terminal program is not usable without
// them. A row of those keys sits under the terminal on a touch screen, as
// Termux's extra keys do. Ctrl and Alt are latched: tapped once they apply to
// the next key, from the bar or the on-screen keyboard (Ctrl then c is
// Ctrl+C); tapped twice they stay on until tapped again. The volume-down key
// latches Ctrl too, as in Termux, where a browser passes it on (most do not).
//
// Its keys go through the terminal's own keyboard handling, so they are what
// the program asked for: application cursor keys, the kitty keyboard
// protocol.

type keyBar struct {
	s       *Session
	box     js.Value // the terminal's box
	el      js.Value
	ctrl    latch
	alt     latch
	ctrlBtn js.Value
	altBtn  js.Value
	fns     []js.Func
}

type latch int

const (
	latchOff latch = iota
	latchOnce
	latchLocked
)

// barKeys are the bar's keys: a label, and the DOM key, code and keyCode
// the terminal is handed for it ("" key for the latches).
var barKeys = []struct {
	label, key, code string
	keyCode          int
}{
	{"Esc", "Escape", "Escape", 27},
	{"Tab", "Tab", "Tab", 9},
	{"Ctrl", "", "", 0},
	{"Alt", "", "", 0},
	{"←", "ArrowLeft", "ArrowLeft", 37},
	{"↓", "ArrowDown", "ArrowDown", 40},
	{"↑", "ArrowUp", "ArrowUp", 38},
	{"→", "ArrowRight", "ArrowRight", 39},
	{"Home", "Home", "Home", 36},
	{"End", "End", "End", 35},
	{"PgUp", "PageUp", "PageUp", 33},
	{"PgDn", "PageDown", "PageDown", 34},
	{"|", "|", "Backslash", 220},
	{"~", "~", "Backquote", 192},
	{"/", "/", "Slash", 191},
	{"-", "-", "Minus", 189},
}

// touchScreen reports whether the page is used by touch, coarsely: a
// phone or a tablet, where the bar is wanted.
func touchScreen() bool {
	m := js.Global().Call("matchMedia", "(pointer: coarse)")
	return m.Truthy() && m.Get("matches").Bool()
}

// wireKeyBar makes the bar under the terminal, in box's parent, and raises
// box's bottom edge so the terminal fits above it.
func (s *Session) wireKeyBar(box js.Value) {
	doc := js.Global().Get("document")
	b := &keyBar{s: s, box: box}
	b.el = doc.Call("createElement", "div")
	b.el.Set("className", "websh-keybar")
	b.el.Get("style").Set("cssText", "position:absolute;left:0;right:0;bottom:0;height:40px;display:flex;gap:4px;"+
		"padding:4px;box-sizing:border-box;overflow-x:auto;background:#1a1d23;border-top:1px solid #333;z-index:20;"+
		"touch-action:pan-x;-webkit-user-select:none;user-select:none")
	for _, k := range barKeys {
		btn := doc.Call("createElement", "button")
		btn.Set("textContent", k.label)
		btn.Get("style").Set("cssText", "flex:0 0 auto;min-width:44px;height:32px;padding:0 8px;border:1px solid #444;"+
			"border-radius:4px;background:#262a33;color:#ddd;font:14px monospace")
		k := k
		f := js.FuncOf(func(_ js.Value, args []js.Value) any {
			args[0].Call("preventDefault") // keep the focus, and so the on-screen keyboard, where it is
			b.press(k.label, k.key, k.code, k.keyCode)
			return nil
		})
		btn.Call("addEventListener", "pointerdown", f)
		b.fns = append(b.fns, f)
		switch k.label {
		case "Ctrl":
			b.ctrlBtn = btn
		case "Alt":
			b.altBtn = btn
		}
		b.el.Call("append", btn)
	}
	box.Get("parentElement").Call("append", b.el)
	box.Get("style").Set("bottom", "40px")

	// Volume-down latches Ctrl, as in Termux, where the browser passes it.
	vol := js.FuncOf(func(_ js.Value, args []js.Value) any {
		if args[0].Get("key").String() == "AudioVolumeDown" {
			args[0].Call("preventDefault")
			b.toggle(&b.ctrl)
		}
		return nil
	})
	js.Global().Call("addEventListener", "keydown", vol, true)
	b.fns = append(b.fns, vol)
	s.bar = b
}

// press acts on one of the bar's keys.
func (b *keyBar) press(label, key, code string, keyCode int) {
	switch label {
	case "Ctrl":
		b.toggle(&b.ctrl)
		return
	case "Alt":
		b.toggle(&b.alt)
		return
	}
	ctrl, alt := b.take()
	ta := b.box.Call("querySelector", "textarea")
	if !ta.Truthy() {
		return
	}
	init := map[string]any{"key": key, "code": code, "keyCode": keyCode, "which": keyCode,
		"ctrlKey": ctrl, "altKey": alt, "bubbles": true, "cancelable": true}
	ta.Call("dispatchEvent", js.Global().Get("KeyboardEvent").New("keydown", init))
	if len(key) == 1 && !ctrl && !alt { // a character the terminal leaves to keypress
		ta.Call("dispatchEvent", js.Global().Get("KeyboardEvent").New("keypress", init))
	}
}

// toggle steps a latch: off, once, locked, off.
func (b *keyBar) toggle(l *latch) {
	*l = (*l + 1) % 3
	b.show()
}

// take is the latches for the next key, letting go of a one-key latch.
func (b *keyBar) take() (ctrl, alt bool) {
	ctrl, alt = b.ctrl != latchOff, b.alt != latchOff
	if b.ctrl == latchOnce {
		b.ctrl = latchOff
	}
	if b.alt == latchOnce {
		b.alt = latchOff
	}
	b.show()
	return ctrl, alt
}

// show colors the latch keys by their state.
func (b *keyBar) show() {
	for _, x := range []struct {
		btn js.Value
		l   latch
	}{{b.ctrlBtn, b.ctrl}, {b.altBtn, b.alt}} {
		bg := map[latch]string{latchOff: "#262a33", latchOnce: "#2f5f9e", latchLocked: "#c47a1c"}[x.l]
		x.btn.Get("style").Set("background", bg)
	}
}

// apply turns what the on-screen keyboard typed into what a latched Ctrl or
// Alt makes of it: Ctrl+letter is its control character, Alt+key is ESC
// before it — or, for a program that asked for the kitty keyboard protocol,
// the key as that encodes it.
func (b *keyBar) apply(data string) string {
	if b == nil || (b.ctrl == latchOff && b.alt == latchOff) {
		return data
	}
	r := []rune(data)
	if len(r) != 1 {
		return data // a paste, or what a key of the bar already made
	}
	ctrl, alt := b.take()
	if flags := b.s.Term.Core.InputHandler().KittyFlags(); flags != 0 {
		ev := &vt.KeyboardEvent{Key: data, CtrlKey: ctrl, AltKey: alt}
		if seq, ok := vt.KittyKey(ev, flags, vt.KittyPress); ok {
			return seq
		}
	}
	out := data
	if ctrl {
		c := unicodeUpper(r[0])
		switch {
		case c >= '@' && c <= '_':
			out = string(c - '@')
		case c == ' ' || c == '2':
			out = "\x00"
		case c == '?':
			out = "\x7f"
		}
	}
	if alt {
		out = "\x1b" + out
	}
	return out
}

func unicodeUpper(r rune) rune {
	return []rune(strings.ToUpper(string(r)))[0]
}
