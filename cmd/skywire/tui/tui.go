// Package tui cmd/skywire/tui/tui.go
//
// An interactive console over the skywire command line: a scrollback of
// everything run so far, a prompt to type the next command into, and the code
// rain falling behind both. `skywire --tui`, or `--tui` alongside any `--help`.
//
// The console starts on the help for the command it was opened on and runs the
// real binary from there. A plain line is run captured — its output is folded
// into the scrollback. A line beginning with `!` is run in the real terminal:
// the TUI steps aside, hands the child the screen (so a pty exec, a live plot,
// anything streaming or full-screen works), and resumes when it exits.
package tui

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/fatih/color"
	"github.com/spf13/cobra"

	"github.com/0magnet/termanim/matrix/backdrop"

	"github.com/skycoin/skywire/pkg/flags"
)

// frameRate is how often the rain is advanced. The simulation is tuned at 30
// steps a second and is driven from elapsed time, so a slower tick costs
// smoothness and not speed — the rain falls at the same rate either way.
const frameRate = 50 * time.Millisecond

// promptText is the prompt on the input line and the marker each run is
// prefixed with in the scrollback, so a command reads the same where it was
// typed and where its output is recorded.
const promptText = "skywire> "

type tickMsg time.Time

// outputMsg carries the result of a captured run back to Update: the line the
// user typed and the child's combined stdout+stderr.
type outputMsg struct {
	line string
	body string
}

// execDoneMsg says an interactive (`!`) run has finished and the alt-screen is
// back. err is whatever the child exited with, for the note in the scrollback.
type execDoneMsg struct {
	line string
	err  error
}

type model struct {
	root *cobra.Command
	self string // path to this binary, the command every run shells out to

	out   viewport.Model  // the scrollback
	input textinput.Model // the prompt line

	// body accumulates the scrollback text. The viewport holds a wrapped copy;
	// this is the source it is re-wrapped from when the width changes.
	body string

	painter *backdrop.Painter
	w, h    int

	// dt is elapsed seconds banked by the ticks and spent by the next frame.
	dt       float64
	lastTick time.Time
}

// Run opens the console on cmd and blocks until the user quits.
func Run(root, focus *cobra.Command) error {
	// Help is rendered in-process into the scrollback. coloredcobra colors via
	// fatih/color, which renders lazily and disables itself when the write
	// target isn't a terminal (our strings.Builder isn't) — so clear its global
	// NoColor, and the codes it emits show on the alt-screen. (Forcing
	// gookit/color here did nothing: coloredcobra doesn't use gookit.)
	color.NoColor = false
	m := newModel(root, focus)
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
	return err
}

func newModel(root, focus *cobra.Command) *model {
	self, _ := os.Executable() //nolint:errcheck

	in := textinput.New()
	in.Prompt = promptText
	in.Focus()

	m := &model{
		root:  root,
		self:  self,
		input: in,
		painter: backdrop.New(backdrop.Options{
			// The screen is composed here, so the backdrop is asked for no
			// padding of its own and told where the layout's empty space is.
			Pad:    -1,
			GapMin: 4,
			// Undimmed, as the help screen is: the cell of clear kept either
			// side of every word is what keeps the text readable, and GapMin
			// already confines the rain to the space the layout left empty.
			Force: true,
		}),
	}

	// Open on the help for the focused command, colored the same as `--help`.
	m.body = renderHelp(focus)
	return m
}

func (m *model) Init() tea.Cmd { return tick() }

func tick() tea.Cmd {
	return tea.Tick(frameRate, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tickMsg:
		if t := time.Time(msg); !m.lastTick.IsZero() {
			m.dt += t.Sub(m.lastTick).Seconds()
		}
		m.lastTick = time.Time(msg)
		return m, tick()

	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		// The framework is the authority on the size, not the terminal.
		m.painter.SetWidth(msg.Width)
		m.layout()
		return m, nil

	case outputMsg:
		m.append(promptText + msg.line + "\n" + msg.body)
		return m, nil

	case execDoneMsg:
		note := "ran in the real terminal (live output was not captured)"
		if msg.err != nil {
			note = fmt.Sprintf("ran in the real terminal: %v", msg.err)
		}
		m.append(promptText + msg.line + "\n" + note)
		return m, nil

	case tea.KeyMsg:
		return m.key(msg)
	}
	return m, nil
}

func (m *model) key(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC, tea.KeyEsc:
		return m, tea.Quit

	case tea.KeyEnter:
		return m.run(strings.TrimSpace(m.input.Value()))

	// The scrollback scrolls independently: some output is longer than fits.
	case tea.KeyPgDown:
		m.out.PageDown()
		return m, nil
	case tea.KeyPgUp:
		m.out.PageUp()
		return m, nil
	case tea.KeyUp:
		m.out.ScrollUp(1)
		return m, nil
	case tea.KeyDown:
		m.out.ScrollDown(1)
		return m, nil
	}

	// Everything else is for the input line.
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// run acts on a submitted line: quit, no-op, interactive (`!`) or captured.
func (m *model) run(line string) (tea.Model, tea.Cmd) {
	m.input.Reset()

	switch line {
	case "":
		return m, nil
	case "exit", "quit", "q":
		return m, tea.Quit
	}

	if strings.HasPrefix(line, "!") {
		rest := strings.TrimSpace(line[1:])
		if rest == "" {
			return m, nil
		}
		args, err := splitArgs(rest)
		if err != nil {
			m.append(promptText + line + "\n" + err.Error())
			return m, nil
		}
		c := exec.Command(m.self, args...) //nolint:gosec // self is our own os.Executable()
		return m, tea.ExecProcess(c, func(err error) tea.Msg {
			return execDoneMsg{line: line, err: err}
		})
	}

	args, err := splitArgs(line)
	if err != nil {
		m.append(promptText + line + "\n" + err.Error())
		return m, nil
	}

	// A command path that only prints help (a group, or an explicit --help) is
	// rendered IN-PROCESS: instant, colored, and free of the cursor-control
	// sequences a subprocess pty folds into the scrollback (the scroll garbage).
	// Anything actually runnable is run as a child — its effects belong in a
	// process, and its plain output has no control sequences either.
	if c := m.helpTarget(args); c != nil {
		m.append(promptText + line + "\n" + renderHelp(c))
		return m, nil
	}
	return m, captureCmd(m.self, line, args)
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

// captureCmd runs self with args, capturing combined stdout+stderr, and reports
// it as an outputMsg.
func captureCmd(self, line string, args []string) tea.Cmd {
	return func() tea.Msg {
		out, err := exec.Command(self, args...).CombinedOutput() //nolint:gosec // self is our own os.Executable()
		body := string(out)
		if err != nil && strings.TrimSpace(body) == "" {
			body = err.Error()
		}
		if strings.TrimSpace(body) == "" {
			body = "(no output)"
		}
		return outputMsg{line: line, body: body}
	}
}

// append adds a block to the scrollback and scrolls to the bottom.
func (m *model) append(block string) {
	block = strings.TrimRight(block, "\n")
	if m.body == "" {
		m.body = block
	} else {
		m.body += "\n" + block
	}
	m.setContent()
	m.out.GotoBottom()
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

// layout sizes the scrollback and input to the current terminal.
func (m *model) layout() {
	w := m.w
	if w < 20 {
		w = 20
	}
	h := m.h - chromeRows
	if h < 1 {
		h = 1
	}
	m.out.Width, m.out.Height = w, h
	m.input.Width = w - len(promptText) - 1
	m.setContent()
	m.out.GotoBottom()
}

// setContent re-wraps the scrollback body to the current width.
func (m *model) setContent() {
	if m.out.Width <= 0 {
		return
	}
	// lipgloss wraps with the escape sequences accounted for, which matters:
	// help arrives from coloredcobra already colored.
	m.out.SetContent(lipgloss.NewStyle().Width(m.out.Width).Render(m.body))
}

// chromeRows is everything on screen that is not the scrollback: the title, the
// rule under it, the input line and the key line.
const chromeRows = 4

var (
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15"))
	dimStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
)

// View is the composed screen with the rain painted in behind it.
//
// The two halves are kept apart: screen decides what the program looks like
// and paint decides what is behind it, and only the first is worth a test.
func (m *model) View() string {
	if m.w == 0 {
		// No size yet: bubbletea sends the first WindowSizeMsg right after
		// Init, so this is one frame at most.
		return ""
	}
	s := m.screen()

	// dt is what has actually elapsed, accumulated by the ticks, so the rain
	// falls at its own rate however often the screen happens to be redrawn. A
	// redraw that is not a tick — a keypress — passes zero and does not move
	// it, or the rain would run at the speed the user types.
	out := m.painter.Frame(s, m.dt)
	m.dt = 0
	return out
}

// screen composes the frame as plain text: title, scrollback, input, key line.
// Exactly m.h rows, none wider than m.w.
func (m *model) screen() string {
	rows := make([]string, 0, m.h)
	rows = append(rows,
		titleStyle.Render("skywire")+dimStyle.Render(" — interactive console"),
		dimStyle.Render(strings.Repeat("─", m.w)),
	)

	body := strings.Split(m.out.View(), "\n")
	lines := m.h - chromeRows
	for i := 0; i < lines; i++ {
		r := ""
		if i < len(body) {
			r = body[i]
		}
		rows = append(rows, r)
	}

	rows = append(rows,
		m.input.View(),
		dimStyle.Render("  enter run · !cmd real terminal · pgup/pgdn scroll · ctrl+c quit"))

	return strings.Join(rows, "\n")
}
