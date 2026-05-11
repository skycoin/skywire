// Package cliskychat cmd/skywire-cli/commands/skychat/root.go
package cliskychat

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

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

	chatCmd.Flags().StringVarP(&recipient, "to", "t", "", "recipient public key (required)")
	chatCmd.Flags().StringVarP(&sendNet, "net", "n", "skynet", "network type for outgoing messages: skynet or dmsg")
	chatCmd.MarkFlagRequired("to") //nolint:errcheck,gosec
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

var listenCmd = &cobra.Command{
	Use:   "listen",
	Short: "Listen for incoming messages",
	Long:  "Connect to skychat SSE endpoint and display incoming messages.",
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
		fmt.Println("\nPress Ctrl+C to stop\n")

		resp, err := http.Get(url) //nolint:gosec
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to connect to SSE: %w", err))
		}
		defer resp.Body.Close() //nolint:errcheck

		if resp.StatusCode != http.StatusOK {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("server returned status %d", resp.StatusCode))
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
		if err := scanner.Err(); err != nil && err.Error() != "EOF" {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("SSE connection error: %w", err))
		}
	},
}
