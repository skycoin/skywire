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

	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
)

var (
	httpAddr    string
	recipient   string
	message     string
	networkType string
)

func init() {
	RootCmd.PersistentFlags().StringVar(&httpAddr, "addr", "127.0.0.1:8001", "skychat HTTP address")

	RootCmd.AddCommand(
		sendCmd,
		listenCmd,
	)

	sendCmd.Flags().StringVarP(&recipient, "to", "t", "", "recipient public key (required)")
	sendCmd.Flags().StringVarP(&message, "msg", "m", "", "message to send (required)")
	sendCmd.Flags().StringVarP(&networkType, "net", "n", "skynet", "network type: skynet or dmsg")
	sendCmd.MarkFlagRequired("to")  //nolint:errcheck,gosec
	sendCmd.MarkFlagRequired("msg") //nolint:errcheck,gosec

	listenCmd.Flags().StringVarP(&networkType, "net", "n", "", "filter by network type (optional)")
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
		// Validate network type
		if networkType != "skynet" && networkType != "dmsg" {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("invalid network type: %s (use 'skynet' or 'dmsg')", networkType))
		}

		// Build request body
		body := map[string]string{
			"recipient": recipient,
			"message":   message,
			"network":   networkType,
		}
		jsonBody, err := json.Marshal(body)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to marshal request: %w", err))
		}

		// Send POST request
		url := fmt.Sprintf("http://%s/message", httpAddr)
		resp, err := http.Post(url, "application/json", bytes.NewReader(jsonBody)) //nolint:gosec
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to send message: %w", err))
		}
		defer resp.Body.Close() //nolint:errcheck

		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
			var errBody bytes.Buffer
			errBody.ReadFrom(resp.Body) //nolint:errcheck,gosec
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("server error (%d): %s", resp.StatusCode, errBody.String()))
		}

		internal.PrintOutput(cmd.Flags(), nil, fmt.Sprintf("Message sent to %s via %s\n", recipient[:16]+"...", networkType))
	},
}

var listenCmd = &cobra.Command{
	Use:   "listen",
	Short: "Listen for incoming messages",
	Long:  "Connect to skychat SSE endpoint and display incoming messages.",
	Run: func(cmd *cobra.Command, _ []string) {
		url := fmt.Sprintf("http://%s/sse", httpAddr)

		fmt.Printf("Listening for messages from %s...\n", httpAddr)
		fmt.Println("Press Ctrl+C to stop")
		fmt.Println()

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
			if strings.HasPrefix(line, "data: ") {
				data := strings.TrimPrefix(line, "data: ")

				// Parse the JSON message
				var msg struct {
					Sender  string `json:"sender"`
					Message string `json:"message"`
				}
				if err := json.Unmarshal([]byte(data), &msg); err != nil {
					fmt.Printf("Raw: %s\n", data)
					continue
				}

				// Format and display the message
				senderShort := msg.Sender
				if len(senderShort) > 16 {
					senderShort = senderShort[:16] + "..."
				}
				fmt.Printf("[%s] %s\n", senderShort, msg.Message)
			}
		}

		if err := scanner.Err(); err != nil {
			if err.Error() != "EOF" {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("SSE connection error: %w", err))
			}
		}
	},
}
