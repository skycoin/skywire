// Package clivisor dht.go — CLI commands for DHT operations
package clivisor

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
)

func init() {
	dhtCmd.AddCommand(dhtStatusCmd)
	dhtCmd.AddCommand(dhtGetCmd)
	dhtCmd.AddCommand(dhtPutCmd)
	dhtCmd.AddCommand(dhtFullNodeCmd)
	dhtCmd.AddCommand(dhtSyncCmd)
	dhtCmd.AddCommand(dhtListCmd)
	RootCmd.AddCommand(dhtCmd)
}

var dhtCmd = &cobra.Command{
	Use:   "dht",
	Short: "DHT operations",
	Long:  "Interact with the visor's Kademlia DHT node.",
}

var dhtStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show DHT node status",
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		status, err := rpcClient.DHTStatus()
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		if !status.Running {
			fmt.Println("DHT node is not running.")
			return
		}
		fmt.Printf("DHT Node Status\n")
		fmt.Printf("  Node ID:       %s\n", status.NodeID)
		fmt.Printf("  Network Size:  %d known peers\n", status.RoutingPeers)
		fmt.Printf("  Stored Items:  %d (whitelisted: %d, trusted: %d, public: %d)\n",
			status.StoredItems, status.WhitelistedItems, status.TrustedItems, status.PublicItems)
		fmt.Printf("  Full Node:     %v\n", status.FullNode)
		total := status.LookupCacheHits + status.LookupDHTHits + status.LookupHTTPHits + status.LookupHTTPMisses
		if total > 0 {
			fmt.Printf("  Lookups:       %d total (cache: %d, DHT: %d, HTTP: %d, miss: %d)\n",
				total, status.LookupCacheHits, status.LookupDHTHits, status.LookupHTTPHits, status.LookupHTTPMisses)
		}
	},
}

var dhtGetCmd = &cobra.Command{
	Use:   "get <public-key> [salt]",
	Short: "Retrieve a value from the DHT",
	Args:  cobra.RangeArgs(1, 2),
	Run: func(cmd *cobra.Command, args []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		salt := ""
		if len(args) > 1 {
			salt = args[1]
		}
		data, err := rpcClient.DHTGet(args[0], salt)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		os.Stdout.Write(data) //nolint:errcheck,gosec
		fmt.Fprintln(os.Stderr)
	},
}

var dhtPutSeq uint64

func init() {
	dhtPutCmd.Flags().Uint64Var(&dhtPutSeq, "seq", 1, "sequence number (must increase on each update)")
}

var dhtPutCmd = &cobra.Command{
	Use:   "put <value> [salt]",
	Short: "Publish a value to the DHT under this visor's key",
	Args:  cobra.RangeArgs(1, 2),
	Run: func(cmd *cobra.Command, args []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		salt := ""
		if len(args) > 1 {
			salt = args[1]
		}
		if err := rpcClient.DHTPut([]byte(args[0]), dhtPutSeq, salt); err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		fmt.Println("Published to DHT.")
	},
}

var dhtFullNodeCmd = &cobra.Command{
	Use:   "full-node <on|off>",
	Short: "Enable or disable DHT full node mode at runtime",
	Long: `Full node mode stores all DHT items regardless of XOR distance.
Normal nodes only store items close to their own ID.

  on  — store everything (deployment servers, bootstrap peers)
  off — store only nearby items (default for regular visors)`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		var full bool
		switch args[0] {
		case "on", "true", "1":
			full = true
		case "off", "false", "0":
			full = false
		default:
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("expected on/off, got %q", args[0]))
		}
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		if err := rpcClient.DHTSetFullNode(full); err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		if full {
			fmt.Println("DHT full node mode enabled (storing all items).")
		} else {
			fmt.Println("DHT full node mode disabled (storing nearby items only).")
		}
	},
}

var dhtSyncSalt string

func init() {
	dhtSyncCmd.Flags().StringVar(&dhtSyncSalt, "salt", "", "filter by namespace (dmsg, tp, svc); empty = all")
}

var dhtSyncCmd = &cobra.Command{
	Use:   "sync [full-node-pk]",
	Short: "Bulk sync items from a DHT full node",
	Long: `Fetch all items from a DHT full node and store them locally.
If no PK is specified, syncs from the first available bootstrap peer.

Examples:
  skywire cli visor dht sync
  skywire cli visor dht sync --salt dmsg
  skywire cli visor dht sync <pk> --salt tp`,
	Args: cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		pk := ""
		if len(args) > 0 {
			pk = args[0]
		}
		result, err := rpcClient.DHTSync(pk, dhtSyncSalt)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		fmt.Printf("Synced %d items from DHT full node.\n", result)
	},
}

var dhtListSalt string

func init() {
	dhtListCmd.Flags().StringVar(&dhtListSalt, "salt", "", "filter by namespace (dmsg, tp, svc); empty = all")
}

var dhtListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all DHT items stored locally",
	Long: `Dump all DHT items stored by this visor's DHT node as JSON.

Examples:
  skywire cli visor dht list
  skywire cli visor dht list --salt dmsg
  skywire cli visor dht list --salt tp`,
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		data, err := rpcClient.DHTGetAll(dhtListSalt)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		fmt.Println(data)
	},
}
