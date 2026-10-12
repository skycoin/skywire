//go:build js && wasm

package web

// remote reports output that may be another machine's: a network session's
// ("remote"), or a desktop terminal's ("terminal": a pty bridged into the
// page, which the host cannot see into, where ssh may be running). Both get
// the remote rules (PROTOCOL.md, Trust); a terminal's output may also set
// the window's title, as every desktop terminal lets it (setTitle).
func (s *Session) remote() bool {
	src := s.Shell.Source()
	return src == "remote" || src == "terminal"
}

// trust is the source a program is told it has (Caps.Trust): a terminal's
// output gets the remote rules, so it is told it is remote.
func (s *Session) trust() string {
	if s.remote() {
		return "remote"
	}
	return s.Shell.Source()
}
