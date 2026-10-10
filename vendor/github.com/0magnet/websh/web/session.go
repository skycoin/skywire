//go:build js && wasm

// Package web mounts a websh shell onto a DOM element.
//
// websh's parts — the interpreter, the applets, the filesystem — are libraries,
// and assembling a working terminal out of them takes a couple of hundred lines
// of line editor, stdin plumbing and raw-mode switching. That assembly is the
// same every time, so it lives here rather than being copied into every program
// that wants a shell on a page.
//
// A Session is a widget: hand it an element and it fills it. Nothing here knows
// whether that element is the whole page or the body of a draggable window, and
// nothing needs to — the terminal observes its own container.
package web

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall/js"

	"github.com/0magnet/afero"
	xterm "github.com/0magnet/xterm-go"
	"github.com/0magnet/xterm-go/vt"

	"github.com/0magnet/websh/shell"
)

// Options configure a Session. The zero value is usable.
type Options struct {
	// FS is the filesystem the shell runs over. Nil creates a fresh
	// in-memory one and seeds it. Passing an existing one is how several
	// sessions come to share files — and how a caller supplies a filesystem
	// restored from somewhere persistent.
	FS afero.Fs

	// Host is the name in the prompt. Empty means "websh".
	Host string

	// Greeting is written once, before the first prompt.
	Greeting string

	// Env holds extra "NAME=value" variables for the shell's environment,
	// overriding the defaults on a name clash. An embedder uses it to put a
	// toolchain on the PATH, say.
	Env []string

	// Scrollback is the number of lines kept. Zero means 2000.
	Scrollback int

	// FontFamily is the CSS font stack the terminal draws with. Empty keeps
	// xterm-go's default. A page that already has a face of its own wants
	// this: the terminal is measured from it at Open, so it cannot be
	// changed afterwards without re-measuring the cell.
	FontFamily string

	// FontSize is the cell size in CSS pixels. Zero keeps the default.
	FontSize float64

	// NoWebGL forces the DOM renderer.
	NoWebGL bool

	// NoKeyBar withholds the row of Esc, Tab, Ctrl, Alt and arrow keys a
	// touch screen gets under the terminal (keybar.go).
	NoKeyBar bool
	// NoZoom withholds the ctrl-wheel / ctrl-plus / ctrl-0 zoom, which is
	// otherwise bound on the element the session was given. A page that wants
	// those gestures to keep zooming the PAGE, or that binds its own because
	// the terminal it draws is not the element the user is pointing at, sets
	// this.
	NoZoom bool

	// AfterCommand runs after each command line finishes, on the shell's
	// goroutine. It is where a caller flushes the filesystem somewhere
	// durable, which has to happen after a command rather than during one.
	AfterCommand func()

	// Exec is the embedder's own commands. Any command line whose first word
	// is not a built-in applet is offered here before the filesystem is
	// searched, and it runs in THIS process on the shell's goroutine.
	//
	// That is the difference that matters in a page. A program exec'd from
	// the filesystem on js/wasm is a separate wasm instance and can only talk
	// back through pipes; a command reached through this one is a Go function
	// in the program the shell is embedded in, with everything that program
	// knows in scope. It may be full-screen — the terminal is right here, and
	// Session sets RawMode and Size on the shell for exactly that.
	//
	//
	// The context carries the shell that dispatched the command, so a
	// full-screen one can find the terminal it was typed into:
	// web.SessionForContext(ctx). A page can hold several terminals, and an
	// embedder that instead remembers the one it built will draw on the wrong
	// one as soon as it does.
	//
	// Report handled false for a command you do not recognize and the shell
	// carries on as though the hook were not set.
	Exec func(ctx context.Context, args []string) (code int, handled bool)

	// OnExit runs when the shell exits — the `exit` builtin, or anything
	// else that makes the interpreter report an exiting shell — on the
	// shell's goroutine, and no further prompt is written.
	//
	// A shell in a page has nowhere to exit TO, so what happens next is
	// the embedder's to decide: a window closes, a session restarts, a
	// panel goes back to what it showed before. Without this the exit was
	// simply unobserved — the interpreter recorded it, the session
	// swallowed the status as it does any other, and printed the next
	// prompt, so typing exit appeared to do nothing whatever.
	//
	// Nil keeps that: the prompt comes back and the session carries on,
	// which is the only safe default for an embedder that has not said
	// what else to do.
	OnExit func()
}

// Session is a terminal with a shell attached, mounted on an element.
type Session struct {
	Term   *xterm.Terminal
	Shell  *shell.Shell
	Editor *shell.LineEditor

	host  string
	lines chan string

	running  bool
	rawInput bool

	// in is the running command's stdin. See inQueue.
	in *inQueue
	// cmds counts the commands run, so something a command started that
	// finishes after it (a font loading) knows it is too late.
	cmds int
	// cooked is the line being typed to a command in cooked mode, held
	// until Enter as a terminal's line discipline holds it.
	cooked []rune
	// setScreenReader turns screen reader mode on or off (a11y.go).
	setScreenReader func(on bool)
	// bar is the key bar on a touch screen, or nil (keybar.go).
	bar *keyBar
	// fonts is a program's font, while it has one (font.go).
	fonts fontState
	// page is the page's title and address before a program changed them,
	// and the link the page was opened by (page.go).
	page pageState
	// mirrorS is the running program's mirror (mirror.go).
	mirrorS mirrorState
	// notes, sounds and dropListen: notifications (notify.go), the running
	// program's sounds (sound.go), and whether it takes drop events (drop.go).
	notes      notifyState
	images     imageState
	sounds     soundState
	dropListen bool
	// line is the command line running now.
	line      string
	cancelRun context.CancelFunc
	closed    bool

	// zoomFns are the ctrl-wheel / ctrl-plus listeners; see zoom.go.
	zoomEl  js.Value
	zoomFns []zoomBinding

	afterCommand func()
	placements   *placements // what programs laid over the cells (place.go)
	onExit       func()
}

// NewSession builds a terminal on el and starts a shell on it.
func NewSession(el js.Value, opt Options) (*Session, error) {
	if el.IsNull() || el.IsUndefined() {
		return nil, fmt.Errorf("websh: cannot mount on a missing element")
	}
	fsys := opt.FS
	if fsys == nil {
		fsys = afero.NewMemMapFs()
		if err := shell.Seed(fsys); err != nil {
			return nil, fmt.Errorf("websh: seeding the filesystem: %w", err)
		}
	}
	host := opt.Host
	if host == "" {
		host = "websh"
	}
	scrollback := opt.Scrollback
	if scrollback == 0 {
		scrollback = 2000
	}

	s := &Session{
		host:         host,
		lines:        make(chan string, 8),
		afterCommand: opt.AfterCommand,
		onExit:       opt.OnExit,
	}

	o := vt.NewOptions()
	o.Scrollback = scrollback
	if opt.FontFamily != "" {
		o.FontFamily = opt.FontFamily
	}
	if opt.FontSize > 0 {
		o.FontSize = opt.FontSize
	}
	// Its name for XTVERSION, and the size reports a program may ask for
	// (cells, and pixels for one drawing pictures to fit them); none of the
	// window-changing ones.
	o.XTVersion = "websh"
	o.WindowOptions.GetWinSizeChars = true
	o.WindowOptions.GetWinSizePixels = true
	o.WindowOptions.GetCellSizePixels = true
	s.Term = xterm.New(o)
	// The terminal goes in a box filling el, so something can sit under it
	// (the key bar) with the terminal fitted above.
	if el.Get("style").Get("position").String() == "" && js.Global().Call("getComputedStyle", el).Get("position").String() == "static" {
		el.Get("style").Set("position", "relative")
	}
	box := js.Global().Get("document").Call("createElement", "div")
	box.Get("style").Set("cssText", "position:absolute;top:0;left:0;right:0;bottom:0")
	el.Call("append", box)
	s.Term.Open(box)
	// Watch the container, not the window: mounted in anything smaller than
	// the page, the window never changes when the terminal's box does.
	s.Term.AutoFit()
	s.wireViewer(el)
	s.wireRemoteEnds()
	s.wireDrop(el)
	if !opt.NoKeyBar && touchScreen() {
		s.wireKeyBar(box)
	}
	s.wireA11y(el, box)
	if !opt.NoWebGL {
		if err := s.Term.EnableWebGL(); err != nil {
			js.Global().Get("console").Call("log", "websh: webgl unavailable: "+err.Error())
		}
	}
	if !opt.NoZoom {
		s.wireZoom(el, s.Term.FontSize())
	}

	s.in = newInQueue()
	pty := func() bool { return s.Shell != nil && s.remote() }
	sh, err := shell.New(fsys, s.in, &termWriter{term: s.Term, pty: pty}, &termWriter{term: s.Term, pty: pty}, opt.Env...)
	if err != nil {
		s.Term.Dispose()
		return nil, fmt.Errorf("websh: %w", err)
	}
	s.Shell = sh
	if err := sh.PopulateBin(); err != nil {
		s.Term.WriteString("failed to populate /bin: " + err.Error() + "\r\n")
	}

	s.Editor = &shell.LineEditor{
		Echo: func(str string) { s.Term.WriteString(str) },
		Redraw: func(content string, back int) {
			line := "\r\x1b[2K" + s.Prompt() + content
			if back > 0 {
				line += fmt.Sprintf("\x1b[%dD", back)
			}
			s.Term.WriteString(line)
		},
		Submit:    func(l string) { s.lines <- l },
		Interrupt: func() { s.Shell.CancelPending(); s.WritePrompt() },
		EOF:       func() { s.Term.WriteString("\r\n"); s.WritePrompt() },
		ClearScreen: func() {
			s.Term.WriteString("\x1b[2J\x1b[H")
			s.WritePrompt()
			s.Term.WriteString(s.Editor.Line())
		},
		Complete: s.complete,
	}
	if err := sh.UseHistory(s.Editor.History, s.Editor.ClearHistory); err != nil {
		s.Term.WriteString("history unavailable: " + err.Error() + "\r\n")
	}

	// Full-screen applets take raw bytes and need the size.
	sh.RawMode = func(on bool) {
		// What was typed toward a line is the program's once it reads keys.
		if on && !s.rawInput && len(s.cooked) > 0 {
			s.writeStdin([]byte(string(s.cooked)))
			s.cooked = s.cooked[:0]
		}
		s.rawInput = on
	}
	sh.Exec = opt.Exec
	sh.Size = func() (int, int) { return s.Term.Core.Cols(), s.Term.Core.Rows() }
	sh.IsTerminal = func(w io.Writer) bool {
		tw, ok := w.(*termWriter)
		return ok && tw.term == s.Term
	}
	// A pending Read returns with nothing.
	sh.WakeStdin = func() { s.in.push(inItem{wake: true}) }

	s.Term.Core.OnData = s.onData
	s.Term.OnTitleChange = s.setTitle
	// The terminal's replies are for the program that asked: never for the
	// line editor, never echoed, and dropped when nothing is running.
	s.Term.Core.OnReply = func(data string) {
		if s.running {
			s.in.push(inItem{b: []byte(data), reply: true})
		}
	}

	// Publish the shell -> session pairing, so a full-screen applet can find
	// the terminal it is running in. See sessionfor.go.
	registerSession(s)

	go s.run()

	if opt.Greeting != "" {
		s.Term.WriteString(opt.Greeting)
	}
	s.WritePrompt()
	return s, nil
}

// Submit runs a line as though it had been typed at the prompt: it is echoed
// where the typing would have appeared, remembered in the history, and run.
//
// It is what a link into a page needs — "open this and run that" — and the
// echo is the point rather than a side effect. A command that arrives from a
// URL should be visible in the scrollback, so that what ran is on the screen
// and not only in the address bar.
//
// The send is on its own goroutine because the line channel is small and the
// run loop may be busy; blocking here would block whatever called it, which on
// this platform is usually the browser's event loop.
func (s *Session) Submit(line string) {
	if s.closed || line == "" {
		return
	}
	s.Term.WriteString(line + "\r\n")
	go func() {
		// A send on a closed session panics; there is nobody left to tell.
		defer func() { _ = recover() }() //nolint:errcheck
		s.lines <- line
	}()
}

// Prompt is the current prompt string, colors and all.
func (s *Session) Prompt() string {
	if s.Shell.Pending() {
		return "\x1b[1;33m>\x1b[0m "
	}
	dir := s.Shell.Dir()
	if strings.HasPrefix(dir, "/home/user") {
		dir = "~" + dir[len("/home/user"):]
	}
	return "\x1b[1;32m" + s.host + "\x1b[0m:\x1b[1;34m" + dir + "\x1b[0m$ "
}

// WritePrompt draws the prompt.
func (s *Session) WritePrompt() {
	s.osc133("A") // a prompt starts (a11y.go)
	s.Term.WriteString(s.Prompt())
	s.osc133("B") // and the command line after it
}

func (s *Session) onData(data string) {
	data = s.bar.apply(data) // a latched Ctrl or Alt on a touch screen
	if s.running {
		if s.rawInput {
			// A full-screen applet owns the terminal: raw bytes, no echo,
			// and Ctrl+C is passed through for it to handle itself.
			s.interruptTyped(data)
			s.writeStdin([]byte(data))
			return
		}
		if strings.Contains(data, "\x03") {
			s.Term.WriteString("^C\r\n")
			s.cooked = s.cooked[:0]
			if s.cancelRun != nil {
				s.cancelRun()
			}
			// A command waiting for input does not see its context end
			// until its read returns: end the read.
			s.in.push(inItem{interrupt: true})
			return
		}
		s.cookedInput(data)
		return
	}
	s.Editor.Input(data)
}

func (s *Session) complete(word string, isFirstWord bool) []string {
	if isFirstWord && !strings.Contains(word, "/") {
		var names []string
		for _, n := range append(shell.AppletNames(), Builtins...) {
			if strings.HasPrefix(n, word) {
				names = append(names, n)
			}
		}
		sort.Strings(names)
		return names
	}
	dir, base := filepath.Split(word)
	search := dir
	if !filepath.IsAbs(search) {
		search = filepath.Join(s.Shell.Dir(), dir)
	}
	infos, err := afero.ReadDir(s.Shell.FS, filepath.Clean(search))
	if err != nil {
		return nil
	}
	var names []string
	for _, info := range infos {
		if !strings.HasPrefix(info.Name(), base) {
			continue
		}
		cand := dir + info.Name()
		if info.IsDir() {
			cand += "/"
		}
		names = append(names, cand)
	}
	sort.Strings(names)
	return names
}

// writeStdin hands input to the running command, in order.
//
// This used to be "go shell.Write(...)" per keystroke, and the bug was
// invisible for as long as nothing depended on the order: a pipe write blocks
// until the command reads, so the goroutine is needed, but N of them racing on
// one pipe is N writes in whatever order the scheduler picks. Typing "exit"
// into a full-screen applet arrived as "xteh".
//
// It survived because the applets that existed read keys as separate events —
// a cursor key in a tcell demo means the same thing whenever it lands. A
// terminal client is the case that cannot tolerate it, because there the order
// IS the content.
//
// One queue (inQueue), so the sequence is whatever was typed, and adding to
// it never blocks the JS callback; a full one drops rather than freeze the
// page.
func (s *Session) writeStdin(b []byte) {
	if len(b) == 0 || s.in == nil {
		return
	}
	s.in.push(inItem{b: b})
}

// run is the command loop. It is a goroutine so the JS event loop — and so the
// terminal — stays responsive while a command runs.
func (s *Session) run() {
	for line := range s.lines {
		if s.closed {
			return
		}
		if !s.Shell.Pending() {
			s.Editor.AddHistory(line)
		}

		// Canceling at the end of the line is safe: the interpreter detaches
		// background jobs from this context, so `sleep 30 &` survives to the
		// next prompt as it would in bash.
		ctx, cancel := context.WithCancel(context.Background())
		s.in.next() // replies to the last command's queries are not this one's
		s.cooked = s.cooked[:0]
		s.cmds++
		s.line = line
		s.cancelRun, s.running = cancel, true

		s.osc133("C") // the command's output starts
		_, err := s.Shell.Run(ctx, line)
		s.osc133("D;" + strconv.Itoa(exitCode(err)))

		s.running, s.cancelRun = false, nil
		cancel()

		if err != nil {
			if msg := err.Error(); !strings.HasPrefix(msg, "exit status") {
				s.Term.WriteString(s.host + ": " + strings.ReplaceAll(msg, "\n", "\r\n") + "\r\n")
			}
		}
		// What the command asked the host for goes with it.
		s.programEnded()
		s.page.linkLine = "" // a link opens its program once
		if s.afterCommand != nil {
			s.afterCommand()
		}
		// Checked here and nowhere else: the interpreter overwrites this at
		// every Run, so it means the line just finished and not the shell.
		if s.onExit != nil && s.Shell.Exited() {
			s.onExit()
			return
		}
		// bash announces the background jobs that have ended before it draws
		// a prompt, and nothing in the interpreter reports one unasked, so
		// without this a job that finished is never mentioned. Not between
		// the lines of an unfinished statement, where bash also stays quiet,
		// and not with the line's own context, which was just canceled.
		if !s.Shell.Pending() {
			s.Shell.ReportJobs(context.Background())
		}
		s.WritePrompt()
	}
}

// Close tears the session down. The filesystem outlives it, so files written
// here are still there for whatever opens next.
func (s *Session) Close() {
	if s.closed {
		return
	}
	s.closed = true
	forgetSession(s)
	s.releaseZoom()
	if s.in != nil {
		s.in.close()
	}
	close(s.lines)
	if s.Term != nil {
		s.Term.Dispose()
	}
}

// termWriter is the shell's stdout or stderr on the terminal.
//
// A write may end part way through a character, as a pty's reads do; the
// rest comes with the next write, so the start is kept for it rather than
// drawn as replacement characters, which take cells of their own and push the
// rest of the row along. A cooked terminal's output processing turns a line
// feed into a new line; a pty's bytes (ssh, mosh, a desktop host's) have had
// that done on the far side, where a program may also move down a line without
// going back to its start, so they are drawn as they are.
type termWriter struct {
	term interface{ WriteString(string) }
	pty  func() bool // the output is a pty's
	mu   sync.Mutex
	tail []byte // the start of a character the next write ends
}

func (w *termWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	b := append(w.tail, p...)
	w.tail = nil
	if cut := partialRune(b); cut > 0 {
		w.tail = append([]byte(nil), b[len(b)-cut:]...)
		b = b[:len(b)-cut]
	}
	s := string(b)
	if w.pty == nil || !w.pty() {
		s = strings.ReplaceAll(s, "\n", "\r\n")
	}
	if s != "" {
		w.term.WriteString(s)
	}
	return len(p), nil
}

// partialRune is how many bytes at the end of b begin a UTF-8 character that
// is not all there yet.
func partialRune(b []byte) int {
	for i := 1; i <= 3 && i <= len(b); i++ {
		c := b[len(b)-i]
		if c < 0x80 {
			return 0 // ASCII ends it: nothing is waiting
		}
		if c >= 0xC0 { // a lead byte, and how long its character is
			need := 2
			if c >= 0xF0 {
				need = 4
			} else if c >= 0xE0 {
				need = 3
			}
			if need > i {
				return i
			}
			return 0
		}
	}
	return 0
}

// Builtins are the interpreter's own commands, which completion offers
// alongside the applets.
var Builtins = []string{
	"cd", "pwd", "echo", "printf", "read", "exit", "export", "unset",
	"source", "test", "true", "false", "set", "shift", "local",
	"declare", "eval", "alias", "unalias", "type", "return", "break",
	"continue", "pushd", "popd", "dirs", "let", "getopts", "wait",
	"jobs", "kill", "disown", "fg", "bg", "enable", "compgen", "history",
	"builtin", "umask", "times", "trap", "shopt", "mapfile", "readarray",
}

// echoCtl is what a terminal in cooked mode echoes for data, as a tty with
// ECHOCTL does: a line ending as a new line, a tab as itself, and any other
// control character in caret form (^[, ^D) rather than acted on — an arrow
// key's ESC [ A echoed raw would move the cursor.
func echoCtl(data string) string {
	var b strings.Builder
	for _, r := range data {
		switch {
		case r == '\r' || r == '\n':
			b.WriteString("\r\n")
		case r == '\t' || r == '\b' || r == 0x7f:
			b.WriteRune(r)
		case r < ' ':
			b.WriteByte('^')
			b.WriteRune(r + '@')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// cookedInput is a terminal's line discipline in cooked mode: what is typed
// is echoed and held until Enter, and then the command gets the line, as a
// tty in canonical mode gives it — so a command reading its input sees whole
// lines, and Backspace, Ctrl+U and Ctrl+W edit the line before it does.
// Ctrl+D ends the input at the start of a line, and otherwise hands over
// what is there.
func (s *Session) cookedInput(data string) {
	var echo strings.Builder
	for _, r := range data {
		switch r {
		case '\r', '\n':
			echo.WriteString("\r\n")
			s.writeStdin([]byte(string(s.cooked) + "\n"))
			s.cooked = s.cooked[:0]
		case 0x7f, '\b': // erase a character
			if n := len(s.cooked); n > 0 {
				s.cooked = s.cooked[:n-1]
				echo.WriteString("\b \b")
			}
		case 0x15: // Ctrl+U: erase the line
			echo.WriteString(strings.Repeat("\b \b", len(s.cooked)))
			s.cooked = s.cooked[:0]
		case 0x17: // Ctrl+W: erase a word
			n := len(s.cooked)
			for n > 0 && s.cooked[n-1] == ' ' {
				n--
			}
			for n > 0 && s.cooked[n-1] != ' ' {
				n--
			}
			echo.WriteString(strings.Repeat("\b \b", len(s.cooked)-n))
			s.cooked = s.cooked[:n]
		case 0x04: // Ctrl+D
			if len(s.cooked) == 0 {
				s.in.push(inItem{interrupt: true}) // the end of the input
			} else {
				s.writeStdin([]byte(string(s.cooked)))
				s.cooked = s.cooked[:0]
			}
		default:
			s.cooked = append(s.cooked, r)
			echo.WriteString(echoCtl(string(r)))
		}
	}
	s.Term.WriteString(echo.String())
}
