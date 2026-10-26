//go:build unix && !android && !ios

package x11term

import (
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/jezek/xgb/xproto"
)

// clipboard is glterm's Clipboard on X selections: CLIPBOARD for copy and
// paste, PRIMARY for what is selected (ICCCM section 2). It runs on the
// window's goroutine.
type clipboard struct {
	w     *window
	owned map[xproto.Atom]string // the selections this window holds
	want  *pasteReq              // the paste waiting on another client
}

type pasteReq struct {
	sel, target xproto.Atom
	paste       func(string)
}

func (c *clipboard) atom(primary bool) xproto.Atom {
	if primary {
		return xproto.AtomPrimary
	}
	return c.w.at.clipboard
}

// Set makes this window the owner of the selection, holding text.
func (c *clipboard) Set(primary bool, text string) {
	sel := c.atom(primary)
	c.owned[sel] = text
	xproto.SetSelectionOwner(c.w.conn, c.w.win, sel, c.w.last)
}

// Get fetches the selection and hands it to paste once it is here: at once
// for one this window holds, or when its owner answers. A newer Get replaces
// one still waiting, so an owner that never answers costs nothing.
func (c *clipboard) Get(primary bool, paste func(string)) {
	sel := c.atom(primary)
	if text, ok := c.owned[sel]; ok {
		// Later, as the terminal may be holding its session's lock.
		c.w.later = append(c.w.later, func() { paste(text) })
		return
	}
	c.want = &pasteReq{sel: sel, target: c.w.at.utf8, paste: paste}
	xproto.ConvertSelection(c.w.conn, c.w.win, sel, c.w.at.utf8, c.w.at.prop, c.w.last)
}

// notify is the owner's answer to a Get.
func (c *clipboard) notify(e xproto.SelectionNotifyEvent) {
	want := c.want
	if want == nil || e.Selection != want.sel || e.Target != want.target {
		return
	}
	if e.Property == xproto.AtomNone {
		if want.target == c.w.at.utf8 { // an old client: ask for Latin-1
			want.target = xproto.AtomString
			xproto.ConvertSelection(c.w.conn, c.w.win, want.sel, want.target, c.w.at.prop, c.w.last)
			return
		}
		c.want = nil
		return
	}
	c.want = nil
	r, err := xproto.GetProperty(c.w.conn, true, c.w.win, e.Property, xproto.GetPropertyTypeAny, 0, 1<<22).Reply()
	if err != nil || r == nil {
		return
	}
	switch {
	case r.Type == c.w.at.incr:
		fmt.Fprintln(os.Stderr, "x11term: the clipboard is too large to paste (INCR transfers are not supported)")
		return
	case r.Format != 8:
		return
	case r.Type == xproto.AtomString:
		want.paste(fromLatin1(r.Value))
	default:
		want.paste(string(r.Value))
	}
}

// request answers another client asking for a selection this window holds.
func (c *clipboard) request(e xproto.SelectionRequestEvent) {
	prop := e.Property
	if prop == xproto.AtomNone {
		prop = e.Target // an obsolete client (ICCCM 2.2)
	}
	text, owned := c.owned[e.Selection]
	typ, format, data, ok := answer(c.w.at, e.Target, text, owned, c.w.px.maxRequest-64)
	if ok {
		xproto.ChangeProperty(c.w.conn, xproto.PropModeReplace, e.Requestor, prop, typ, format,
			uint32(len(data)*8/int(format)), data) //nolint:gosec // bounded by the request size
	} else {
		prop = xproto.AtomNone
	}
	ev := xproto.SelectionNotifyEvent{Time: e.Time, Requestor: e.Requestor, Selection: e.Selection, Target: e.Target, Property: prop}
	xproto.SendEvent(c.w.conn, false, e.Requestor, 0, string(ev.Bytes()))
}

// answer is the property a request for target gets when the selection holds
// text: its type, format and data, or ok false to refuse it. One larger than
// limit is refused, there being no INCR transfer here.
func answer(at atoms, target xproto.Atom, text string, owned bool, limit int) (typ xproto.Atom, format byte, data []byte, ok bool) {
	if !owned {
		return 0, 0, nil, false
	}
	switch target {
	case at.targets:
		return xproto.AtomAtom, 32, u32(uint32(at.targets), uint32(at.utf8), uint32(at.text), uint32(xproto.AtomString)), true
	case at.utf8, at.text:
		data, typ = []byte(text), at.utf8
	case xproto.AtomString:
		data, typ = []byte(latin1(text)), xproto.AtomString
	default:
		return 0, 0, nil, false
	}
	if len(data) > limit {
		return 0, 0, nil, false
	}
	return typ, 8, data, true
}

// latin1 is s in ISO Latin-1, which STRING properties are, with ? for what
// it has no character for.
func latin1(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r > 0xff {
			r = '?'
		}
		b.WriteByte(byte(r))
	}
	return b.String()
}

func fromLatin1(b []byte) string {
	out := make([]byte, 0, len(b))
	for _, c := range b {
		out = utf8.AppendRune(out, rune(c))
	}
	return string(out)
}
