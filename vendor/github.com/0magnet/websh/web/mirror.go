//go:build js && wasm

package web

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"syscall/js"
)

// The mirror (PROTOCOL.md, Accessibility): what a program shows, as
// structure, kept in a region screen readers read and eyes do not see. It
// lives in the page itself, not in a sandbox, so it is rebuilt from what the
// program sent keeping only plain structure: the tags and attributes below,
// links only to web addresses, nothing that runs or styles or asks.

// mirrorLimit is the most one mirror may be.
const mirrorLimit = 1 << 20

var mirrorTags = map[string]bool{
	"a": true, "abbr": true, "article": true, "aside": true, "b": true, "blockquote": true, "br": true,
	"caption": true, "code": true, "dd": true, "del": true, "details": true, "dfn": true, "div": true,
	"dl": true, "dt": true, "em": true, "figcaption": true, "figure": true, "footer": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true, "header": true, "hr": true,
	"i": true, "img": true, "ins": true, "kbd": true, "li": true, "main": true, "mark": true, "nav": true,
	"ol": true, "p": true, "pre": true, "q": true, "s": true, "section": true, "small": true, "span": true,
	"strong": true, "sub": true, "summary": true, "sup": true, "table": true, "tbody": true, "td": true,
	"tfoot": true, "th": true, "thead": true, "time": true, "tr": true, "u": true, "ul": true,
}

// mirrorDrop are elements dropped with all they hold; any other unknown
// element is dropped and what it holds kept.
var mirrorDrop = map[string]bool{
	"script": true, "style": true, "iframe": true, "object": true, "embed": true, "template": true,
	"svg": true, "math": true, "link": true, "meta": true, "form": true, "input": true, "button": true,
	"select": true, "textarea": true, "noscript": true, "frame": true, "frameset": true, "base": true,
	"audio": true, "video": true, "canvas": true,
}

var mirrorAttrs = map[string]bool{
	"href": true, "src": true, "alt": true, "title": true, "lang": true, "colspan": true, "rowspan": true,
	"scope": true, "datetime": true, "role": true,
}

type mirrorState struct {
	el   js.Value
	part []byte
}

// mirror takes one chunk of the running program's mirror.
func (s *Session) mirror(arg string) {
	enc, chunk, _ := strings.Cut(arg, ";")
	var m struct {
		More bool `json:"more"`
	}
	mb, err := base64.StdEncoding.DecodeString(enc)
	if err != nil || json.Unmarshal(mb, &m) != nil {
		return
	}
	b, err := base64.StdEncoding.DecodeString(chunk)
	if err != nil || len(s.mirrorS.part)+len(b) > mirrorLimit {
		s.mirrorS.part = nil
		return
	}
	s.mirrorS.part = append(s.mirrorS.part, b...)
	if m.More {
		return
	}
	html := string(s.mirrorS.part)
	s.mirrorS.part = nil
	s.mirrorShow(html)
}

// mirrorShow puts html, cleaned, in the mirror region; empty withdraws it.
func (s *Session) mirrorShow(html string) {
	el := s.mirrorEl()
	if !el.Truthy() {
		return
	}
	el.Set("textContent", "")
	if strings.TrimSpace(html) == "" {
		return
	}
	doc := js.Global().Get("DOMParser").New().Call("parseFromString", html, "text/html")
	mirrorCopy(doc.Get("body"), el, js.Global().Get("document"))
}

// mirrorClear withdraws the mirror, as the program exits.
func (s *Session) mirrorClear() {
	s.mirrorS.part = nil
	if s.mirrorS.el.Truthy() {
		s.mirrorS.el.Set("textContent", "")
	}
}

// mirrorEl is the region, made on first use beside the terminal.
func (s *Session) mirrorEl() js.Value {
	if s.mirrorS.el.Truthy() {
		return s.mirrorS.el
	}
	if s.placements == nil || !s.placements.root.Truthy() {
		return js.Value{}
	}
	el := js.Global().Get("document").Call("createElement", "div")
	el.Call("setAttribute", "role", "region")
	el.Call("setAttribute", "aria-label", "What the program in the terminal shows")
	el.Call("setAttribute", "aria-live", "polite")
	el.Set("className", "websh-mirror")
	// Read by assistive technology, invisible on the screen.
	el.Get("style").Set("cssText", "position:absolute;width:1px;height:1px;margin:-1px;padding:0;border:0;overflow:hidden;clip:rect(0 0 0 0);clip-path:inset(50%);white-space:nowrap")
	s.placements.root.Call("append", el)
	s.mirrorS.el = el
	return el
}

// mirrorCopy rebuilds from's children under to, keeping plain structure.
func mirrorCopy(from, to, doc js.Value) {
	kids := from.Get("childNodes")
	for i := 0; i < kids.Length(); i++ {
		n := kids.Index(i)
		switch n.Get("nodeType").Int() {
		case 3: // text
			to.Call("append", doc.Call("createTextNode", n.Get("textContent")))
		case 1: // element
			tag := strings.ToLower(n.Get("tagName").String())
			switch {
			case mirrorDrop[tag]:
			case mirrorTags[tag]:
				e := doc.Call("createElement", tag)
				attrs := n.Get("attributes")
				for j := 0; j < attrs.Length(); j++ {
					a := attrs.Index(j)
					name, val := strings.ToLower(a.Get("name").String()), a.Get("value").String()
					if !mirrorAttrs[name] && !strings.HasPrefix(name, "aria-") {
						continue
					}
					if (name == "href" || name == "src") && !mirrorURL(val, name == "src") {
						continue
					}
					e.Call("setAttribute", name, val)
				}
				if tag == "a" {
					e.Call("setAttribute", "rel", "noopener noreferrer")
					e.Call("setAttribute", "target", "_blank")
				}
				to.Call("append", e)
				mirrorCopy(n, e, doc)
			default:
				mirrorCopy(n, to, doc) // what it holds, without it
			}
		}
	}
}

// mirrorURL is whether a link or a picture may point at u: the web, a path
// on this page's site, or (a link only) mail.
func mirrorURL(u string, picture bool) bool {
	l := strings.ToLower(strings.TrimSpace(u))
	switch {
	case strings.HasPrefix(l, "https://"), strings.HasPrefix(l, "http://"):
		return true
	case picture:
		return false
	case strings.HasPrefix(l, "mailto:"):
		return true
	case strings.HasPrefix(l, "/") && !strings.HasPrefix(l, "//"), strings.HasPrefix(l, "#"):
		return true
	}
	return false
}
