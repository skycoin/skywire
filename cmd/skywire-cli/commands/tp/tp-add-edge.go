// Package clitp cmd/skywire-cli/commands/tp/tp-add-edge.go
package clitp

import (
	"fmt"
	"sync"
	"time"

	"github.com/spf13/cobra"

	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport"
)

var (
	edgeBatchSize int
	edgeVerbose   bool
)

func init() {
	addTpCmd.AddCommand(addEdgeCmd)
	addEdgeCmd.Flags().StringVarP(&tpdURL, "tpdurl", "d", "", "transport discovery url")
	addEdgeCmd.Flags().IntVarP(&edgeBatchSize, "batch", "b", 5, "number of transports to add in parallel")
	addEdgeCmd.Flags().BoolVarP(&edgeVerbose, "verbose", "v", false, "verbose output")
	addEdgeCmd.Flags().DurationVarP(&timeout, "timeout", "o", 30*time.Second, "timeout for each transport addition")
	addEdgeCmd.Flags().StringVar(&clirpc.Addr, "rpc", "localhost:3435", "RPC server address")
}

var addEdgeCmd = &cobra.Command{
	Use:   "edge <public-key>",
	Short: "Add transports to all edges of a public key",
	Long: `Query transport discovery for all transports connected to the specified
public key, then attempt to add transports to each of those edge keys.

This is useful for connecting to all visors that a specific visor is
connected to (e.g., connecting to all edges of a well-connected hub).`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		// Parse the target public key
		var targetPK cipher.PubKey
		if err := targetPK.Set(args[0]); err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("invalid public key: %w", err))
		}

		// Get RPC client
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("unable to create RPC client: %w", err))
		}

		// Get our own public key to exclude from edges
		overview, err := rpcClient.Overview()
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to get visor overview: %w", err))
		}
		ourPK := overview.PubKey

		// Query transport discovery for transports of the target PK
		var entries []*transport.Entry
		if tpdURL != "" {
			entries, err = getTransportsByEdge(tpdURL, targetPK)
		} else {
			entries, err = rpcClient.DiscoverTransportsByPK(targetPK)
		}
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to query transport discovery: %w", err))
		}

		if len(entries) == 0 {
			fmt.Printf("No transports found for %s\n", targetPK.String()[:8])
			return
		}

		// Extract unique edge PKs (excluding target PK and our own PK)
		edgePKs := make(map[cipher.PubKey]bool)
		for _, entry := range entries {
			for _, edge := range entry.Edges {
				if edge != targetPK && edge != ourPK {
					edgePKs[edge] = true
				}
			}
		}

		if len(edgePKs) == 0 {
			fmt.Printf("No edge keys found for %s (excluding self)\n", targetPK.String()[:8])
			return
		}

		// Convert to slice
		var pksToConnect []cipher.PubKey
		for pk := range edgePKs {
			pksToConnect = append(pksToConnect, pk)
		}

		if edgeVerbose {
			fmt.Printf("Found %d edges for %s, attempting to connect...\n", len(pksToConnect), targetPK.String()[:8])
		}

		// Process in parallel batches
		var wg sync.WaitGroup
		semaphore := make(chan struct{}, edgeBatchSize)

		type result struct {
			pk      cipher.PubKey
			success bool
			tpType  string
			err     error
		}
		results := make([]result, len(pksToConnect))

		for i, pk := range pksToConnect {
			wg.Add(1)
			go func(idx int, edgePK cipher.PubKey) {
				defer wg.Done()
				semaphore <- struct{}{}
				defer func() { <-semaphore }()

				res := result{pk: edgePK}

				// Try STCPR first, then SUDPH, then DMSG
				tpTypes := []string{"stcpr", "sudph", "dmsg"}
				for _, tpType := range tpTypes {
					_, err := rpcClient.AddTransport(edgePK, tpType, timeout, "", false, false)
					if err == nil {
						res.success = true
						res.tpType = tpType
						break
					}
					res.err = err
				}

				results[idx] = res

				if edgeVerbose {
					if res.success {
						fmt.Printf("✓ %s (%s)\n", edgePK.String()[:8], res.tpType)
					} else {
						fmt.Printf("✗ %s: %v\n", edgePK.String()[:8], res.err)
					}
				}
			}(i, pk)
		}

		wg.Wait()

		// Print summary
		successCount := 0
		for _, r := range results {
			if r.success {
				successCount++
			}
		}
		fmt.Printf("\nConnected to %d/%d edges of %s\n", successCount, len(pksToConnect), targetPK.String()[:8])
	},
}
