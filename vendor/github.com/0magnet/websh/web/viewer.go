//go:build js && wasm

package web

import (
	"encoding/base64"
	"encoding/json"
	"strings"

	"github.com/0magnet/websh/progressive"
	"syscall/js"

	"github.com/0magnet/winbox-go"
)

// The viewer: a program in the terminal shows an image in a window of its
// own over the shell, by writing an escape sequence, as OSC 8 makes a link.
// It is output like any other, so it works the same from a program on the
// other end of a remote shell; a terminal that does not know it ignores it,
// so a program may write it wherever it runs.
//
//	OSC 7337 ; view ; <data> ST   show the image in the window (made if need be),
//	                              unless the person closed it
//	OSC 7337 ; open ; <data> ST   the same, even if they did
//	OSC 7337 ; close ST           close the window
//
// <data> is base64 of a JSON object: {"url": "https://…", "title": "…"}.

// ViewerOSC is the viewer's OSC number.
const ViewerOSC = 7337

// A viewer is a session's one image window. It is made inside the
// session's own element, so it shares the terminal's stacking context and
// shows above it however the page raises the terminal.
type viewer struct {
	root      js.Value
	win       *winbox.WinBox
	img       js.Value
	dismissed bool // the person closed it: view leaves it closed
	closing   bool // the program is closing it
}

// wireViewer has the terminal answer the viewer's escape sequence.
func (s *Session) wireViewer(el js.Value) {
	v := &viewer{root: el}
	p := newPlacements(s, el)
	s.placements = p
	s.Term.Core.InputHandler().RegisterOscHandler(1337, func(data string) bool {
		s.download(data)
		return true
	})
	s.Term.Core.InputHandler().SetApcHandler(func(data string) bool {
		s.kitty(data) // kitty's graphics protocol (images.go)
		return true
	})
	s.Term.Core.InputHandler().RegisterOscHandler(9, func(data string) bool {
		s.osc9(data)
		return true
	})
	s.Term.Core.InputHandler().RegisterOscHandler(777, func(data string) bool {
		s.osc777(data)
		return true
	})
	s.Term.Core.InputHandler().RegisterOscHandler(99, func(data string) bool {
		s.osc99(data)
		return true
	})
	s.Term.Core.InputHandler().RegisterOscHandler(52, func(data string) bool {
		s.clipboard(data)
		return true
	})
	s.Term.Core.InputHandler().RegisterOscHandler(ViewerOSC, func(data string) bool {
		cmd, arg, _ := strings.Cut(data, ";")
		p.reposition()
		switch cmd {
		case "caps?":
			s.Term.Core.Input(progressive.Reply(s.caps(p)), false)
		case "place":
			id, enc, _ := strings.Cut(arg, ";")
			var d placeData
			if b, err := base64.StdEncoding.DecodeString(enc); err == nil && json.Unmarshal(b, &d) == nil && id != "" {
				p.place(id, d)
			}
		case "sound":
			s.sound(arg)
		case "icon":
			s.setIcon(arg)
		case "listen":
			if arg == "drop" {
				s.dropListen = s.running
			}
		case "mirror":
			s.mirror(arg)
		case "page":
			s.setPage(arg)
		case "font":
			s.font(arg)
		case "ship":
			name, rest, _ := strings.Cut(arg, ";")
			meta, chunk, _ := strings.Cut(rest, ";")
			p.ship(name, meta, chunk)
		case "post":
			id, enc, _ := strings.Cut(arg, ";")
			if b, err := base64.StdEncoding.DecodeString(enc); err == nil {
				p.post(id, b)
			}
		case "remove":
			p.remove(arg)
		case "clear":
			p.clear()
		case "close":
			v.close()
		case "view", "open":
			var m struct {
				URL   string `json:"url"`
				Title string `json:"title"`
			}
			b, err := base64.StdEncoding.DecodeString(arg)
			if err != nil || json.Unmarshal(b, &m) != nil {
				return true
			}
			if !strings.HasPrefix(m.URL, "https://") && !strings.HasPrefix(m.URL, "http://") {
				return true // only an image from the web, never a script
			}
			if cmd == "open" {
				v.dismissed = false
			}
			if !v.dismissed {
				v.show(m.URL, m.Title)
			}
		}
		return true
	})
}

// show puts the image at url in the window, making the window if there is
// none.
func (v *viewer) show(url, title string) {
	if v.win != nil {
		v.img.Set("src", url)
		v.win.SetTitle(title)
		return
	}
	doc := js.Global().Get("document")
	box := doc.Call("createElement", "div")
	box.Get("style").Set("cssText", "width:100%;height:100%;display:flex;align-items:center;justify-content:center;background:#000")
	v.img = doc.Call("createElement", "img")
	v.img.Get("style").Set("cssText", "max-width:100%;max-height:100%;object-fit:contain")
	v.img.Set("alt", title)
	v.img.Set("src", url)
	box.Call("append", v.img)
	vw := js.Global().Get("innerWidth").Float()
	w := min(480, vw*0.45)
	v.win = winbox.New(&winbox.Options{
		Root:   v.root,
		Title:  title,
		Mount:  box,
		Width:  winbox.Px(w),
		Height: winbox.Px(w * 0.85),
		X:      winbox.Px(vw - w - 24),
		Y:      winbox.Px(48),
		OnClose: func(*winbox.WinBox, bool) bool {
			if !v.closing {
				v.dismissed = true
			}
			v.win = nil
			return false
		},
	})
}

// close closes the window, if there is one, without counting it as the
// person's choice.
func (v *viewer) close() {
	if v.win == nil {
		return
	}
	v.closing = true
	v.win.Close(true)
	v.closing = false
}
