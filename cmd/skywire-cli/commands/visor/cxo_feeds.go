// Package clivisor cmd/skywire-cli/commands/visor/cxo_feeds.go
//
// `skywire cli visor cxo` — surface for the user-publishable CXO
// TreeStore feeds. Each feed is its own DMSG listener under the
// visor's PK; subscribers connect to (visor PK, feed.dmsg_port) and
// receive the published path tree.
//
// Discovery happens out-of-band via the dmsghttp logserver's /feeds
// endpoint (and via this CLI which calls the same data source over
// RPC).
package clivisor

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"text/tabwriter"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
)

var (
	cxoJSON            bool
	cxoFeedDescription string
)

func init() {
	cxoFeedsCmd.Flags().BoolVar(&cxoJSON, "json", false, "emit raw JSON")
	cxoFeedAddCmd.Flags().StringVarP(&cxoFeedDescription, "desc", "d", "", "human-readable description")
	cxoFeedsCmd.AddCommand(cxoFeedAddCmd)
	cxoFeedsCmd.AddCommand(cxoFeedRmCmd)
	cxoFeedsCmd.AddCommand(cxoFeedLsCmd)
	cxoCmd.AddCommand(cxoFeedsCmd)
	RootCmd.AddCommand(cxoCmd)
}

var cxoCmd = &cobra.Command{
	Use:   "cxo",
	Short: "CXO TreeStore controls",
	Long:  "Inspect and manage user-published CXO feeds running on the visor.",
}

var cxoFeedsCmd = &cobra.Command{
	Use:   "feeds",
	Short: "List, add, and remove CXO user feeds",
	Long: `Each visor publishes a single system feed (telemetry) by default.
User feeds are independent CXO TreeStore publishers running on
distinct DMSG ports under the same visor PK. Subscribers fetch
http://<visor-pk>.dmsg/feeds for the catalog, then connect to the
desired (PK, dmsg_port) pair over DMSG.`,
	Run: func(cmd *cobra.Command, _ []string) {
		// Default to ls when invoked without a subcommand.
		_ = cmd.Help() //nolint:errcheck,gosec
	},
}

var cxoFeedLsCmd = &cobra.Command{
	Use:   "ls",
	Short: "List published CXO feeds (system + user)",
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		feeds := rpcClient.ListCXOFeeds()
		if cxoJSON {
			_ = json.NewEncoder(os.Stdout).Encode(feeds) //nolint:errcheck,gosec
			return
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tDMSG PORT\tSYSTEM\tDESCRIPTION") //nolint:errcheck,gosec
		for _, f := range feeds {
			desc := f.Description
			if desc == "" {
				desc = "-"
			}
			fmt.Fprintf(w, "%s\t%d\t%v\t%s\n", f.Name, f.DmsgPort, f.System, desc) //nolint:errcheck,gosec
		}
		w.Flush() //nolint:errcheck,gosec
	},
}

var cxoFeedAddCmd = &cobra.Command{
	Use:   "add <name> <dmsg-port>",
	Short: "Register a new CXO user feed",
	Long: `Spin up a CXO TreeStore publisher under the visor's PK on the
given DMSG port. The port must be unused by other feeds and not
collide with reserved DMSG ports (e.g. 46 hypervisor, 47 transport
setup, 50 system telemetry).`,
	Args: cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		port, err := strconv.ParseUint(args[1], 10, 16)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("invalid dmsg port %q: %w", args[1], err))
		}
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		if err := rpcClient.RegisterCXOFeed(args[0], uint16(port), cxoFeedDescription); err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("RegisterCXOFeed failed: %w", err))
		}
		fmt.Printf("Feed %q registered on dmsg:%d\n", args[0], port)
	},
}

var cxoFeedRmCmd = &cobra.Command{
	Use:   "rm <name>",
	Short: "Stop and remove a CXO user feed",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		if err := rpcClient.UnregisterCXOFeed(args[0]); err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("UnregisterCXOFeed failed: %w", err))
		}
		fmt.Printf("Feed %q removed\n", args[0])
	},
}
