//go:build js && wasm

package web

import (
	"encoding/base64"
	"strings"
	"syscall/js"
	"time"
)

// Notifications (PROTOCOL.md, Files, notifications...): the standard
// sequences — OSC 9 (iTerm2), OSC 777 notify (urxvt, foot) and kitty's OSC
// 99 — become a notification: the system's when the tab is out of sight and
// the person has allowed them, otherwise a note over the terminal. Remote
// output gets one every few seconds at most.

const (
	notifyMaxText = 500
	notifyGap     = 3 * time.Second // between remote notifications
	notifyShown   = 6 * time.Second // how long a note stays
)

type notifyState struct {
	last  time.Time
	kitty map[string]*[2]string // kitty OSC 99 chunks by id: title, body
}

// osc9 is iTerm2's OSC 9: the text is the notification. ConEmu's numbered
// subcommands (progress, "4;1;50") are not notifications.
func (s *Session) osc9(data string) {
	if i := strings.IndexByte(data, ';'); i > 0 && strings.Trim(data[:i], "0123456789") == "" {
		return
	}
	s.notify("", data)
}

// osc777 is urxvt's OSC 777: notify;title;body.
func (s *Session) osc777(data string) {
	cmd, rest, _ := strings.Cut(data, ";")
	if cmd != "notify" {
		return
	}
	title, body, _ := strings.Cut(rest, ";")
	s.notify(title, body)
}

// osc99 is kitty's: metadata (colon-separated key=value) ; payload. i is
// the notification's id, d=0 says more chunks follow, p which part this is
// (title, the default, or body), e=1 that the payload is base64.
func (s *Session) osc99(data string) {
	meta, payload, _ := strings.Cut(data, ";")
	kv := map[string]string{"d": "1", "p": "title"}
	for _, f := range strings.Split(meta, ":") {
		if k, v, ok := strings.Cut(f, "="); ok {
			kv[k] = v
		}
	}
	if kv["e"] == "1" {
		b, err := base64.StdEncoding.DecodeString(payload)
		if err != nil {
			return
		}
		payload = string(b)
	}
	if s.notes.kitty == nil {
		s.notes.kitty = map[string]*[2]string{}
	}
	n := s.notes.kitty[kv["i"]]
	if n == nil {
		n = &[2]string{}
		s.notes.kitty[kv["i"]] = n
	}
	switch kv["p"] {
	case "title":
		n[0] += payload
	case "body":
		n[1] += payload
	default:
		return // icons, buttons, queries: not shown here
	}
	if kv["d"] == "0" {
		return
	}
	delete(s.notes.kitty, kv["i"])
	s.notify(n[0], n[1])
}

// notify shows title and body.
func (s *Session) notify(title, body string) {
	title, body = clip(title), clip(body)
	if title == "" && body == "" {
		return
	}
	if s.remote() {
		if time.Since(s.notes.last) < notifyGap {
			return
		}
	}
	s.notes.last = time.Now()
	doc := js.Global().Get("document")
	if n := js.Global().Get("Notification"); n.Truthy() && doc.Get("hidden").Bool() && n.Get("permission").String() == "granted" {
		head := title
		if head == "" {
			head, body = body, ""
		}
		n.New(head, map[string]any{"body": body})
		return
	}
	s.toast(title, body)
}

// toast is a note over the terminal's top right corner, gone by itself or
// with a click.
func (s *Session) toast(title, body string) {
	if s.placements == nil || !s.placements.root.Truthy() {
		return
	}
	doc := js.Global().Get("document")
	el := doc.Call("createElement", "div")
	el.Call("setAttribute", "role", "status")
	el.Get("style").Set("cssText", "position:absolute;top:8px;right:8px;z-index:30;max-width:360px;padding:8px 12px;"+
		"background:rgba(20,24,32,.95);color:#e6e6e6;border:1px solid #3b82f6;border-radius:6px;font:13px sans-serif;"+
		"box-shadow:0 4px 16px rgba(0,0,0,.5);cursor:pointer;white-space:pre-wrap")
	if title != "" {
		t := doc.Call("createElement", "div")
		t.Get("style").Set("fontWeight", "bold")
		t.Set("textContent", title)
		el.Call("append", t)
	}
	if body != "" {
		b := doc.Call("createElement", "div")
		b.Set("textContent", body)
		el.Call("append", b)
	}
	root := s.placements.root
	if root.Get("style").Get("position").String() == "" {
		root.Get("style").Set("position", "relative")
	}
	root.Call("append", el)
	var gone js.Func
	gone = js.FuncOf(func(js.Value, []js.Value) any {
		el.Call("remove")
		gone.Release()
		return nil
	})
	el.Call("addEventListener", "click", gone, map[string]any{"once": true})
	once(func() { el.Call("remove") }, func(f js.Value) {
		js.Global().Call("setTimeout", f, int(notifyShown/time.Millisecond))
	})
}

func clip(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < ' ' && r != '\n' {
			return -1
		}
		return r
	}, s)
	if len(s) > notifyMaxText {
		s = s[:notifyMaxText] + "…"
	}
	return s
}
