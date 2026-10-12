package vt

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// The kitty keyboard protocol
// (https://sw.kovidgoyal.net/kitty/keyboard-protocol/): a program asks for
// keys the legacy encodings cannot tell apart — Escape alone, Ctrl+I from
// Tab, Shift+Enter, a key's release — by pushing enhancement flags, and the
// terminal reports keys as CSI ... u accordingly. Each screen (main,
// alternate) keeps its own stack of flags.

// Kitty keyboard enhancement flags.
const (
	KittyDisambiguate   = 1  // Escape and modified keys as CSI u
	KittyEventTypes     = 2  // repeat and release as well as press
	KittyAlternateKeys  = 4  // the shifted key beside the base key
	KittyAllKeysAsCodes = 8  // every key, text too, as CSI u
	KittyAssociatedText = 16 // the text a key types, with it
	kittyAllFlags       = 31
)

// Kitty key event types.
const (
	KittyPress   = 1
	KittyRepeat  = 2
	KittyRelease = 3
)

// kittyStackLimit is how deep a screen's stack goes, as kitty's does.
const kittyStackLimit = 256

// KittyFlags are the enhancement flags in effect on the active screen.
func (h *InputHandler) KittyFlags() int {
	s := h.kittyStack()
	if len(*s) == 0 {
		return 0
	}
	return (*s)[len(*s)-1]
}

// ResetKittyKeyboard drops every screen's flags: an embedder's shell does
// it when a program ends, so one that crashed with flags pushed does not
// leave the keys encoded for it.
func (h *InputHandler) ResetKittyKeyboard() {
	h.kittyMain, h.kittyAlt = nil, nil
}

func (h *InputHandler) kittyStack() *[]int {
	if h.bufferService.Buffers.Active() == h.bufferService.Buffers.Alt() {
		return &h.kittyAlt
	}
	return &h.kittyMain
}

// kittyQuery is CSI ? u: the flags in effect.
func (h *InputHandler) kittyQuery(*Params) bool {
	h.coreService.TriggerDataEvent("\x1b[?"+strconv.Itoa(h.KittyFlags())+"u", false)
	return true
}

// kittyPush is CSI > flags u.
func (h *InputHandler) kittyPush(params *Params) bool {
	s := h.kittyStack()
	if len(*s) >= kittyStackLimit {
		*s = (*s)[1:]
	}
	*s = append(*s, paramAt(params, 0)&kittyAllFlags)
	return true
}

// kittyPop is CSI < n u: pops n (1 by default).
func (h *InputHandler) kittyPop(params *Params) bool {
	s := h.kittyStack()
	n := max(1, paramAt(params, 0))
	if n >= len(*s) {
		*s = nil
	} else {
		*s = (*s)[:len(*s)-n]
	}
	return true
}

// kittySet is CSI = flags ; mode u: mode 1 sets the flags, 2 adds them,
// 3 takes them away.
func (h *InputHandler) kittySet(params *Params) bool {
	s := h.kittyStack()
	flags := paramAt(params, 0) & kittyAllFlags
	mode := 1
	if params.Length > 1 {
		mode = paramAt(params, 1)
	}
	cur := h.KittyFlags()
	switch mode {
	case 1:
		cur = flags
	case 2:
		cur |= flags
	case 3:
		cur &^= flags
	default:
		return true
	}
	if len(*s) == 0 {
		*s = append(*s, cur)
	} else {
		(*s)[len(*s)-1] = cur
	}
	return true
}

// kittyFunctional maps DOM key names to kitty's encoding: a number with a
// final (~ or u), or a legacy letter final with number 1.
var kittyFunctional = map[string]struct {
	num   int
	final byte
}{
	"Escape": {27, 'u'}, "Enter": {13, 'u'}, "Tab": {9, 'u'}, "Backspace": {127, 'u'},
	"Insert": {2, '~'}, "Delete": {3, '~'}, "PageUp": {5, '~'}, "PageDown": {6, '~'},
	"ArrowUp": {1, 'A'}, "ArrowDown": {1, 'B'}, "ArrowRight": {1, 'C'}, "ArrowLeft": {1, 'D'},
	"Home": {1, 'H'}, "End": {1, 'F'},
	"F1": {1, 'P'}, "F2": {1, 'Q'}, "F3": {13, '~'}, "F4": {1, 'S'},
	"F5": {15, '~'}, "F6": {17, '~'}, "F7": {18, '~'}, "F8": {19, '~'},
	"F9": {20, '~'}, "F10": {21, '~'}, "F11": {23, '~'}, "F12": {24, '~'},
}

// kittyModifierKeys are the modifier keys themselves, reported only when
// every key is (KittyAllKeysAsCodes), by DOM code.
var kittyModifierKeys = map[string]int{
	"ShiftLeft": 57441, "ControlLeft": 57442, "AltLeft": 57443, "MetaLeft": 57444,
	"ShiftRight": 57447, "ControlRight": 57448, "AltRight": 57449, "MetaRight": 57450,
	"CapsLock": 57358, "NumLock": 57360,
}

// KittyKey encodes a key event under flags, for an event of the given type.
// ok is false where the protocol leaves the key to its legacy encoding (a
// press) or has nothing to send (a release or repeat it does not report).
func KittyKey(ev *KeyboardEvent, flags, event int) (seq string, ok bool) {
	if flags == 0 {
		return "", false
	}
	all := flags&KittyAllKeysAsCodes != 0
	if event != KittyPress && flags&KittyEventTypes == 0 {
		if event == KittyRelease {
			return "", false
		}
		event = KittyPress // a repeat, reported as another press
	}
	mods := 0
	if ev.ShiftKey {
		mods |= 1
	}
	if ev.AltKey {
		mods |= 2
	}
	if ev.CtrlKey {
		mods |= 4
	}
	if ev.MetaKey {
		mods |= 8
	}

	// The modifier keys themselves.
	if code, isMod := kittyModifierKeys[ev.Code]; isMod {
		if !all {
			return "", false
		}
		return kittyCSI(strconv.Itoa(code), mods, event, "", 'u'), true
	}

	if f, isFn := kittyFunctional[ev.Key]; isFn {
		switch ev.Key {
		case "Enter", "Tab", "Backspace":
			// Unmodified, these stay legacy unless every key is a code, so a
			// shell after a program that crashed still runs `reset`.
			if !all && mods == 0 {
				return "", false
			}
		case "Escape":
		default:
			// The other function keys keep their legacy forms (with
			// modifiers as xterm sends them) unless a release or repeat must
			// be told, or every key is a code.
			if !all && (event == KittyPress || flags&KittyEventTypes == 0) {
				return "", false
			}
		}
		num := strconv.Itoa(f.num)
		if f.final != 'u' && f.final != '~' && mods == 0 && event == KittyPress {
			num = ""
		}
		return kittyCSI(num, mods, event, "", f.final), true
	}

	// A key that types text.
	r, size := utf8.DecodeRuneInString(ev.Key)
	if r == utf8.RuneError || size != len(ev.Key) {
		return "", false // a dead key, a compose sequence, a key without a name here
	}
	textOnly := mods&^1 == 0 // nothing but Shift
	if !all && textOnly {
		return "", false // text is sent as text
	}
	base := unicode.ToLower(r)
	if b, ok := baseKey(ev); ok {
		base = b
	}
	code := strconv.Itoa(int(base))
	if flags&KittyAlternateKeys != 0 && ev.ShiftKey && r != base {
		code += ":" + strconv.Itoa(int(r))
	}
	text := ""
	if all && flags&KittyAssociatedText != 0 && event != KittyRelease && textOnly {
		var cps []string
		for _, c := range ev.Key {
			cps = append(cps, strconv.Itoa(int(c)))
		}
		text = strings.Join(cps, ":")
	}
	return kittyCSI(code, mods, event, text, 'u'), true
}

// baseKey is the key a shifted symbol is on (Shift+1 types "!", on 1), from
// the DOM code.
func baseKey(ev *KeyboardEvent) (rune, bool) {
	if pair, ok := keycodeKeyMappings[ev.KeyCode]; ok && ev.ShiftKey {
		r, _ := utf8.DecodeRuneInString(pair[0])
		return r, true
	}
	return 0, false
}

// kittyCSI writes CSI number ; modifiers[:event] [; text] final, leaving
// out what is the default.
func kittyCSI(num string, mods, event int, text string, final byte) string {
	var b strings.Builder
	b.WriteString("\x1b[")
	b.WriteString(num)
	if mods != 0 || event != KittyPress || text != "" {
		if num == "" {
			b.WriteString("1")
		}
		b.WriteString(";")
		if mods != 0 || event != KittyPress {
			b.WriteString(strconv.Itoa(mods + 1))
		}
		if event != KittyPress {
			b.WriteString(":" + strconv.Itoa(event))
		}
		if text != "" {
			b.WriteString(";" + text)
		}
	}
	b.WriteByte(final)
	return b.String()
}
