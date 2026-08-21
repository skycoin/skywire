// Package clitp cmd/skywire-cli/commands/tp/tp-rm.go c4-vis-cli
package clitp

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/pkg/cipher"
)

var (
	removeAll          bool
	rmRemoteVisors     []string
	rmRemoteTransports []string
)

func init() {
	rmTpCmd.Flags().BoolVarP(&removeAll, "all", "a", false, "remove all transports")
	rmTpCmd.Flags().StringVarP(&tpID, "id", "i", "", "transport ID to remove (may also be given positionally: tp rm <id>)")
	rmTpCmd.Flags().StringSliceVar(&rmRemoteVisors, "remote", nil, "remove transport on remote visor(s) via embedded TPS (comma-separated PKs)")
	// --tp is the historical spelling for the remote transport ID(s); keep it
	// working as a hidden alias but steer users to the standard -i/--id
	// (comma-separated when removing several on a remote via --remote).
	rmTpCmd.Flags().StringSliceVar(&rmRemoteTransports, "tp", nil, "transport ID(s) to remove on remote visor (comma-separated, use with --remote)")
	_ = rmTpCmd.Flags().MarkHidden("tp") //nolint:errcheck
	rmTpCmd.Flags().StringVar(&clirpc.Addr, "rpc", clirpc.DefaultRPCAddr, "RPC server address (env: SKYWIRE_RPC)")
}

var rmTpCmd = &cobra.Command{
	Use:                   "rm [id]",
	Short:                 "Remove transport(s) by id",
	Long:                  "\n    Remove transport(s) by id — from the LOCAL visor by default.\n\n    The transport ID may be passed positionally (tp rm <id>) or with -i/--id.\n    Use --remote <visor-pk> with -i/--id to remove transports on a REMOTE\n    visor via the embedded Transport Setup Node (TPS); see also `tps rm`.",
	DisableFlagsInUseLine: true,
	Args:                  cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		// Accept the transport ID positionally (tp rm <id>) as well as via
		// -i/--id, matching `tp add <pk>`. The explicit flag wins if both given.
		if tpID == "" && len(args) > 0 {
			tpID = args[0]
		}

		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}

		// Handle --remote flag: remove transport on remote visor(s) via embedded TPS
		if len(rmRemoteVisors) > 0 {
			// The remote transport ID(s) may come from --tp (legacy, multiple)
			// or -i/--id / the positional arg (single). Prefer --tp when set.
			if len(rmRemoteTransports) == 0 && tpID != "" {
				rmRemoteTransports = []string{tpID}
			}
			if len(rmRemoteTransports) == 0 {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("-i/--id (or --tp) required with --remote to specify transport ID(s)"))
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
						logger.WithError(err).Warnf("Failed to remove transport %s on %s", tpIDStr, targetPK.String())
					} else {
						successCount++
						logger.Infof("Removed transport %s on %s", tpIDStr, targetPK.String())
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
