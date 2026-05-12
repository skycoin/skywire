// Package cliskychat cmd/skywire-cli/commands/skychat/tui_unified.go
//
// Unified bubbletea TUI: a picker view that lists every conversation
// the local visor knows about (1:1 peers + group memberships) and lets
// the operator drop into any of them. Switching between conversations
// is a single keystroke; new conversations start from the picker too.
//
// Layout (terminal):
//
//	┌─ skychat ─────────────────────────────────────────────────────┐
//	│ [picker]                                                       │
//	│  ▸ DM   other-claude            02f9aa58…                      │
//	│    DM   prod-claude             03d1d78e…                      │
//	│    GRP  claude-coordination     72afa409… (3 members)          │
//	│                                                                │
//	│ ↑/↓ select  enter open  N new dm  G create group  J join inv  │
//	│ ALT+enter scroll  esc/q quit                                  │
//	└────────────────────────────────────────────────────────────────┘
//
// In-chat view is the same shell as chat.go's 1:1 view but with the
// header showing the conversation type + alias-resolved name, and
// the IO layer swapping between SSE+/message (1:1) and GroupPoll+
// GroupSend (groups). Esc returns to the picker.
//
// The existing `skywire cli skychat chat -t <pk>` short-circuits into
// the 1:1 in-chat view (current behavior preserved). New default
// (no -t / no -g): land on the picker.

package cliskychat

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/skycoin/skywire/pkg/cipher"
)

// pickerEntry is one row in the conversation picker.
type pickerEntry struct {
	kind    string // "dm" | "group"
	id      string // PK hex (dm) or group UUID (group)
	display string // alias / group name; falls back to short id
	subline string // e.g. "3 members" for group, peer count for dm
}

// convoMessage is one rendered message in the unified history pane.
// Same shape as chat.go's chatMsg; copied here so the unified TUI is
// independent of the 1:1 chat model and the two can evolve apart.
type convoMessage struct {
	When     time.Time
	Sender   string // resolved display name (or PK) — for rendering
	SenderPK string // raw PK hex from wire — for routing keys; empty for outgoing
	Network  string // skynet/dmsg (1:1 only)
	Body     string
	Err      string // when set, render in red
	Self     bool   // outgoing
}

// unifiedView is which screen the TUI is currently on.
type unifiedView int

const (
	viewPicker unifiedView = iota
	viewDM
	viewGroup
	viewPrompt // textinput-driven prompt for "new dm pk", "join invite", "create name"
)

// promptKind discriminates what the prompt is asking for.
type promptKind int

const (
	promptNone promptKind = iota
	promptNewDM
	promptJoinInvite
	promptCreateGroup
)

// unifiedModel is the bubbletea model that drives the entire skychat
// TUI. State is a tagged union over the four view kinds; only one is
// active at any time.
type unifiedModel struct {
	addr    string
	network string // outgoing network for 1:1 sends

	view   unifiedView
	prompt promptKind

	// Picker state.
	entries  []pickerEntry
	cursor   int
	loadErr  string // surfaced from initial GroupList / history fetch
	loadedAt time.Time

	// Conversation history (per-convo).
	history map[string][]convoMessage // key: "dm:<pk>" or "group:<id>"

	// Active conversation (when view == viewDM | viewGroup).
	activeKind string
	activeID   string
	activeName string
	activeMeta string

	// Bubbletea components shared across views.
	vp    viewport.Model
	input textinput.Model

	width  int
	height int
	ready  bool

	// 1:1 SSE stream — single stream for all 1:1 traffic; we filter
	// per-convo at display time so users in DMs see only their PK.
	sseIncoming <-chan convoMessage
	sseErrCh    <-chan error
	sseCancel   context.CancelFunc

	// Group poll loop emits groupMsg events; one channel for all
	// joined groups. We route by GroupID per event.
	groupIncoming <-chan groupTUIMsg
	groupErrCh    <-chan error
	groupCancel   context.CancelFunc

	// Status surfaced on the bottom bar.
	statusErr string
}

// groupTUIMsg is one inbound group message from the poll loop.
type groupTUIMsg struct {
	GroupID  string
	SenderPK string
	Body     string
	When     time.Time
}

// tuiIncomingDM / tuiIncomingGroup are tea.Msg wrappers for the two
// stream channels. The Update branch routes them into the convo
// history then re-arms the wait.
type tuiIncomingDM struct{ msg convoMessage }
type tuiIncomingGroup struct{ msg groupTUIMsg }

// tuiSSEErr / tuiGroupErr surface stream-level failures.
type tuiSSEErr struct{ err error }
type tuiGroupErr struct{ err error }

// tuiMutationErr surfaces an error from a picker-level mutation
// (group join / create) that has no associated history bucket to
// render into. The handler renders it as a status banner.
type tuiMutationErr struct{ err error }

// tuiPickerLoaded is fired after the initial GroupList + history
// fetch returns. Carries the populated entries.
type tuiPickerLoaded struct {
	entries []pickerEntry
	err     string
}

// tuiSent reports an outgoing send result so the UI can render an
// error row if the publish failed.
type tuiSent struct {
	key  string // history key the message was for
	body string
	err  error
}

func runUnifiedTUI(addr, network string) error {
	in := textinput.New()
	in.Focus()
	in.CharLimit = 4096

	m := &unifiedModel{
		addr:    addr,
		network: network,
		view:    viewPicker,
		input:   in,
		history: map[string][]convoMessage{},
	}
	prog := tea.NewProgram(m, tea.WithAltScreen())
	_, err := prog.Run()
	if m.sseCancel != nil {
		m.sseCancel()
	}
	if m.groupCancel != nil {
		m.groupCancel()
	}
	return err
}

// Init wires up the initial commands: picker load + stream pumps.
func (m *unifiedModel) Init() tea.Cmd {
	return tea.Batch(
		textinput.Blink,
		m.startSSE(),
		m.startGroupPoll(),
		m.loadPicker(),
	)
}

// loadPicker fetches the list of known peers (from /history/peers)
// and joined groups (from RPC GroupList). Returns a tuiPickerLoaded
// event when done. Errors are surfaced as a status line, not fatal.
func (m *unifiedModel) loadPicker() tea.Cmd {
	addr := m.addr
	return func() tea.Msg {
		var entries []pickerEntry

		// DMs from /history/peers — only peers we have any chat
		// history with. Aliases without history are NOT auto-added
		// here; the operator can use "N" to start a fresh dm.
		peers, err := fetchHistoryPeers(addr)
		if err == nil {
			for _, p := range peers {
				e := pickerEntry{kind: "dm", id: p, display: p}
				if alias := lookupAlias(p); alias != "" {
					e.display = alias
				}
				entries = append(entries, e)
			}
		}

		// Groups via RPC.
		groups, gErr := fetchGroupList()
		if gErr == nil {
			for _, g := range groups {
				entries = append(entries, pickerEntry{
					kind:    "group",
					id:      g.ID,
					display: g.Name,
					subline: fmt.Sprintf("%d members · %s", len(g.Members), g.Role),
				})
			}
		}

		sort.Slice(entries, func(i, j int) bool {
			if entries[i].kind != entries[j].kind {
				return entries[i].kind == "dm"
			}
			return entries[i].display < entries[j].display
		})

		out := tuiPickerLoaded{entries: entries}
		if err != nil {
			out.err = fmt.Sprintf("DMs: %v", err)
		}
		if gErr != nil {
			if out.err != "" {
				out.err += "; "
			}
			out.err += fmt.Sprintf("groups: %v", gErr)
		}
		return out
	}
}

// startSSE opens the chat-app's /sse stream and starts forwarding
// incoming messages onto an internal channel for the model to drain.
// The stream lives for the lifetime of the TUI; per-conversation
// filtering is purely display-side.
func (m *unifiedModel) startSSE() tea.Cmd {
	inCh := make(chan convoMessage, 64)
	errCh := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background()) //nolint:gosec // cancel stored on model + invoked by runUnifiedTUI on exit
	m.sseIncoming = inCh
	m.sseErrCh = errCh
	m.sseCancel = cancel

	addr := m.addr
	go func() {
		raw := make(chan chatMsg, 64)
		go streamSSE(ctx, addr, raw, errCh)
		for cm := range raw {
			if ctx.Err() != nil {
				return
			}
			// Re-wrap into convoMessage with display-name resolution.
			disp := cm.Sender
			if alias := lookupAlias(cm.Sender); alias != "" {
				disp = alias
			}
			inCh <- convoMessage{
				When:     cm.When,
				Sender:   disp,
				SenderPK: cm.Sender,
				Network:  cm.Network,
				Body:     cm.Body,
				Self:     false,
			}
		}
	}()
	return m.waitSSE()
}

func (m *unifiedModel) waitSSE() tea.Cmd {
	return func() tea.Msg {
		select {
		case msg, ok := <-m.sseIncoming:
			if !ok {
				return tuiSSEErr{err: fmt.Errorf("sse channel closed")}
			}
			return tuiIncomingDM{msg: msg}
		case err, ok := <-m.sseErrCh:
			if !ok {
				return nil
			}
			return tuiSSEErr{err: err}
		}
	}
}

// startGroupPoll polls the visor's group inbox via RPC at 1Hz and
// pushes new messages onto an internal channel. Auto-reconnects
// the rpc client on visor restart, same pattern as the standalone
// group listen command.
func (m *unifiedModel) startGroupPoll() tea.Cmd {
	inCh := make(chan groupTUIMsg, 64)
	errCh := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background()) //nolint:gosec // cancel stored on model + invoked by runUnifiedTUI on exit
	m.groupIncoming = inCh
	m.groupErrCh = errCh
	m.groupCancel = cancel

	go func() {
		runGroupPollLoop(ctx, inCh, errCh)
	}()
	return m.waitGroup()
}

func (m *unifiedModel) waitGroup() tea.Cmd {
	return func() tea.Msg {
		select {
		case msg, ok := <-m.groupIncoming:
			if !ok {
				return tuiGroupErr{err: fmt.Errorf("group channel closed")}
			}
			return tuiIncomingGroup{msg: msg}
		case err, ok := <-m.groupErrCh:
			if !ok {
				return nil
			}
			return tuiGroupErr{err: err}
		}
	}
}

func (m *unifiedModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		headerH := 2
		footerH := 2
		if !m.ready {
			m.vp = viewport.New(msg.Width, msg.Height-headerH-footerH)
			m.vp.SetContent("")
			m.ready = true
		} else {
			m.vp.Width = msg.Width
			m.vp.Height = msg.Height - headerH - footerH
		}
		m.input.Width = msg.Width - len(m.input.Prompt) - 1
		m.width = msg.Width
		m.height = msg.Height

	case tuiPickerLoaded:
		m.entries = msg.entries
		m.loadErr = msg.err
		m.loadedAt = time.Now()

	case tuiIncomingDM:
		// Route off the raw PK from the wire, not the resolved display
		// name. If the alias was removed mid-session, the display would
		// no longer round-trip to the PK and the inbound would split
		// from the outbound (keyed by activeID/PK) into a separate
		// "dm:<alias>" history bucket.
		routePK := msg.msg.SenderPK
		if routePK == "" {
			routePK = senderToKey(msg.msg.Sender) // fallback for any caller still on the old path
		}
		key := "dm:" + routePK
		m.history[key] = append(m.history[key], msg.msg)
		if m.view == viewDM && m.historyKey() == key {
			m.refreshViewport()
		}
		cmds = append(cmds, m.waitSSE())

	case tuiIncomingGroup:
		key := "group:" + msg.msg.GroupID
		disp := msg.msg.SenderPK
		if alias := lookupAlias(msg.msg.SenderPK); alias != "" {
			disp = alias
		}
		cm := convoMessage{
			When:   msg.msg.When,
			Sender: disp,
			Body:   msg.msg.Body,
		}
		m.history[key] = append(m.history[key], cm)
		if m.view == viewGroup && m.historyKey() == key {
			m.refreshViewport()
		}
		cmds = append(cmds, m.waitGroup())

	case tuiSent:
		row := convoMessage{
			When:    time.Now(),
			Sender:  "you",
			Network: m.network,
			Body:    msg.body,
			Self:    true,
		}
		if msg.err != nil {
			row.Err = msg.err.Error()
		}
		m.history[msg.key] = append(m.history[msg.key], row)
		if m.historyKey() == msg.key {
			m.refreshViewport()
		}

	case tuiSSEErr:
		if msg.err != nil {
			m.statusErr = "sse: " + msg.err.Error()
		}

	case tuiGroupErr:
		if msg.err != nil {
			m.statusErr = "group: " + msg.err.Error()
		}

	case tuiMutationErr:
		if msg.err != nil {
			m.statusErr = msg.err.Error()
		}

	case tea.KeyMsg:
		switch m.view {
		case viewPicker:
			return m.updatePicker(msg, cmds)
		case viewPrompt:
			return m.updatePrompt(msg, cmds)
		case viewDM, viewGroup:
			return m.updateChat(msg, cmds)
		}
	}

	var inputCmd tea.Cmd
	m.input, inputCmd = m.input.Update(msg)
	cmds = append(cmds, inputCmd)
	if m.ready {
		var vpCmd tea.Cmd
		m.vp, vpCmd = m.vp.Update(msg)
		cmds = append(cmds, vpCmd)
	}
	return m, tea.Batch(cmds...)
}

func (m *unifiedModel) updatePicker(msg tea.KeyMsg, cmds []tea.Cmd) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "esc", "q":
		return m, tea.Quit
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.entries)-1 {
			m.cursor++
		}
	case "enter":
		if len(m.entries) == 0 {
			return m, nil
		}
		e := m.entries[m.cursor]
		m.openConvo(e)
	case "n":
		m.startPrompt(promptNewDM, "Enter peer PK or alias: ")
	case "g":
		m.startPrompt(promptCreateGroup, "New group name: ")
	case "i":
		// "i" not "j" — j is already bound to vim-style down
		// navigation. i for "invite" is mnemonic + non-conflicting.
		m.startPrompt(promptJoinInvite, "Paste invite token: ")
	case "r":
		cmds = append(cmds, m.loadPicker())
	}
	return m, tea.Batch(cmds...)
}

func (m *unifiedModel) updatePrompt(msg tea.KeyMsg, cmds []tea.Cmd) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "esc":
		m.endPrompt()
	case "enter":
		text := strings.TrimSpace(m.input.Value())
		m.endPrompt()
		switch m.prompt {
		case promptNewDM:
			if text != "" {
				pk, err := resolveTarget(text)
				if err != nil {
					m.statusErr = err.Error()
					return m, nil
				}
				m.openConvo(pickerEntry{kind: "dm", id: pk.Hex(), display: displayPK(pk.Hex())})
			}
		case promptJoinInvite:
			if text != "" {
				cmds = append(cmds, m.runJoinInvite(text))
			}
		case promptCreateGroup:
			if text != "" {
				cmds = append(cmds, m.runCreateGroup(text))
			}
		}
		m.prompt = promptNone
	}
	var inputCmd tea.Cmd
	m.input, inputCmd = m.input.Update(msg)
	cmds = append(cmds, inputCmd)
	return m, tea.Batch(cmds...)
}

func (m *unifiedModel) updateChat(msg tea.KeyMsg, cmds []tea.Cmd) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.exitConvo()
		return m, tea.Batch(cmds...)
	case "ctrl+n":
		if m.network == "skynet" {
			m.network = "dmsg"
		} else {
			m.network = "skynet"
		}
	case "enter":
		text := strings.TrimSpace(m.input.Value())
		if text == "" {
			return m, nil
		}
		m.input.Reset()
		cmds = append(cmds, m.runSend(text))
	case "pgup":
		m.vp.HalfPageUp()
	case "pgdown":
		m.vp.HalfPageDown()
	}
	var inputCmd tea.Cmd
	m.input, inputCmd = m.input.Update(msg)
	cmds = append(cmds, inputCmd)
	if m.ready {
		var vpCmd tea.Cmd
		m.vp, vpCmd = m.vp.Update(msg)
		cmds = append(cmds, vpCmd)
	}
	return m, tea.Batch(cmds...)
}

func (m *unifiedModel) startPrompt(kind promptKind, placeholder string) {
	m.prompt = kind
	m.view = viewPrompt
	m.input.Reset()
	m.input.Placeholder = placeholder
	m.input.Prompt = "» "
	m.input.CharLimit = 4096
	if m.ready {
		m.input.Width = m.width - len(m.input.Prompt) - 1
	}
}

func (m *unifiedModel) endPrompt() {
	m.prompt = promptNone
	m.input.Reset()
	m.view = viewPicker
}

func (m *unifiedModel) openConvo(e pickerEntry) {
	m.activeKind = e.kind
	m.activeID = e.id
	m.activeName = e.display
	m.activeMeta = e.subline
	if e.kind == "dm" {
		m.view = viewDM
	} else {
		m.view = viewGroup
	}
	m.input.Reset()
	m.input.Placeholder = "type message; Enter to send, Esc back to picker"
	m.input.Prompt = "» "
	m.input.CharLimit = 4096
	if m.ready {
		m.input.Width = m.width - len(m.input.Prompt) - 1
	}
	m.refreshViewport()
}

func (m *unifiedModel) exitConvo() {
	m.view = viewPicker
	m.activeKind = ""
	m.activeID = ""
	m.activeName = ""
	m.activeMeta = ""
	m.input.Reset()
}

func (m *unifiedModel) historyKey() string {
	if m.activeKind == "" {
		return ""
	}
	return m.activeKind + ":" + m.activeID
}

func (m *unifiedModel) refreshViewport() {
	if !m.ready {
		return
	}
	m.vp.SetContent(m.renderConvoHistory())
	m.vp.GotoBottom()
}

func (m *unifiedModel) renderConvoHistory() string {
	hist := m.history[m.historyKey()]
	if len(hist) == 0 {
		return lipgloss.NewStyle().Foreground(lipgloss.Color("241")).
			Render("(no messages yet — type below and hit Enter)")
	}
	youStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("39")).Bold(true)
	peerStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("213")).Bold(true)
	errStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("241"))

	var b strings.Builder
	for _, msg := range hist {
		ts := msg.When.Format("15:04:05")
		var sender string
		if msg.Self {
			sender = youStyle.Render("you")
		} else {
			sender = peerStyle.Render(msg.Sender)
		}
		net := ""
		if msg.Network != "" {
			net = dimStyle.Render(" /" + msg.Network)
		}
		if msg.Err != "" {
			fmt.Fprintf(&b, "%s %s%s %s — %s\n", dimStyle.Render(ts), sender, net,
				msg.Body, errStyle.Render(msg.Err))
		} else {
			fmt.Fprintf(&b, "%s %s%s  %s\n", dimStyle.Render(ts), sender, net, msg.Body)
		}
	}
	return b.String()
}

func (m *unifiedModel) runSend(text string) tea.Cmd {
	kind := m.activeKind
	id := m.activeID
	addr := m.addr
	net := m.network
	key := m.historyKey()
	return func() tea.Msg {
		switch kind {
		case "dm":
			_, err := postMessage(addr, id, text, net, 0)
			return tuiSent{key: key, body: text, err: err}
		case "group":
			err := groupSendRPC(id, text)
			return tuiSent{key: key, body: text, err: err}
		}
		return nil
	}
}

func (m *unifiedModel) runJoinInvite(invite string) tea.Cmd {
	return func() tea.Msg {
		err := groupJoinRPC(invite)
		if err != nil {
			return tuiMutationErr{err: fmt.Errorf("group join: %w", err)}
		}
		// Successful join: refresh the picker so the new group shows.
		return m.refreshAfterMutation()
	}
}

func (m *unifiedModel) runCreateGroup(name string) tea.Cmd {
	return func() tea.Msg {
		err := groupCreateRPC(name)
		if err != nil {
			return tuiMutationErr{err: fmt.Errorf("group create: %w", err)}
		}
		return m.refreshAfterMutation()
	}
}

func (m *unifiedModel) refreshAfterMutation() tea.Msg {
	entries, _, gErrStr := loadPickerInline()
	return tuiPickerLoaded{entries: entries, err: gErrStr}
}

// View renders the current screen.
func (m *unifiedModel) View() string {
	if !m.ready {
		return "starting skychat tui…"
	}
	switch m.view {
	case viewPicker:
		return m.viewPicker()
	case viewPrompt:
		return m.viewPromptScreen()
	case viewDM, viewGroup:
		return m.viewChat()
	}
	return ""
}

func (m *unifiedModel) viewPicker() string {
	title := lipgloss.NewStyle().Bold(true).Render("skychat — pick a conversation")
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	row := lipgloss.NewStyle().Padding(0, 1)
	sel := lipgloss.NewStyle().Padding(0, 1).Foreground(lipgloss.Color("212")).Bold(true)

	var b strings.Builder
	b.WriteString(title + "\n")
	if m.loadErr != "" {
		b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("214")).
			Render("⚠ "+m.loadErr) + "\n")
	}
	b.WriteString("\n")
	if len(m.entries) == 0 {
		b.WriteString(dim.Render("(no conversations yet — press N for a new DM or J to join a group)") + "\n")
	}
	for i, e := range m.entries {
		left := "  "
		if i == m.cursor {
			left = "▸ "
		}
		kindTag := dim.Render("DM ")
		if e.kind == "group" {
			kindTag = dim.Render("GRP")
		}
		idTag := dim.Render(shortID(e.id))
		meta := ""
		if e.subline != "" {
			meta = "  " + dim.Render(e.subline)
		}
		line := fmt.Sprintf("%s%s  %-30s  %s%s", left, kindTag, e.display, idTag, meta)
		if i == m.cursor {
			b.WriteString(sel.Render(line) + "\n")
		} else {
			b.WriteString(row.Render(line) + "\n")
		}
	}
	b.WriteString("\n" + dim.Render("↑/↓ select  enter open  n new dm  g create group  i join invite  r refresh  q quit"))
	if m.statusErr != "" {
		b.WriteString("\n" + lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Render(m.statusErr))
	}
	return b.String()
}

func (m *unifiedModel) viewPromptScreen() string {
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	label := ""
	switch m.prompt {
	case promptNewDM:
		label = "New direct message — type peer PK (66 hex) or alias, Enter confirms, Esc cancels"
	case promptJoinInvite:
		label = "Join group — paste invite token (begins with skychat:invite:), Enter confirms, Esc cancels"
	case promptCreateGroup:
		label = "Create group — type a name, Enter confirms, Esc cancels"
	}
	return lipgloss.NewStyle().Bold(true).Render("skychat") + "\n\n" +
		dim.Render(label) + "\n\n" +
		m.input.View()
}

func (m *unifiedModel) viewChat() string {
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	header := lipgloss.NewStyle().Bold(true).Render(m.activeName)
	if m.activeMeta != "" {
		header += " " + dim.Render("· "+m.activeMeta)
	}
	if m.activeKind == "dm" {
		header += " " + dim.Render("· DM · "+m.network)
	} else {
		header += " " + dim.Render("· GROUP")
	}
	footer := dim.Render("enter send  esc back  pgup/pgdn scroll  ctrl+n cycle network  ctrl+c quit")
	if m.statusErr != "" {
		footer = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Render(m.statusErr) + "\n" + footer
	}
	return header + "\n" + m.vp.View() + "\n" + m.input.View() + "\n" + footer
}

// senderToKey turns the SSE-side sender field (alias OR PK) back into
// a routing key. If alias is set, look it up; otherwise treat as PK
// directly. Conservative: if neither resolves, return raw input.
func senderToKey(sender string) string {
	// Try parse as PK directly.
	var pk cipher.PubKey
	if err := pk.Set(sender); err == nil {
		return pk.Hex()
	}
	// Resolve as alias.
	if pk, err := resolveTarget(sender); err == nil {
		return pk.Hex()
	}
	return sender
}

// displayPK returns the alias for a PK if known, else the PK itself.
func displayPK(pk string) string {
	if alias := lookupAlias(pk); alias != "" {
		return alias
	}
	return pk
}

// shortID truncates a long id for picker display while keeping
// enough prefix to disambiguate.
func shortID(id string) string {
	if len(id) <= 12 {
		return id
	}
	return id[:8] + "…"
}
