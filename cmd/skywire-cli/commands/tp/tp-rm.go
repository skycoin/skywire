// Package clitp cmd/skywire-cli/commands/tp/tp-rm.go
package clitp

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
)

var (
	removeAll          bool
	rmRemoteVisors     []string
	rmRemoteTransports []string
)

func init() {
	rmTpCmd.Flags().BoolVarP(&removeAll, "all", "a", false, "remove all transports")
	rmTpCmd.Flags().StringVarP(&tpID, "id", "i", "", "remove transport of given ID")
	rmTpCmd.Flags().StringSliceVar(&rmRemoteVisors, "remote", nil, "remove transport on remote visor(s) via embedded TPS (comma-separated PKs)")
	rmTpCmd.Flags().StringSliceVar(&rmRemoteTransports, "tp", nil, "transport ID(s) to remove on remote visor (comma-separated, use with --remote)")
	rmTpCmd.Flags().StringVar(&clirpc.Addr, "rpc", "localhost:3435", "RPC server address")
}

var rmTpCmd = &cobra.Command{
	Use:                   "rm",
	Short:                 "Remove transport(s) by id",
	Long:                  "\n    Remove transport(s) by id\n\n    Use --remote with --tp to remove transports on a remote visor via the embedded TPS",
	DisableFlagsInUseLine: true,
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}

		// Handle --remote flag: remove transport on remote visor(s) via embedded TPS
		if len(rmRemoteVisors) > 0 {
			if len(rmRemoteTransports) == 0 {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("--tp flag required with --remote to specify transport ID(s)"))
			}

			// Parse all remote visor PKs
			var targetPKs []cipher.PubKey
			for _, pkStr := range rmRemoteVisors {
				var pk cipher.PubKey
				if err := pk.Set(pkStr); err != nil {
					internal.PrintFatalError(cmd.Flags(), fmt.Errorf("invalid target public key %s: %w", pkStr, err))
				}
				targetPKs = append(targetPKs, pk)
			}

			var totalSuccess, totalFail int
			var allErrors []string
			var results []map[string]interface{}

			for _, targetPK := range targetPKs {
				var successCount, failCount int
				var errors []string

				if len(targetPKs) > 1 {
					fmt.Printf("=== Remote visor: %s ===\n", targetPK.String())
				}

				for _, tpIDStr := range rmRemoteTransports {
					tID := internal.ParseUUID(cmd.Flags(), "transport-id", tpIDStr)

					if err := rpcClient.TPSRemoveTransport(targetPK, tID); err != nil {
						failCount++
						errors = append(errors, fmt.Sprintf("%s: %v", tpIDStr, err))
						logger.WithError(err).Warnf("Failed to remove transport %s on %s", tpIDStr, targetPK.String()[:16])
					} else {
						successCount++
						logger.Infof("Removed transport %s on %s", tpIDStr, targetPK.String()[:16])
					}
				}

				totalSuccess += successCount
				totalFail += failCount
				allErrors = append(allErrors, errors...)

				result := map[string]interface{}{
					"target":  targetPK.String(),
					"success": successCount,
					"failed":  failCount,
				}
				if len(errors) > 0 {
					result["errors"] = errors
				}
				results = append(results, result)
			}

			internal.PrintOutput(cmd.Flags(), results, func() string {
				if totalFail == 0 {
					return fmt.Sprintf("OK - removed %d transport(s) on %d visor(s)\n", totalSuccess, len(targetPKs))
				}
				return fmt.Sprintf("Removed %d/%d transports on %d visor(s)\nErrors:\n  %s\n",
					totalSuccess, totalSuccess+totalFail, len(targetPKs),
					fmt.Sprintf("%v", allErrors))
			}())
			return
		}

		// Local removal
		if removeAll {
			internal.Catch(cmd.Flags(), rpcClient.RemoveAllTransports())
			internal.PrintOutput(cmd.Flags(), "OK", "OK\n")
		} else if tpID != "" {
			tID := internal.ParseUUID(cmd.Flags(), "transport-id", tpID)
			if err != nil {
				os.Exit(1)
			}
			internal.Catch(cmd.Flags(), rpcClient.RemoveTransport(tID))
			internal.PrintOutput(cmd.Flags(), "OK", "OK\n")
		} else {
			internal.PrintOutput(cmd.Flags(), "", cmd.Help())
		}
	},
}
