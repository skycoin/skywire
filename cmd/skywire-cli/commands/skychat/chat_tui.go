// Package cliskychat cmd/skywire-cli/commands/skychat/chat_tui.go c5-cli-skychat
//
// The 1:1 chat screen, drawn with progkit. The command spec stays in chat.go.
// In websh the message line is a real text field and the conversation
// selectable text.
package cliskychat

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/0magnet/progkit"
	"github.com/gdamore/tcell/v3"
	"github.com/gdamore/tcell/v3/color"

	"github.com/skycoin/skywire/pkg/cipher"
)

var (
	titleStyle = tcell.StyleDefault.Bold(true).Foreground(color.PaletteColor(205))
	dimStyle   = tcell.StyleDefault.Foreground(color.PaletteColor(241))
	errStyle   = tcell.StyleDefault.Foreground(color.PaletteColor(196))
	youStyle   = tcell.StyleDefault.Foreground(color.PaletteColor(39)).Bold(true)
	peerStyle  = tcell.StyleDefault.Foreground(color.PaletteColor(213)).Bold(true)
)

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

type chatModel struct {
	addr      string
	recipient string
	network   string

	// awaitingRecipient is true until a recipient PK is given: the line
	// then takes the PK, and Enter switches to chatting.
	awaitingRecipient bool
	recipientErr      string

	mu      sync.Mutex
	history []chatMsg
	sseErr  string

	view  *progkit.Text
	input *progkit.Input
}

func runChatTUI(addr, recipient, network string) error {
	app, err := progkit.Open()
	if err != nil {
		return err
	}
	defer app.Close()

	inCh := make(chan chatMsg, 64)
	errCh := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go streamSSE(ctx, addr, inCh, errCh)

	m := &chatModel{
		addr:              addr,
		recipient:         recipient,
		network:           network,
		awaitingRecipient: recipient == "",
		view:              &progkit.Text{ID: "history", Follow: true, Selectable: true},
		input:             &progkit.Input{ID: "line"},
	}
	m.setPlaceholder()
	m.input.OnSubmit = func(text string) { m.submit(app, text) }

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case msg := <-inCh:
				m.append(msg)
				app.Redraw()
			case err := <-errCh:
				m.mu.Lock()
				m.sseErr = err.Error()
				m.mu.Unlock()
				app.Redraw()
			}
		}
	}()

	app.Run(m.draw, func(ev tcell.Event) bool {
		switch ev := ev.(type) {
		case *tcell.EventKey:
			switch {
			case ev.Key() == tcell.KeyEscape, progkit.IsCtrl(ev, 'c'):
				return false
			case progkit.IsCtrl(ev, 'n'):
				m.network = otherNetwork(m.network)
			case ev.Key() == tcell.KeyPgUp, ev.Key() == tcell.KeyPgDn, ev.Key() == tcell.KeyUp, ev.Key() == tcell.KeyDown:
				m.view.Key(ev)
			default:
				m.input.Key(ev)
			}
		case *tcell.EventMouse:
			m.view.Mouse(ev)
		}
		return true
	})
	return nil
}

// otherNetwork cycles the network outgoing messages use. Messages already
// delivered keep the network they went over.
func otherNetwork(n string) string {
	if n == "skynet" {
		return "dmsg"
	}
	return "skynet"
}

func (m *chatModel) setPlaceholder() {
	if m.awaitingRecipient {
		m.input.Placeholder = "paste recipient PK (66 hex chars); Enter to confirm, Esc to quit"
	} else {
		m.input.Placeholder = "type message; Enter to send, Esc to quit"
	}
}

func (m *chatModel) submit(app *progkit.App, text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	m.input.SetValue("")
	if m.awaitingRecipient {
		var pk cipher.PubKey
		if err := pk.Set(text); err != nil {
			m.recipientErr = fmt.Sprintf("invalid PK: %v", err)
			return
		}
		m.recipient = pk.String()
		m.awaitingRecipient = false
		m.recipientErr = ""
		m.setPlaceholder()
		return
	}
	addr, recipient, network := m.addr, m.recipient, m.network
	go func() {
		row := chatMsg{When: time.Now(), Sender: "you", Network: network, Body: text}
		if _, err := postMessage(addr, recipient, text, network, 0); err != nil {
			row.Network, row.Err = "", err.Error()
		}
		m.append(row)
		app.Redraw()
	}()
}

func (m *chatModel) append(msg chatMsg) {
	m.mu.Lock()
	m.history = append(m.history, msg)
	m.mu.Unlock()
}

func (m *chatModel) draw(f *progkit.Frame) {
	m.mu.Lock()
	history := append([]chatMsg(nil), m.history...)
	sseErr := m.sseErr
	m.mu.Unlock()
	rows := make([]convoMessage, len(history))
	for i, h := range history {
		rows[i] = convoMessage{When: h.When, Sender: h.Sender, Network: h.Network, Body: h.Body, Err: h.Err, Self: h.Sender == "you"}
	}
	m.view.SetLines(renderConvo(rows))

	header, rest := f.Size().SplitTop(1)
	body, foot := rest.SplitBottom(2)
	x := progkit.DrawText(f.Screen, 0, header.Y, header.W, "skychat  ", titleStyle)
	switch {
	case m.awaitingRecipient && m.recipientErr != "":
		x += progkit.DrawText(f.Screen, x, header.Y, header.W-x, m.recipientErr, errStyle)
	case m.awaitingRecipient:
		x += progkit.DrawText(f.Screen, x, header.Y, header.W-x, "pick a recipient PK below", dimStyle)
	default:
		x += progkit.DrawText(f.Screen, x, header.Y, header.W-x, "to "+m.recipient, dimStyle)
	}
	x += progkit.DrawText(f.Screen, x, header.Y, header.W-x, fmt.Sprintf("  network=%s  addr=%s", m.network, m.addr), tcell.StyleDefault)
	if sseErr != "" {
		progkit.DrawText(f.Screen, x+2, header.Y, header.W-x-2, "[SSE down: "+sseErr+"]", errStyle)
	}

	m.view.Draw(f, body)

	prompt := "» "
	if m.awaitingRecipient {
		prompt = "to: "
	}
	n := progkit.DrawText(f.Screen, 0, foot.Y, foot.W, prompt, tcell.StyleDefault)
	m.input.Draw(f, progkit.Rect{X: n, Y: foot.Y, W: foot.W - n, H: 1}, true)
	hint := "Enter send | ↑/↓ PgUp/PgDn scroll | Ctrl+N toggle network | Esc/Ctrl+C quit"
	if m.awaitingRecipient {
		hint = "Enter to confirm recipient | Ctrl+N toggle network | Esc/Ctrl+C quit"
	}
	progkit.DrawText(f.Screen, 0, foot.Y+1, foot.W, hint, dimStyle)
}

// renderConvo is a conversation as styled lines, oldest first.
func renderConvo(hist []convoMessage) []progkit.Line {
	if len(hist) == 0 {
		return []progkit.Line{{{Text: "(no messages yet — type below and hit Enter)", Style: dimStyle}}}
	}
	out := make([]progkit.Line, 0, len(hist))
	for _, msg := range hist {
		sender := progkit.Span{Text: msg.Sender, Style: peerStyle}
		if msg.Self {
			sender = progkit.Span{Text: "you", Style: youStyle}
		}
		line := progkit.Line{{Text: msg.When.Format("15:04:05") + " ", Style: dimStyle}, sender}
		if msg.Network != "" {
			line = append(line, progkit.Span{Text: " /" + msg.Network, Style: dimStyle})
		}
		if msg.Err != "" {
			line = append(line, progkit.Span{Text: fmt.Sprintf("  ✗ send failed: %s — %q", msg.Err, msg.Body), Style: errStyle})
		} else {
			line = append(line, progkit.Span{Text: "  " + msg.Body, Style: tcell.StyleDefault})
		}
		out = append(out, line)
	}
	return out
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
	resp, err := chatClient.Do(req)
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
