// Package cliskychat cmd/skywire-cli/commands/skychat/root.go
package cliskychat

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
)

var (
	httpAddr  string
	recipient string
	message   string
	// sendNet and listenNet are deliberately separate variables: an
	// earlier version bound the same `networkType` package var to
	// both sendCmd's and listenCmd's --net flag with different
	// defaults. cobra's StringVarP writes the default into the
	// variable at init() time, so the second registration won
	// — sendCmd's default of "skynet" was clobbered by listenCmd's
	// default of "", and `skychat send -t X -m hi` (no --net)
	// surfaced "invalid network type:" because the var was empty.
	sendNet      string
	sendWait     time.Duration
	sendRetries  int
	sendVerbose  bool
	listenNet    string
	listenFrom   string
	listenRaw    bool
	historyPeer  string
	historyLimit int
	historySince string
)

func init() {
	RootCmd.PersistentFlags().StringVar(&httpAddr, "addr", "127.0.0.1:8001", "skychat HTTP address")

	RootCmd.AddCommand(
		sendCmd,
		listenCmd,
		chatCmd,
		historyCmd,
		statusCmd,
		aliasCmd,
	)

	sendCmd.Flags().StringVarP(&recipient, "to", "t", "", "recipient public key (required)")
	sendCmd.Flags().StringVarP(&message, "msg", "m", "", "message to send (required)")
	sendCmd.Flags().StringVarP(&sendNet, "net", "n", "skynet", "network type: skynet or dmsg")
	sendCmd.Flags().DurationVarP(&sendWait, "wait", "w", 5*time.Second, "wait for peer-receipt ack up to this duration (e.g. 5s, 30s); 0 disables wait and returns success on WriteFrame (fire-and-forget). Default 5s gives delivery confirmation.")
	sendCmd.Flags().IntVarP(&sendRetries, "retries", "r", 1, "extra retry attempts on HTTP/transport failure (default 1). 0 disables retry. Each retry waits 200ms × attempt before retrying. Ack timeouts (peer-side failures with --wait) are NOT retried.")
	sendCmd.Flags().BoolVar(&sendVerbose, "verbose", false, "surface per-layer detail to stderr: POST request URL+payload, HTTP response status+headers, ack timing, /status counter deltas (outbound_msg_count / fail / retry / fallback). Use to debug send failures.")
	sendCmd.MarkFlagRequired("to")  //nolint:errcheck,gosec
	sendCmd.MarkFlagRequired("msg") //nolint:errcheck,gosec

	listenCmd.Flags().StringVarP(&listenNet, "net", "n", "", "filter by network type (optional; default = all)")
	listenCmd.Flags().StringVar(&listenFrom, "from", "", "filter by sender public key (optional; full hex PK)")
	// --raw and --json are mutually exclusive; default (neither set)
	// escapes \n and \r so each event is exactly one line of stdout
	// for agent / log-aggregator consumption. See escape.go for the
	// rationale and exact transformation.
	listenCmd.Flags().BoolVar(&listenRaw, "raw", false, "emit unescaped multi-line message bodies (humans reading directly; not safe for line-based log aggregators)")

	historyCmd.Flags().StringVar(&historyPeer, "peer", "", "filter by peer public key (optional; full hex PK)")
	historyCmd.Flags().IntVar(&historyLimit, "limit", 100, "max messages to return (1-1000)")
	historyCmd.Flags().StringVar(&historySince, "since", "", "only return messages newer than this duration (e.g. 1h, 30m, 24h)")

	chatCmd.Flags().StringVarP(&recipient, "to", "t", "", "recipient public key (optional; TUI prompts for it if omitted)")
	chatCmd.Flags().StringVarP(&sendNet, "net", "n", "skynet", "network type for outgoing messages: skynet or dmsg")
	// --to deliberately NOT MarkFlagRequired: an empty recipient
	// drops the TUI into "pick a peer" mode where the textinput
	// gates entry to chat view on a valid PK. Matches the GUI's
	// flow where you type the PK into the header before sending.
}

// RootCmd contains skychat commands
var RootCmd = &cobra.Command{
	Use:   "skychat",
	Short: "Skychat messaging",
	Long:  "Send and receive messages via skychat.",
}

var sendCmd = &cobra.Command{
	Use:   "send",
	Short: "Send a message",
	Long: `Send a message to a remote public key via skychat.

Default semantics (--wait=5s): the message is wrapped in a chat-msg
envelope with a unique id and the command waits up to 5 seconds for
the peer's chat-app to send a chat-ack envelope back. Success means
the peer's chat-app actually received and processed the message —
not just that the local visor handed it to its transport.

With --wait=0 the command returns success on WriteFrame (fire-and-
forget), useful for automation that doesn't care whether the peer
received the message. Pre-2026-05-14 default behavior.

With --wait=DURATION (non-zero, non-default) waits that long for an
ack instead of 5s. Server clamps --wait to [100ms, 60s]; values
outside that range are normalized server-side.

Outcomes (--wait > 0):

  acked within DURATION: command prints "Acked by <pk> in <ms>ms"
                         and exits 0.
  timeout:               command prints "Send to <pk> via <net> not
                         acked: <reason>" and exits 1.
  peer on old binary:    --wait will time out because the chat-msg
                         envelope is interpreted as plain JSON text
                         by pre-2026-05-12 peers. The message was
                         delivered but the peer can't ack. Use
                         --wait=0 against known-old peers.`,
	Run: func(cmd *cobra.Command, _ []string) {
		pk, err := resolveTarget(recipient)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		if err := validateNetwork(sendNet); err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		// Retry on HTTP/transport failure only. Ack timeouts (peer-side
		// failure with --wait) are NOT retried: a peer on the old
		// unframed binary will time out every time, and a healthy peer
		// that took >wait to ack will get the message twice if we
		// retry. The in-handler redial+retry already absorbs the
		// common transient stale-conn case; --retries here is the
		// belt-and-suspenders for HTTP-layer / chat-app-down cases.
		attempts := sendRetries + 1
		if attempts < 1 {
			attempts = 1
		}
		errOut := cmd.ErrOrStderr()
		var preCounters *statusCounters
		if sendVerbose {
			fmt.Fprintf(errOut, "[verbose] target: %s\n", pk.String())                 //nolint:errcheck
			fmt.Fprintf(errOut, "[verbose] network: %s\n", sendNet)                    //nolint:errcheck
			fmt.Fprintf(errOut, "[verbose] msg bytes: %d\n", len(message))             //nolint:errcheck
			fmt.Fprintf(errOut, "[verbose] wait timeout: %s\n", sendWait)              //nolint:errcheck
			fmt.Fprintf(errOut, "[verbose] retries on transport fail: %d\n", attempts) //nolint:errcheck
			fmt.Fprintf(errOut, "[verbose] chat-app addr: http://%s/message\n", httpAddr)
			if c, err := fetchStatusCounters(httpAddr); err == nil {
				preCounters = c
				fmt.Fprintf(errOut, "[verbose] pre-send counters: %s\n", c.summary()) //nolint:errcheck
			} else {
				fmt.Fprintf(errOut, "[verbose] pre-send counters: <fetch failed: %v>\n", err) //nolint:errcheck
			}
		}
		var ack *AckResponse
		for i := 0; i < attempts; i++ {
			if sendVerbose && i > 0 {
				fmt.Fprintf(errOut, "[verbose] retry attempt %d/%d (backoff fired)\n", i+1, attempts) //nolint:errcheck
			}
			ack, err = postMessage(httpAddr, pk.String(), message, sendNet, sendWait)
			if sendVerbose {
				if err != nil {
					fmt.Fprintf(errOut, "[verbose] attempt %d: error: %v\n", i+1, err) //nolint:errcheck
				} else if ack != nil {
					fmt.Fprintf(errOut, "[verbose] attempt %d: ack=%v ms=%d id=%s reason=%q\n", i+1, ack.Acked, ack.MS, ack.ID, ack.Reason) //nolint:errcheck
				} else {
					fmt.Fprintf(errOut, "[verbose] attempt %d: post OK (fire-and-forget, no ack)\n", i+1) //nolint:errcheck
				}
			}
			if err == nil {
				break
			}
			if i+1 < attempts {
				backoff := time.Duration(200*(i+1)) * time.Millisecond
				time.Sleep(backoff)
			}
		}
		if sendVerbose && preCounters != nil {
			if c, err := fetchStatusCounters(httpAddr); err == nil {
				fmt.Fprintf(errOut, "[verbose] post-send counters: %s\n", c.summary()) //nolint:errcheck
				fmt.Fprintf(errOut, "[verbose] delta: %s\n", c.delta(preCounters))     //nolint:errcheck
			}
		}
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		if sendWait > 0 {
			if ack != nil && ack.Acked {
				internal.PrintOutput(cmd.Flags(), nil, fmt.Sprintf("Acked by %s in %dms (id=%s)\n", pk.String(), ack.MS, ack.ID))
			} else {
				reason := "no ack"
				if ack != nil && ack.Reason != "" {
					reason = ack.Reason
				}
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("send to %s via %s not acked: %s", pk.String(), sendNet, reason))
			}
			return
		}
		internal.PrintOutput(cmd.Flags(), nil, fmt.Sprintf("Message sent to %s via %s\n", pk.String(), sendNet))
	},
}

var historyCmd = &cobra.Command{
	Use:   "history",
	Short: "Print persisted message history",
	Long: `Print the locally persisted message history.

Recovery path for missed messages: when the listener is down (visor
restart, shell filter ate the events, the agent driver missed a
notification), the history store still has them. This command reads
straight from the chat-app's /history HTTP endpoint and prints the
result.

Filters:
  --peer  <PK>     only this peer's messages (full hex PK)
  --limit N        max messages to return (default 100, max 1000)
  --since DURATION client-side filter: drop messages older than this
                   (e.g. 1h, 30m, 24h, 168h for a week)

With --json: prints {ts, peer, from, outgoing, text} per line (NDJSON,
matches the listen schema as closely as possible). Without --json:
prints "<ISO ts> [in|out @ peer] body" one per line.

Returns an error if the chat-app has persistence disabled.`,
	Run: func(cmd *cobra.Command, _ []string) {
		if historyLimit <= 0 || historyLimit > 1000 {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("--limit must be 1-1000, got %d", historyLimit))
		}
		if historyPeer != "" {
			pk, err := resolveTarget(historyPeer)
			if err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("--peer: %w", err))
			}
			historyPeer = pk.Hex()
		}
		var sinceCutoff time.Time
		if historySince != "" {
			d, err := time.ParseDuration(historySince)
			if err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("invalid --since duration %q: %w", historySince, err))
			}
			if d < 0 {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("--since duration must be positive, got %s", d))
			}
			sinceCutoff = time.Now().Add(-d)
		}

		url := fmt.Sprintf("http://%s/history?limit=%d", httpAddr, historyLimit)
		if historyPeer != "" {
			url += "&peer=" + historyPeer
		}
		resp, err := http.Get(url) //nolint:gosec
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("history fetch: %w", err))
		}
		defer resp.Body.Close() //nolint:errcheck
		if resp.StatusCode == http.StatusServiceUnavailable {
			internal.PrintFatalError(cmd.Flags(), errors.New("chat-app persistence not enabled (start visor with --persist-skychat-history)"))
		}
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body) //nolint:errcheck
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("history fetch: server %d: %s", resp.StatusCode, strings.TrimSpace(string(body))))
		}

		// /history returns a JSON array of history.Message records.
		var msgs []struct {
			Peer      string    `json:"peer"`
			From      string    `json:"from"`
			Outgoing  bool      `json:"outgoing"`
			Text      string    `json:"text"`
			Timestamp time.Time `json:"timestamp"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&msgs); err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("decode history: %w", err))
		}

		jsonMode, _ := cmd.Flags().GetBool(internal.JSONString) //nolint:errcheck
		out := cmd.OutOrStdout()

		for _, m := range msgs {
			if !sinceCutoff.IsZero() && m.Timestamp.Before(sinceCutoff) {
				continue
			}
			if jsonMode {
				ev := struct {
					TS       time.Time `json:"ts"`
					Peer     string    `json:"peer"`
					From     string    `json:"from,omitempty"`
					Outgoing bool      `json:"outgoing"`
					Text     string    `json:"text"`
				}{
					TS:       m.Timestamp,
					Peer:     m.Peer,
					From:     m.From,
					Outgoing: m.Outgoing,
					Text:     m.Text,
				}
				b, _ := json.Marshal(ev)          //nolint:errcheck
				_, _ = out.Write(append(b, '\n')) //nolint:errcheck
			} else {
				dir := "in"
				if m.Outgoing {
					dir = "out"
				}
				peerDisplay := m.Peer
				if alias := lookupAlias(m.Peer); alias != "" {
					peerDisplay = alias
				}
				fmt.Fprintf(out, "%s [%s @ %s] %s\n", m.Timestamp.UTC().Format(time.RFC3339), dir, peerDisplay, m.Text) //nolint:errcheck
			}
		}
	},
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Probe the skychat app's health",
	Long: `Print a snapshot of the skychat app's runtime health: visor PK,
SSE subscriber count, active peer conns and their PKs, whether
persistence + pairing are enabled.

Operator use: "is my chat app working" without docker exec / ss /
curl gymnastics. Returns 1 + a clear error if the chat-app's HTTP
endpoint isn't reachable.

Default output is human-readable lines; --json emits the raw status
object suitable for scripts.`,
	Run: func(cmd *cobra.Command, _ []string) {
		url := fmt.Sprintf("http://%s/status", httpAddr)
		resp, err := http.Get(url) //nolint:gosec
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("chat app unreachable at %s: %w", httpAddr, err))
		}
		defer resp.Body.Close() //nolint:errcheck
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body) //nolint:errcheck
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("status fetch: server %d: %s", resp.StatusCode, strings.TrimSpace(string(body))))
		}

		raw, err := io.ReadAll(resp.Body)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("status read: %w", err))
		}

		jsonMode, _ := cmd.Flags().GetBool(internal.JSONString) //nolint:errcheck
		out := cmd.OutOrStdout()

		if jsonMode {
			_, _ = out.Write(raw) //nolint:errcheck
			if len(raw) > 0 && raw[len(raw)-1] != '\n' {
				_, _ = out.Write([]byte{'\n'}) //nolint:errcheck
			}
			return
		}

		var status struct {
			VisorPK            string   `json:"visor_pk"`
			SSESubscribers     int      `json:"sse_subscribers"`
			ActivePeerConns    int      `json:"active_peer_conns"`
			Peers              []string `json:"peers"`
			PersistenceEnabled bool     `json:"persistence_enabled"`
			PairingEnabled     bool     `json:"pairing_enabled"`
			FrameProtoVersion  int      `json:"frame_proto_version"`
			SchemaVersion      string   `json:"schema_version"`
			AppUptimeSec       int64    `json:"app_uptime_sec"`
			InboundMsgCount    uint64   `json:"inbound_msg_count"`
			OutboundMsgCount   uint64   `json:"outbound_msg_count"`
			InboundDropCount   uint64   `json:"inbound_drop_count"`
			LastRxTS           string   `json:"last_rx_ts"`
			LastSendTS         string   `json:"last_send_ts"`
			Error              string   `json:"error,omitempty"`
		}
		if err := json.Unmarshal(raw, &status); err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("decode status: %w", err))
		}
		fmt.Fprintf(out, "Chat app at %s\n", httpAddr)                            //nolint:errcheck
		fmt.Fprintf(out, "  visor PK:           %s\n", status.VisorPK)            //nolint:errcheck
		fmt.Fprintf(out, "  app uptime:         %ds\n", status.AppUptimeSec)      //nolint:errcheck
		fmt.Fprintf(out, "  frame proto:        v%d\n", status.FrameProtoVersion) //nolint:errcheck
		fmt.Fprintf(out, "  schema:             %s\n", status.SchemaVersion)      //nolint:errcheck
		fmt.Fprintf(out, "  SSE subscribers:    %d\n", status.SSESubscribers)     //nolint:errcheck
		fmt.Fprintf(out, "  active peer conns:  %d\n", status.ActivePeerConns)    //nolint:errcheck
		if len(status.Peers) > 0 {
			for _, p := range status.Peers {
				fmt.Fprintf(out, "    - %s\n", p) //nolint:errcheck
			}
		}
		fmt.Fprintf(out, "  inbound msgs:       %d (drops: %d)\n", status.InboundMsgCount, status.InboundDropCount) //nolint:errcheck
		fmt.Fprintf(out, "  outbound msgs:      %d\n", status.OutboundMsgCount)                                     //nolint:errcheck
		if status.LastRxTS != "" {
			fmt.Fprintf(out, "  last rx:            %s\n", status.LastRxTS) //nolint:errcheck
		}
		if status.LastSendTS != "" {
			fmt.Fprintf(out, "  last send:          %s\n", status.LastSendTS) //nolint:errcheck
		}
		fmt.Fprintf(out, "  persistence:        %v\n", status.PersistenceEnabled) //nolint:errcheck
		fmt.Fprintf(out, "  pairing:            %v\n", status.PairingEnabled)     //nolint:errcheck
		if status.Error != "" {
			fmt.Fprintf(out, "  error:              %s\n", status.Error) //nolint:errcheck
		}
	},
}

// validateNetwork rejects anything but "skynet" / "dmsg" — same
// values the skychat app's /message handler accepts. Centralized so
// `send`, the interactive `chat` view, and any future variant share
// one source of truth.
func validateNetwork(n string) error {
	switch n {
	case "skynet", "dmsg":
		return nil
	}
	return fmt.Errorf("invalid network type %q (use 'skynet' or 'dmsg')", n)
}

// AckResponse is the JSON body the skychat app's /message endpoint
// returns when wait_ms > 0. Exported so the TUI / scripted callers
// can consume the same shape.
type AckResponse struct {
	Acked  bool   `json:"acked"`
	ID     string `json:"id,omitempty"`
	MS     int64  `json:"ms,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// postMessage POSTs the message payload to the skychat app's HTTP
// /message endpoint. Returns nil on 2xx (with no wait) or the ack
// response when wait > 0. An error means the request failed at the
// HTTP layer or the chat-app surfaced a 4xx/5xx that's not the
// 504-on-timeout case (which is returned as a non-nil ack with
// Acked=false).
func postMessage(addr, recipientPK, msg, network string, wait time.Duration) (*AckResponse, error) {
	payload := map[string]interface{}{
		"recipient": recipientPK,
		"message":   msg,
		"network":   network,
	}
	if wait > 0 {
		payload["wait_ms"] = wait.Milliseconds()
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	url := fmt.Sprintf("http://%s/message", addr)
	// Client timeout: wait + grace. Without this the http.Client's
	// default no-timeout would let a server-side hang block the CLI
	// indefinitely; with it, the CLI deadline is always at least
	// `wait` + 5s so the server's own clamp/timeout fires first.
	hc := &http.Client{Timeout: wait + 5*time.Second}
	if wait <= 0 {
		hc.Timeout = 30 * time.Second
	}
	resp, err := hc.Post(url, "application/json", bytes.NewReader(body)) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("post: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	// 504 carries a non-nil AckResponse{Acked:false, Reason:"timeout"}
	// — surfaced to the caller, not treated as a server error.
	if resp.StatusCode == http.StatusGatewayTimeout {
		var ack AckResponse
		if err := json.NewDecoder(resp.Body).Decode(&ack); err == nil {
			return &ack, nil
		}
		return &AckResponse{Acked: false, Reason: "timeout"}, nil
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if wait > 0 {
			var ack AckResponse
			if err := json.NewDecoder(resp.Body).Decode(&ack); err != nil {
				return nil, fmt.Errorf("decode ack: %w", err)
			}
			return &ack, nil
		}
		return nil, nil
	}
	var errBody bytes.Buffer
	_, _ = errBody.ReadFrom(resp.Body) //nolint:errcheck
	return nil, fmt.Errorf("server error %d: %s", resp.StatusCode, strings.TrimSpace(errBody.String()))
}

// Reconnect cadence for the listen loop. Starts at minReconnectDelay
// (short enough to recover from a normal visor restart without
// noticeable downtime) and doubles up to maxReconnectDelay on
// repeated failures (long enough that a visor that's down for good
// stops hammering 127.0.0.1). Successful connect resets the backoff.
const (
	minReconnectDelay = 1 * time.Second
	maxReconnectDelay = 30 * time.Second
)

var listenCmd = &cobra.Command{
	Use:   "listen",
	Short: "Listen for incoming messages",
	Long: `Connect to skychat SSE endpoint and display incoming messages.

Reconnects automatically when the connection drops (e.g. the visor
restarts). Exit with Ctrl+C.

Output modes (mutually exclusive — passing both --raw and --json
errors out):
  text (default): one line per event, format "[sender[/net]] body" for
    inbound messages, "[>sender[/net]] body" for outbound mirrors and
    other non-inbound directions; reconnect errors to stderr.
    Embedded newlines and carriage returns in the body are escaped to
    the literal two-character sequences \n / \r so every event is
    exactly ONE line of stdout. This is the mode agents and log-line
    aggregators should use — otherwise a multi-line chat body would
    fragment into N stdout events.
  --raw: same text format, but the body is NOT escaped. Use this when
    a human is reading the output directly and wants the original
    multi-line layout. Not safe for monitor harnesses that treat each
    line as a separate event.
  --json: one JSON object per stdout line (NDJSON). Bodies pass through
    JSON's native string encoding (\n, \r, etc are escaped by the JSON
    encoder), so this mode is also one-line-per-event. Event types:
    {"type":"banner",...}    once at startup (addr + filter context)
    {"type":"msg",...}       a message; fields: ts, from, net, body, dir
    {"type":"reconnect",...} SSE stream is being re-opened; fields: ts, err, delay_ms
    {"type":"error",...}     unparseable / unexpected; fields: ts, err
    Errors do NOT go to stderr in JSON mode — every signal is on stdout
    with a "type" tag so consumers can demux without merging two streams.

Direction field ("dir") on msg events:
  "in"   — message received from a peer.
  "out"  — local visor sent the message; mirror surfaced so headless
           listeners see a complete transcript. NOTE: "out" means the
           framed payload was handed to the skywire transport (WriteFrame
           returned without error). It does NOT mean the peer's chat-app
           has received or processed the message. Peer-app receipt-ack
           is a deferred protocol feature (msg-id + chat-ack envelope);
           do not treat "dir":"out" as delivery confirmation.
  Future values ("relay", "group-in", "group-out") may appear as group
  and relay flows land; consumers should treat unknown dir values as
  non-inbound rather than rejecting them.

Filters:
  --net   skynet|dmsg      surface only that transport's messages
  --from  <PK hex>         surface only this sender (handy for N-way
                           channels; filter applies before output)`,
	Run: func(cmd *cobra.Command, _ []string) {
		// Empty listenNet means "all"; if set, only print messages
		// whose network field matches.
		netFilter := listenNet
		if netFilter != "" {
			if err := validateNetwork(netFilter); err != nil {
				internal.PrintFatalError(cmd.Flags(), err)
			}
		}
		// --from accepts either a 66-hex PK or an alias name. Resolve
		// up front and convert to the canonical hex form server-side.
		if listenFrom != "" {
			pk, err := resolveTarget(listenFrom)
			if err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("--from: %w", err))
			}
			listenFrom = pk.Hex()
		}

		jsonMode, _ := cmd.Flags().GetBool(internal.JSONString) //nolint:errcheck
		if jsonMode && listenRaw {
			internal.PrintFatalError(cmd.Flags(), errors.New("--raw and --json are mutually exclusive"))
		}
		out := cmd.OutOrStdout()
		errOut := cmd.ErrOrStderr()

		url := fmt.Sprintf("http://%s/sse", httpAddr)

		if jsonMode {
			emitJSON(out, listenEvent{
				Type:   evtBanner,
				TS:     time.Now().UTC(),
				Addr:   httpAddr,
				Schema: listenSchemaVersion,
				Net:    netFilter,
				From:   listenFrom,
			})
		} else {
			fmt.Fprintf(out, "Listening for messages from %s", httpAddr) //nolint:errcheck
			if netFilter != "" {
				fmt.Fprintf(out, " (net=%s)", netFilter) //nolint:errcheck
			}
			if listenFrom != "" {
				fmt.Fprintf(out, " (from=%s)", listenFrom) //nolint:errcheck
			}
			fmt.Fprint(out, "\nPress Ctrl+C to stop\n\n") //nolint:errcheck
		}

		ctx := cmd.Context()
		if ctx == nil {
			ctx = context.Background()
		}

		delay := minReconnectDelay
		for {
			err := streamSSEOnce(ctx, out, url, netFilter, listenFrom, jsonMode, listenRaw)
			if ctx.Err() != nil {
				// Caller hit Ctrl+C; exit cleanly.
				return
			}
			if err == nil {
				// Server closed the stream cleanly; treat as a
				// reconnect prompt rather than an error.
				delay = minReconnectDelay
			} else {
				if jsonMode {
					emitJSON(out, listenEvent{
						Type:    evtReconnect,
						TS:      time.Now().UTC(),
						Err:     err.Error(),
						DelayMS: delay.Milliseconds(),
					})
				} else {
					fmt.Fprintf(errOut, "skychat listen: %v (reconnecting in %s)\n", err, delay) //nolint:errcheck
				}
			}

			// Sleep before retry, but honor Ctrl+C while sleeping.
			t := time.NewTimer(delay)
			select {
			case <-t.C:
			case <-ctx.Done():
				t.Stop()
				return
			}

			// Exponential backoff up to maxReconnectDelay.
			delay *= 2
			if delay > maxReconnectDelay {
				delay = maxReconnectDelay
			}
		}
	},
}

// listenEvent is the NDJSON wire-shape emitted by listen --json.
// One JSON object per stdout line; consumers parse line-at-a-time.
// Field tagging is stable — adding new fields is backward-compatible
// (consumers ignore unknown fields); changing or removing a field
// requires a wire-version bump.
type listenEvent struct {
	Type string    `json:"type"`
	TS   time.Time `json:"ts"`

	// Banner-only.
	Addr   string `json:"addr,omitempty"`
	Schema string `json:"schema,omitempty"` // listen-output schema version (currently "v1")

	// Msg-only.
	ID        string `json:"id,omitempty"`         // stable per-event identifier; used by send-ack to correlate
	From      string `json:"from,omitempty"`       // local side: own PK on dir:out, peer PK on dir:in
	FromAlias string `json:"from_alias,omitempty"` // resolved alias for From (consumer-side, best-effort)
	To        string `json:"to,omitempty"`         // dir:out only — peer the message was sent to
	ToAlias   string `json:"to_alias,omitempty"`   // resolved alias for To (consumer-side, best-effort)
	Net       string `json:"net,omitempty"`
	Body      string `json:"body,omitempty"`
	Dir       string `json:"dir,omitempty"` // "in" | "out" | future: "relay" | "group-in" | "group-out"
	Len       int    `json:"len,omitempty"` // body byte length, surfaced for size-debug w/o re-parsing

	// Reconnect / error.
	Err     string `json:"err,omitempty"`
	DelayMS int64  `json:"delay_ms,omitempty"`
}

// listenSchemaVersion is the CLI-side view of the wire-shape version.
// Mirrors what the chat app emits in /status as "schema_version".
const listenSchemaVersion = "v1"

const (
	evtBanner    = "banner"
	evtMsg       = "msg"
	evtReconnect = "reconnect"
	evtError     = "error"
)

// emitJSON writes one NDJSON line. Errors are intentionally swallowed
// — the listener is best-effort and we can't do anything useful if
// stdout is broken.
func emitJSON(w io.Writer, ev listenEvent) {
	b, err := json.Marshal(ev)
	if err != nil {
		return
	}
	b = append(b, '\n')
	_, _ = w.Write(b) //nolint:errcheck
}

// streamSSEOnce opens a single SSE connection and drains it until
// the stream ends, the context is canceled, or an error occurs.
// Returns nil on clean end-of-stream so the caller can retry
// without surfacing it as a failure.
//
// netFilter (""|skynet|dmsg) and fromFilter ("" | full-hex PK)
// suppress non-matching events before any output.
//
// raw=false (default) escapes \n / \r in the message body so every
// text-mode event is exactly one line of stdout; raw=true preserves
// the original body verbatim. jsonMode supersedes both (the JSON
// encoder handles escaping natively).
func streamSSEOnce(ctx context.Context, out io.Writer, url, netFilter, fromFilter string, jsonMode, raw bool) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("build SSE request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("server returned status %d", resp.StatusCode)
	}

	scanner := bufio.NewScanner(resp.Body)
	// Default Scanner buffer is 64KB which can split long SSE payloads
	// (skychat frames cap at 64KB but the SSE wrapping adds the "data: "
	// prefix + newline). Bump to 256KB to leave headroom.
	scanner.Buffer(make([]byte, 0, 64*1024), 256*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		var msg struct {
			Sender  string `json:"sender"`
			Message string `json:"message"`
			Network string `json:"network,omitempty"`
			Dir     string `json:"dir,omitempty"` // "in" | "out" | future: "relay" | "group-in" | "group-out"
			ID      string `json:"id,omitempty"`
			To      string `json:"to,omitempty"`
			Len     int    `json:"len,omitempty"`
		}
		if err := json.Unmarshal([]byte(data), &msg); err != nil {
			if jsonMode {
				emitJSON(out, listenEvent{
					Type: evtError,
					TS:   time.Now().UTC(),
					Err:  fmt.Sprintf("unparseable SSE data: %s", data),
				})
			} else {
				fmt.Fprintf(out, "Raw: %s\n", data) //nolint:errcheck
			}
			continue
		}
		if netFilter != "" && msg.Network != "" && msg.Network != netFilter {
			continue
		}
		if fromFilter != "" && msg.Sender != fromFilter {
			continue
		}

		// dir defaults to "in" for back-compat with older skychat-app
		// servers that emit SSE events without a dir field at all.
		dir := msg.Dir
		if dir == "" {
			dir = "in"
		}

		if jsonMode {
			ev := listenEvent{
				Type: evtMsg,
				TS:   time.Now().UTC(),
				ID:   msg.ID,
				From: msg.Sender,
				To:   msg.To,
				Net:  msg.Network,
				Body: msg.Message,
				Dir:  dir,
				Len:  msg.Len,
			}
			// Best-effort alias resolution: if the local addressbook
			// has a name for the PK, surface it as a sibling field so
			// scripted consumers can pretty-print without re-querying.
			// Empty when unknown — never blocks or errors.
			if alias := lookupAlias(msg.Sender); alias != "" {
				ev.FromAlias = alias
			}
			if alias := lookupAlias(msg.To); alias != "" {
				ev.ToAlias = alias
			}
			emitJSON(out, ev)
		} else {
			netSuffix := ""
			if msg.Network != "" {
				netSuffix = "/" + msg.Network
			}
			// ">" marks anything that ISN'T a normal inbound event —
			// outgoing-mirror, future relay/group flows. Keeps text
			// mode's one-line format readable while distinguishing
			// the common-case "in" from everything else.
			dirPrefix := ""
			if dir != "in" {
				dirPrefix = ">"
			}
			// Reverse-resolve the sender PK to a friendlier alias for
			// display. Falls back to the hex PK when no alias matches.
			display := msg.Sender
			if alias := lookupAlias(msg.Sender); alias != "" {
				display = alias
			}
			body := msg.Message
			if !raw {
				body = escapeForOneLine(body)
			}
			fmt.Fprintf(out, "[%s%s%s] %s\n", dirPrefix, display, netSuffix, body) //nolint:errcheck
		}
	}
	// scanner.Err() returns nil on a clean EOF; io.EOF /
	// io.ErrUnexpectedEOF / a canceled context all show up here too.
	// Map "the connection went away" cases to nil so the caller's
	// retry loop treats them as a normal end-of-stream rather than a
	// fatal log line. A real error (server-side malformed frame,
	// etc.) still bubbles up.
	if err := scanner.Err(); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || ctx.Err() != nil {
			return nil
		}
		return err
	}
	return nil
}
