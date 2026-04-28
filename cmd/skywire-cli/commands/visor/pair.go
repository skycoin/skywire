// Package clivisor cmd/skywire-cli/commands/visor/pair.go
//
// `skywire cli visor pair` — drive the visor's chat-pair feed
// manager from the CLI. Handy for testing the pairing pipeline
// without running the skychat app, and for scripted setups where
// pairs are bootstrapped from config (e.g. seed-pair lists in
// integration tests).
//
// Each subcommand wraps one Visor.Pair* RPC method:
//
//	pair add <peer-pk>          → PairAdd
//	pair ls                     → PairList
//	pair rm <peer-pk>           → PairRemove
//	pair send <peer-pk> <text>  → PairSend
//	pair poll [--since <RFC3339>] → PairPoll
package clivisor

import (
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/pkg/cipher"
)

var pairPollSince string

func init() {
	pairPollCmd.Flags().StringVar(&pairPollSince, "since", "",
		"only report messages with TS strictly after this RFC3339 timestamp; empty drains the full window")
	pairCmd.AddCommand(pairAddCmd)
	pairCmd.AddCommand(pairLsCmd)
	pairCmd.AddCommand(pairRmCmd)
	pairCmd.AddCommand(pairSendCmd)
	pairCmd.AddCommand(pairPollCmd)
	RootCmd.AddCommand(pairCmd)
}

var pairCmd = &cobra.Command{
	Use:   "pair",
	Short: "Manage chat-pair feeds",
	Long: `Drive the visor's chat-pair feed manager.

Each pair is a per-partner CXO feed with an allowlist of exactly the
partner's public key. Both ends must call ` + "`pair add`" + ` with the
other's PK before messages flow.

Without a subcommand this prints the active pair list.`,
	Run: func(cmd *cobra.Command, _ []string) {
		listPairs(cmd)
	},
}

var pairAddCmd = &cobra.Command{
	Use:   "add <peer-pk>",
	Short: "Register a chat pair with peer-pk",
	Long: `Open a chat-pair publisher (allowlist=[peer-pk]) and a subscriber
to the peer's matching publisher. The peer must call ` + "`pair add`" + ` with
this visor's PK on its side for the connection to actually flow.`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		peer := internal.ParsePK(cmd.Flags(), "peer-pk", args[0])
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		if err := rpcClient.PairAdd(peer); err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("PairAdd: %w", err))
		}
		internal.PrintOutput(cmd.Flags(),
			map[string]any{"peer_pk": peer.Hex(), "added": true},
			fmt.Sprintf("Pair with %s registered\n", peer.Hex()))
	},
}

var pairLsCmd = &cobra.Command{
	Use:   "ls",
	Short: "List chat pairs",
	Run: func(cmd *cobra.Command, _ []string) {
		listPairs(cmd)
	},
}

var pairRmCmd = &cobra.Command{
	Use:   "rm <peer-pk>",
	Short: "Tear down a chat pair",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		peer := internal.ParsePK(cmd.Flags(), "peer-pk", args[0])
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		if err := rpcClient.PairRemove(peer); err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("PairRemove: %w", err))
		}
		internal.PrintOutput(cmd.Flags(),
			map[string]any{"peer_pk": peer.Hex(), "removed": true},
			fmt.Sprintf("Pair with %s removed\n", peer.Hex()))
	},
}

var pairSendCmd = &cobra.Command{
	Use:   "send <peer-pk> <text>",
	Short: "Publish a message into the pair feed",
	Long: `Send one chat message via the per-pair CXO feed. The peer's
subscriber will see it on the next CXO publish-batch cycle.`,
	Args: cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		peer := internal.ParsePK(cmd.Flags(), "peer-pk", args[0])
		text := args[1]
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		if err := rpcClient.PairSend(peer, text); err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("PairSend: %w", err))
		}
		internal.PrintOutput(cmd.Flags(),
			map[string]any{"peer_pk": peer.Hex(), "sent": true},
			fmt.Sprintf("Sent to %s: %s\n", peer.Hex(), text))
	},
}

var pairPollCmd = &cobra.Command{
	Use:   "poll",
	Short: "Drain inbound pair messages",
	Long: `Pull every inbound pair message buffered on the visor with TS
strictly after --since (or the full inbox window when --since is empty).

The visor keeps a bounded ring (1024 messages by default); poll often
enough that it doesn't roll over between reads.`,
	Run: func(cmd *cobra.Command, _ []string) {
		var since time.Time
		if pairPollSince != "" {
			t, err := time.Parse(time.RFC3339, pairPollSince)
			if err != nil {
				internal.PrintFatalError(cmd.Flags(),
					fmt.Errorf("--since: %w (expected RFC3339)", err))
			}
			since = t
		}
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		msgs, err := rpcClient.PairPoll(since)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("PairPoll: %w", err))
		}
		var buf strings.Builder
		w := tabwriter.NewWriter(&buf, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "TIMESTAMP\tPEER\tTEXT") //nolint:errcheck,gosec
		for _, m := range msgs {
			fmt.Fprintf(w, "%s\t%s\t%s\n", //nolint:errcheck,gosec
				m.TS.UTC().Format(time.RFC3339Nano), m.PeerPK.Hex()[:16]+"...", m.Text)
		}
		w.Flush() //nolint:errcheck,gosec
		internal.PrintOutput(cmd.Flags(), msgs, buf.String())
	},
}

// listPairs renders the current pair set, called by both the bare
// `pair` command and the explicit `pair ls`.
func listPairs(cmd *cobra.Command) {
	rpcClient, err := clirpc.Client(cmd.Flags())
	if err != nil {
		internal.PrintFatalError(cmd.Flags(), err)
	}
	pairs, err := rpcClient.PairList()
	if err != nil {
		internal.PrintFatalError(cmd.Flags(), fmt.Errorf("PairList: %w", err))
	}
	if len(pairs) == 0 {
		internal.PrintOutput(cmd.Flags(), pairs, "No pairs registered.\n")
		return
	}
	var buf strings.Builder
	w := tabwriter.NewWriter(&buf, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "PEER\tSTATUS\tPORT\tESTABLISHED\tLAST MSG") //nolint:errcheck,gosec
	for _, p := range pairs {
		last := "-"
		if !p.LastMessageAt.IsZero() {
			last = p.LastMessageAt.UTC().Format(time.RFC3339)
		}
		fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\n", //nolint:errcheck,gosec
			truncatePK(p.PeerPK),
			p.Status, p.Port,
			p.EstablishedAt.UTC().Format(time.RFC3339),
			last)
	}
	w.Flush() //nolint:errcheck,gosec
	internal.PrintOutput(cmd.Flags(), pairs, buf.String())
}

func truncatePK(pk cipher.PubKey) string {
	s := pk.Hex()
	if len(s) > 16 {
		return s[:16] + "..."
	}
	return s
}
