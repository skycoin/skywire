//go:build js && wasm

// Package pty pkg/pty/pty_websh_js.go c2-app-pty
package pty

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/0magnet/afero"
	"github.com/0magnet/sh/v3/interp"
	"github.com/0magnet/websh/shell"
)

// Pty hosts a websh session in place of a pseudo-terminal. The remote
// terminal's bytes feed websh's line editor, or the stdin of the command that
// is running, and the shell's output is what Read returns. The shell works on
// the visor's own filesystem, the one sftp serves.
type Pty struct {
	mu      sync.Mutex
	started bool
	sh      *shell.Shell
	editor  *shell.LineEditor
	out     *outBuf
	stdinW  *io.PipeWriter
	lines   chan string
	running bool
	raw     bool
	cancel  context.CancelFunc
	cols    int
	rows    int
	stop    sync.Once
}

// webshFS is the session's filesystem: the visor's own, which in a browser is
// the worker's in-memory jsfs. Tests swap in a memory filesystem.
var webshFS = func() afero.Fs { return afero.NewOsFs() }

// NewPty constructs a websh-backed Pty host.
func NewPty() *Pty { return &Pty{cols: wsCols, rows: wsRows} }

// crlf adapts shell output (LF) to a terminal (CRLF).
type crlf struct{ w io.Writer }

func (c crlf) Write(p []byte) (int, error) {
	if _, err := io.WriteString(c.w, strings.ReplaceAll(string(p), "\n", "\r\n")); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Start opens the websh session. name and args name a host shell, which a
// browser does not have, so they are ignored.
func (s *Pty) Start(_ string, _ []string, sz *WinSize, env []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return ErrPtyAlreadyRunning
	}
	if sz != nil && sz.Cols > 0 && sz.Rows > 0 {
		s.cols, s.rows = int(sz.Cols), int(sz.Rows)
	}
	s.out = newOutBuf()
	stdinR, stdinW := io.Pipe()
	s.stdinW = stdinW
	out := crlf{s.out}
	sh, err := shell.New(webshFS(), stdinR, out, out, env...)
	if err != nil {
		return fmt.Errorf("websh: %w", err)
	}
	sh.RawMode = func(on bool) { s.raw = on }
	sh.Size = func() (int, int) { return s.cols, s.rows }
	registerSkywireApplet()
	s.sh = sh
	s.lines = make(chan string, 8)
	s.editor = &shell.LineEditor{
		Echo: s.echo,
		Redraw: func(content string, back int) {
			line := "\r\x1b[2K" + s.prompt() + content
			if back > 0 {
				line += fmt.Sprintf("\x1b[%dD", back)
			}
			s.echo(line)
		},
		Submit:      func(line string) { s.lines <- line },
		Interrupt:   func() { s.sh.CancelPending(); s.echo(s.prompt()) },
		EOF:         func() { s.lines <- "exit" },
		ClearScreen: func() { s.echo("\x1b[2J\x1b[H" + s.prompt() + s.editor.Line()) },
	}
	if err := sh.UseHistory(s.editor.History, s.editor.ClearHistory); err != nil {
		return fmt.Errorf("websh history: %w", err)
	}
	s.started = true
	go s.run()
	s.echo("websh on a browser visor. Type help for commands.\r\n" + s.prompt())
	return nil
}

func (s *Pty) echo(str string) { _, _ = io.WriteString(s.out, str) } //nolint:errcheck

func (s *Pty) prompt() string {
	if s.sh.Pending() {
		return "> "
	}
	return s.sh.Dir() + "$ "
}

// run executes submitted lines one at a time and ends the session on exit.
func (s *Pty) run() {
	for line := range s.lines {
		if !s.sh.Pending() {
			s.editor.AddHistory(line)
		}
		ctx, cancel := context.WithCancel(context.Background())
		s.mu.Lock()
		s.cancel, s.running = cancel, true
		s.mu.Unlock()
		_, err := s.sh.Run(ctx, line)
		cancel()
		s.mu.Lock()
		s.running, s.raw = false, false
		s.mu.Unlock()
		if err != nil && ctx.Err() == nil {
			s.echo(err.Error() + "\r\n")
		}
		if s.sh.Exited() {
			_ = s.Stop() //nolint:errcheck
			return
		}
		s.echo(s.prompt())
	}
}

// Read returns shell output for the remote terminal.
func (s *Pty) Read(b []byte) (int, error) {
	if !s.isStarted() {
		return 0, ErrPtyNotRunning
	}
	return s.out.Read(b)
}

// Write takes the remote terminal's input.
func (s *Pty) Write(b []byte) (int, error) {
	if !s.isStarted() {
		return 0, ErrPtyNotRunning
	}
	s.mu.Lock()
	running, raw, cancel := s.running, s.raw, s.cancel
	s.mu.Unlock()
	data := string(b)
	switch {
	case running && raw:
		// a full-screen applet such as edit or less owns the terminal
		return s.stdinW.Write(b)
	case running:
		if strings.Contains(data, "\x03") {
			if cancel != nil {
				cancel()
			}
			return len(b), nil
		}
		s.echo(strings.ReplaceAll(data, "\r", "\r\n"))
		if _, err := s.stdinW.Write([]byte(strings.ReplaceAll(data, "\r", "\n"))); err != nil {
			return 0, err
		}
	default:
		s.editor.Input(data)
	}
	return len(b), nil
}

// SetPtySize records the remote terminal's size for full-screen applets.
func (s *Pty) SetPtySize(sz *WinSize) error {
	if sz == nil {
		return nil
	}
	s.mu.Lock()
	s.cols, s.rows = int(sz.Cols), int(sz.Rows)
	s.mu.Unlock()
	return nil
}

// Stop ends the session. Read then returns EOF, which closes it remotely.
func (s *Pty) Stop() error {
	if !s.isStarted() {
		return nil
	}
	s.stop.Do(func() {
		s.mu.Lock()
		cancel := s.cancel
		s.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		_ = s.stdinW.Close() //nolint:errcheck
		_ = s.out.Close()    //nolint:errcheck
	})
	return nil
}

func (s *Pty) isStarted() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.started
}

var skywireAppletOnce sync.Once

// registerSkywireApplet makes `skywire ...` run the skywire CLI on the page's
// process layer, the same path a remote `pty exec` takes.
func registerSkywireApplet() {
	skywireAppletOnce.Do(func() {
		shell.RegisterApplet("skywire", "the skywire CLI, e.g. skywire cli visor info",
			func(ctx context.Context, _ *shell.Shell, hc *interp.HandlerContext, args []string) int {
				req := &CommandExecReq{Name: "skywire", Arg: args}
				code, _ := execBuiltin(ctx, req, hc.Stdout, hc.Stderr)
				return code
			})
	})
}

// outBuf is the session's output. Writes never block, so the shell and the
// line editor's echo cannot stall waiting for the remote side to read.
type outBuf struct {
	mu     sync.Mutex
	cond   *sync.Cond
	buf    bytes.Buffer
	closed bool
}

func newOutBuf() *outBuf {
	b := &outBuf{}
	b.cond = sync.NewCond(&b.mu)
	return b
}

func (b *outBuf) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return 0, io.ErrClosedPipe
	}
	b.buf.Write(p)
	b.cond.Broadcast()
	return len(p), nil
}

func (b *outBuf) Read(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for b.buf.Len() == 0 && !b.closed {
		b.cond.Wait()
	}
	if b.buf.Len() == 0 {
		return 0, io.EOF
	}
	return b.buf.Read(p)
}

func (b *outBuf) Close() error {
	b.mu.Lock()
	b.closed = true
	b.cond.Broadcast()
	b.mu.Unlock()
	return nil
}
