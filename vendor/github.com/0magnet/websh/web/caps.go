//go:build js && wasm

package web

import (
	"runtime/debug"
	"syscall/js"

	"github.com/0magnet/websh/progressive"
)

// caps is this session's answer to the caps query (PROTOCOL.md, Discovery):
// what it offers the program whose output is on the terminal now, by how far
// it trusts that output, and the cell metrics a picture needs to fit cells.
func (s *Session) caps(p *placements) *progressive.Caps {
	c := &progressive.Caps{V: 1, Host: "websh", Version: version(), Trust: s.trust()}
	c.Cols, c.Rows = s.Term.Core.Cols(), s.Term.Core.Rows()
	if screen := p.root.Call("querySelector", ".xterm-screen"); screen.Truthy() && c.Cols > 0 && c.Rows > 0 {
		c.Cell.W = screen.Get("clientWidth").Float() / float64(c.Cols)
		c.Cell.H = screen.Get("clientHeight").Float() / float64(c.Rows)
	}
	c.DPR = js.Global().Get("devicePixelRatio").Float()
	c.Features = []string{"place", "place.input", "event", "post", "ship", "download", "clipboard", "mirror", "notify", "sound", "drop", "image", "kitty-graphics"}
	if c.Trust != "remote" {
		c.Features = append(c.Features, "widget.offer", "font", "title", "page", "icon")
	} else if s.Shell.Source() == "terminal" {
		c.Features = append(c.Features, "title") // a desktop terminal's window takes it
	}
	c.Path = s.linkPath()
	return c
}

// Version is websh's version, stamped at link time
// (-ldflags "-X github.com/0magnet/websh/web.Version=..."): build.sh does,
// for the TinyGo build, whose released versions record no module information
// for runtime/debug (tinygo-org/tinygo#5592 adds it, after 0.42).
var Version string

// version is websh's version in this binary: as stamped, else the main
// module's when websh is the program, the dependency's when it is part of
// another.
func version() string {
	if Version != "" {
		return Version
	}
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	if bi.Main.Path == "github.com/0magnet/websh" {
		return bi.Main.Version
	}
	for _, d := range bi.Deps {
		if d.Path == "github.com/0magnet/websh" {
			return d.Version
		}
	}
	return ""
}

// Caps is what this session offers the program running in it now: its
// answer to the caps query. A program compiled into the page — which draws
// on the terminal directly and does not read it, so cannot ask — is handed
// it by the page (progressive.Set) before it runs.
func (s *Session) Caps() *progressive.Caps {
	if s.placements == nil {
		return nil
	}
	return s.caps(s.placements)
}
