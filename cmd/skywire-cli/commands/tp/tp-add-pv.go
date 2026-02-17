// Package clitp cmd/skywire-cli/commands/tp/tp-add-pv.go
package clitp

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/bitfield/script"
	"github.com/spf13/cobra"

	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/servicedisc"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport"
	types "github.com/skycoin/skywire/pkg/transport/types"
	"github.com/skycoin/skywire/pkg/visor"
)

var (
	pvCount          int
	pvTransportType  string
	pvTimeout        time.Duration
	pvSDURL          string
	pvUTURL          string
	pvTPDURL         string
	pvDmsgURL        string
	pvCacheFileSD    string
	pvCacheFileUT    string
	pvCacheFileTPD   string
	pvCacheFileDmsgD string
	pvCacheFilesAge  int
	pvNoFilterOnline bool
	pvForceAttempt   bool
	pvRetries        int
	pvMinTransports  int
)

func init() {
	addPvCmd.Flags().IntVarP(&pvCount, "count", "n", 5, "number of public visors to add transports to")
	addPvCmd.Flags().StringVarP(&pvTransportType, "type", "t", "", "transport type (stcpr, sudph, dmsg)")
	addPvCmd.Flags().DurationVarP(&pvTimeout, "timeout", "o", 0, "operation timeout")
	addPvCmd.Flags().StringVarP(&pvSDURL, "sdurl", "a", deployment.Prod.ServiceDiscovery, "service discovery url")
	addPvCmd.Flags().StringVarP(&pvUTURL, "uturl", "w", deployment.Prod.UptimeTracker, "uptime tracker url")
	addPvCmd.Flags().StringVarP(&pvTPDURL, "tpdurl", "d", deployment.Prod.TransportDiscovery, "transport discovery url")
	addPvCmd.Flags().StringVar(&pvDmsgURL, "dmsg", deployment.Prod.DmsgDiscovery, "dmsg discovery url")
	addPvCmd.Flags().StringVar(&pvCacheFileSD, "cfs", os.TempDir()+"/visorsd.json", "SD cache file location")
	addPvCmd.Flags().StringVar(&pvCacheFileUT, "cfu", os.TempDir()+"/ut.json", "UT cache file location")
	addPvCmd.Flags().StringVar(&pvCacheFileTPD, "cft", os.TempDir()+"/tpd.json", "TPD cache file location")
	addPvCmd.Flags().StringVar(&pvCacheFileDmsgD, "cfdd", os.TempDir()+"/dmsgd.json", "Dmsg Discovery cache file location")
	addPvCmd.Flags().IntVarP(&pvCacheFilesAge, "cfa", "m", 5, "update cache files if older than n minutes")
	addPvCmd.Flags().BoolVarP(&pvNoFilterOnline, "noton", "f", false, "do not filter by online status")
	addPvCmd.Flags().BoolVar(&pvForceAttempt, "force", false, "attempt dmsg transport without checking dmsg discovery")
	addPvCmd.Flags().IntVar(&pvRetries, "retries", 1, "number of times to retry per transport type")
	addPvCmd.Flags().IntVar(&pvMinTransports, "min", 0, "minimum transport count for target visors")
	addPvCmd.Flags().StringVar(&clirpc.Addr, "rpc", "localhost:3435", "RPC server address")
	addTpCmd.AddCommand(addPvCmd)
}

var addPvCmd = &cobra.Command{
	Use:   "pv",
	Short: "Add transports to public visors",
	Long: `Add transports to public visors, starting with those that have the most transports.

  Fetches public visors from service discovery and attempts to establish
  transports to the top N visors (by transport count). This is useful for
  improving network connectivity and reachability.`,
	Run: func(cmd *cobra.Command, _ []string) {
		if pvTransportType != "" && pvTransportType != "dmsg" && pvTransportType != "stcpr" && pvTransportType != "sudph" {
			logger.Fatal("Invalid transport type specified:", pvTransportType)
		}

		isJSON, _ := cmd.Flags().GetBool(internal.JSONString) //nolint:errcheck
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}

		// Get local visor's PK to exclude from targets
		overview, err := rpcClient.Overview()
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to get visor overview: %w", err))
		}
		localPK := overview.PubKey.String()

		// Fetch public visors from service discovery
		sds := internal.GetData(pvCacheFileSD, pvSDURL+"/api/services?type="+servicedisc.ServiceTypeVisor, pvCacheFilesAge)

		var pks []string
		if pvNoFilterOnline {
			sdJQ := `.[] | .address | split(":")[0]`
			pks, _ = script.Echo(sds).JQ(sdJQ).Replace(`"`, "").Slice() //nolint:errcheck
		} else {
			// Filter by online status
			uts := internal.GetData(pvCacheFileUT, pvUTURL+"/uptimes?v=v2", pvCacheFilesAge)
			joinedJSON := fmt.Sprintf(`{"sd": %s, "ut": %s}`, sds, uts)
			jqFilter := `
			[ .ut[] | select(.on) | .pk ] as $online
			| .sd[]
			| select((.address | split(":")[0]) as $pk | $pk | IN($online[]))
			| .address | split(":")[0]
			`
			pks, _ = script.Echo(joinedJSON).JQ(jqFilter).Replace(`"`, "").Slice() //nolint:errcheck
		}

		if len(pks) == 0 {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("no public visors found"))
		}

		// Fetch transport counts from TPD
		tpd := internal.GetData(pvCacheFileTPD, pvTPDURL+"/all-transports", pvCacheFilesAge)
		var entries []*transport.Entry
		if err := json.Unmarshal([]byte(tpd), &entries); err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to parse TPD data: %w", err))
		}

		// Count transports per PK
		tpCount := make(map[string]int)
		for _, entry := range entries {
			if entry.Edges[0] == entry.Edges[1] {
				continue
			}
			tpCount[entry.Edges[0].String()]++
			tpCount[entry.Edges[1].String()]++
		}

		// Build sorted list of public visors by transport count
		type visorInfo struct {
			pk    string
			count int
		}
		var candidates []visorInfo
		for _, pk := range pks {
			if pk == localPK {
				continue // Skip self
			}
			count := tpCount[pk]
			if count >= pvMinTransports {
				candidates = append(candidates, visorInfo{pk: pk, count: count})
			}
		}

		// Sort by transport count descending
		sort.Slice(candidates, func(i, j int) bool {
			return candidates[i].count > candidates[j].count
		})

		if len(candidates) == 0 {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("no eligible public visors found"))
		}

		// Limit to requested count
		if pvCount > 0 && pvCount < len(candidates) {
			candidates = candidates[:pvCount]
		}

		// Fetch dmsg discovery data if needed
		var dmsgkeys []string
		if !pvForceAttempt && (pvTransportType == "" || pvTransportType == "dmsg") {
			dmsgEntries := internal.GetData(pvCacheFileDmsgD, pvDmsgURL+"/dmsg-discovery/entries", pvCacheFilesAge)
			dmsgkeys, _ = script.Echo(dmsgEntries).JQ(".[]").Replace(`"`, "").Slice() //nolint:errcheck
		}

		contains := func(slice []string, s string) bool {
			for _, item := range slice {
				if item == s {
					return true
				}
			}
			return false
		}

		// Add transports to candidates
		var results []*visor.TransportSummary
		successCount := 0
		failCount := 0

		if !isJSON {
			fmt.Printf("Adding transports to %d public visors (sorted by transport count)...\n\n", len(candidates))
		}

		for i, candidate := range candidates {
			pk := candidate.pk
			var cipherPK cipher.PubKey
			if err := cipherPK.Set(pk); err != nil {
				if !isJSON {
					logger.Warnf("[%d/%d] Invalid PK %s: %v", i+1, len(candidates), pk[:16], err)
				}
				failCount++
				continue
			}

			if !isJSON {
				fmt.Printf("[%d/%d] %s (%d transports)...\n", i+1, len(candidates), pk[:16]+"...", candidate.count)
			}

			// Check dmsg availability if dmsg is explicitly requested
			if !pvForceAttempt && pvTransportType == "dmsg" && !contains(dmsgkeys, pk) {
				if !isJSON {
					logger.Warnf("  Skipping: not found in dmsg discovery")
				}
				failCount++
				continue
			}

			var tp *visor.TransportSummary
			var tpErr error

			if pvTransportType != "" {
				// Specific transport type requested
				for attempt := 1; attempt <= pvRetries; attempt++ {
					tp, tpErr = rpcClient.AddTransport(cipherPK, pvTransportType, pvTimeout, "", false)
					if tpErr == nil {
						if !isJSON {
							logger.Infof("  Established %v transport", pvTransportType)
						}
						break
					}
					if !isJSON && attempt < pvRetries {
						logger.WithError(tpErr).Warnf("  Failed (attempt %d/%d), retrying...", attempt, pvRetries)
					}
				}
				if tpErr != nil {
					if !isJSON {
						logger.WithError(tpErr).Errorf("  Failed to establish %v transport after %d attempts", pvTransportType, pvRetries)
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
				if pvForceAttempt || contains(dmsgkeys, pk) {
					transportTypes = append(transportTypes, types.DMSG)
				}

			typeLoop:
				for _, tpType := range transportTypes {
					for attempt := 1; attempt <= pvRetries; attempt++ {
						tp, tpErr = rpcClient.AddTransport(cipherPK, string(tpType), pvTimeout, "", false)
						if tpErr == nil {
							if !isJSON {
								logger.Infof("  Established %v transport", tpType)
							}
							break typeLoop
						}
						if !isJSON {
							if attempt < pvRetries {
								logger.WithError(tpErr).Warnf("  Failed %v (attempt %d/%d), retrying...", tpType, attempt, pvRetries)
							} else {
								logger.WithError(tpErr).Warnf("  Failed %v after %d attempts", tpType, pvRetries)
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
		if !isJSON {
			fmt.Printf("\nSummary: %d/%d transports established\n", successCount, len(candidates))
		}

		if isJSON {
			internal.PrintOutput(cmd.Flags(), results, "")
		} else if len(results) > 0 {
			fmt.Println("\nEstablished transports:")
			for _, tp := range results {
				PrintTransports(cmd.Flags(), tp)
			}
		}

		if failCount > 0 && successCount == 0 {
			os.Exit(1)
		}
	},
}
