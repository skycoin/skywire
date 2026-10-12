//go:build js && wasm

package web

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"syscall/js"
)

// The page a program runs in (PROTOCOL.md, The page; Files): its title and
// address while the program runs, files it offers, and the clipboard. A
// remote program gets the title and address of no one's page, and asks the
// person before it saves a file or fills the clipboard.

// pageState is the page as it was before the running program changed it.
type pageState struct {
	saved bool
	title string
	url   string
	// icon is the favicon link's href before a program set one; iconSet
	// says it did.
	icon    string
	iconSet bool
	// The link the page was opened by: a command line, and where in it.
	linkLine string
	linkPath string
}

func (s *Session) pageSave() {
	if !s.page.saved {
		s.page.saved = true
		s.page.title = js.Global().Get("document").Get("title").String()
		s.page.url = js.Global().Get("location").Get("href").String()
	}
}

// pageRestore puts the title and address back, as the program exits.
func (s *Session) pageRestore() {
	if !s.page.saved {
		return
	}
	js.Global().Get("document").Set("title", s.page.title)
	js.Global().Get("history").Call("replaceState", js.Null(), "", s.page.url)
	if s.page.iconSet {
		if s.page.icon == "" {
			iconLink().Call("remove")
		} else {
			iconLink().Set("href", s.page.icon)
		}
		s.page.iconSet = false
	}
	s.page.saved = false
}

// setIcon is OSC 7337 icon: the page's favicon while the program runs, from
// the web or a data: picture.
func (s *Session) setIcon(enc string) {
	if !s.running || s.remote() {
		return
	}
	var m struct {
		URL string `json:"url"`
	}
	b, err := base64.StdEncoding.DecodeString(enc)
	if err != nil || json.Unmarshal(b, &m) != nil || len(m.URL) > 1<<20 {
		return
	}
	u := strings.ToLower(m.URL)
	if !strings.HasPrefix(u, "https://") && !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "data:image/") {
		return
	}
	s.pageSave()
	l := iconLink()
	if !s.page.iconSet {
		s.page.iconSet, s.page.icon = true, l.Call("getAttribute", "href").String()
		if l.Call("getAttribute", "href").IsNull() {
			s.page.icon = ""
		}
	}
	l.Set("href", m.URL)
}

// iconLink is the page's favicon link, made if it has none.
func iconLink() js.Value {
	doc := js.Global().Get("document")
	l := doc.Call("querySelector", "link[rel~='icon']")
	if !l.Truthy() {
		l = doc.Call("createElement", "link")
		l.Set("rel", "icon")
		doc.Get("head").Call("append", l)
	}
	return l
}

// setTitle is OSC 0 and OSC 2: the page's title, while the program runs.
func (s *Session) setTitle(title string) {
	// A terminal's output names its window, as on a desktop; a remote one does not.
	if !s.running || s.Shell.Source() == "remote" {
		return
	}
	s.pageSave()
	js.Global().Get("document").Set("title", title)
}

// setPage is OSC 7337 page: where the program is, put in the page's address
// so it can be linked to and opened again. The address carries the command
// that is running and the program's path in it, in the fragment, which
// every host — a static one too — hands back untouched.
func (s *Session) setPage(enc string) {
	if !s.running || s.remote() {
		return
	}
	var m struct {
		Path  string `json:"path"`
		Title string `json:"title"`
	}
	b, err := base64.StdEncoding.DecodeString(enc)
	if err != nil || json.Unmarshal(b, &m) != nil || len(m.Path) > 2048 || strings.ContainsAny(m.Path, "\x00\r\n") {
		return
	}
	s.pageSave()
	loc := js.Global().Get("location")
	frag := "run=" + url.QueryEscape(s.line)
	if m.Path != "" {
		frag += "&at=" + url.QueryEscape(m.Path)
	}
	js.Global().Get("history").Call("replaceState", js.Null(), "", loc.Get("pathname").String()+loc.Get("search").String()+"#"+frag)
	if m.Title != "" {
		js.Global().Get("document").Set("title", m.Title)
	}
}

// OpenLink reads the address the page was opened by. A link made by a
// program (setPage) carries a command; it is typed at the prompt for the
// person to run, never run for them — a link that ran commands would let
// anyone's page reach this shell's files — and the program it starts is
// told, through Discovery, where in it the link pointed.
func (s *Session) OpenLink() {
	frag := strings.TrimPrefix(js.Global().Get("location").Get("hash").String(), "#")
	v, err := url.ParseQuery(frag)
	if err != nil || v.Get("run") == "" {
		return
	}
	line := strings.Map(func(r rune) rune {
		if r < ' ' {
			return -1 // one line, and no control characters
		}
		return r
	}, v.Get("run"))
	s.page.linkLine, s.page.linkPath = line, v.Get("at")
	s.Editor.Input(line)
}

// linkPath is where in the running command the page's link pointed, if the
// command is the one the link carried.
func (s *Session) linkPath() string {
	if s.page.linkLine != "" && s.line == s.page.linkLine {
		return s.page.linkPath
	}
	return ""
}

// download is iTerm2's OSC 1337 File= with inline=0: a file the program
// offers, saved by the browser.
func (s *Session) download(data string) {
	head, body, ok := strings.Cut(data, ":")
	if !ok || !strings.HasPrefix(head, "File=") {
		return
	}
	args := map[string]string{}
	for _, kv := range strings.Split(strings.TrimPrefix(head, "File="), ";") {
		k, v, _ := strings.Cut(kv, "=")
		args[k] = v
	}
	if args["inline"] == "1" { // a picture in the text (images.go)
		if b, err := base64.StdEncoding.DecodeString(body); err == nil && len(b) <= imageLimit {
			s.iterm2Inline(args, b)
		}
		return
	}
	nb, err := base64.StdEncoding.DecodeString(args["name"])
	name := strings.Map(func(r rune) rune {
		if r < ' ' || r == '/' || r == '\\' {
			return '_'
		}
		return r
	}, string(nb))
	if err != nil || name == "" {
		name = "download"
	}
	b, err := base64.StdEncoding.DecodeString(body)
	if err != nil {
		return
	}
	if s.remote() && !js.Global().Call("confirm", "A program on another machine offers a file: "+name+" ("+strconv.Itoa(len(b))+" bytes). Save it?").Bool() {
		return
	}
	buf := js.Global().Get("Uint8Array").New(len(b))
	js.CopyBytesToJS(buf, b)
	blob := js.Global().Get("Blob").New(js.ValueOf([]any{buf}), map[string]any{"type": "application/octet-stream"})
	u := js.Global().Get("URL").Call("createObjectURL", blob)
	a := js.Global().Get("document").Call("createElement", "a")
	a.Set("href", u)
	a.Set("download", name)
	a.Call("click")
	once(func() { js.Global().Get("URL").Call("revokeObjectURL", u) }, func(f js.Value) {
		js.Global().Call("setTimeout", f, 1000)
	})
}

// clipboard is OSC 52: the program fills the clipboard, or asks what is on
// it. What the person copied elsewhere is theirs, so a program reads it only
// when they say yes, each time; the browser may ask as well.
func (s *Session) clipboard(data string) {
	sel, enc, ok := strings.Cut(data, ";")
	if !ok {
		return
	}
	if enc == "?" {
		s.clipboardRead(sel)
		return
	}
	b, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		return
	}
	if s.remote() && !js.Global().Call("confirm", "A program on another machine wants to put "+strconv.Itoa(len(b))+" bytes on the clipboard. Allow it?").Bool() {
		return
	}
	if cb := js.Global().Get("navigator").Get("clipboard"); cb.Truthy() {
		p := cb.Call("writeText", string(b))
		once(func() {}, func(f js.Value) { p.Call("catch", f) }) // a page without focus may refuse; nothing to tell
	}
}

// once hands use a JS function that runs f the first time it is called and
// then lets itself go.
func once(f func(), use func(js.Value)) {
	var fn js.Func
	fn = js.FuncOf(func(js.Value, []js.Value) any {
		fn.Release()
		f()
		return nil
	})
	use(fn.Value)
}

// clipboardRead answers OSC 52's query, if the person allows it: the text
// on the clipboard, as OSC 52 ; <selection> ; <base64>. A refusal is
// answered with nothing, as a terminal that does not allow reading does.
func (s *Session) clipboardRead(sel string) {
	who := "The program in the terminal"
	if s.remote() {
		who = "A program on another machine"
	}
	cb := js.Global().Get("navigator").Get("clipboard")
	if !cb.Truthy() || !js.Global().Call("confirm", who+" asks to read your clipboard. Allow it this once?").Bool() {
		return
	}
	cmd := s.cmds
	var ok, fail js.Func
	release := func() { ok.Release(); fail.Release() }
	ok = js.FuncOf(func(_ js.Value, args []js.Value) any {
		defer release()
		if s.running && s.cmds == cmd {
			s.Term.Core.Input("\x1b]52;"+sel+";"+base64.StdEncoding.EncodeToString([]byte(args[0].String()))+"\x1b\\", false)
		}
		return nil
	})
	fail = js.FuncOf(func(js.Value, []js.Value) any {
		defer release()
		return nil
	})
	cb.Call("readText").Call("then", ok, fail)
}
