// Package clidmsg cmd/skywire-cli/commands/dmsg/chat.go: standalone
// dmsg-only chat. Run when the local visor is down (or absent) so
// operators can still reach a support channel.
//
// The visor-app skychat (cmd/apps/skychat) uses the visor's identity
// and the visor's dmsg.Client — its main strength is sharing the same
// PK operators publish in service-discovery, but the cost is "if the
// visor is broken, skychat is broken too." This command sidesteps
// that: it stands up its OWN dmsg.Client with a separate secret key,
// listens on a dmsg port for inbound chat, and dials peers directly
// over dmsg for outbound. No visor RPC, no route-finder, no skynet.
//
// Wire format mirrors the visor-app skychat post PR #2504:
//
//	[4-byte BE length][JSON envelope]
//
// where the JSON shape is {"sender","message","ts","network"}. Two
// skywire dmsg chat instances can talk to each other; cross-talk
// with visor-app skychat works if the visor-app side is also on the
// post-#2504 framed protocol.
//
// Why a TUI rather than send/listen subcommands like dmsgcurl: the
// expected use is interactive — an operator opens it, types a few
// messages back and forth with support, closes it. send/listen
// would force the operator to manage two terminal windows. Same
// reasoning as `skywire cli skychat chat`.
package clidmsg

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
)

const (
	// chatDmsgPort is the dmsg port `skywire dmsg chat` listens on
	// by default. 9001 sits well clear of the visor's reserved
	// system ports (everything <300 plus the 80/100/136 cluster)
	// AND outside the visor app routing-port range, so two
	// standalone instances and a visor with skychat can coexist
	// on the same dmsg-discovery without port collisions. Operator
	// can override with --port.
	chatDmsgPort = uint16(9001)
	// chatMaxFrameSize bounds claimed-length sanity on the read
	// side. 64 KiB matches the visor-app skychat ceiling so a
	// peer running either implementation can write the largest
	// frame the other accepts.
	chatMaxFrameSize = 64 * 1024
)

var (
	chatTo   string
	chatPort uint16
)

func init() {
	chatCmd.Flags().SortFlags = false
	chatCmd.Flags().VarP(&sk, "sk", "s",
		"secret key for the standalone dmsg client (a random one is generated if unset; required for stable identity across sessions)")
	chatCmd.Flags().StringVarP(&chatTo, "to", "t", "",
		"recipient public key (optional; TUI prompts if omitted)")
	chatCmd.Flags().Uint16VarP(&chatPort, "port", "p", chatDmsgPort,
		"local dmsg port to listen on for inbound chat")
	chatCmd.Flags().StringVarP(&logLvl, "loglvl", "l", "fatal",
		"[ debug | warn | error | fatal | panic | trace | info ]")
	if env := os.Getenv("DMSG_SK"); env != "" {
		sk.Set(env) //nolint:errcheck,gosec
	}
}

var chatCmd = &cobra.Command{
	Use:   "chat",
	Short: "Interactive chat over dmsg (standalone, no visor required)",
	Long: `DMSG chat — interactive 1:1 messaging directly over the dmsg network.

Standalone tool: stands up its own dmsg.Client using --sk (or a
randomly-generated key if --sk is omitted), listens on --port for
inbound, dials peers over dmsg for outbound. Use this when the local
visor is down or absent and you still need to reach a peer.

Identity note: --sk gives the standalone client a stable public key.
Without --sk, every invocation gets a fresh PK and any peer trying to
reach you needs to be re-told the new PK. Set DMSG_SK env or pass
--sk to keep an identity across sessions.

Recipient: pass --to <pk> to pin the conversation, or run without
--to to drop into pick-a-peer mode (the TUI prompts).

Ctrl+C / Esc to quit.`,
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	RunE: func(cmd *cobra.Command, _ []string) error {
		log := logging.MustGetLogger("dmsg-chat")
		if logLvl != "" {
			if lvl, err := logging.LevelFromString(logLvl); err == nil {
				logging.SetLevel(lvl)
			}
		}

		// Resolve identity. If --sk wasn't passed, generate a fresh
		// keypair — gives the operator a working session but a
		// rolling PK. Stable PK requires --sk on every call.
		myPK, mySK := resolveChatIdentity(sk)
		if !cmd.Flags().Changed("sk") && os.Getenv("DMSG_SK") == "" {
			fmt.Fprintf(os.Stderr,
				"WARN: ephemeral identity. Your PK is %s — peers can't reach you across sessions. Use --sk or DMSG_SK= for a stable identity.\n\n",
				myPK)
		}

		// Validate --to up front if provided. Empty value drops the
		// TUI into pick-a-peer mode.
		recipient := ""
		if chatTo != "" {
			var rpk cipher.PubKey
			if err := rpk.Set(chatTo); err != nil {
				return fmt.Errorf("invalid --to public key %q: %w", chatTo, err)
			}
			recipient = rpk.String()
		}

		ctx, cancel := cmdutil.SignalContext(context.Background(), log)
		defer cancel()

		// Start dmsg client. startDmsgClient (curl.go) returns a
		// usable client + close hook; ConnectMultiple is implicit
		// inside the Serve / Ready dance.
		dmsgC, closeDmsg, err := startDmsgClient(ctx, log, myPK, mySK)
		if err != nil {
			return fmt.Errorf("dmsg client: %w", err)
		}
		defer closeDmsg()

		// Listen for inbound chat. Each accepted stream gets its own
		// reader goroutine; messages funnel into the shared inCh
		// which the TUI drains via tea.Cmd.
		listener, err := dmsgC.Listen(chatPort)
		if err != nil {
			return fmt.Errorf("dmsg listen :%d: %w", chatPort, err)
		}
		defer listener.Close() //nolint:errcheck
		log.WithField("port", chatPort).Info("dmsg chat listening")

		inCh := make(chan incomingChatMsg, 64)
		errCh := make(chan error, 1)
		go acceptInbound(ctx, listener, log, inCh, errCh)

		fmt.Fprintf(os.Stderr, "Your PK: %s\nListening on :%d\n\n", myPK, chatPort)

		return runChatTUI(ctx, log, dmsgC, myPK, recipient, inCh, errCh)
	},
}

// resolveChatIdentity returns the (PK, SK) the standalone client
// runs as. If skFlag derives a valid PK, that pair is used;
// otherwise a fresh keypair is generated. The signature returns an
// error slot deliberately empty — kept on the API in case a future
// SK-from-file path needs to surface I/O failures.
func resolveChatIdentity(skFlag cipher.SecKey) (cipher.PubKey, cipher.SecKey) {
	pk, err := skFlag.PubKey()
	if err == nil {
		return pk, skFlag
	}
	gpk, gsk := cipher.GenerateKeyPair()
	return gpk, gsk
}

// incomingChatMsg is one inbound message after framing + JSON decode.
type incomingChatMsg struct {
	SenderPK string    `json:"sender"`
	Message  string    `json:"message"`
	TS       time.Time `json:"ts"`
	Network  string    `json:"network"`
}

// outgoingChatMsg is what we marshal + send on the wire.
type outgoingChatMsg = incomingChatMsg

func acceptInbound(ctx context.Context, l net.Listener, log *logging.Logger,
	out chan<- incomingChatMsg, errs chan<- error) {
	for {
		c, err := l.Accept()
		if err != nil {
			if ctx.Err() == nil {
				select {
				case errs <- err:
				default:
				}
			}
			return
		}
		go readInbound(ctx, c, log, out)
	}
}

func readInbound(ctx context.Context, c net.Conn, log *logging.Logger, out chan<- incomingChatMsg) {
	defer c.Close() //nolint:errcheck
	for {
		payload, err := readFrame(c)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				log.WithError(err).Debug("inbound read")
			}
			return
		}
		var msg incomingChatMsg
		if err := json.Unmarshal(payload, &msg); err != nil {
			log.WithError(err).Debug("inbound unmarshal")
			continue
		}
		select {
		case <-ctx.Done():
			return
		case out <- msg:
		}
	}
}

// readFrame reads one length-prefixed message. Mirrors the visor-app
// skychat framed wire (#2504) so peers from either implementation
// interop.
func readFrame(c net.Conn) ([]byte, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(c, hdr[:]); err != nil {
		return nil, err
	}
	length := binary.BigEndian.Uint32(hdr[:])
	if length == 0 {
		return nil, errors.New("chat: zero-length frame")
	}
	if length > chatMaxFrameSize {
		return nil, fmt.Errorf("chat: frame %d > max %d (peer running old unframed skychat?)", length, chatMaxFrameSize)
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(c, payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func writeFrame(c net.Conn, payload []byte) error {
	if len(payload) == 0 {
		return errors.New("chat: empty payload")
	}
	if len(payload) > chatMaxFrameSize {
		return fmt.Errorf("chat: payload %d > max %d", len(payload), chatMaxFrameSize)
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(payload))) //nolint:gosec
	if _, err := c.Write(hdr[:]); err != nil {
		return err
	}
	_, err := c.Write(payload)
	return err
}

// ============================== TUI ==============================
//
// Bubbletea split-pane: viewport history on top, textinput compose
// at the bottom. Parallel to cmd/skywire-cli/commands/skychat/chat.go
// but talks dmsg.Client.DialStream directly instead of POSTing to a
// local skychat HTTP server.

type chatRow struct {
	When   time.Time
	Sender string
	Body   string
	Err    string // when set, this is a send-failure row (red)
	Self   bool   // true for messages we sent
}

type chatModel struct {
	ctx       context.Context
	dmsgC     *dmsg.Client
	myPK      string
	recipient string
	port      uint16

	awaitingRecipient bool
	recipientErr      string

	history []chatRow
	vp      viewport.Model
	input   textinput.Model

	width  int
	height int
	ready  bool

	listenErr string

	incoming <-chan incomingChatMsg
	errs     <-chan error

	// peerConnMu guards reuse of an open outbound stream to the
	// pinned recipient. Re-dialing per message is correct but
	// burns dmsg session capacity unnecessarily for a conversation
	// of any length.
	peerConnMu sync.Mutex
	peerConn   net.Conn
}

type incomingTeaMsg struct{ msg incomingChatMsg }
type listenErrTeaMsg struct{ err error }
type sentTeaMsg struct {
	body string
	err  error
}

func runChatTUI(ctx context.Context, log *logging.Logger, dmsgC *dmsg.Client,
	myPK cipher.PubKey, recipient string, incoming <-chan incomingChatMsg, errs <-chan error) error {

	in := textinput.New()
	in.Focus()
	in.CharLimit = 4096
	if recipient == "" {
		in.Placeholder = "paste recipient PK (66 hex chars); Enter to confirm, Esc to quit"
		in.Prompt = "to: "
		in.CharLimit = 128
	} else {
		in.Placeholder = "type message; Enter to send, Esc to quit"
		in.Prompt = "» "
	}

	m := &chatModel{
		ctx:               ctx,
		dmsgC:             dmsgC,
		myPK:              myPK.String(),
		recipient:         recipient,
		port:              chatPort,
		awaitingRecipient: recipient == "",
		input:             in,
		incoming:          incoming,
		errs:              errs,
	}
	prog := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := prog.Run(); err != nil {
		log.WithError(err).Debug("tui run")
		return err
	}
	m.closePeerConn()
	return nil
}

func (m *chatModel) Init() tea.Cmd {
	return tea.Batch(textinput.Blink, m.waitIncoming(), m.waitListenErr())
}

func (m *chatModel) waitIncoming() tea.Cmd {
	return func() tea.Msg {
		select {
		case <-m.ctx.Done():
			return nil
		case msg, ok := <-m.incoming:
			if !ok {
				return listenErrTeaMsg{err: errors.New("inbound channel closed")}
			}
			return incomingTeaMsg{msg: msg}
		}
	}
}

func (m *chatModel) waitListenErr() tea.Cmd {
	return func() tea.Msg {
		select {
		case <-m.ctx.Done():
			return nil
		case err, ok := <-m.errs:
			if !ok {
				return nil
			}
			return listenErrTeaMsg{err: err}
		}
	}
}

func (m *chatModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		headerH := 2 // header + my-pk line
		footerH := 2
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
		case "enter":
			text := m.input.Value()
			if text == "" {
				return m, nil
			}
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
					m.input.Width = m.width - len(m.input.Prompt) - 1
				}
				return m, nil
			}
			m.input.Reset()
			body := text
			cmds = append(cmds, func() tea.Msg {
				err := m.sendOne(body)
				return sentTeaMsg{body: body, err: err}
			})
		case "pgup":
			m.vp.HalfPageUp()
		case "pgdown":
			m.vp.HalfPageDown()
		}

	case incomingTeaMsg:
		m.append(chatRow{
			When:   nonZeroTS(msg.msg.TS),
			Sender: msg.msg.SenderPK,
			Body:   msg.msg.Message,
		})
		cmds = append(cmds, m.waitIncoming())

	case sentTeaMsg:
		row := chatRow{
			When:   time.Now().UTC(),
			Sender: m.myPK,
			Body:   msg.body,
			Self:   true,
		}
		if msg.err != nil {
			row.Err = msg.err.Error()
		}
		m.append(row)

	case listenErrTeaMsg:
		if msg.err != nil {
			m.listenErr = msg.err.Error()
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

func (m *chatModel) append(row chatRow) {
	m.history = append(m.history, row)
	if !m.ready {
		return
	}
	wasAtBottom := m.vp.AtBottom()
	m.vp.SetContent(m.renderHistory())
	if wasAtBottom {
		m.vp.GotoBottom()
	}
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

func (m *chatModel) renderHistory() string {
	if len(m.history) == 0 {
		return lipgloss.NewStyle().Foreground(lipgloss.Color("241")).
			Render("(no messages yet — type below and hit Enter)")
	}
	youStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("39")).Bold(true)
	peerStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("213")).Bold(true)
	errStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("241"))

	var b lipgloss.Style
	_ = b
	out := ""
	for _, row := range m.history {
		ts := row.When.Format("15:04:05")
		var sender string
		if row.Self {
			sender = youStyle.Render("you")
		} else {
			sender = peerStyle.Render(shortPK(row.Sender))
		}
		body := row.Body
		if row.Err != "" {
			body = errStyle.Render(fmt.Sprintf("✗ send failed: %s — %q", row.Err, row.Body))
		}
		out += fmt.Sprintf("%s %s  %s\n", dimStyle.Render(ts), sender, body)
	}
	return out
}

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

	var header string
	if m.awaitingRecipient {
		hint := dimStyle.Render("pick a recipient PK below")
		if m.recipientErr != "" {
			hint = errStyle.Render(m.recipientErr)
		}
		header = fmt.Sprintf("%s  %s  port=:%d",
			hdrStyle.Render("dmsg chat"), hint, m.port)
	} else {
		header = fmt.Sprintf("%s  %s  port=:%d",
			hdrStyle.Render("dmsg chat"),
			dimStyle.Render("to "+shortPK(m.recipient)), m.port)
	}
	if m.listenErr != "" {
		header += "  " + errStyle.Render("[listen err: "+m.listenErr+"]")
	}
	myLine := dimStyle.Render("you = " + shortPK(m.myPK))

	var footer string
	if m.awaitingRecipient {
		footer = dimStyle.Render("Enter confirm recipient | Esc/Ctrl+C quit")
	} else {
		footer = dimStyle.Render("Enter send | ↑/↓ PgUp/PgDn scroll | Esc/Ctrl+C quit")
	}

	return header + "\n" + myLine + "\n" + m.vp.View() + "\n" + m.input.View() + "\n" + footer
}
