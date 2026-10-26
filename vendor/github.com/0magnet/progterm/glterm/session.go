//go:build unix

package glterm

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"

	"github.com/0magnet/xterm-go/vt"
	"github.com/creack/pty"
)

// Session is a shell on a pty, with the terminal it draws on.
//
// The terminal is read and written from two goroutines — the pty's output and
// the window's input and drawing — so every use of Term goes through Lock and
// Unlock, or through the methods here, which take them.
type Session struct {
	Term *vt.Terminal

	mu   sync.Mutex
	pty  *os.File
	cmd  *exec.Cmd
	done chan struct{}
	err  error

	// OnChange is called, from the reading goroutine, whenever the screen
	// may have changed.
	OnChange func()
}

// Start runs shell, with args, on a pty of cols by rows.
func Start(shell string, args []string, cols, rows int) (*Session, error) {
	o := vt.NewOptions()
	o.Scrollback = 5000
	o.XTVersion = "progterm-gl"
	s := &Session{Term: vt.NewTerminal(o), done: make(chan struct{})}
	s.Term.Resize(cols, rows)

	cmd := exec.Command(shell, args...) //nolint:gosec // the user's own shell, by their flag
	cmd.Env = append(os.Environ(), "TERM=xterm-256color", "COLORTERM=truecolor")
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)}) //nolint:gosec // a window's size in cells
	if err != nil {
		return nil, err
	}
	s.pty, s.cmd = f, cmd
	// What the terminal says goes to the program: typed keys, and its
	// replies to the program's queries.
	s.Term.OnData = func(d string) { s.pty.WriteString(d) } //nolint:errcheck,gosec // a closed pty ends the session on the read side
	go s.read()
	return s, nil
}

func (s *Session) read() {
	defer close(s.done)
	buf := make([]byte, 32*1024)
	for {
		n, err := s.pty.Read(buf)
		if n > 0 {
			s.mu.Lock()
			s.Term.Write(buf[:n])
			s.mu.Unlock()
			if s.OnChange != nil {
				s.OnChange()
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, os.ErrClosed) {
				s.err = err
			}
			if s.cmd != nil {
				s.cmd.Wait() //nolint:errcheck,gosec // the shell's exit status is its own business
			}
			return
		}
	}
}

// Lock and Unlock guard Term against the reading goroutine.
func (s *Session) Lock()   { s.mu.Lock() }
func (s *Session) Unlock() { s.mu.Unlock() }

// Input is what was typed, encoded as the terminal sends it.
func (s *Session) Input(data string) {
	if data == "" {
		return
	}
	s.mu.Lock()
	s.Term.ScrollToBottom()
	s.Term.Input(data, true)
	s.mu.Unlock()
}

// Resize makes the terminal and the pty cols by rows, with the window's size
// in pixels for a program that draws pictures to fit.
func (s *Session) Resize(cols, rows, wpx, hpx int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cols == s.Term.Cols() && rows == s.Term.Rows() {
		return
	}
	s.Term.Resize(cols, rows)
	pty.Setsize(s.pty, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows), X: uint16(wpx), Y: uint16(hpx)}) //nolint:errcheck,gosec // a window's size
}

// Done is closed when the shell has exited.
func (s *Session) Done() <-chan struct{} { return s.done }

// Close ends the session: the shell gets a hangup, as when a terminal closes.
func (s *Session) Close() error {
	if s.cmd != nil && s.cmd.Process != nil {
		s.cmd.Process.Signal(syscall.SIGHUP) //nolint:errcheck,gosec // it may have exited
	}
	return s.pty.Close()
}

// DefaultShell is websh where it is installed, and otherwise the user's shell.
func DefaultShell() string {
	if p, err := exec.LookPath("websh"); err == nil {
		return p
	}
	if s := os.Getenv("SHELL"); s != "" {
		return s
	}
	return "/bin/sh"
}

// Paste sends pasted text to the program: line endings as Enter, and inside
// the bracketed-paste markers when the program asked for them, so a shell
// does not run a pasted line before it is looked at.
func (s *Session) Paste(text string) {
	text = strings.ReplaceAll(text, "\r\n", "\r")
	text = strings.ReplaceAll(text, "\n", "\r")
	s.mu.Lock()
	if s.Term.CoreService().DecPrivateModes.BracketedPasteMode {
		text = "\x1b[200~" + text + "\x1b[201~"
	}
	s.Term.ScrollToBottom()
	s.Term.Input(text, true)
	s.mu.Unlock()
}
