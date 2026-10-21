//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package glterm

import (
	"os"
	"strings"
	"syscall"

	"github.com/0magnet/xterm-go/vt"
	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

// StartFunc runs fn in this process on a pty of cols by rows, for a program
// with no binary to start, as in a phone's app. fn gets the pty's terminal
// and the Ctrl+C (os.Interrupt) and Ctrl+\ (SIGQUIT) typed into it, which the
// kernel delivers to no one for a terminal the process did not start on.
// The session is done when fn has returned.
func StartFunc(fn func(tty *os.File, sig <-chan os.Signal) int, cols, rows int) (*Session, error) {
	o := vt.NewOptions()
	o.Scrollback = 5000
	o.XTVersion = "progterm-gl"
	s := &Session{Term: vt.NewTerminal(o), done: make(chan struct{})}
	s.Term.Resize(cols, rows)

	ptm, pts, err := pty.Open()
	if err != nil {
		return nil, err
	}
	pty.Setsize(ptm, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)}) //nolint:errcheck,gosec // a window's size in cells
	sig := make(chan os.Signal, 4)
	s.pty = ptm
	s.Term.OnData = func(d string) {
		signalFor(ptm, d, sig)
		s.pty.WriteString(d) //nolint:errcheck,gosec // a closed pty ends the session on the read side
	}
	go func() {
		fn(pts, sig)
		pts.Close() //nolint:errcheck,gosec // with the program's own copies closed, the pty's reads end
	}()
	go s.read()
	return s, nil
}

// signalFor sends the signal a typed interrupt or quit character raises, when
// the terminal's settings say the kernel would raise one: ISIG on, as while a
// command runs, and not at a prompt or in a full-screen program.
func signalFor(ptm *os.File, typed string, sig chan<- os.Signal) {
	t, err := unix.IoctlGetTermios(int(ptm.Fd()), ioctlGetTermios)
	if err != nil || t.Lflag&unix.ISIG == 0 {
		return
	}
	send := func(s os.Signal) {
		select {
		case sig <- s:
		default: // one waiting is as good as several
		}
	}
	if c := t.Cc[unix.VINTR]; c != 0 && strings.IndexByte(typed, c) >= 0 {
		send(os.Interrupt)
	}
	if c := t.Cc[unix.VQUIT]; c != 0 && strings.IndexByte(typed, c) >= 0 {
		send(syscall.SIGQUIT)
	}
}
