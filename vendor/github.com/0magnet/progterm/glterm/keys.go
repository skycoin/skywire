package glterm

import "github.com/0magnet/xterm-go/vt"

// special are the named keys as the browser names them: the key code and key
// name vt's encoder takes (it is xterm.js's).
var special = map[Key]struct {
	code int
	name string
}{
	KeyEnter: {13, "Enter"}, KeyEscape: {27, "Escape"}, KeyBackspace: {8, "Backspace"}, KeyTab: {9, "Tab"},
	KeyInsert: {45, "Insert"}, KeyDelete: {46, "Delete"},
	KeyHome: {36, "Home"}, KeyEnd: {35, "End"}, KeyPageUp: {33, "PageUp"}, KeyPageDown: {34, "PageDown"},
	KeyLeft: {37, "ArrowLeft"}, KeyUp: {38, "ArrowUp"}, KeyRight: {39, "ArrowRight"}, KeyDown: {40, "ArrowDown"},
	KeyF1: {112, "F1"}, KeyF2: {113, "F2"}, KeyF3: {114, "F3"}, KeyF4: {115, "F4"},
	KeyF5: {116, "F5"}, KeyF6: {117, "F6"}, KeyF7: {118, "F7"}, KeyF8: {119, "F8"},
	KeyF9: {120, "F9"}, KeyF10: {121, "F10"}, KeyF11: {122, "F11"}, KeyF12: {123, "F12"},
}

// Encode is what a key press sends to the program, or "" for a key the
// terminal has no sequence for. appCursor is whether the program asked for
// application cursor keys (DECCKM).
func Encode(e KeyEvent, appCursor bool) string {
	ctrl, alt, shift := e.Mods&Ctrl != 0, e.Mods&Alt != 0, e.Mods&Shift != 0
	if s, ok := special[e.Key]; ok {
		r := vt.EvaluateKeyboardEvent(&vt.KeyboardEvent{
			KeyCode: s.code, Key: s.name, CtrlKey: ctrl, AltKey: alt, ShiftKey: shift,
		}, appCursor, false, false)
		return r.Key
	}
	if e.Rune <= 0 {
		return ""
	}
	r := e.Rune
	if ctrl {
		c, ok := control(r)
		if !ok {
			return ""
		}
		r = c
	}
	if alt {
		return "\x1b" + string(r) // Alt is Meta: ESC before the key
	}
	return string(r)
}

// control is Ctrl with r, as a terminal sends it.
func control(r rune) (rune, bool) {
	switch {
	case r >= 'a' && r <= 'z':
		return r - 'a' + 1, true
	case r >= '@' && r <= '_': // @ A-Z [ \ ] ^ _
		return r - '@', true
	case r == ' ' || r == '2':
		return 0, true
	case r == '3':
		return 0x1b, true
	case r == '4':
		return 0x1c, true
	case r == '5':
		return 0x1d, true
	case r == '6':
		return 0x1e, true
	case r == '7' || r == '-' || r == '/':
		return 0x1f, true
	case r == '8' || r == '?':
		return 0x7f, true
	}
	return 0, false
}
