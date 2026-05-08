// Package clitp cmd/skywire-cli/commands/tp/tp-add.go
package clitp

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/pkg/cipher"
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
	retries          int
	userLabel        bool
	noRegister       bool
	remoteVisorPKs   []string
	addVerbose       bool
	addVerboseLevel  string
)

func init() {
	addTpCmd.Flags().StringVarP(&rpk, "rpk", "r", "", "remote public key.")
	addTpCmd.Flags().StringVarP(&transportType, "type", "t", "", "type of transport to add.")
	addTpCmd.Flags().DurationVarP(&timeout, "timeout", "o", 0, "if specified, sets an operation timeout")
	addTpCmd.Flags().IntVarP(&retries, "retries", "n", 1, "number of times to retry per transport type")
	addTpCmd.Flags().StringVar(&clirpc.Addr, "rpc", clirpc.DefaultRPCAddr, "RPC server address (env: SKYWIRE_RPC)")
	addTpCmd.Flags().BoolVarP(&userLabel, "user", "u", false, "set transport label to 'user' (default is 'skycoin')")
	addTpCmd.Flags().BoolVar(&noRegister, "no-register", false, "skip transport discovery registration (implies --user)")
	addTpCmd.Flags().StringSliceVar(&remoteVisorPKs, "remote", nil, "request transport via TPS on remote visor(s) (comma-separated PKs)")
	addTpCmd.Flags().StringVar(&stcpAddr, "addr", "", "remote address (ip:port) for stcp transport")
	addTpCmd.Flags().BoolVarP(&addVerbose, "verbose", "v", false, "stream the visor's transport-layer logs (transport_manager, dmsgC, stcpr, sudph, address_resolver) while dialing")
	addTpCmd.Flags().StringVar(&addVerboseLevel, "verbose-level", "debug", "minimum log level when --verbose is set: trace|debug|info|warn|error")
	clirpc.RegisterFetchFlags(addTpCmd)
}

var stcpAddr string

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
		if transportType != "dmsg" && transportType != "stcpr" && transportType != "sudph" && transportType != "stcp" && transportType != "" {
			logger.Fatal("Invalid transport type specified:", transportType)
		}
		if stcpAddr != "" && transportType != "stcp" {
			logger.Fatal("--addr flag requires -t stcp")
		}
		if transportType == "stcp" && stcpAddr == "" {
			logger.Fatal("stcp transport requires --addr ip:port")
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

		// --verbose: subscribe to transport-layer logs for the
		// duration of this command. AddTransport routinely hangs in
		// the underlying dial when a peer is unreachable; this lights
		// up the actual handshake / AR / dmsg sequence in real time.
		if addVerbose {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			vs, vErr := clirpc.OpenVerbose(ctx, clirpc.Addr, clirpc.VerboseFilter{
				Modules: []string{"transport_manager", "dmsgC", "stcpr", "sudph", "address_resolver", "tp"},
				Level:   addVerboseLevel,
			})
			if vErr != nil {
				internal.PrintFatalError(cmd.Flags(), vErr)
			}
			_ = vs.WaitSubscribed(ctx, 2*time.Second) //nolint:errcheck
			defer vs.Close()
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

		// For stcp, inject the address into the visor's PK table before dialing
		if transportType == "stcp" && stcpAddr != "" {
			for _, pk := range pks {
				if err := rpcClient.SetSTCPAddr(pk, stcpAddr); err != nil {
					internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to set STCP address: %w", err))
				}
			}
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
						fmt.Printf("[%d/%d] Requesting transport on %s to %s via TPS...\n", i+1, len(pks), targetPK.String(), pk.String())
					}

					var tpResp *visor.TPSTransportResponse
					var tpErr error

					if useEmbeddedTPS {
						// Use embedded TPS only - if it fails, don't try external nodes
						tpResp, tpErr = rpcClient.TPSAddTransport(targetPK, pk, tpType)
						if tpErr == nil && !isJSON {
							logger.Infof("Established %v transport on %s to %s via embedded TPS", tpType, targetPK.String(), pk.String())
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
								logger.Debugf("Trying TPS node %s", tpsPK.String())
							}

							// Health check
							if err := rpcClient.TPSExternalHealthCheck(tpsPK); err != nil {
								if !isJSON {
									logger.WithError(err).Debugf("TPS %s health check failed", tpsPK.String())
								}
								continue
							}

							// Try to add transport via this TPS
							tpResp, tpErr = rpcClient.TPSExternalAddTransport(tpsPK, targetPK, pk, tpType)
							if tpErr == nil {
								if !isJSON {
									logger.Infof("Established %v transport on %s to %s via TPS %s", tpType, targetPK.String(), pk.String(), tpsPK.String())
								}
								break
							}
							if !isJSON {
								logger.WithError(tpErr).Debugf("TPS %s failed to add transport", tpsPK.String())
							}
						}
					}

					if tpErr != nil {
						if !isJSON {
							logger.WithError(tpErr).Errorf("Failed to establish transport on %s to %s", targetPK.String(), pk.String())
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
					fmt.Printf("Visor %s: %d/%d transports established\n", targetPK.String(), successCount, len(pks))
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

		// Process each public key
		var results []*visor.TransportSummary
		var lastErr error
		successCount := 0
		failCount := 0

		for i, pk := range pks {
			if len(pks) > 1 && !isJSON {
				fmt.Printf("[%d/%d] Adding transport to %s...\n", i+1, len(pks), pk.String())
			}

			// (Removed pre-creation dmsg port 136 probe. Direct transports
			// don't need port 136 — that's the route-setup await port,
			// only consulted when something routes THROUGH a visor — and
			// the probe was returning false negatives in practice (stale
			// dmsg-discovery cache picking a delegated server the target
			// no longer holds, even when the destination is reachable
			// via other servers). For an explicit reachability check use
			// `skywire cli dmsg probe <pk> 136`.)

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
					lastErr = tpErr
					failCount++
					continue
				}
			} else {
				// No transport type specified - try stcpr, sudph, dmsg in order.
				// The visor will return "entry not found in discovery" for dmsg
				// if the PK is not registered — no need to pre-check.
				transportTypes := []types.Type{
					types.STCPR,
					types.SUDPH,
					types.DMSG,
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
					lastErr = tpErr
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
			// If EVERY attempt failed, surface the error in the JSON output
			// so callers (tests, scripts, the UI) can see why. Previously
			// we'd return {"output": []} with exit 1, which left the caller
			// guessing what went wrong.
			if failCount > 0 && successCount == 0 && lastErr != nil {
				internal.PrintFatalError(cmd.Flags(),
					fmt.Errorf("tp add failed for all %d target(s): %w", failCount, lastErr))
			}
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
