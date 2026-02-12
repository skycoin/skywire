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
					tp, tpErr = rpcClient.AddTransport(pk, transportType, timeout)
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
						tp, tpErr = rpcClient.AddTransport(pk, string(tpType), timeout)
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
