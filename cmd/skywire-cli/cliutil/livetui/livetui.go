// Package livetui — small bubbletea-based "watch this output"
// helper. Wraps any function that produces a string in a scrollable
// viewport that re-renders on a configurable tick. Used by CLI
// commands that want a watch(1)-style live refresh without taking
// a dependency on watch(1) and without the "every tick rewrites the
// whole screen" terminal-jump experience.
//
// Use: build a Refresh callback that returns the snapshot the user
// should see RIGHT NOW (as a string). Pass it to Run with an
// interval. Run blocks until the user hits q / Ctrl+C; while it's
// running it calls Refresh every interval and pipes the result into
// a viewport that the user can scroll up/down independently of new
// frames arriving.
//
// The viewport preserves scroll position when new content streams
// in — same Ink-style "scrollable while updating" pattern Claude
// Code uses, mirrored here for skywire CLI commands.
package livetui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Refresh produces the snapshot to display on each tick. ctx is
// canceled when the user quits the TUI; long-running Refresh
// implementations should respect it so the program shuts down
// promptly. Errors are surfaced in the header line (red) but
// don't terminate the loop — operators usually want to watch a
// transient error recover live rather than have the TUI die.
type Refresh func(ctx context.Context) (string, error)

// Options tunes a Run invocation. All fields optional; sensible
// defaults below.
type Options struct {
	// Title shown in the header. Defaults to "skywire live".
	Title string
	// Interval between Refresh calls. Defaults to 1s. Don't go
	// below ~200ms — bubbletea's render budget gets uncomfortable
	// and the spinner stutters.
	Interval time.Duration
}

func (o Options) withDefaults() Options {
	if o.Title == "" {
		o.Title = "skywire live"
	}
	if o.Interval <= 0 {
		o.Interval = time.Second
	}
	return o
}

// Run blocks running the TUI. Returns nil on user-initiated quit
// (q / Ctrl+C); returns the tea.Program error otherwise.
func Run(refresh Refresh, opts Options) error {
	opts = opts.withDefaults()
	ctx, cancel := context.WithCancel(context.Background())
	sp := spinner.New()
	sp.Spinner = spinner.Line
	sp.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("86"))

	m := &model{
		refresh:  refresh,
		interval: opts.Interval,
		title:    opts.Title,
		ctx:      ctx,
		cancel:   cancel,
		spinner:  sp,
	}
	prog := tea.NewProgram(m, tea.WithAltScreen())
	_, err := prog.Run()
	cancel()
	return err
}

type tickMsg time.Time

type refreshResult struct {
	content string
	err     error
}

type model struct {
	refresh  Refresh
	interval time.Duration
	title    string

	ctx    context.Context
	cancel context.CancelFunc

	viewport viewport.Model
	spinner  spinner.Model
	ready    bool
	width    int
	height   int

	content string
	lastErr string
	tickCnt int
	started time.Time
	quit    bool
}

func (m *model) Init() tea.Cmd {
	m.started = time.Now()
	return tea.Batch(
		m.tickNow(),
		m.spinner.Tick,
	)
}

// tickNow fires a Refresh immediately (used at startup so the first
// frame is real content, not a blank screen).
func (m *model) tickNow() tea.Cmd {
	return func() tea.Msg {
		out, err := m.refresh(m.ctx)
		return refreshResult{content: out, err: err}
	}
}

func (m *model) tickCmd() tea.Cmd {
	return tea.Tick(m.interval, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			m.quit = true
			m.cancel()
			return m, tea.Quit
		case "home", "g":
			m.viewport.GotoTop()
		case "end", "G":
			m.viewport.GotoBottom()
		}

	case tea.WindowSizeMsg:
		headerH := 2
		footerH := 1
		if !m.ready {
			m.viewport = viewport.New(msg.Width, msg.Height-headerH-footerH)
			m.viewport.YPosition = headerH
			m.viewport.SetContent(m.content)
			m.ready = true
		} else {
			m.viewport.Width = msg.Width
			m.viewport.Height = msg.Height - headerH - footerH
		}
		m.width = msg.Width
		m.height = msg.Height

	case tickMsg:
		// Fire a fresh refresh and queue the next tick.
		cmds = append(cmds, m.tickNow(), m.tickCmd())

	case refreshResult:
		m.tickCnt++
		if msg.err != nil {
			m.lastErr = msg.err.Error()
		} else {
			m.lastErr = ""
			m.content = msg.content
			if m.ready {
				wasAtBottom := m.viewport.AtBottom()
				m.viewport.SetContent(m.content)
				if wasAtBottom {
					m.viewport.GotoBottom()
				}
			}
		}
		// Don't requeue tickCmd here — tickMsg branch did that already.
		// On startup, the first refreshResult arrives WITHOUT a prior
		// tickMsg (we called tickNow in Init); kick the periodic
		// schedule off now.
		if m.tickCnt == 1 {
			cmds = append(cmds, m.tickCmd())
		}

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		cmds = append(cmds, cmd)
	}

	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	cmds = append(cmds, cmd)
	return m, tea.Batch(cmds...)
}

func (m *model) View() string {
	if m.quit {
		return ""
	}
	if !m.ready {
		return "Initializing...\n"
	}

	hdrStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205"))
	errStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("241"))

	elapsed := time.Since(m.started).Truncate(time.Second)
	hdr := fmt.Sprintf("%s %s | tick #%d every %s | elapsed %s",
		m.spinner.View(),
		hdrStyle.Render(m.title),
		m.tickCnt,
		m.interval,
		elapsed,
	)
	if m.lastErr != "" {
		hdr += "  " + errStyle.Render("err: "+m.lastErr)
	}

	footer := dimStyle.Render("↑/↓ scroll | g/G top/bottom | q quit")

	// Use strings.Builder to avoid the trailing-newline trim issue
	// some terminals show when viewport content ends with \n.
	var b strings.Builder
	b.WriteString(hdr)
	b.WriteString("\n")
	b.WriteString(m.viewport.View())
	b.WriteString("\n")
	b.WriteString(footer)
	return b.String()
}
