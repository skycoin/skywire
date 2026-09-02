// Package cliskychat cmd/skywire-cli/commands/skychat/chat.go c4-vis-cli
// interactive bubbletea split-pane chat against the local skychat
// app's HTTP/SSE interface.
//
// Top: scrollable viewport of message history (sent + received).
// Bottom: textinput compose line. Enter sends; Esc / Ctrl+C quits.
// A background goroutine reads the /sse stream and pushes incoming
// messages into the bubbletea program via a channel; outgoing
// messages POST to /message synchronously on Enter.
//
// `--to <pk>` pins the conversation to one remote. Messages from
// other senders still appear in the history (so you can see other
// peers trying to reach you) but Enter always sends to --to.
package cliskychat

import (
	"fmt"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
)

var chatCmd = &cobra.Command{
	Use:   "chat",
	Short: "Interactive chat TUI (bubbletea split-pane)",
	Long: `Interactive chat against the local skychat app.

Without --to: launches the unified TUI — a conversation picker that
lists known 1:1 peers (from chat history) and joined groups. Pick
an entry with ↑/↓ + Enter; Esc returns to the picker. From the
picker you can also press N (new DM), J (join group via invite), G
(create group), R (refresh).

With --to <pk-or-alias>: short-circuits straight into a 1:1 chat
view for that recipient. Same view shape as the picker path but
the conversation list is hidden.

Top pane scrolls message history; bottom pane is the compose line.
Enter sends. Esc or Ctrl+C quits/back. ↑/↓ or PgUp/PgDn scroll
history. Ctrl+N cycles outgoing network (skynet/dmsg) for 1:1
sends.

Requires the skychat app to be running (default --addr 127.0.0.1:8001).`,
	Run: func(cmd *cobra.Command, _ []string) {
		// Empty recipient: default to the unified picker.
		// Anything non-empty must parse as a valid PK or a known
		// alias up front so a typo on the command line surfaces
		// immediately rather than after the TUI takes over the
		// terminal.
		if err := validateNetwork(sendNet); err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		if recipient == "" {
			if err := runUnifiedTUI(httpAddr, sendNet); err != nil {
				internal.PrintFatalError(cmd.Flags(), err)
			}
			return
		}
		pk, err := resolveTarget(recipient)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("--to: %w", err))
		}
		if err := runChatTUI(httpAddr, pk.String(), sendNet); err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
	},
}

// shortPK abbreviates a public key for display in fixed-width panes: the
// first 12 chars uniquely identify any peer in practice and keep the visual
// alignment clean.
func shortPK(pk string) string {
	if len(pk) <= 12 {
		return pk
	}
	return pk[:12] + "…"
}
