//go:build js && wasm

package web

import (
	"sync"

	"github.com/0magnet/websh/shell"
	xterm "github.com/0magnet/xterm-go"
)

// Finding the terminal an applet is running in.
//
// An applet is handed a context, a *shell.Shell and a HandlerContext of pipes.
// That is the right shape for the ordinary kind, which reads stdin and writes
// stdout and does not care what is on the other end. It is not enough for a
// FULL-SCREEN one — a pager, a tcell demo, a terminal client — which has to
// draw on the terminal itself and needs to know how big it is.
//
// The Shell already carries RawMode for exactly those applets, so the ability
// to take the terminal over was there while the terminal itself was not
// reachable. Each embedder solved that privately, by remembering the pane it
// built in a package variable, which works right up until there are two
// terminals and the applet running in the second one draws on the first.
//
// The session knows both halves, so it registers the pairing here.

var sessions sync.Map // *shell.Shell -> *Session

// SessionFor returns the session driving sh, or nil when sh is not one of
// ours — an applet running under a shell that was built directly rather than
// by a session, which is what the tests do.
func SessionFor(sh *shell.Shell) *Session {
	if sh == nil {
		return nil
	}
	if v, ok := sessions.Load(sh); ok {
		return v.(*Session)
	}
	return nil
}

// TerminalFor returns the terminal an applet running under sh is drawing on,
// or nil when there is not one. The common case, spelled out, so an applet
// does not have to know that a Session is what owns a Terminal.
func TerminalFor(sh *shell.Shell) *xterm.Terminal {
	s := SessionFor(sh)
	if s == nil {
		return nil
	}
	return s.Term
}

func registerSession(s *Session) {
	if s != nil && s.Shell != nil {
		sessions.Store(s.Shell, s)
	}
}

// forgetSession drops the pairing. Not tidiness: the map is keyed by a pointer
// the embedder may otherwise be finished with, and leaving it in a package-level
// map keeps the shell, its interpreter and its whole environment alive for as
// long as the page is open.
func forgetSession(s *Session) {
	if s != nil && s.Shell != nil {
		sessions.Delete(s.Shell)
	}
}
