// Package clivisor dht.go — CLI commands for DHT operations
package clivisor

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"text/tabwriter"
	"time"

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
	dhtCmd.AddCommand(dhtPeersCmd)
	dhtCmd.AddCommand(dhtReconcileCmd)
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

var (
	dhtListSalt       string
	dhtListWithTarget bool
)

func init() {
	dhtListCmd.Flags().StringVar(&dhtListSalt, "salt", "", "filter by namespace (dmsg, tp, svc); empty = all")
	dhtListCmd.Flags().BoolVar(&dhtListWithTarget, "with-target", false, "emit {target, value} objects instead of bare values (for diffing against HTTP discoveries)")
}

var dhtPeersJSON bool

func init() {
	dhtPeersCmd.Flags().BoolVar(&dhtPeersJSON, "json", false, "emit raw JSON")
}

var dhtPeersCmd = &cobra.Command{
	Use:   "peers",
	Short: "Dump the DHT routing table (every K-bucket peer)",
	Long: `List every peer currently in this node's K-bucket routing
table. Useful for "is the DHT actually connected to anyone?" debugging
that 'dht status' (which shows only the peer count) cannot answer.

Output is sorted by bucket index then by last-seen (newest first).
With --json, emits the raw JSON array.`,
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		peers, err := rpcClient.DHTPeers()
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		if dhtPeersJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			internal.Catch(cmd.Flags(), enc.Encode(peers))
			return
		}
		if len(peers) == 0 {
			fmt.Println("No peers in routing table.")
			return
		}
		sort.SliceStable(peers, func(i, j int) bool {
			if peers[i].Bucket != peers[j].Bucket {
				return peers[i].Bucket < peers[j].Bucket
			}
			return peers[i].LastSeen.After(peers[j].LastSeen)
		})
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "BUCKET\tPK\tNODE_ID\tLAST_SEEN") //nolint:errcheck
		for _, p := range peers {
			age := time.Since(p.LastSeen).Truncate(time.Second).String()
			fmt.Fprintf(w, "%d\t%s\t%s\t%s ago\n", //nolint:errcheck
				p.Bucket, p.PK, p.NodeID, age)
		}
		w.Flush() //nolint:errcheck,gosec
	},
}

var dhtReconcileSalt string

func init() {
	dhtReconcileCmd.Flags().StringVar(&dhtReconcileSalt, "salt", "", "filter by namespace (dmsg, tp, svc); empty = all")
}

var dhtReconcileCmd = &cobra.Command{
	Use:   "reconcile <full-node-pk>",
	Short: "One-shot pull+push reconcile against a remote full node",
	Long: `Manually run one pull+push reconcile pass against a specific
remote full node. The full-node pull loop already does this hourly
against bootstrap and advertised full nodes; this command is for
forcing convergence faster (e.g., after a config change) or for
debugging cross-peer divergence.

Restricted to peers in BootstrapPKs ∪ FindAdvertisedFullNodes — the
receiver-side PutMirror handler stores any signed item we push without
distance/admission gating, so pushing to a non-full-node would
overflow its store.

Examples:
  skywire cli visor dht reconcile <pk>
  skywire cli visor dht reconcile <pk> --salt tp`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		pulled, pushed, err := rpcClient.DHTReconcile(args[0], dhtReconcileSalt)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		fmt.Printf("Reconcile complete: pulled=%d pushed=%d\n", pulled, pushed)
	},
}

var dhtListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all DHT items stored locally",
	Long: `Dump all DHT items stored by this visor's DHT node as JSON.

With --with-target, each item is emitted as {"target": "<hex>", "value": ...}
where target is the storage key (subject PK ⊕ salt hash). Useful when
diffing against an HTTP discovery whose records are keyed by visor PK.

Examples:
  skywire cli visor dht list
  skywire cli visor dht list --salt dmsg
  skywire cli visor dht list --salt tp --with-target`,
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		var data string
		if dhtListWithTarget {
			data, err = rpcClient.DHTListWithTargets(dhtListSalt)
		} else {
			data, err = rpcClient.DHTGetAll(dhtListSalt)
		}
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		fmt.Println(data)
	},
}
