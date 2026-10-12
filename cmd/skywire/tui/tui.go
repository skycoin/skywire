//go:build !(js && wasm)

// Package tui cmd/skywire/tui/tui.go
//
// An interactive console over the skywire command line: a scrollback of
// everything run so far, a prompt to type the next command into, and the code
// rain falling behind both. `skywire --tui`, or `--tui` alongside any `--help`.
//
// The console starts on the help for the command it was opened on and runs the
// real binary from there. A plain line is run captured — its output is folded
// into the scrollback. A line beginning with `!` is run in the real terminal:
// the console steps aside, hands the child the screen (so a pty exec, a live
// plot, anything streaming or full-screen works), and resumes when it exits.
package tui

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/0magnet/progkit"
	"github.com/fatih/color"
	"github.com/gdamore/tcell/v3"
	tcolor "github.com/gdamore/tcell/v3/color"
	"github.com/rivo/uniseg"
	"github.com/spf13/cobra"

	"github.com/0magnet/termanim/matrix/backdrop"

	"github.com/skycoin/skywire/pkg/flags"
)

// frameRate is how often the rain is advanced. The simulation is driven from
// elapsed time, so a slower tick costs smoothness and not speed.
const frameRate = 50 * time.Millisecond

// promptText is the prompt on the input line and the marker each run is
// prefixed with in the scrollback, so a command reads the same where it was
// typed and where its output is recorded.
const promptText = "skywire> "

// chromeRows is everything on screen that is not the scrollback: the title, the
// rule under it, the input line and the key line.
const chromeRows = 4

const (
	titleSGR = "\x1b[1;97m"
	dimSGR   = "\x1b[38;5;245m"
	resetSGR = "\x1b[0m"
)

type model struct {
	root *cobra.Command
	self string // path to this binary, the command every run shells out to

	app   *progkit.App
	input *progkit.Input

	mu   sync.Mutex
	body string // the scrollback, as the commands printed it

	top      int  // first scrollback line in view
	atBottom bool // follow new output
	quit     bool

	painter *backdrop.Painter
	width   int
	last    time.Time
}

// Run opens the console on focus and blocks until the user quits.
func Run(root, focus *cobra.Command) error {
	// Help is rendered in-process into the scrollback. coloredcobra colors via
	// fatih/color, which disables itself when the write target isn't a
	// terminal (our strings.Builder isn't), so clear its global NoColor.
	color.NoColor = false
	app, err := progkit.Open()
	if err != nil {
		return err
	}
	defer app.Close()

	m := newModel(root, focus)
	m.app = app
	done := make(chan struct{})
	defer close(done)
	go func() {
		t := time.NewTicker(frameRate)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				app.Redraw()
			}
		}
	}()
	app.Run(m.draw, m.handle)
	return nil
}

func newModel(root, focus *cobra.Command) *model {
	self, _ := os.Executable() //nolint:errcheck
	m := &model{
		root:     root,
		self:     self,
		atBottom: true,
		painter: backdrop.New(backdrop.Options{
			// The screen is composed here, so the backdrop is asked for no
			// padding of its own and told where the layout's empty space is.
			Pad:    -1,
			GapMin: 4,
			// Undimmed, as the help screen is: the cell of clear kept either
			// side of every word is what keeps the text readable.
			Force: true,
		}),
		body: renderHelp(focus),
	}
	m.input = &progkit.Input{ID: "prompt", OnSubmit: m.run}
	return m
}

// draw composes the screen as text, paints the rain behind it, and lays the
// prompt over its row.
func (m *model) draw(f *progkit.Frame) {
	if f.W != m.width {
		m.width = f.W
		m.painter.SetWidth(f.W)
	}
	now := time.Now()
	dt := 0.0
	if !m.last.IsZero() {
		dt = now.Sub(m.last).Seconds()
	}
	m.last = now

	m.mu.Lock()
	lines := wrapANSI(m.body, max(f.W, 20))
	m.mu.Unlock()
	view := max(f.H-chromeRows, 1)
	maxTop := max(len(lines)-view, 0)
	if m.atBottom || m.top > maxTop {
		m.top = maxTop
	}

	rows := make([]string, 0, f.H)
	rows = append(rows,
		titleSGR+"skywire"+resetSGR+dimSGR+" — interactive console"+resetSGR,
		dimSGR+strings.Repeat("─", f.W)+resetSGR,
	)
	for i := 0; i < view; i++ {
		r := ""
		if m.top+i < len(lines) {
			r = lines[m.top+i]
		}
		rows = append(rows, r)
	}
	rows = append(rows,
		promptText+m.input.Value(),
		dimSGR+"  enter run · !cmd real terminal · pgup/pgdn scroll · ctrl+c quit"+resetSGR)

	out := m.painter.Frame(strings.Join(rows, "\n"), dt)
	for y, l := range progkit.ParseANSI(out, tcell.StyleDefault) {
		progkit.DrawLine(f.Screen, 0, y, f.W, l)
	}
	inputRow := f.H - 2
	progkit.DrawText(f.Screen, 0, inputRow, f.W, promptText, tcell.StyleDefault)
	m.input.Style = tcell.StyleDefault.Foreground(tcolor.Default)
	m.input.Draw(f, progkit.Rect{X: len(promptText), Y: inputRow, W: f.W - len(promptText), H: 1}, true)
}

func (m *model) handle(ev tcell.Event) bool {
	view := 10
	if _, h := m.app.Screen.Size(); h > chromeRows {
		view = h - chromeRows
	}
	switch ev := ev.(type) {
	case *tcell.EventKey:
		switch {
		case ev.Key() == tcell.KeyEscape, progkit.IsCtrl(ev, 'c'):
			return false
		case ev.Key() == tcell.KeyPgUp:
			m.scroll(-view)
		case ev.Key() == tcell.KeyPgDn:
			m.scroll(view)
		case ev.Key() == tcell.KeyUp:
			m.scroll(-1)
		case ev.Key() == tcell.KeyDown:
			m.scroll(1)
		default:
			m.input.Key(ev)
		}
	case *tcell.EventMouse:
		switch {
		case ev.Buttons()&tcell.WheelUp != 0:
			m.scroll(-3)
		case ev.Buttons()&tcell.WheelDown != 0:
			m.scroll(3)
		}
	}
	return !m.quit
}

// scroll moves the view by n lines; reaching the end follows new output again.
func (m *model) scroll(n int) {
	m.atBottom = false
	m.top = max(m.top+n, 0)
	m.mu.Lock()
	lines := len(wrapANSI(m.body, max(m.width, 20)))
	m.mu.Unlock()
	_, h := m.app.Screen.Size()
	if m.top >= lines-max(h-chromeRows, 1) {
		m.atBottom = true
	}
}

// run acts on a submitted line: quit, no-op, interactive (`!`) or captured.
func (m *model) run(line string) {
	line = strings.TrimSpace(line)
	m.input.SetValue("")
	switch line {
	case "":
		return
	case "exit", "quit", "q":
		m.quit = true
		return
	}

	if strings.HasPrefix(line, "!") {
		rest := strings.TrimSpace(line[1:])
		if rest == "" {
			return
		}
		args, err := splitArgs(rest)
		if err != nil {
			m.append(promptText + line + "\n" + err.Error())
			return
		}
		m.append(promptText + line + "\n" + m.interactive(args))
		return
	}

	args, err := splitArgs(line)
	if err != nil {
		m.append(promptText + line + "\n" + err.Error())
		return
	}
	// A command path that only prints help (a group, or an explicit --help) is
	// rendered IN-PROCESS: instant, colored, and free of cursor-control
	// sequences. Anything runnable is run as a child.
	if c := m.helpTarget(args); c != nil {
		m.append(promptText + line + "\n" + renderHelp(c))
		return
	}
	go func() {
		m.append(promptText + line + "\n" + capture(m.self, args))
		m.app.Redraw()
	}()
}

// interactive hands the terminal to a child and takes it back when it exits.
func (m *model) interactive(args []string) string {
	if err := m.app.Screen.Suspend(); err != nil {
		return fmt.Sprintf("could not give up the terminal: %v", err)
	}
	c := exec.Command(m.self, args...) //nolint:gosec // self is our own os.Executable()
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	err := c.Run()
	if rerr := m.app.Screen.Resume(); rerr != nil && err == nil {
		err = rerr
	}
	if err != nil {
		return fmt.Sprintf("ran in the real terminal: %v", err)
	}
	return "ran in the real terminal (live output was not captured)"
}

// capture runs self with args and returns its combined output.
func capture(self string, args []string) string {
	out, err := exec.Command(self, args...).CombinedOutput() //nolint:gosec // self is our own os.Executable()
	body := string(out)
	if err != nil && strings.TrimSpace(body) == "" {
		body = err.Error()
	}
	if strings.TrimSpace(body) == "" {
		body = "(no output)"
	}
	return body
}

// append adds a block to the scrollback and follows it.
func (m *model) append(block string) {
	block = strings.TrimRight(block, "\n")
	m.mu.Lock()
	if m.body == "" {
		m.body = block
	} else {
		m.body += "\n" + block
	}
	m.mu.Unlock()
	m.atBottom = true
}

// wrapANSI splits s into lines no wider than w cells. Escape sequences are
// kept and take no width, and the colors in effect at a break carry on in
// the next line.
func wrapANSI(s string, w int) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		var (
			cur    strings.Builder
			active string // SGR sequences since the last reset
			width  int
			state  = -1
		)
		for len(line) > 0 {
			if line[0] == 0x1b {
				n := escapeLen(line)
				seq := line[:n]
				line = line[n:]
				state = -1 // text after a sequence starts afresh
				cur.WriteString(seq)
				if strings.HasPrefix(seq, "\x1b[") && strings.HasSuffix(seq, "m") {
					if seq == "\x1b[0m" || seq == "\x1b[m" {
						active = ""
					} else {
						active += seq
					}
				}
				continue
			}
			var cluster string
			var gw int
			cluster, line, gw, state = uniseg.FirstGraphemeClusterInString(line, state)
			if width+gw > w && width > 0 {
				cur.WriteString(resetSGR)
				out = append(out, cur.String())
				cur.Reset()
				cur.WriteString(active)
				width = 0
			}
			cur.WriteString(cluster)
			width += gw
		}
		out = append(out, cur.String())
	}
	return out
}

// escapeLen is the length of the escape sequence s starts with: a CSI up to
// its final byte, an OSC up to BEL or ST, or ESC and one byte.
func escapeLen(s string) int {
	if len(s) < 2 {
		return len(s)
	}
	switch s[1] {
	case '[':
		for i := 2; i < len(s); i++ {
			if s[i] >= 0x40 && s[i] <= 0x7e {
				return i + 1
			}
		}
	case ']':
		for i := 2; i < len(s); i++ {
			if s[i] == 0x07 {
				return i + 1
			}
			if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '\\' {
				return i + 2
			}
		}
	default:
		return 2
	}
	return len(s)
}

// helpTarget resolves args to the command whose help to show, or nil if the
// line is a runnable command that should be executed instead.
func (m *model) helpTarget(args []string) *cobra.Command {
	c, rest, err := m.root.Find(args)
	if err != nil || c == nil {
		return nil
	}
	for _, a := range args {
		if a == "--help" || a == "-h" {
			return c
		}
	}
	// A non-runnable command (a group like `cli` or `cli visor`) prints its help
	// when run with no extra args; a runnable one with args is a real command.
	if !c.Runnable() && len(rest) == 0 {
		return c
	}
	return nil
}

// renderHelp returns a command's help as `<cmd> --help` prints it — colored
// (fatih/color NoColor cleared) with the rain suppressed, since the console
// draws one continuous rain behind everything.
func renderHelp(c *cobra.Command) string {
	// coloredcobra reads fatih/color's global NoColor lazily at Help() time;
	// keep it cleared so help rendered into our off-screen buffer keeps its
	// color (Run sets this too, but a stray reset elsewhere must not silently
	// blank a subcommand's help — cli's was rendering white before this).
	color.NoColor = false
	var buf strings.Builder
	out := c.OutOrStdout()
	c.SetOut(&buf)
	flags.WithPlainHelp(func() {
		if err := c.Help(); err != nil {
			fmt.Fprintf(&buf, "help for %s: %v\n", c.CommandPath(), err)
		}
	})
	c.SetOut(out)
	return buf.String()
}

// splitArgs is a quote-aware split of a command line: single and double quotes
// group, and an unterminated quote is an error rather than a silent guess.
func splitArgs(line string) ([]string, error) {
	var args []string
	var cur strings.Builder
	inWord := false
	quote := rune(0) // 0, '\'' or '"'

	for _, r := range line {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
			inWord = true
		case r == ' ' || r == '\t':
			if inWord {
				args = append(args, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			cur.WriteRune(r)
			inWord = true
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated %c quote", quote)
	}
	if inWord {
		args = append(args, cur.String())
	}
	return args, nil
}
