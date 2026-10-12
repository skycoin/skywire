// Package cliskychat cmd/skywire-cli/commands/skychat/tui_unified.go c4-vis-cli
//
// The unified skychat screen, drawn with progkit: a picker listing every
// conversation the local visor knows (1:1 peers and group memberships), and
// the conversation picked. New conversations start from the picker too.
//
// `skywire cli skychat chat -t <pk>` goes straight to the 1:1 screen
// (chat_tui.go); with no -t and no -g it lands on the picker.
package cliskychat

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/0magnet/progkit"
	"github.com/gdamore/tcell/v3"
	"github.com/gdamore/tcell/v3/color"
)

// pickerEntry is one row in the conversation picker.
type pickerEntry struct {
	kind    string // "dm" or "group"
	id      string // peer PK for a dm, group ID for a group
	display string // alias, or the PK / group name
	subline string // group: member count and role
}

// convoMessage is one message of a conversation, as shown.
type convoMessage struct {
	When     time.Time
	Sender   string // display: alias or PK
	SenderPK string // the PK on the wire, for routing
	Network  string
	Body     string
	Err      string
	Self     bool
}

// groupTUIMsg is one group message the poll loop found.
type groupTUIMsg struct {
	GroupID  string
	SenderPK string
	Body     string
	When     time.Time
}

type promptKind int

const (
	promptNone promptKind = iota
	promptNewDM
	promptJoinInvite
	promptCreateGroup
)

var (
	pickTitle = tcell.StyleDefault.Bold(true)
	pickWarn  = tcell.StyleDefault.Foreground(color.PaletteColor(214))
)

type unifiedModel struct {
	addr    string
	network string
	app     *progkit.App

	mu        sync.Mutex
	entries   []pickerEntry
	loadErr   string
	statusErr string
	history   map[string][]convoMessage

	prompt     promptKind
	activeKind string // "" while on the picker
	activeID   string
	activeName string
	activeMeta string

	picker *progkit.List
	view   *progkit.Text
	input  *progkit.Input
}

func runUnifiedTUI(addr, network string) error {
	app, err := progkit.Open()
	if err != nil {
		return err
	}
	defer app.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m := &unifiedModel{
		addr:    addr,
		network: network,
		app:     app,
		history: map[string][]convoMessage{},
		picker:  &progkit.List{ID: "picker"},
		view:    &progkit.Text{ID: "convo", Follow: true, Selectable: true},
		input:   &progkit.Input{ID: "line"},
	}
	m.picker.OnActivate = func(i int) { m.openEntry(i) }
	m.input.OnSubmit = m.submit
	go m.loadPicker()
	m.streamDMs(ctx)
	m.streamGroups(ctx)

	app.Run(m.draw, m.handle)
	return nil
}

func (m *unifiedModel) loadPicker() {
	entries, dmErr, groupErr := loadPickerInline()
	var errs []string
	if dmErr != "" {
		errs = append(errs, "DMs: "+dmErr)
	}
	if groupErr != "" {
		errs = append(errs, "groups: "+groupErr)
	}
	m.mu.Lock()
	m.entries, m.loadErr = entries, strings.Join(errs, "; ")
	m.mu.Unlock()
	m.app.Redraw()
}

// streamDMs follows the chat app's /sse stream for the whole session; what
// a conversation shows is filtered on drawing.
func (m *unifiedModel) streamDMs(ctx context.Context) {
	raw := make(chan chatMsg, 64)
	errCh := make(chan error, 1)
	go streamSSE(ctx, m.addr, raw, errCh)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case cm := <-raw:
				disp := cm.Sender
				if alias := lookupAlias(cm.Sender); alias != "" {
					disp = alias
				}
				// Route by the PK on the wire, not the display name, so a
				// removed alias does not split one conversation in two.
				m.add("dm:"+cm.Sender, convoMessage{When: cm.When, Sender: disp, SenderPK: cm.Sender, Network: cm.Network, Body: cm.Body})
			case err := <-errCh:
				m.setStatus("sse: " + err.Error())
			}
		}
	}()
}

// streamGroups polls the visor's group inbox over RPC.
func (m *unifiedModel) streamGroups(ctx context.Context) {
	inCh := make(chan groupTUIMsg, 64)
	errCh := make(chan error, 1)
	go runGroupPollLoop(ctx, inCh, errCh)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case g := <-inCh:
				m.add("group:"+g.GroupID, convoMessage{When: g.When, Sender: displayPK(g.SenderPK), SenderPK: g.SenderPK, Body: g.Body})
			case err := <-errCh:
				m.setStatus("group: " + err.Error())
			}
		}
	}()
}

func (m *unifiedModel) add(key string, msg convoMessage) {
	m.mu.Lock()
	m.history[key] = append(m.history[key], msg)
	m.mu.Unlock()
	m.app.Redraw()
}

func (m *unifiedModel) setStatus(s string) {
	m.mu.Lock()
	m.statusErr = s
	m.mu.Unlock()
	m.app.Redraw()
}

func (m *unifiedModel) historyKey() string {
	if m.activeKind == "" {
		return ""
	}
	return m.activeKind + ":" + m.activeID
}

func (m *unifiedModel) handle(ev tcell.Event) bool {
	switch ev := ev.(type) {
	case *tcell.EventKey:
		if progkit.IsCtrl(ev, 'c') {
			return false
		}
		switch {
		case m.prompt != promptNone:
			if ev.Key() == tcell.KeyEscape {
				m.endPrompt()
				return true
			}
			m.input.Key(ev)
		case m.activeKind != "":
			switch {
			case ev.Key() == tcell.KeyEscape:
				m.activeKind, m.activeID, m.activeName, m.activeMeta = "", "", "", ""
				m.input.SetValue("")
			case progkit.IsCtrl(ev, 'n'):
				m.network = otherNetwork(m.network)
			case ev.Key() == tcell.KeyPgUp, ev.Key() == tcell.KeyPgDn, ev.Key() == tcell.KeyUp, ev.Key() == tcell.KeyDown:
				m.view.Key(ev)
			default:
				m.input.Key(ev)
			}
		default:
			return m.pickerKey(ev)
		}
	case *tcell.EventMouse:
		if m.activeKind != "" {
			m.view.Mouse(ev)
		}
	}
	return true
}

func (m *unifiedModel) pickerKey(ev *tcell.EventKey) bool {
	switch {
	case ev.Key() == tcell.KeyEscape, progkit.Typed(ev) == "q":
		return false
	case progkit.Typed(ev) == "k":
		m.picker.Selected--
	case progkit.Typed(ev) == "j":
		m.picker.Selected++
	case progkit.Typed(ev) == "n":
		m.startPrompt(promptNewDM, "Enter peer PK or alias")
	case progkit.Typed(ev) == "g":
		m.startPrompt(promptCreateGroup, "New group name")
	case progkit.Typed(ev) == "i":
		m.startPrompt(promptJoinInvite, "Paste invite token")
	case progkit.Typed(ev) == "r":
		go m.loadPicker()
	default:
		m.picker.Key(ev)
	}
	return true
}

func (m *unifiedModel) openEntry(i int) {
	m.mu.Lock()
	var e pickerEntry
	ok := i >= 0 && i < len(m.entries)
	if ok {
		e = m.entries[i]
	}
	m.mu.Unlock()
	if ok {
		m.openConvo(e)
	}
}

func (m *unifiedModel) openConvo(e pickerEntry) {
	m.activeKind, m.activeID, m.activeName, m.activeMeta = e.kind, e.id, e.display, e.subline
	m.input.SetValue("")
	m.input.Placeholder = "type message; Enter to send, Esc back to picker"
}

func (m *unifiedModel) startPrompt(kind promptKind, placeholder string) {
	m.prompt = kind
	m.input.SetValue("")
	m.input.Placeholder = placeholder
}

func (m *unifiedModel) endPrompt() {
	m.prompt = promptNone
	m.input.SetValue("")
}

// submit answers the open prompt, or sends to the open conversation.
func (m *unifiedModel) submit(text string) {
	text = strings.TrimSpace(text)
	if m.prompt != promptNone {
		kind := m.prompt
		m.endPrompt()
		if text == "" {
			return
		}
		switch kind {
		case promptNewDM:
			pk, err := resolveTarget(text)
			if err != nil {
				m.setStatus(err.Error())
				return
			}
			m.openConvo(pickerEntry{kind: "dm", id: pk.Hex(), display: displayPK(pk.Hex())})
		case promptJoinInvite:
			go m.mutate("group join", func() error { return groupJoinRPC(text) })
		case promptCreateGroup:
			go m.mutate("group create", func() error { return groupCreateRPC(text) })
		}
		return
	}
	if text == "" || m.activeKind == "" {
		return
	}
	m.input.SetValue("")
	kind, id, network, key := m.activeKind, m.activeID, m.network, m.historyKey()
	go func() {
		var err error
		if kind == "dm" {
			_, err = postMessage(m.addr, id, text, network, 0)
		} else {
			err = groupSendRPC(id, text)
		}
		row := convoMessage{When: time.Now(), Sender: "you", Network: network, Body: text, Self: true}
		if kind != "dm" {
			row.Network = ""
		}
		if err != nil {
			row.Err = err.Error()
		}
		m.add(key, row)
	}()
}

// mutate runs a group change and reloads the picker so it shows.
func (m *unifiedModel) mutate(what string, f func() error) {
	if err := f(); err != nil {
		m.setStatus(fmt.Sprintf("%s: %v", what, err))
		return
	}
	m.loadPicker()
}

func (m *unifiedModel) draw(f *progkit.Frame) {
	m.mu.Lock()
	entries := append([]pickerEntry(nil), m.entries...)
	loadErr, statusErr := m.loadErr, m.statusErr
	hist := append([]convoMessage(nil), m.history[m.historyKey()]...)
	m.mu.Unlock()

	switch {
	case m.prompt != promptNone:
		m.drawPrompt(f)
	case m.activeKind != "":
		m.drawChat(f, hist, statusErr)
	default:
		m.drawPicker(f, entries, loadErr, statusErr)
	}
}

func (m *unifiedModel) drawPicker(f *progkit.Frame, entries []pickerEntry, loadErr, statusErr string) {
	all := f.Size()
	head, rest := all.SplitTop(2)
	body, foot := rest.SplitBottom(2)
	progkit.DrawText(f.Screen, 0, head.Y, head.W, "skychat — pick a conversation", pickTitle)
	if loadErr != "" {
		progkit.DrawText(f.Screen, 0, head.Y+1, head.W, "⚠ "+loadErr, pickWarn)
	}
	items := make([]string, len(entries))
	for i, e := range entries {
		kind := "DM "
		if e.kind == "group" {
			kind = "GRP"
		}
		meta := ""
		if e.subline != "" {
			meta = "  " + e.subline
		}
		items[i] = fmt.Sprintf("%s  %-30s  %s%s", kind, e.display, e.id, meta)
	}
	if len(items) == 0 {
		progkit.DrawText(f.Screen, 0, body.Y, body.W, "(no conversations yet — press n for a new DM or i to join a group)", dimStyle)
	} else {
		m.picker.Items = items
		m.picker.Draw(f, body, true)
	}
	progkit.DrawText(f.Screen, 0, foot.Y, foot.W, "↑/↓ select  enter open  n new dm  g create group  i join invite  r refresh  q quit", dimStyle)
	if statusErr != "" {
		progkit.DrawText(f.Screen, 0, foot.Y+1, foot.W, statusErr, errStyle)
	}
}

func (m *unifiedModel) drawPrompt(f *progkit.Frame) {
	label := ""
	switch m.prompt {
	case promptNewDM:
		label = "New direct message — type peer PK (66 hex) or alias, Enter confirms, Esc cancels"
	case promptJoinInvite:
		label = "Join group — paste invite token (begins with skychat:invite:), Enter confirms, Esc cancels"
	case promptCreateGroup:
		label = "Create group — type a name, Enter confirms, Esc cancels"
	}
	progkit.DrawText(f.Screen, 0, 0, f.W, "skychat", pickTitle)
	progkit.DrawText(f.Screen, 0, 2, f.W, label, dimStyle)
	n := progkit.DrawText(f.Screen, 0, 4, f.W, "» ", tcell.StyleDefault)
	m.input.Draw(f, progkit.Rect{X: n, Y: 4, W: f.W - n, H: 1}, true)
}

func (m *unifiedModel) drawChat(f *progkit.Frame, hist []convoMessage, statusErr string) {
	head, rest := f.Size().SplitTop(1)
	body, foot := rest.SplitBottom(3)
	x := progkit.DrawText(f.Screen, 0, head.Y, head.W, m.activeName, pickTitle)
	meta := ""
	if m.activeMeta != "" {
		meta = " · " + m.activeMeta
	}
	if m.activeKind == "dm" {
		meta += " · DM · " + m.network
	} else {
		meta += " · GROUP"
	}
	progkit.DrawText(f.Screen, x, head.Y, head.W-x, meta, dimStyle)

	m.view.SetLines(renderConvo(hist))
	m.view.Draw(f, body)

	n := progkit.DrawText(f.Screen, 0, foot.Y, foot.W, "» ", tcell.StyleDefault)
	m.input.Draw(f, progkit.Rect{X: n, Y: foot.Y, W: foot.W - n, H: 1}, true)
	if statusErr != "" {
		progkit.DrawText(f.Screen, 0, foot.Y+1, foot.W, statusErr, errStyle)
	}
	progkit.DrawText(f.Screen, 0, foot.Y+2, foot.W, "enter send  esc back  pgup/pgdn scroll  ctrl+n cycle network  ctrl+c quit", dimStyle)
}

// displayPK returns the alias for a PK if known, else the PK itself.
func displayPK(pk string) string {
	if alias := lookupAlias(pk); alias != "" {
		return alias
	}
	return pk
}
