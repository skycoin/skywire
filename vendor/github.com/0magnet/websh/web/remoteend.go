//go:build js && wasm

package web

import (
	"strings"

	"github.com/0magnet/xterm-go/vt"
)

// A program on another machine does not end when a command does: the host
// sees one ssh or mosh session, and the program at its far end can exit,
// crash or be killed inside it. What it placed would outlive it, and a
// click on a widget it left would reach whatever reads the session next —
// the remote shell, as typed text. So for remote output the host ends the
// program's requests at the marks a program's end leaves in the stream:
// leaving the alternate screen, a full reset (RIS), and the remote shell's
// prompt mark (OSC 133 A); and at the person's Ctrl+C or Ctrl+\ on its way
// there. A program that stays on the normal screen and leaves no mark is
// asked to send clear itself (PROTOCOL.md, Trust).

// wireRemoteEnds watches the output for those marks. Each handler only
// looks: it returns false, so the terminal still does what the sequence
// asks.
func (s *Session) wireRemoteEnds() {
	ih := s.Term.Core.InputHandler()
	ih.RegisterEscHandler(vt.FunctionID{Final: "c"}, func() bool {
		s.remoteEnded()
		return false
	})
	ih.RegisterOscHandler(133, func(data string) bool {
		if data == "A" || strings.HasPrefix(data, "A;") {
			s.remoteEnded()
		}
		return false
	})
	bufs := s.Term.Core.Buffers()
	prev := bufs.OnBufferActivate
	bufs.OnBufferActivate = func(active, inactive *vt.Buffer) {
		if prev != nil {
			prev(active, inactive)
		}
		if active == bufs.Normal() {
			s.remoteEnded()
		}
	}
}

// remoteEnded ends what a remote program asked for, as its end is seen.
func (s *Session) remoteEnded() {
	if s.remote() {
		s.programEnded()
	}
}

// programEnded takes away what a program asked the host for: what it laid
// over the cells and shipped, its font, the page's title and address, its
// mirror, its sounds, its file drops and its keyboard flags. It runs as a
// command ends, and as a remote program's end is seen (remoteEnded).
func (s *Session) programEnded() {
	if s.placements != nil {
		s.placements.clear()
		s.placements.forgetShipped()
	}
	s.fontRestore()
	s.pageRestore()
	s.mirrorClear()
	s.soundsEnd()
	s.dropListen = false
	// A program that pushed kitty keyboard flags and did not pop them —
	// crashed, say — does not leave the keys encoded for it.
	s.Term.Core.InputHandler().ResetKittyKeyboard()
}

// interruptTyped is the person's Ctrl+C or Ctrl+\ on its way to a session
// on another machine: the usual end of a program there that stays on the
// normal screen under a shell that marks no prompts, which leaves no mark in
// the output to see. It still reaches the far side.
func (s *Session) interruptTyped(data string) {
	if strings.ContainsAny(data, "\x03\x1c") {
		s.remoteEnded()
	}
}
