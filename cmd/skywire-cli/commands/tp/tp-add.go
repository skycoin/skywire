// Package clitp cmd/skywire-cli/commands/tp/tp-add.go
package clitp

import (
	"fmt"
	"os"
	"time"

	"github.com/bitfield/script"
	"github.com/spf13/cobra"

	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	types "github.com/skycoin/skywire/pkg/transport/types"
	"github.com/skycoin/skywire/pkg/visor"
)

var (
	tpdURL           string
	rootNode         string
	lastNode         string
	rootnode         cipher.PubKey
	lastnode         cipher.PubKey
	cacheFileTPD     string
	cacheFileDmsgD   string
	cacheFileUT      string
	cacheFileSDProxy string
	cacheFileSDVPN   string
	cacheFileSDVisor string
	padSpaces        int
	isStats          bool
	rawData          bool
	refinedData      bool
	noFilterOnline   bool
	onlyOnline       bool
	transportType    string
	timeout          time.Duration
	rpk              string
	cacheFilesAge    int
	forceAttempt     bool
	dmsgdURL         string
	retries          int
	userLabel        bool
	noRegister       bool
	remoteVisorPKs   []string

// queryHealth	bool
)

func init() {
	addTpCmd.Flags().StringVarP(&rpk, "rpk", "r", "", "remote public key.")
	addTpCmd.Flags().StringVarP(&transportType, "type", "t", "", "type of transport to add.")
	addTpCmd.Flags().DurationVarP(&timeout, "timeout", "o", 0, "if specified, sets an operation timeout")
	addTpCmd.Flags().StringVarP(&dmsgdURL, "dmsg", "d", deployment.Prod.DmsgDiscovery, "dmsg discovery URL")
	//TODO
	//	listCmd.Flags().BoolVarP(&queryHealth, "health", "q", false, "check /health of remote visor over dmsg before creating transport")
	addTpCmd.Flags().BoolVarP(&forceAttempt, "force", "f", false, "attempt dmsg transport creation without checking dmsg discovery")
	addTpCmd.Flags().StringVar(&cacheFileDmsgD, "cfdd", os.TempDir()+"/dmsgd.json", "Dmsg Discovery cache file location")
	addTpCmd.Flags().IntVarP(&cacheFilesAge, "cfa", "m", 5, "update cache files if older than n minutes")
	addTpCmd.Flags().IntVarP(&retries, "retries", "n", 1, "number of times to retry per transport type")
	addTpCmd.Flags().StringVar(&clirpc.Addr, "rpc", "localhost:3435", "RPC server address")
	addTpCmd.Flags().BoolVarP(&userLabel, "user", "u", false, "set transport label to 'user' (default is 'skycoin')")
	addTpCmd.Flags().BoolVar(&noRegister, "no-register", false, "skip transport discovery registration (implies --user)")
	addTpCmd.Flags().StringSliceVar(&remoteVisorPKs, "remote", nil, "request transport via TPS on remote visor(s) (comma-separated PKs)")
}

var addTpCmd = &cobra.Command{
	Use:   "add <public-key> [public-key]...",
	Short: "Add transport(s) to one or more remote public keys",
	Long: `
    Add transport(s)
		Accepts one or more remote public keys as arguments.
		If the transport type is unspecified,
		the visor will attempt to establish a transport
		in the following order: stcpr, sudph, dmsg`,
	Args:                  cobra.MinimumNArgs(1),
	DisableFlagsInUseLine: true,
	Run: func(cmd *cobra.Command, args []string) {
		if transportType != "dmsg" && transportType != "stcpr" && transportType != "sudph" && transportType != "" {
			logger.Fatal("Invalid transport type specified:", transportType)
		}
		// --no-register implies --user label
		if noRegister {
			userLabel = true
		}
		// Determine label string
		label := ""
		if userLabel {
			label = "user"
		}
		isJSON, _ := cmd.Flags().GetBool(internal.JSONString) //nolint:errcheck
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}

		// Collect public keys from args and -r flag
		var pks []cipher.PubKey
		if rpk != "" {
			var pk cipher.PubKey
			internal.Catch(cmd.Flags(), pk.Set(rpk))
			pks = append(pks, pk)
		}
		for _, arg := range args {
			pk := internal.ParsePK(cmd.Flags(), "remote-public-key", arg)
			pks = append(pks, pk)
		}

		if len(pks) == 0 {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("no public keys specified"))
		}

		// Handle --remote flag: request transport via TPS on remote visor(s)
		if len(remoteVisorPKs) > 0 {
			// Parse all remote visor PKs
			var targetPKs []cipher.PubKey
			for _, pkStr := range remoteVisorPKs {
				var pk cipher.PubKey
				internal.Catch(cmd.Flags(), pk.Set(pkStr))
				targetPKs = append(targetPKs, pk)
			}

			// Determine transport type (default to dmsg for TPS)
			tpType := transportType
			if tpType == "" {
				tpType = "dmsg"
			}

			// Check if embedded TPS is running - if so, use only embedded TPS
			// If embedded TPS is not running, use external TPS nodes
			tpsStatus, tpsStatusErr := rpcClient.TPSStatus()
			useEmbeddedTPS := tpsStatusErr == nil && tpsStatus != nil && tpsStatus.Enabled

			var tpsResults []*visor.TPSTransportResponse
			totalSuccess := 0
			totalFail := 0

			// For each remote visor, request transports to all target PKs
			for ti, targetPK := range targetPKs {
				if len(targetPKs) > 1 && !isJSON {
					fmt.Printf("\n=== Remote visor %d/%d: %s ===\n", ti+1, len(targetPKs), targetPK.String())
				}

				successCount := 0
				failCount := 0

				for i, pk := range pks {
					if !isJSON {
						fmt.Printf("[%d/%d] Requesting transport on %s to %s via TPS...\n", i+1, len(pks), targetPK.String()[:16]+"...", pk.String()[:16]+"...")
					}

					var tpResp *visor.TPSTransportResponse
					var tpErr error

					if useEmbeddedTPS {
						// Use embedded TPS only - if it fails, don't try external nodes
						tpResp, tpErr = rpcClient.TPSAddTransport(targetPK, pk, tpType)
						if tpErr == nil && !isJSON {
							logger.Infof("Established %v transport on %s to %s via embedded TPS", tpType, targetPK.String()[:16], pk.String()[:16])
						}
					} else {
						// Embedded TPS not running - use external TPS nodes
						tpsNodes, err := rpcClient.GetTransportSetupNodesSorted()
						if err != nil {
							if !isJSON {
								logger.WithError(err).Error("Failed to get TPS nodes")
							}
							failCount++
							continue
						}

						if len(tpsNodes) == 0 {
							if !isJSON {
								logger.Error("No TPS nodes configured")
							}
							failCount++
							continue
						}

						// Try each TPS node (already sorted by health, healthy first)
						for _, tpsPK := range tpsNodes {
							if !isJSON {
								logger.Debugf("Trying TPS node %s", tpsPK.String()[:16])
							}

							// Health check
							if err := rpcClient.TPSExternalHealthCheck(tpsPK); err != nil {
								if !isJSON {
									logger.WithError(err).Debugf("TPS %s health check failed", tpsPK.String()[:16])
								}
								continue
							}

							// Try to add transport via this TPS
							tpResp, tpErr = rpcClient.TPSExternalAddTransport(tpsPK, targetPK, pk, tpType)
							if tpErr == nil {
								if !isJSON {
									logger.Infof("Established %v transport on %s to %s via TPS %s", tpType, targetPK.String()[:16], pk.String()[:16], tpsPK.String()[:16])
								}
								break
							}
							if !isJSON {
								logger.WithError(tpErr).Debugf("TPS %s failed to add transport", tpsPK.String()[:16])
							}
						}
					}

					if tpErr != nil {
						if !isJSON {
							logger.WithError(tpErr).Errorf("Failed to establish transport on %s to %s", targetPK.String()[:16], pk.String()[:16])
						}
						failCount++
						continue
					}

					if tpResp != nil {
						tpsResults = append(tpsResults, tpResp)
						successCount++
					}
				}

				totalSuccess += successCount
				totalFail += failCount

				if len(pks) > 1 && !isJSON {
					fmt.Printf("Visor %s: %d/%d transports established\n", targetPK.String()[:16], successCount, len(pks))
				}
			}

			// Print results
			if !isJSON {
				fmt.Printf("\nTotal Summary: %d/%d transports established via TPS\n", totalSuccess, totalSuccess+totalFail)
			}

			if isJSON {
				internal.PrintOutput(cmd.Flags(), tpsResults, "")
			} else {
				for _, tp := range tpsResults {
					fmt.Printf("id: %s\n", tp.ID)
					fmt.Printf("local: %s\n", tp.Local)
					fmt.Printf("remote: %s\n", tp.Remote)
					fmt.Printf("type: %s\n", tp.Type)
				}
			}

			if totalFail > 0 && totalSuccess == 0 {
				os.Exit(1)
			}
			return
		}

		// Fetch dmsg discovery data once (for all PKs) - only used for dmsg transport checks
		var dmsgkeys []string
		if !forceAttempt && (transportType == "" || transportType == "dmsg") {
			dmsgEntries := internal.GetData(cacheFileDmsgD, dmsgdURL+"/dmsg-discovery/entries", cacheFilesAge)
			dmsgkeys, _ = script.Echo(dmsgEntries).JQ(".[]").Replace(`"`, "").Slice() //nolint:errcheck
		}

		// Helper to check if pk is in slice
		contains := func(slice []string, s string) bool {
			for _, item := range slice {
				if item == s {
					return true
				}
			}
			return false
		}

		// Process each public key
		var results []*visor.TransportSummary
		successCount := 0
		failCount := 0

		for i, pk := range pks {
			if len(pks) > 1 && !isJSON {
				fmt.Printf("[%d/%d] Adding transport to %s...\n", i+1, len(pks), pk.String()[:16]+"...")
			}

			// Check dmsg availability if dmsg is explicitly requested
			if !forceAttempt && transportType == "dmsg" && !contains(dmsgkeys, pk.String()) {
				if !isJSON {
					logger.Warnf("Skipping %s: not found in dmsg discovery", pk.String()[:16])
				}
				failCount++
				continue
			}

			var tp *visor.TransportSummary
			var tpErr error

			if transportType != "" {
				// Specific transport type requested
				for attempt := 1; attempt <= retries; attempt++ {
					tp, tpErr = rpcClient.AddTransport(pk, transportType, timeout, label, noRegister, false)
					if tpErr == nil {
						if !isJSON {
							logger.Infof("Established %v transport to %v", transportType, pk)
						}
						break
					}
					if !isJSON && attempt < retries {
						logger.WithError(tpErr).Warnf("Failed to establish %v transport (attempt %d/%d), retrying...", transportType, attempt, retries)
					}
				}
				if tpErr != nil {
					if !isJSON {
						logger.WithError(tpErr).Errorf("Failed to establish %v transport to %v after %d attempts", transportType, pk, retries)
					}
					failCount++
					continue
				}
			} else {
				// No transport type specified - try stcpr, sudph, dmsg in order
				transportTypes := []types.Type{
					types.STCPR,
					types.SUDPH,
				}
				// Only include dmsg if found in dmsg discovery (or force is set)
				if forceAttempt || contains(dmsgkeys, pk.String()) {
					transportTypes = append(transportTypes, types.DMSG)
				}

			typeLoop:
				for _, tpType := range transportTypes {
					for attempt := 1; attempt <= retries; attempt++ {
						tp, tpErr = rpcClient.AddTransport(pk, string(tpType), timeout, label, noRegister, false)
						if tpErr == nil {
							if !isJSON {
								logger.Infof("Established %v transport to %v", tpType, pk)
							}
							break typeLoop
						}
						if !isJSON {
							if attempt < retries {
								logger.WithError(tpErr).Warnf("Failed to establish %v transport (attempt %d/%d), retrying...", tpType, attempt, retries)
							} else {
								logger.WithError(tpErr).Warnf("Failed to establish %v transport after %d attempts", tpType, retries)
							}
						}
					}
				}

				if tpErr != nil {
					failCount++
					continue
				}
			}

			if tp != nil {
				results = append(results, tp)
				successCount++
			}
		}

		// Print results
		if len(pks) > 1 && !isJSON {
			fmt.Printf("\nSummary: %d/%d transports established\n", successCount, len(pks))
		}

		if isJSON {
			internal.PrintOutput(cmd.Flags(), results, "")
		} else {
			for _, tp := range results {
				PrintTransports(cmd.Flags(), tp)
			}
		}

		if failCount > 0 && successCount == 0 {
			os.Exit(1)
		}
	},
}
