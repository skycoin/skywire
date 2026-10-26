//go:build unix && !android && !ios

package x11term

import (
	"unicode"

	"github.com/0magnet/progterm/glterm"
)

// The core protocol's modifier bits in a key or button event's state.
const (
	maskShift   = 1 << 0
	maskLock    = 1 << 1
	maskControl = 1 << 2
	maskButton1 = 1 << 8
	maskButton2 = 1 << 9
	maskButton3 = 1 << 10
)

// Keysyms from X.Org's keysymdef.h that are not characters.
const (
	xkISOLevel3Shift = 0xfe03
	xkISOLeftTab     = 0xfe20
	xkBackSpace      = 0xff08
	xkTab            = 0xff09
	xkReturn         = 0xff0d
	xkEscape         = 0xff1b
	xkHome           = 0xff50
	xkLeft           = 0xff51
	xkUp             = 0xff52
	xkRight          = 0xff53
	xkDown           = 0xff54
	xkPrior          = 0xff55
	xkNext           = 0xff56
	xkEnd            = 0xff57
	xkInsert         = 0xff63
	xkModeSwitch     = 0xff7e
	xkNumLock        = 0xff7f
	xkKPEnter        = 0xff8d
	xkKPHome         = 0xff95
	xkKPLeft         = 0xff96
	xkKPUp           = 0xff97
	xkKPRight        = 0xff98
	xkKPDown         = 0xff99
	xkKPPrior        = 0xff9a
	xkKPNext         = 0xff9b
	xkKPEnd          = 0xff9c
	xkKPInsert       = 0xff9e
	xkKPDelete       = 0xff9f
	xkKPMultiply     = 0xffaa
	xkKPAdd          = 0xffab
	xkKPSubtract     = 0xffad
	xkKPDecimal      = 0xffae
	xkKPDivide       = 0xffaf
	xkKP0            = 0xffb0
	xkKP9            = 0xffb9
	xkKPEqual        = 0xffbd
	xkF1             = 0xffbe
	xkF12            = 0xffc9
	xkAltL           = 0xffe9
	xkAltR           = 0xffea
	xkMetaL          = 0xffe7
	xkMetaR          = 0xffe8
	xkSuperL         = 0xffeb
	xkSuperR         = 0xffec
	xkDelete         = 0xffff
)

// named are the keysyms of glterm's named keys.
var named = map[uint32]glterm.Key{
	xkBackSpace: glterm.KeyBackspace, xkTab: glterm.KeyTab, xkISOLeftTab: glterm.KeyTab,
	xkReturn: glterm.KeyEnter, xkKPEnter: glterm.KeyEnter, xkEscape: glterm.KeyEscape,
	xkHome: glterm.KeyHome, xkKPHome: glterm.KeyHome, xkEnd: glterm.KeyEnd, xkKPEnd: glterm.KeyEnd,
	xkLeft: glterm.KeyLeft, xkKPLeft: glterm.KeyLeft, xkUp: glterm.KeyUp, xkKPUp: glterm.KeyUp,
	xkRight: glterm.KeyRight, xkKPRight: glterm.KeyRight, xkDown: glterm.KeyDown, xkKPDown: glterm.KeyDown,
	xkPrior: glterm.KeyPageUp, xkKPPrior: glterm.KeyPageUp, xkNext: glterm.KeyPageDown, xkKPNext: glterm.KeyPageDown,
	xkInsert: glterm.KeyInsert, xkKPInsert: glterm.KeyInsert, xkDelete: glterm.KeyDelete, xkKPDelete: glterm.KeyDelete,
}

// keypad are the keypad's characters, which keysymdef.h does not give as
// code points.
var keypad = map[uint32]rune{
	xkKPMultiply: '*', xkKPAdd: '+', xkKPSubtract: '-', xkKPDecimal: '.', xkKPDivide: '/', xkKPEqual: '=',
}

// keymap is the server's keyboard: the keysyms on each keycode and which
// modifier bits Alt, Super, Num Lock and the other groups are on.
type keymap struct {
	min     byte
	per     int
	syms    []uint32
	alt     uint16
	super   uint16
	numLock uint16
	mode    uint16 // Mode_switch: the second group
	level3  uint16 // ISO_Level3_Shift, AltGr
}

// newKeymap reads a keymap from GetKeyboardMapping's keysyms, per to a
// keycode from first, and GetModifierMapping's keycodes, perMod to a modifier.
func newKeymap(first byte, per int, syms []uint32, perMod int, mods []byte) *keymap {
	k := &keymap{min: first, per: per, syms: syms}
	for m := 0; m < 8 && perMod > 0; m++ {
		for _, code := range mods[m*perMod : min(len(mods), (m+1)*perMod)] {
			if code == 0 {
				continue
			}
			for l := 0; l < per; l++ {
				ks := k.sym(code, l)
				bit := uint16(1) << m
				switch ks {
				case xkAltL, xkAltR, xkMetaL, xkMetaR:
					if m >= 3 {
						k.alt |= bit
					}
				case xkSuperL, xkSuperR:
					k.super |= bit
				case xkNumLock:
					k.numLock |= bit
				case xkModeSwitch:
					k.mode |= bit
				case xkISOLevel3Shift:
					k.level3 |= bit
				}
			}
		}
	}
	if k.alt == 0 {
		k.alt = 1 << 3 // Mod1, where every keymap puts it
	}
	return k
}

func (k *keymap) sym(code byte, i int) uint32 {
	at := (int(code)-int(k.min))*k.per + i
	if code < k.min || i >= k.per || at < 0 || at >= len(k.syms) {
		return 0
	}
	return k.syms[at]
}

// lookup is the keysym keycode code makes with state held, as the core
// protocol chooses it (X11 protocol, section 5, "Keyboards").
func (k *keymap) lookup(code byte, state uint16) uint32 {
	base := 0
	switch {
	case state&k.level3 != 0 && k.sym(code, 4) != 0:
		base = 4
	case state&k.mode != 0 && k.sym(code, 2) != 0:
		base = 2
	}
	lo, hi := k.sym(code, base), k.sym(code, base+1)
	if hi == 0 {
		hi = upper(lo)
	}
	shift := state&maskShift != 0
	if state&k.numLock != 0 && isKeypad(hi) {
		if shift {
			return lo
		}
		return hi
	}
	if shift {
		return hi
	}
	if state&maskLock != 0 {
		return upper(lo)
	}
	return lo
}

func isKeypad(ks uint32) bool { return ks >= 0xff80 && ks <= 0xffbd }

// upper is a letter's keysym in upper case, or ks.
func upper(ks uint32) uint32 {
	r := toRune(ks)
	if r == 0 || unicode.ToUpper(r) == r {
		return ks
	}
	u := unicode.ToUpper(r)
	if u < 0x100 {
		return uint32(u) //nolint:gosec // Latin 1 keysyms are their code points
	}
	return 0x01000000 | uint32(u) //nolint:gosec // a code point| uint32(u) //nolint:gosec // as above
}

// toRune is the character keysym ks types, or 0.
func toRune(ks uint32) rune {
	switch {
	case ks >= 0x20 && ks <= 0x7e, ks >= 0xa0 && ks <= 0xff:
		return rune(ks)
	case ks&0xff000000 == 0x01000000:
		return rune(ks & 0x00ffffff)
	case ks >= xkKP0 && ks <= xkKP9:
		return '0' + rune(ks-xkKP0)
	}
	if r, ok := keypad[ks]; ok {
		return r
	}
	return keysymCodePoints[rune(ks)] //nolint:gosec // keysyms are 29 bits
}

// mods is glterm's modifiers in state.
func (k *keymap) mods(state uint16) glterm.Mods {
	var m glterm.Mods
	if state&maskShift != 0 {
		m |= glterm.Shift
	}
	if state&maskControl != 0 {
		m |= glterm.Ctrl
	}
	if state&k.alt != 0 {
		m |= glterm.Alt
	}
	if state&k.super != 0 {
		m |= glterm.Meta
	}
	return m
}

// translate is what a key press of keysym ks with mods m is to the terminal:
// a key event, or text typed with neither Ctrl nor Alt. ok is false for a key
// that types nothing, such as a modifier on its own.
func translate(ks uint32, m glterm.Mods) (e glterm.KeyEvent, text string, ok bool) {
	e.Mods = m
	if k, isNamed := named[ks]; isNamed {
		e.Key = k
		if ks == xkISOLeftTab {
			e.Mods |= glterm.Shift
		}
		return e, "", true
	}
	if ks >= xkF1 && ks <= xkF12 {
		e.Key = glterm.KeyF1 + glterm.Key(ks-xkF1)
		return e, "", true
	}
	r := toRune(ks)
	if r == 0 {
		return e, "", false
	}
	if m&(glterm.Ctrl|glterm.Alt) == 0 {
		return e, string(r), true
	}
	e.Rune = r
	return e, "", true
}
