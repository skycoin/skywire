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
	"github.com/skycoin/skywire/pkg/cipher"
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
	sendNet    string
	listenNet  string
	listenFrom string
)

func init() {
	RootCmd.PersistentFlags().StringVar(&httpAddr, "addr", "127.0.0.1:8001", "skychat HTTP address")

	RootCmd.AddCommand(
		sendCmd,
		listenCmd,
		chatCmd,
	)

	sendCmd.Flags().StringVarP(&recipient, "to", "t", "", "recipient public key (required)")
	sendCmd.Flags().StringVarP(&message, "msg", "m", "", "message to send (required)")
	sendCmd.Flags().StringVarP(&sendNet, "net", "n", "skynet", "network type: skynet or dmsg")
	sendCmd.MarkFlagRequired("to")  //nolint:errcheck,gosec
	sendCmd.MarkFlagRequired("msg") //nolint:errcheck,gosec

	listenCmd.Flags().StringVarP(&listenNet, "net", "n", "", "filter by network type (optional; default = all)")
	listenCmd.Flags().StringVar(&listenFrom, "from", "", "filter by sender public key (optional; full hex PK)")

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
	Long:  "Send a message to a remote public key via skychat.",
	Run: func(cmd *cobra.Command, _ []string) {
		var pk cipher.PubKey
		if err := pk.Set(recipient); err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("invalid recipient public key %q: %w", recipient, err))
		}
		if err := validateNetwork(sendNet); err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		if err := postMessage(httpAddr, pk.String(), message, sendNet); err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		internal.PrintOutput(cmd.Flags(), nil, fmt.Sprintf("Message sent to %s via %s\n", pk.String(), sendNet))
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

// postMessage POSTs the message payload to the skychat app's HTTP
// /message endpoint. Returns nil on 2xx, an error with the response
// body otherwise. Pulled out of sendCmd so the interactive `chat`
// TUI can reuse it on every Enter.
func postMessage(addr, recipientPK, msg, network string) error {
	body, err := json.Marshal(map[string]string{
		"recipient": recipientPK,
		"message":   msg,
		"network":   network,
	})
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
	url := fmt.Sprintf("http://%s/message", addr)
	resp, err := http.Post(url, "application/json", bytes.NewReader(body)) //nolint:gosec
	if err != nil {
		return fmt.Errorf("post: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	var errBody bytes.Buffer
	_, _ = errBody.ReadFrom(resp.Body) //nolint:errcheck
	return fmt.Errorf("server error %d: %s", resp.StatusCode, strings.TrimSpace(errBody.String()))
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

Output modes:
  text (default): one line per event, format "[sender[/net]] body" for
    inbound messages, "[>sender[/net]] body" for outbound mirrors and
    other non-inbound directions; reconnect errors to stderr.
  --json: one JSON object per stdout line (NDJSON). Event types:
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
		// --from must be a valid full-hex PK if supplied; reject early
		// rather than silently never matching.
		var fromFilter cipher.PubKey
		if listenFrom != "" {
			if err := fromFilter.Set(listenFrom); err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("invalid --from public key %q: %w", listenFrom, err))
			}
		}

		jsonMode, _ := cmd.Flags().GetBool(internal.JSONString) //nolint:errcheck
		out := cmd.OutOrStdout()
		errOut := cmd.ErrOrStderr()

		url := fmt.Sprintf("http://%s/sse", httpAddr)

		if jsonMode {
			emitJSON(out, listenEvent{
				Type: evtBanner,
				TS:   time.Now().UTC(),
				Addr: httpAddr,
				Net:  netFilter,
				From: listenFrom,
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
			err := streamSSEOnce(ctx, out, url, netFilter, listenFrom, jsonMode)
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
	Addr string `json:"addr,omitempty"`

	// Msg-only.
	From string `json:"from,omitempty"`
	Net  string `json:"net,omitempty"`
	Body string `json:"body,omitempty"`
	Dir  string `json:"dir,omitempty"` // "in" | "out"

	// Reconnect / error.
	Err     string `json:"err,omitempty"`
	DelayMS int64  `json:"delay_ms,omitempty"`
}

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
func streamSSEOnce(ctx context.Context, out io.Writer, url, netFilter, fromFilter string, jsonMode bool) error {
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
			emitJSON(out, listenEvent{
				Type: evtMsg,
				TS:   time.Now().UTC(),
				From: msg.Sender,
				Net:  msg.Network,
				Body: msg.Message,
				Dir:  dir,
			})
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
			fmt.Fprintf(out, "[%s%s%s] %s\n", dirPrefix, msg.Sender, netSuffix, msg.Message) //nolint:errcheck
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
