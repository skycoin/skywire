// Package clidmsg cmd/skywire-cli/commands/dmsg/chat_tui.go c5-cli-dmsg
//
// The screen of `cli dmsg chat`, drawn with progkit: the conversation, a line
// to type into, and the recipient to pick first when none was given. In websh
// the line is a real text field and the conversation selectable text.
package clidmsg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/0magnet/progkit"
	"github.com/gdamore/tcell/v3"
	"github.com/gdamore/tcell/v3/color"

	"github.com/skycoin/skywire/pkg/cipher"
	dmsg "github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
)

var (
	chatTitle = tcell.StyleDefault.Bold(true).Foreground(color.PaletteColor(205))
	chatDim   = tcell.StyleDefault.Foreground(color.PaletteColor(241))
	chatErr   = tcell.StyleDefault.Foreground(color.PaletteColor(196))
)

type chatModel struct {
	ctx       context.Context
	dmsgC     *dmsg.Client
	myPK      string
	recipient string
	port      uint16

	awaitingRecipient bool
	recipientErr      string

	mu        sync.Mutex
	history   []chatRow
	listenErr string

	view  *progkit.Text
	input *progkit.Input

	// peerConnMu guards reuse of an open outbound stream to the pinned
	// recipient. Re-dialing per message is correct but burns dmsg session
	// capacity for a conversation of any length.
	peerConnMu sync.Mutex
	peerConn   net.Conn
}

func runChatTUI(ctx context.Context, log *logging.Logger, dmsgC *dmsg.Client,
	myPK cipher.PubKey, recipient string, incoming <-chan incomingChatMsg, errs <-chan error) error {

	app, err := progkit.Open()
	if err != nil {
		log.WithError(err).Debug("tui open")
		return err
	}
	defer app.Close()

	m := &chatModel{
		ctx:               ctx,
		dmsgC:             dmsgC,
		myPK:              myPK.String(),
		recipient:         recipient,
		port:              chatPort,
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
			case msg, ok := <-incoming:
				if !ok {
					m.setListenErr(errors.New("inbound channel closed"))
					app.Redraw()
					return
				}
				m.append(chatRow{When: nonZeroTS(msg.TS), Sender: msg.SenderPK, Body: msg.Message})
				app.Redraw()
			case err, ok := <-errs:
				if ok && err != nil {
					m.setListenErr(err)
					app.Redraw()
				}
			}
		}
	}()

	app.Run(m.draw, func(ev tcell.Event) bool {
		switch ev := ev.(type) {
		case *tcell.EventKey:
			switch {
			case ev.Key() == tcell.KeyEscape, progkit.IsCtrl(ev, 'c'):
				return false
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
	m.closePeerConn()
	return nil
}

func (m *chatModel) setPlaceholder() {
	if m.awaitingRecipient {
		m.input.Placeholder = "paste recipient PK (66 hex chars); Enter to confirm, Esc to quit"
	} else {
		m.input.Placeholder = "type message; Enter to send, Esc to quit"
	}
}

func (m *chatModel) setListenErr(err error) {
	m.mu.Lock()
	m.listenErr = err.Error()
	m.mu.Unlock()
}

// submit confirms the recipient, or sends a message to it.
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
	go func() {
		row := chatRow{When: time.Now().UTC(), Sender: m.myPK, Body: text, Self: true}
		if err := m.sendOne(text); err != nil {
			row.Err = err.Error()
		}
		m.append(row)
		app.Redraw()
	}()
}

func (m *chatModel) append(row chatRow) {
	m.mu.Lock()
	m.history = append(m.history, row)
	m.mu.Unlock()
}

func (m *chatModel) draw(f *progkit.Frame) {
	m.mu.Lock()
	history := append([]chatRow(nil), m.history...)
	listenErr := m.listenErr
	m.mu.Unlock()
	m.view.SetLines(renderHistory(history))

	header, rest := f.Size().SplitTop(2)
	body, foot := rest.SplitBottom(2)
	x := progkit.DrawText(f.Screen, 0, header.Y, header.W, "dmsg chat  ", chatTitle)
	switch {
	case m.awaitingRecipient && m.recipientErr != "":
		x += progkit.DrawText(f.Screen, x, header.Y, header.W-x, m.recipientErr, chatErr)
	case m.awaitingRecipient:
		x += progkit.DrawText(f.Screen, x, header.Y, header.W-x, "pick a recipient PK below", chatDim)
	default:
		x += progkit.DrawText(f.Screen, x, header.Y, header.W-x, "to "+m.recipient, chatDim)
	}
	x += progkit.DrawText(f.Screen, x, header.Y, header.W-x, fmt.Sprintf("  port=:%d", m.port), tcell.StyleDefault)
	if listenErr != "" {
		progkit.DrawText(f.Screen, x+2, header.Y, header.W-x-2, "[listen err: "+listenErr+"]", chatErr)
	}
	progkit.DrawText(f.Screen, 0, header.Y+1, header.W, "you = "+m.myPK, chatDim)

	m.view.Draw(f, body)

	prompt := "» "
	if m.awaitingRecipient {
		prompt = "to: "
	}
	n := progkit.DrawText(f.Screen, 0, foot.Y, foot.W, prompt, tcell.StyleDefault)
	m.input.Draw(f, progkit.Rect{X: n, Y: foot.Y, W: foot.W - n, H: 1}, true)
	hint := "Enter send | ↑/↓ PgUp/PgDn scroll | Esc/Ctrl+C quit"
	if m.awaitingRecipient {
		hint = "Enter confirm recipient | Esc/Ctrl+C quit"
	}
	progkit.DrawText(f.Screen, 0, foot.Y+1, foot.W, hint, chatDim)
}

func renderHistory(history []chatRow) []progkit.Line {
	if len(history) == 0 {
		return []progkit.Line{{{Text: "(no messages yet — type below and hit Enter)", Style: chatDim}}}
	}
	you := tcell.StyleDefault.Foreground(color.PaletteColor(39)).Bold(true)
	peer := tcell.StyleDefault.Foreground(color.PaletteColor(213)).Bold(true)
	out := make([]progkit.Line, 0, len(history))
	for _, row := range history {
		sender := progkit.Span{Text: "you", Style: you}
		if !row.Self {
			sender = progkit.Span{Text: row.Sender, Style: peer}
		}
		body := progkit.Span{Text: row.Body, Style: tcell.StyleDefault}
		if row.Err != "" {
			body = progkit.Span{Text: fmt.Sprintf("✗ send failed: %s — %q", row.Err, row.Body), Style: chatErr}
		}
		out = append(out, progkit.Line{
			{Text: row.When.Format("15:04:05") + " ", Style: chatDim},
			sender,
			{Text: "  ", Style: tcell.StyleDefault},
			body,
		})
	}
	return out
}

func (m *chatModel) sendOne(body string) error {
	if m.recipient == "" {
		return errors.New("no recipient")
	}
	var rpk cipher.PubKey
	if err := rpk.Set(m.recipient); err != nil {
		return err
	}
	c, err := m.ensurePeerConn(rpk)
	if err != nil {
		return err
	}
	env := outgoingChatMsg{
		SenderPK: m.myPK,
		Message:  body,
		TS:       time.Now().UTC(),
		Network:  "dmsg",
	}
	payload, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	if err := writeFrame(c, payload); err != nil {
		// Stream may have died; drop the cached conn so the next
		// send re-dials. Don't bury the error; surface it on the
		// failure row.
		m.closePeerConn()
		return fmt.Errorf("write: %w", err)
	}
	return nil
}

// ensurePeerConn returns an open framed stream to the pinned
// recipient, dialing one if needed. Cached between calls — closed
// when sendOne sees a write error so a half-dead conn doesn't
// persist.
func (m *chatModel) ensurePeerConn(rpk cipher.PubKey) (net.Conn, error) {
	m.peerConnMu.Lock()
	defer m.peerConnMu.Unlock()
	if m.peerConn != nil {
		return m.peerConn, nil
	}
	ctx, cancel := context.WithTimeout(m.ctx, 15*time.Second)
	defer cancel()
	stream, err := m.dmsgC.DialStream(ctx, dmsg.Addr{PK: rpk, Port: chatDmsgPort})
	if err != nil {
		return nil, fmt.Errorf("dial: %w", err)
	}
	m.peerConn = stream
	return stream, nil
}

func (m *chatModel) closePeerConn() {
	m.peerConnMu.Lock()
	defer m.peerConnMu.Unlock()
	if m.peerConn != nil {
		_ = m.peerConn.Close() //nolint:errcheck
		m.peerConn = nil
	}
}

func nonZeroTS(t time.Time) time.Time {
	if t.IsZero() {
		return time.Now().UTC()
	}
	return t
}
