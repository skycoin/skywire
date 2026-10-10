package progkit

import (
	"strings"

	"github.com/gdamore/tcell/v3"
)

// IsCtrl reports whether ev is Ctrl and the letter ch, in either encoding
// tcell uses: a legacy KeyCtrlA..Z, or KeyRune with ModCtrl when the
// terminal reports keys in full (kitty's protocol).
func IsCtrl(ev *tcell.EventKey, ch byte) bool {
	if ch >= 'A' && ch <= 'Z' {
		ch += 'a' - 'A'
	}
	if ev.Key() == tcell.KeyRune {
		return ev.Modifiers()&tcell.ModCtrl != 0 && strings.EqualFold(ev.Str(), string(ch))
	}
	return ev.Key() == tcell.KeyCtrlA+tcell.Key(ch-'a')
}

// Typed is the text ev types, or "" when it is not plain typing.
func Typed(ev *tcell.EventKey) string {
	if ev.Key() != tcell.KeyRune || ev.Modifiers()&(tcell.ModCtrl|tcell.ModAlt|tcell.ModMeta) != 0 {
		return ""
	}
	return ev.Str()
}
