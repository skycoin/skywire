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
	sendNet   string
	listenNet string
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
restarts). Exit with Ctrl+C.`,
	Run: func(cmd *cobra.Command, _ []string) {
		// Empty listenNet means "all"; if set, only print messages
		// whose network field matches.
		filter := listenNet
		if filter != "" {
			if err := validateNetwork(filter); err != nil {
				internal.PrintFatalError(cmd.Flags(), err)
			}
		}

		url := fmt.Sprintf("http://%s/sse", httpAddr)
		fmt.Printf("Listening for messages from %s", httpAddr)
		if filter != "" {
			fmt.Printf(" (filter: %s)", filter)
		}
		fmt.Print("\nPress Ctrl+C to stop\n\n")

		ctx := cmd.Context()
		if ctx == nil {
			ctx = context.Background()
		}

		delay := minReconnectDelay
		for {
			err := streamSSEOnce(ctx, url, filter)
			if ctx.Err() != nil {
				// Caller hit Ctrl+C; exit cleanly.
				return
			}
			if err == nil {
				// Server closed the stream cleanly; treat as a
				// reconnect prompt rather than an error.
				delay = minReconnectDelay
			} else {
				fmt.Fprintf(cmd.ErrOrStderr(), //nolint:errcheck
					"skychat listen: %v (reconnecting in %s)\n", err, delay)
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

// streamSSEOnce opens a single SSE connection and drains it until
// the stream ends, the context is canceled, or an error occurs.
// Returns nil on clean end-of-stream so the caller can retry
// without surfacing it as a failure.
func streamSSEOnce(ctx context.Context, url, filter string) error {
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
		}
		if err := json.Unmarshal([]byte(data), &msg); err != nil {
			fmt.Printf("Raw: %s\n", data)
			continue
		}
		if filter != "" && msg.Network != "" && msg.Network != filter {
			continue
		}
		net := ""
		if msg.Network != "" {
			net = "/" + msg.Network
		}
		fmt.Printf("[%s%s] %s\n", msg.Sender, net, msg.Message)
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
