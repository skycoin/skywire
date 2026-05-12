// Package cliskychat cmd/skywire-cli/commands/skychat/chat.go:
// interactive bubbletea split-pane chat against the local skychat
// app's HTTP/SSE interface.
//
// Top: scrollable viewport of message history (sent + received).
// Bottom: textinput compose line. Enter sends; Esc / Ctrl+C quits.
// A background goroutine reads the /sse stream and pushes incoming
// messages into the bubbletea program via a channel; outgoing
// messages POST to /message synchronously on Enter.
//
// `--to <pk>` pins the conversation to one remote. Messages from
// other senders still appear in the history (so you can see other
// peers trying to reach you) but Enter always sends to --to.
package cliskychat

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	"github.com/skycoin/skywire/pkg/cipher"
)

var chatCmd = &cobra.Command{
	Use:   "chat",
	Short: "Interactive chat TUI (bubbletea split-pane)",
	Long: `Interactive chat against the local skychat app.

Top pane scrolls message history; bottom pane is the compose line.
Enter sends. Esc or Ctrl+C quits. ↑/↓ or PgUp/PgDn scroll history.

Requires the skychat app to be running (default --addr 127.0.0.1:8001).
Use --to <pk> to pin outgoing messages to one recipient. When
--to is omitted, the TUI starts in "pick a peer" mode and prompts
for a PK in the compose line; on a valid hex PK the view
transitions to chat. Incoming messages from any sender still
appear in history.`,
	Run: func(cmd *cobra.Command, _ []string) {
		// Empty recipient is allowed: the TUI prompts for it.
		// Anything non-empty must parse as a valid PK up front so
		// a typo on the command line surfaces immediately rather
		// than after the TUI takes over the terminal.
		recipientPK := ""
		if recipient != "" {
			var pk cipher.PubKey
			if err := pk.Set(recipient); err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("invalid --to public key %q: %w", recipient, err))
			}
			recipientPK = pk.String()
		}
		if err := validateNetwork(sendNet); err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		if err := runChatTUI(httpAddr, recipientPK, sendNet); err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
	},
}

// chatMsg is one rendered message in the history pane. Both incoming
// (SSE) and outgoing (just-sent) messages flow into the same slice
// so the operator sees their own writes interleaved with replies.
type chatMsg struct {
	When    time.Time
	Sender  string // "you" for outgoing; remote PK (or its prefix) for incoming
	Network string
	Body    string
	Err     string // when set, this is a send-failure row (rendered red)
}

// incomingMsg wraps an SSE event for the Update loop.
type incomingMsg struct {
	msg chatMsg
}

// sseErrMsg signals the SSE stream errored / closed; the model
// renders a status line so the operator knows incoming has stopped.
type sseErrMsg struct {
	err error
}

// sentMsg is the result of a postMessage call from the Enter handler.
type sentMsg struct {
	body string
	err  error
}

type chatModel struct {
	addr      string
	recipient string
	network   string

	// awaitingRecipient is true when the operator launched the
	// TUI without --to <pk>. The textinput is repurposed as a
	// "paste the recipient PK here" prompt; Enter validates and
	// flips the model into chat mode. Mirrors the GUI's "enter
	// recipient PK in the header" flow.
	awaitingRecipient bool
	// recipientErr surfaces the last PK-parse failure inline so
	// the operator sees why their paste didn't take, instead of
	// staring at an unchanged prompt.
	recipientErr string

	history []chatMsg
	vp      viewport.Model
	input   textinput.Model

	width  int
	height int
	ready  bool

	sseLive bool
	sseErr  string

	incoming <-chan chatMsg
	sseErrCh <-chan error
}

func runChatTUI(addr, recipient, network string) error {
	in := textinput.New()
	in.Focus()
	in.CharLimit = 4096
	if recipient == "" {
		// Pick-a-peer mode: textinput accepts a 66-char hex PK.
		// CharLimit bumped to fit a PK + small slack for paste
		// noise. Prompt shape borrowed from the GUI's PK input.
		in.Placeholder = "paste recipient PK (66 hex chars); Enter to confirm, Esc to quit"
		in.Prompt = "to: "
		in.CharLimit = 128
	} else {
		in.Placeholder = "type message; Enter to send, Esc to quit"
		in.Prompt = "» "
	}

	inCh := make(chan chatMsg, 64)
	errCh := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	go streamSSE(ctx, addr, inCh, errCh)

	m := &chatModel{
		addr:              addr,
		recipient:         recipient,
		network:           network,
		input:             in,
		sseLive:           true,
		incoming:          inCh,
		sseErrCh:          errCh,
		awaitingRecipient: recipient == "",
	}
	prog := tea.NewProgram(m, tea.WithAltScreen())
	_, err := prog.Run()
	cancel()
	return err
}

// streamSSE connects to the skychat app's /sse endpoint, parses
// `data: {...}` lines, and pushes chatMsg values onto out. Sends one
// error onto errs when the stream dies (any cause) and returns.
// Context cancellation is the clean-exit signal.
func streamSSE(ctx context.Context, addr string, out chan<- chatMsg, errs chan<- error) {
	url := fmt.Sprintf("http://%s/sse", addr)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		errs <- err
		return
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		errs <- err
		return
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		errs <- fmt.Errorf("SSE status %d", resp.StatusCode)
		return
	}
	scanner := bufio.NewScanner(resp.Body)
	// SSE lines can be large for embedded payloads; bump the buffer.
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var raw struct {
			Sender  string `json:"sender"`
			Message string `json:"message"`
			Network string `json:"network,omitempty"`
		}
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &raw); err != nil {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case out <- chatMsg{
			When:    time.Now(),
			Sender:  raw.Sender,
			Network: raw.Network,
			Body:    raw.Message,
		}:
		}
	}
	if err := scanner.Err(); err != nil && ctx.Err() == nil {
		errs <- err
	}
}

func (m *chatModel) Init() tea.Cmd {
	return tea.Batch(textinput.Blink, m.waitIncoming(), m.waitSSEErr())
}

// waitIncoming returns a Cmd that blocks on the SSE channel until a
// message arrives (or the channel closes), then delivers it as an
// incomingMsg. The Update branch re-arms a fresh waitIncoming so the
// pump is always primed.
func (m *chatModel) waitIncoming() tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-m.incoming
		if !ok {
			return sseErrMsg{err: fmt.Errorf("SSE channel closed")}
		}
		return incomingMsg{msg: msg}
	}
}

func (m *chatModel) waitSSEErr() tea.Cmd {
	return func() tea.Msg {
		err, ok := <-m.sseErrCh
		if !ok {
			return nil
		}
		return sseErrMsg{err: err}
	}
}

func (m *chatModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		headerH := 1
		footerH := 2 // status + input
		if !m.ready {
			m.vp = viewport.New(msg.Width, msg.Height-headerH-footerH)
			m.vp.SetContent(m.renderHistory())
			m.ready = true
		} else {
			m.vp.Width = msg.Width
			m.vp.Height = msg.Height - headerH - footerH
		}
		m.input.Width = msg.Width - len(m.input.Prompt) - 1
		m.width = msg.Width
		m.height = msg.Height

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			return m, tea.Quit
		case "ctrl+n":
			// Cycle outgoing network type. Affects subsequent
			// Sends only — already-delivered messages keep their
			// per-message network tag. Header reflects the new
			// value immediately. Pick-a-peer mode also honors
			// this (operator can decide skynet vs dmsg before
			// they even type the recipient PK).
			if m.network == "skynet" {
				m.network = "dmsg"
			} else {
				m.network = "skynet"
			}
			return m, nil
		case "enter":
			text := strings.TrimSpace(m.input.Value())
			if text == "" {
				return m, nil
			}
			// In pick-a-peer mode, Enter parses the PK and
			// transitions into chat mode. The textinput is
			// then reset with the chat-mode prompt + placeholder
			// for the actual messaging flow.
			if m.awaitingRecipient {
				var pk cipher.PubKey
				if err := pk.Set(text); err != nil {
					m.recipientErr = fmt.Sprintf("invalid PK: %v", err)
					m.input.Reset()
					return m, nil
				}
				m.recipient = pk.String()
				m.awaitingRecipient = false
				m.recipientErr = ""
				m.input.Reset()
				m.input.Placeholder = "type message; Enter to send, Esc to quit"
				m.input.Prompt = "» "
				m.input.CharLimit = 4096
				if m.ready {
					// Header now shows the recipient, so the
					// "to <prompt>" line widens; refit input.
					m.input.Width = m.width - len(m.input.Prompt) - 1
				}
				return m, nil
			}
			m.input.Reset()
			toAddr := m.addr
			toRecipient := m.recipient
			toNetwork := m.network
			cmds = append(cmds, func() tea.Msg {
				_, err := postMessage(toAddr, toRecipient, text, toNetwork, 0)
				return sentMsg{body: text, err: err}
			})
		case "pgup":
			m.vp.HalfPageUp()
		case "pgdown":
			m.vp.HalfPageDown()
		}

	case incomingMsg:
		m.appendMsg(msg.msg)
		cmds = append(cmds, m.waitIncoming())

	case sentMsg:
		if msg.err != nil {
			m.appendMsg(chatMsg{
				When:   time.Now(),
				Sender: "you",
				Body:   msg.body,
				Err:    msg.err.Error(),
			})
		} else {
			m.appendMsg(chatMsg{
				When:    time.Now(),
				Sender:  "you",
				Network: m.network,
				Body:    msg.body,
			})
		}

	case sseErrMsg:
		m.sseLive = false
		if msg.err != nil {
			m.sseErr = msg.err.Error()
		}
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	cmds = append(cmds, cmd)
	if m.ready {
		m.vp, cmd = m.vp.Update(msg)
		cmds = append(cmds, cmd)
	}
	return m, tea.Batch(cmds...)
}

// appendMsg pushes a row onto the history slice, re-renders, and
// auto-scrolls to bottom if the user wasn't manually scrolled up.
func (m *chatModel) appendMsg(msg chatMsg) {
	m.history = append(m.history, msg)
	if !m.ready {
		return
	}
	wasAtBottom := m.vp.AtBottom()
	m.vp.SetContent(m.renderHistory())
	if wasAtBottom {
		m.vp.GotoBottom()
	}
}

func (m *chatModel) renderHistory() string {
	if len(m.history) == 0 {
		return lipgloss.NewStyle().Foreground(lipgloss.Color("241")).
			Render("(no messages yet — type below and hit Enter)")
	}
	youStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("39")).Bold(true)
	peerStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("213")).Bold(true)
	errStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("241"))

	var b strings.Builder
	for _, msg := range m.history {
		ts := msg.When.Format("15:04:05")
		var sender string
		if msg.Sender == "you" {
			sender = youStyle.Render("you")
		} else {
			sender = peerStyle.Render(shortPK(msg.Sender))
		}
		net := ""
		if msg.Network != "" {
			net = dimStyle.Render("/" + msg.Network)
		}
		body := msg.Body
		if msg.Err != "" {
			body = errStyle.Render(fmt.Sprintf("✗ send failed: %s — %q", msg.Err, msg.Body))
		}
		fmt.Fprintf(&b, "%s %s%s  %s\n", dimStyle.Render(ts), sender, net, body)
	}
	return b.String()
}

// shortPK trims the on-screen sender label to a readable head; the
// SSE feed sends the full 66-char hex but a chat history is unreadable
// at that width. The first 12 chars uniquely identify any peer in
// practice and keep the visual alignment clean.
func shortPK(pk string) string {
	if len(pk) <= 12 {
		return pk
	}
	return pk[:12] + "…"
}

func (m *chatModel) View() string {
	if !m.ready {
		return "Initializing…\n"
	}
	hdrStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205"))
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	errStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("196"))

	// Header differs between pick-a-peer and chat modes. In
	// pick-a-peer the "to <pk>" slot is replaced with a prompt
	// hint and (if applicable) the last parse error.
	var header string
	if m.awaitingRecipient {
		hint := dimStyle.Render("pick a recipient PK below")
		if m.recipientErr != "" {
			hint = errStyle.Render(m.recipientErr)
		}
		header = fmt.Sprintf("%s  %s  network=%s  addr=%s",
			hdrStyle.Render("skychat"), hint, m.network, m.addr)
	} else {
		header = fmt.Sprintf("%s  %s  network=%s  addr=%s",
			hdrStyle.Render("skychat"),
			dimStyle.Render("to "+shortPK(m.recipient)),
			m.network, m.addr,
		)
	}
	if !m.sseLive {
		header += "  " + errStyle.Render("[SSE down: "+m.sseErr+"]")
	}

	var footer string
	if m.awaitingRecipient {
		footer = dimStyle.Render("Enter to confirm recipient | Ctrl+N toggle network | Esc/Ctrl+C quit")
	} else {
		footer = dimStyle.Render("Enter send | ↑/↓ PgUp/PgDn scroll | Ctrl+N toggle network | Esc/Ctrl+C quit")
	}

	var b strings.Builder
	b.WriteString(header)
	b.WriteString("\n")
	b.WriteString(m.vp.View())
	b.WriteString("\n")
	b.WriteString(m.input.View())
	b.WriteString("\n")
	b.WriteString(footer)
	return b.String()
}
