//go:build unix

// Package console runs websh on the terminal it was started from: the same
// shell, applets and line editor as the browser session (web.Session), on the
// machine's own files and programs (shell.NewOS).
//
// It is a terminal's shell in the ordinary sense. The prompt is websh's line
// editor, in raw mode; a command runs with the terminal back in the kernel's
// cooked mode, and a program started from the PATH is handed the terminal
// itself, so an editor, a pager or ssh works as it does under any other shell.
// Ctrl+C interrupts the line: the kernel signals the programs in it, and an
// applet waiting on the terminal sees the end of its input.
//
// What it does not have is job control. Ctrl+Z is ignored rather than
// suspending a program the shell could then never resume.
package console

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/0magnet/sh/v3/interp"
	"github.com/0magnet/sh/v3/syntax"
	"golang.org/x/sys/unix"
	"golang.org/x/term"

	"github.com/0magnet/websh/shell"
)

// Options configure a console. The zero value is usable.
type Options struct {
	// Host is the name in the prompt. Empty means "websh".
	Host string

	// Greeting is written once, before the first prompt.
	Greeting string

	// Env holds extra "NAME=value" variables, overriding the process's own.
	Env []string

	// Exec is the embedder's own commands, run in this process; see
	// shell.Shell.Exec. A full-screen one finds its terminal through the
	// shell in its context: shell.FromContext(ctx).
	Exec func(ctx context.Context, args []string) (code int, handled bool)

	// AfterCommand runs after each command line, on the shell's goroutine.
	AfterCommand func()

	// In and Out are the terminal. Nil means os.Stdin and os.Stdout.
	In, Out *os.File

	// Signals carries Ctrl+C (os.Interrupt) and Ctrl+\ (SIGQUIT) for a terminal
	// that is not the process's own, as a window's tab is: its host sends them
	// here. Nil listens for the process's signals.
	Signals <-chan os.Signal
}

// Run is an interactive websh on the terminal until the shell exits, and
// returns its exit status. In must be a terminal.
func Run(opt Options) (int, error) {
	in, out := opt.In, opt.Out
	if in == nil {
		in = os.Stdin
	}
	if out == nil {
		out = os.Stdout
	}
	fd := int(in.Fd())
	if !term.IsTerminal(fd) {
		return 1, errors.New("console: input is not a terminal")
	}
	cooked, err := term.GetState(fd)
	if err != nil {
		return 1, fmt.Errorf("console: %w", err)
	}
	defer term.Restore(fd, cooked) //nolint:errcheck,gosec // nothing to do about it on the way out

	tin, err := pollable(fd)
	if err != nil {
		return 1, fmt.Errorf("console: %w", err)
	}
	// The shell that started this one gets its terminal back as it gave it.
	defer unix.SetNonblock(fd, false) //nolint:errcheck,gosec // as above
	defer tin.Close()                 //nolint:errcheck,gosec // a dup

	c := &console{
		host:  opt.Host,
		in:    tin,
		out:   out,
		fd:    fd,
		state: cooked,
	}
	if c.host == "" {
		c.host = "websh"
	}
	sh, err := shell.NewOS(tin, out, out, opt.Env...)
	if err != nil {
		return 1, fmt.Errorf("console: %w", err)
	}
	c.sh = sh
	sh.TTY = tin
	sh.Exec = opt.Exec
	sh.Size = func() (int, int) {
		w, h, err := term.GetSize(int(out.Fd()))
		if err != nil {
			return 80, 24
		}
		return w, h
	}
	sh.RawMode = c.rawMode
	sh.IsTerminal = func(w io.Writer) bool {
		f, ok := w.(*os.File)
		return ok && term.IsTerminal(int(f.Fd()))
	}
	sh.WakeStdin = c.endReads

	c.ed = &shell.LineEditor{
		Echo:   c.write,
		Prompt: func() string { return c.shown },
		Cols: func() int {
			w, _ := sh.Size()
			return w
		},
		Submit:    func(l string) { c.line, c.done = l, true },
		Interrupt: func() { c.sh.CancelPending(); c.writePrompt() },
		EOF:       func() { c.eof = true },
		ClearScreen: func() {
			c.write("\x1b[2J\x1b[H")
			c.writePrompt()
			c.ed.Redisplay()
		},
		Complete: sh.Complete,
	}
	if err := sh.UseHistory(c.ed.History, c.ed.ClearHistory); err != nil {
		c.write("history unavailable: " + err.Error() + "\r\n")
	}

	// Ctrl+C reaches the shell as SIGINT while a command runs; at the prompt
	// the terminal is raw and it is a key. Ctrl+\ is the same to a shell.
	// Ctrl+Z is ignored, and the ignoring is inherited by the programs the
	// shell starts: without job control, a stopped program is one the shell
	// would wait on forever.
	sigs := opt.Signals
	if sigs == nil {
		ch := make(chan os.Signal, 4)
		signal.Notify(ch, os.Interrupt, syscall.SIGQUIT)
		signal.Ignore(syscall.SIGTSTP)
		defer signal.Stop(ch)
		sigs = ch
	}
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for {
			select {
			case <-stop:
				return
			case _, ok := <-sigs:
				if !ok {
					return
				}
				c.interrupt()
			}
		}
	}()

	if opt.Greeting != "" {
		c.write(strings.ReplaceAll(opt.Greeting, "\n", "\r\n"))
	}
	if err := c.runRC(); err != nil {
		c.write(c.host + ": " + shell.RCFile + ": " + strings.ReplaceAll(err.Error(), "\n", "\r\n") + "\r\n")
	}
	for {
		line, ok := c.readLine()
		if !ok {
			c.write("\r\n")
			return 0, nil
		}
		if !sh.Pending() {
			c.ed.AddHistory(line)
		}
		interrupted, err := c.runLine(line)
		if opt.AfterCommand != nil {
			opt.AfterCommand()
		}
		// Ctrl+C ends a line the way exit ends a shell, as far as the
		// interpreter can tell; only an exit that was not interrupted is one.
		if sh.Exited() && !interrupted {
			return exitCode(err), nil
		}
		if !sh.Pending() {
			sh.ReportJobs(context.Background())
		}
	}
}

type console struct {
	host  string
	sh    *shell.Shell
	ed    *shell.LineEditor
	shown string   // the prompt writePrompt drew last, which the editor draws again
	in    *os.File // the terminal, non-blocking: see pollable
	out   *os.File
	fd    int
	state *term.State // the terminal as the shell found it: cooked

	// line and done are what the editor submitted; eof is Ctrl+D on an empty
	// line. All three are set by the editor's callbacks, on readLine's
	// goroutine.
	line string
	done bool
	eof  bool

	mu     sync.Mutex
	cancel context.CancelFunc // the running line's, or nil at the prompt
}

// readLine runs the line editor in raw mode until a line is submitted, and
// reports false at the end of the input.
func (c *console) readLine() (string, bool) {
	c.writePrompt()
	if _, err := term.MakeRaw(c.fd); err != nil {
		return "", false
	}
	defer term.Restore(c.fd, c.state) //nolint:errcheck,gosec // the next prompt tries again
	c.done, c.eof = false, false
	var buf [4096]byte
	var tail []byte // the start of a character the next read ends
	for !c.done && !c.eof {
		n, err := c.in.Read(buf[:])
		if n > 0 {
			b := append(tail, buf[:n]...)
			tail = nil
			if cut := partialRune(b); cut > 0 {
				tail = append([]byte(nil), b[len(b)-cut:]...)
				b = b[:len(b)-cut]
			}
			c.ed.Input(string(b))
		}
		if err != nil {
			return "", false
		}
	}
	return c.line, c.done
}

// runLine runs one line with the terminal cooked, and reports what the shell
// returned and whether Ctrl+C interrupted it.
func (c *console) runLine(line string) (interrupted bool, err error) {
	ctx, cancel := context.WithCancel(context.Background())
	c.mu.Lock()
	c.cancel = cancel
	c.mu.Unlock()

	c.osc133("C")
	_, err = c.sh.Run(ctx, line)
	interrupted = ctx.Err() != nil
	c.osc133("D;" + strconv.Itoa(exitCode(err)))

	c.mu.Lock()
	c.cancel = nil
	c.mu.Unlock()
	cancel()
	// The reads Ctrl+C ended can wait again, and the terminal is non-blocking
	// for the shell whatever a program left it as.
	c.in.SetReadDeadline(time.Time{}) //nolint:errcheck,gosec // a pollable file takes one
	unix.SetNonblock(c.fd, true)      //nolint:errcheck,gosec // reads still wait in the poller
	// A program may have left the terminal raw, or the cursor keys in
	// application mode; the prompt sets raw again for itself.
	term.Restore(c.fd, c.state) //nolint:errcheck,gosec // as above

	if err != nil && !c.sh.Exited() {
		if msg := err.Error(); !strings.HasPrefix(msg, "exit status") && !errors.Is(err, context.Canceled) {
			c.write(c.host + ": " + strings.ReplaceAll(msg, "\n", "\r\n") + "\r\n")
		}
	}
	return interrupted, err
}

// interrupt is Ctrl+C while a line runs: the line's context ends, which
// interrupts its programs, and an applet waiting on the terminal is woken.
func (c *console) interrupt() {
	c.mu.Lock()
	cancel := c.cancel
	c.mu.Unlock()
	if cancel == nil {
		return // at the prompt, or between lines: nothing to stop
	}
	cancel()
	c.endReads()
}

// endReads ends the reads waiting on the terminal now: an applet's, or the
// read builtin's. The next line's may wait again (runLine).
func (c *console) endReads() { c.in.SetReadDeadline(time.Now()) } //nolint:errcheck,gosec // as above

// pollable is a second descriptor for the terminal fd, made non-blocking, as
// a file Go reads in its poller: a read of it can then be ended with a
// deadline, which a read of a blocking terminal cannot. The shell reads the
// terminal through it; a program it starts gets the terminal blocking again
// (shell.Shell.TTY).
func pollable(fd int) (*os.File, error) {
	dup, err := unix.Dup(fd)
	if err != nil {
		return nil, err
	}
	if err := unix.SetNonblock(dup, true); err != nil {
		unix.Close(dup) //nolint:errcheck,gosec // already failing
		return nil, err
	}
	return os.NewFile(uintptr(dup), "/dev/tty"), nil
}

// rawMode is shell.Shell.RawMode: a full-screen applet takes the terminal raw,
// and gives it back cooked.
func (c *console) rawMode(on bool) {
	if on {
		term.MakeRaw(c.fd) //nolint:errcheck,gosec // the applet draws regardless
		return
	}
	term.Restore(c.fd, c.state) //nolint:errcheck,gosec // as above
}

func (c *console) write(s string) { io.WriteString(c.out, s) } //nolint:errcheck,gosec // a terminal that cannot be written has nobody to tell

// prompt is the current prompt, colors and all, as the browser session draws
// it: a home directory shows as ~.
func (c *console) prompt() string {
	if c.sh.Pending() {
		return "\x1b[1;33m>\x1b[0m "
	}
	if p, ok := c.sh.PS1(); ok {
		return p
	}
	dir := c.sh.Dir()
	if home := os.Getenv("HOME"); home != "" && (dir == home || strings.HasPrefix(dir, home+"/")) {
		dir = "~" + dir[len(home):]
	}
	return "\x1b[1;32m" + c.host + "\x1b[0m:\x1b[1;34m" + dir + "\x1b[0m$ "
}

// writePrompt draws the prompt between the shell-integration marks a terminal
// uses to find prompts and commands (OSC 133), as the browser session does.
func (c *console) writePrompt() {
	c.osc133("A")
	c.shown = c.prompt()
	c.write(shell.PromptText(c.shown))
	c.osc133("B")
}

func (c *console) osc133(mark string) { c.write("\x1b]133;" + mark + "\x1b\\") }

// exitCode is a command's exit status from what Run returned.
func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var es interp.ExitStatus
	if errors.As(err, &es) {
		return int(es)
	}
	return 1
}

// partialRune is how many bytes at the end of b begin a UTF-8 character that
// is not all there yet.
func partialRune(b []byte) int {
	for i := 1; i <= utf8.UTFMax-1 && i <= len(b); i++ {
		if utf8.RuneStart(b[len(b)-i]) {
			if utf8.FullRune(b[len(b)-i:]) {
				return 0
			}
			return i
		}
	}
	return 0
}

// Script runs src as a websh script on the OS, without a terminal, and returns
// its exit status: `websh -c` and `websh FILE`. args are the positional
// parameters, $1 onward.
func Script(src io.Reader, name string, args []string, opt Options) (int, error) {
	file, err := syntax.NewParser().Parse(src, name)
	if err != nil {
		return 2, err
	}
	sh, err := shell.NewOS(os.Stdin, os.Stdout, os.Stderr, opt.Env...)
	if err != nil {
		return 1, err
	}
	sh.Exec = opt.Exec
	sh.Runner.Params = args
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	err = sh.Runner.Run(ctx, file)
	var es interp.ExitStatus
	switch {
	case err == nil:
		return 0, nil
	case errors.As(err, &es):
		return int(es), nil
	}
	return 1, err
}

// runRC sources ~/.webshrc as a line runs, so Ctrl+C ends it.
func (c *console) runRC() error {
	ctx, cancel := context.WithCancel(context.Background())
	c.mu.Lock()
	c.cancel = cancel
	c.mu.Unlock()
	err := c.sh.SourceRC(ctx)
	interrupted := ctx.Err() != nil
	c.mu.Lock()
	c.cancel = nil
	c.mu.Unlock()
	cancel()
	c.in.SetReadDeadline(time.Time{}) //nolint:errcheck,gosec // as in runLine
	if interrupted {
		return nil
	}
	return err
}
