// Package clivisor cmd/skywire-cli/commands/visor/ping-graph.go
package clivisor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/blang/semver/v4"
	"github.com/spf13/cobra"

	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/visor/rpcgrpc"
)

// transportEntry represents a transport from TPD
type transportEntry struct {
	ID    string    `json:"t_id"`
	Type  string    `json:"type"`
	Edges [2]string `json:"edges"`
}

// uptimeEntry represents a visor from UT
type uptimeEntry struct {
	PK      string `json:"pk"`
	Online  bool   `json:"on"`
	Version string `json:"version,omitempty"`
}

var (
	graphVersion      string
	graphMaxLevel     int
	graphRedundancy   bool
	graphTimeout      time.Duration
	graphSetupTimeout time.Duration
	graphTries        int
	graphPcktSize     int
	graphCacheTPD     string
	graphCacheUT      string
	graphCacheAge     int
	graphTPDURL       string
	graphUTURL        string
	graphOnlineOnly   bool
	graphContinue     bool
	graphStartLevel   int
	graphUseDmsg      bool
	graphDmsgOnly     bool
	graphLocalRoute   bool
	graphShowRoute    bool
	graphHopLatency   bool
	graphOutputJSON   string
	graphOutputText   string
)

func init() {
	pingGraphCmd.Flags().StringVarP(&graphVersion, "version", "v", "", "filter by minimum version (e.g., '1.3.34' matches 1.3.34, 1.3.34+dirty, 1.3.35, etc.)")
	pingGraphCmd.Flags().IntVarP(&graphMaxLevel, "max-level", "l", 0, "maximum hop level (0 = unlimited)")
	pingGraphCmd.Flags().BoolVar(&graphRedundancy, "redundancy", false, "test all transport types to same visor")
	pingGraphCmd.Flags().DurationVarP(&graphTimeout, "timeout", "o", 30*time.Second, "timeout per ping attempt")
	pingGraphCmd.Flags().IntVarP(&graphTries, "tries", "t", 1, "ping attempts per visor")
	pingGraphCmd.Flags().IntVarP(&graphPcktSize, "size", "s", 2, "packet size in KB")
	pingGraphCmd.Flags().StringVar(&graphCacheTPD, "cft", os.TempDir()+"/tpd.json", "TPD cache file location")
	pingGraphCmd.Flags().StringVar(&graphCacheUT, "cfu", os.TempDir()+"/ut.json", "UT cache file location")
	pingGraphCmd.Flags().IntVarP(&graphCacheAge, "cfa", "m", 5, "update cache files if older than n minutes")
	pingGraphCmd.Flags().StringVar(&graphTPDURL, "tpdurl", deployment.Prod.TransportDiscovery, "transport discovery URL")
	pingGraphCmd.Flags().StringVar(&graphUTURL, "uturl", deployment.Prod.UptimeTracker, "uptime tracker URL")
	pingGraphCmd.Flags().BoolVarP(&graphOnlineOnly, "online", "g", false, "only ping visors marked online in UT")
	pingGraphCmd.Flags().BoolVarP(&graphContinue, "continue", "c", false, "continue on ping failure (don't stop at failed level)")
	pingGraphCmd.Flags().IntVar(&graphStartLevel, "start-level", 1, "start pinging from this level (skip earlier levels)")
	pingGraphCmd.Flags().BoolVar(&graphUseDmsg, "dmsg", false, "also ping over dmsg before pinging via skywire route")
	pingGraphCmd.Flags().BoolVar(&graphDmsgOnly, "dmsg-only", false, "only ping over dmsg (skip skywire route ping)")
	pingGraphCmd.Flags().BoolVar(&graphLocalRoute, "local-route", false, "calculate routes locally using cached TPD data instead of querying route finder")
	pingGraphCmd.Flags().BoolVar(&graphShowRoute, "show-route", false, "show the route hops used for the ping")
	pingGraphCmd.Flags().BoolVar(&graphHopLatency, "hop-latency", false, "measure per-hop latency for multi-hop routes (requires --show-route)")
	pingGraphCmd.Flags().DurationVar(&graphSetupTimeout, "setup-timeout", 30*time.Second, "timeout for route setup phase")
	pingGraphCmd.Flags().StringVar(&graphOutputJSON, "output-json", "", "write results to JSON file (with timestamp)")
	pingGraphCmd.Flags().StringVar(&graphOutputText, "output-text", "", "write results to text file (with timestamp)")
	pingCmd.AddCommand(pingGraphCmd)
}

var pingGraphCmd = &cobra.Command{
	Use:   "graph",
	Short: "Ping visors across the network by hop level",
	Long: `Ping visors reachable from this visor, organized by hop distance.

Level 1: Visors with direct transports to local visor
Level 2: Visors connected to Level 1 visors
Level 3: Visors connected to Level 2 visors
...and so on until no new visors are found.

Uses cached TPD and UT data to build the network graph, then pings
each visor at each level. Skips visors already pinged at earlier levels.`,
	Run: func(cmd *cobra.Command, _ []string) {
		// Get local visor info
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to connect to visor RPC: %w", err))
		}
		overview, err := rpcClient.Overview()
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to get visor overview: %w", err))
		}
		localPK := overview.PubKey.String()
		fmt.Printf("Local visor: %s\n", localPK)

		// Fetch and parse TPD data
		fmt.Printf("Fetching transport discovery data...\n")
		tpdRaw := internal.GetData(graphCacheTPD, graphTPDURL+"/all-transports", graphCacheAge)
		var transports []transportEntry
		if err := json.Unmarshal([]byte(tpdRaw), &transports); err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to parse TPD data: %w", err))
		}
		fmt.Printf("Loaded %d transports from TPD\n", len(transports))

		// Build adjacency map
		type neighbor struct {
			pk     string
			tpID   string
			tpType string
		}
		adjacency := make(map[string][]neighbor)
		for _, tp := range transports {
			edge0, edge1 := tp.Edges[0], tp.Edges[1]
			adjacency[edge0] = append(adjacency[edge0], neighbor{pk: edge1, tpID: tp.ID, tpType: tp.Type})
			if edge0 != edge1 {
				adjacency[edge1] = append(adjacency[edge1], neighbor{pk: edge0, tpID: tp.ID, tpType: tp.Type})
			}
		}

		// Parse minimum version filter if specified
		var minSemver semver.Version
		var filterByVersion bool
		if graphVersion != "" {
			vStr := strings.TrimPrefix(graphVersion, "v")
			var err error
			minSemver, err = semver.Parse(vStr)
			if err != nil {
				// Try adding .0 if only major.minor
				parts := strings.Split(vStr, ".")
				if len(parts) == 2 {
					minSemver, err = semver.Parse(vStr + ".0")
				}
				if err != nil {
					internal.PrintFatalError(cmd.Flags(), fmt.Errorf("invalid version format: %s", graphVersion))
				}
			}
			filterByVersion = true
		}

		// Fetch and parse UT data for version filtering
		versionMap := make(map[string]string)
		onlineSet := make(map[string]bool)
		versionFilteredSet := make(map[string]bool)
		if filterByVersion || graphOnlineOnly {
			fmt.Printf("Fetching uptime tracker data...\n")
			utRaw := internal.GetData(graphCacheUT, graphUTURL+"/uptimes?v=v2", graphCacheAge)
			var uptimes []uptimeEntry
			if err := json.Unmarshal([]byte(utRaw), &uptimes); err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to parse UT data: %w", err))
			}
			for _, ut := range uptimes {
				versionMap[ut.PK] = ut.Version
				if ut.Online {
					onlineSet[ut.PK] = true
				}
				// Check version filter using semver comparison
				if filterByVersion && ut.Version != "" {
					vStr := strings.TrimPrefix(ut.Version, "v")
					// Remove dirty suffix variations for semver parsing
					vStr = strings.Fields(vStr)[0]         // "1.3.34 dirty" -> "1.3.34"
					vStr = strings.Split(vStr, "+")[0]     // "1.3.34+dirty" -> "1.3.34"
					vStr = strings.SplitN(vStr, "-", 2)[0] // "1.3.34-dirty" -> "1.3.34"
					if v, err := semver.Parse(vStr); err == nil {
						if v.GTE(minSemver) {
							versionFilteredSet[ut.PK] = true
						}
					}
				}
			}
			fmt.Printf("Loaded %d visors from UT\n", len(uptimes))
			if filterByVersion {
				fmt.Printf("Visors matching version >= %s: %d\n", graphVersion, len(versionFilteredSet))
			}
		}

		// Check if local visor has transports
		if _, exists := adjacency[localPK]; !exists {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("local visor has no transports in TPD"))
		}

		// Setup gRPC client for streaming ping
		grpcClient, err := rpcgrpc.NewPingClient(clirpc.Addr)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to connect to gRPC server: %w", err))
		}
		defer grpcClient.Close() //nolint:errcheck

		// Setup context with signal handling
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		go func() {
			<-sigCh
			fmt.Println("\nCanceled.")
			cancel()
		}()

		// Result tracking for file output
		type pingResultEntry struct {
			Timestamp    string   `json:"timestamp"`
			PK           string   `json:"pk"`
			Version      string   `json:"version"`
			Level        int      `json:"level"`
			DmsgSuccess  bool     `json:"dmsg_success,omitempty"`
			DmsgLatency  float64  `json:"dmsg_latency_ms,omitempty"`
			RouteSuccess bool     `json:"route_success,omitempty"`
			RouteLatency float64  `json:"route_latency_ms,omitempty"`
			RouteHops    []string `json:"route_hops,omitempty"`
			Error        string   `json:"error,omitempty"`
			Transports   int      `json:"transports"`
		}
		type graphResults struct {
			StartTime  string                 `json:"start_time"`
			EndTime    string                 `json:"end_time"`
			LocalVisor string                 `json:"local_visor"`
			MinVersion string                 `json:"min_version,omitempty"`
			OnlineOnly bool                   `json:"online_only"`
			Results    []pingResultEntry      `json:"results"`
			LevelStats map[int]map[string]int `json:"level_stats"`
			TotalStats map[string]int         `json:"total_stats"`
		}
		results := graphResults{
			StartTime:  time.Now().Format(time.RFC3339),
			LocalVisor: localPK,
			MinVersion: graphVersion,
			OnlineOnly: graphOnlineOnly,
			Results:    []pingResultEntry{},
			LevelStats: make(map[int]map[string]int),
			TotalStats: make(map[string]int),
		}
		var textOutput strings.Builder
		textOutput.WriteString(fmt.Sprintf("=== Ping Graph Results ===\n"))
		textOutput.WriteString(fmt.Sprintf("Start Time: %s\n", results.StartTime))
		textOutput.WriteString(fmt.Sprintf("Local Visor: %s\n", localPK))
		if graphVersion != "" {
			textOutput.WriteString(fmt.Sprintf("Min Version: %s\n", graphVersion))
		}
		textOutput.WriteString("\n")

		// BFS to find visors at each level
		visited := make(map[string]bool)
		visited[localPK] = true

		currentLevel := []string{localPK}
		level := 0

		// Statistics
		type levelStats struct {
			total     int
			success   int
			failed    int
			skipped   int
			redundant int
		}
		stats := make(map[int]*levelStats)

		// Helper to check if visor passes filters
		passesFilter := func(pk string) bool {
			if graphOnlineOnly && !onlineSet[pk] {
				return false
			}
			if filterByVersion && !versionFilteredSet[pk] {
				return false
			}
			return true
		}

		// Detailed ping result for tracking
		type pingDetail struct {
			dmsgSuccess  bool
			dmsgLatency  float64
			routeSuccess bool
			routeLatency float64
			routeHops    []string
			hopLatencies []float64 // Per-hop cumulative latencies (ms)
			errMsg       string
		}

		// Helper to ping a visor
		pingVisor := func(pk string) (success bool, detail pingDetail) {
			var dmsgLatencyMs, routeLatencyMs float64
			var routeHopsList []string
			var capturedHops []rpcgrpc.RouteHopDetail
			var errors []string

			// Dmsg callback
			dmsgCallback := func(seq int32, lat time.Duration, isSetup bool, routeHops []rpcgrpc.RouteHopDetail, pingErr error) {
				_ = routeHops // unused for dmsg
				if isSetup {
					fmt.Printf("  Dmsg dial: %0.2f ms\n", 1000*lat.Seconds())
					return
				}
				if pingErr != nil {
					fmt.Printf("  Dmsg Ping %d: error: %v\n", seq, pingErr)
					errors = append(errors, fmt.Sprintf("dmsg: %v", pingErr))
					return
				}
				dmsgLatencyMs = 1000 * lat.Seconds()
				detail.dmsgSuccess = true
				fmt.Printf("  Dmsg Ping %d: %0.2f ms\n", seq, dmsgLatencyMs)
			}

			// Route callback
			routeCallback := func(seq int32, lat time.Duration, isSetup bool, routeHops []rpcgrpc.RouteHopDetail, pingErr error) {
				if isSetup {
					fmt.Printf("  Route setup: %0.2f ms\n", 1000*lat.Seconds())
					if len(routeHops) > 0 {
						capturedHops = routeHops
						routeHopsList = make([]string, len(routeHops))
						for i, hop := range routeHops {
							routeHopsList[i] = fmt.Sprintf("%s->%s@%s(%s)", hop.From, hop.To, hop.TpID, hop.TpType)
						}
						if graphShowRoute {
							fmt.Printf("  Route: [")
							for i, hop := range routeHops {
								if i > 0 {
									fmt.Printf(" ")
								}
								fmt.Printf("%s -> %s @ %s (%s)", hop.From, hop.To, hop.TpID, hop.TpType)
							}
							fmt.Printf("]\n")
						}
					}
					return
				}
				if pingErr != nil {
					fmt.Printf("  Ping %d: error: %v\n", seq, pingErr)
					errors = append(errors, fmt.Sprintf("route: %v", pingErr))
					return
				}
				routeLatencyMs = 1000 * lat.Seconds()
				detail.routeSuccess = true
				fmt.Printf("  Ping %d: %0.2f ms\n", seq, routeLatencyMs)
			}

			// Ping over dmsg first if requested
			if graphUseDmsg || graphDmsgOnly {
				err := grpcClient.StreamDmsgPing(ctx, pk, int32(graphTries), int32(graphPcktSize), graphTimeout, dmsgCallback)
				if err != nil {
					if ctx.Err() != nil {
						return false, detail
					}
					fmt.Printf("  Dmsg Error: %v\n", err)
					errors = append(errors, fmt.Sprintf("dmsg stream: %v", err))
				}
			}

			// Ping over skywire route (unless dmsg-only)
			if !graphDmsgOnly {
				if graphLocalRoute {
					fmt.Printf("  Calculating route locally...\n")
				} else {
					fmt.Printf("  Querying route finder...\n")
				}
				os.Stdout.Sync() //nolint:errcheck
				err := grpcClient.StreamPing(ctx, pk, int32(graphTries), int32(graphPcktSize), graphLocalRoute, graphTimeout, graphSetupTimeout, routeCallback)
				if err != nil {
					if ctx.Err() != nil {
						return false, detail
					}
					fmt.Printf("  Route Error: %v\n", err)
					errors = append(errors, fmt.Sprintf("route stream: %v", err))
				}

				// Per-hop latency measurement if enabled and we have a multi-hop route
				if graphHopLatency && len(capturedHops) > 1 && detail.routeSuccess {
					fmt.Printf("  Measuring per-hop latency...\n")
					hopLatencies := make([]float64, len(capturedHops))
					var prevLatency float64

					// Ping each intermediate hop to get cumulative latency
					for i, hop := range capturedHops {
						hopPK := hop.To
						var hopLatencyMs float64

						// Simple ping callback for hop measurement
						hopCallback := func(seq int32, lat time.Duration, isSetup bool, _ []rpcgrpc.RouteHopDetail, pingErr error) {
							if isSetup || pingErr != nil {
								return
							}
							hopLatencyMs = 1000 * lat.Seconds()
						}

						// Try to ping this hop
						hopErr := grpcClient.StreamPing(ctx, hopPK, 1, int32(graphPcktSize), graphLocalRoute, graphTimeout, graphSetupTimeout, hopCallback)
						if hopErr == nil && hopLatencyMs > 0 {
							hopLatencies[i] = hopLatencyMs
							segmentLatency := hopLatencyMs - prevLatency
							if i == 0 {
								fmt.Printf("    Hop %d (%s): %.2f ms\n", i+1, hopPK[:8]+"...", hopLatencyMs)
							} else {
								fmt.Printf("    Hop %d (%s): %.2f ms (segment: +%.2f ms)\n", i+1, hopPK[:8]+"...", hopLatencyMs, segmentLatency)
							}
							prevLatency = hopLatencyMs
						} else {
							fmt.Printf("    Hop %d (%s): failed\n", i+1, hopPK[:8]+"...")
						}
					}
					detail.hopLatencies = hopLatencies
				}
			}

			// Build final detail
			detail.dmsgLatency = dmsgLatencyMs
			detail.routeLatency = routeLatencyMs
			detail.routeHops = routeHopsList
			if len(errors) > 0 {
				detail.errMsg = strings.Join(errors, "; ")
			}

			// Success if either method succeeded
			success = detail.dmsgSuccess || detail.routeSuccess
			return success, detail
		}

		fmt.Printf("\n=== Starting Network Graph Ping ===\n")
		if filterByVersion {
			fmt.Printf("Minimum version: %s\n", graphVersion)
		}
		if graphOnlineOnly {
			fmt.Printf("Online only: yes\n")
		}
		fmt.Printf("\n")

		// Main BFS loop
		for {
			level++
			if graphMaxLevel > 0 && level > graphMaxLevel {
				fmt.Printf("Reached max level %d\n", graphMaxLevel)
				break
			}

			// Find all visors at this level
			var nextLevel []string
			levelVisors := make(map[string][]neighbor) // pk -> transports to it

			for _, pk := range currentLevel {
				for _, n := range adjacency[pk] {
					if visited[n.pk] {
						continue
					}
					if !passesFilter(n.pk) {
						continue
					}
					levelVisors[n.pk] = append(levelVisors[n.pk], n)
				}
			}

			if len(levelVisors) == 0 {
				fmt.Printf("Level %d: No new visors found\n", level)
				break
			}

			stats[level] = &levelStats{total: len(levelVisors)}
			fmt.Printf("=== Level %d: %d visors ===\n", level, len(levelVisors))

			// Ping each visor at this level
			for pk, neighbors := range levelVisors {
				if ctx.Err() != nil {
					fmt.Println("Canceled.")
					goto done
				}

				visited[pk] = true

				// Count redundant transports
				if len(neighbors) > 1 {
					stats[level].redundant++
				}

				// Skip if below start level
				if level < graphStartLevel {
					stats[level].skipped++
					nextLevel = append(nextLevel, pk)
					continue
				}

				ver := versionMap[pk]
				if ver == "" {
					ver = "unknown"
				}
				fmt.Printf("\nPinging %s (version: %s, transports: %d)\n", pk, ver, len(neighbors))

				success, detail := pingVisor(pk)

				// Record result
				entry := pingResultEntry{
					Timestamp:    time.Now().Format(time.RFC3339),
					PK:           pk,
					Version:      ver,
					Level:        level,
					DmsgSuccess:  detail.dmsgSuccess,
					DmsgLatency:  detail.dmsgLatency,
					RouteSuccess: detail.routeSuccess,
					RouteLatency: detail.routeLatency,
					RouteHops:    detail.routeHops,
					Error:        detail.errMsg,
					Transports:   len(neighbors),
				}
				results.Results = append(results.Results, entry)

				// Add to text output
				textOutput.WriteString(fmt.Sprintf("[%s] Level %d: %s (v%s)\n", entry.Timestamp, level, pk, ver))
				if detail.dmsgSuccess {
					textOutput.WriteString(fmt.Sprintf("  DMSG: %.2f ms\n", detail.dmsgLatency))
				}
				if detail.routeSuccess {
					textOutput.WriteString(fmt.Sprintf("  Route: %.2f ms\n", detail.routeLatency))
				}
				if detail.errMsg != "" {
					textOutput.WriteString(fmt.Sprintf("  Errors: %s\n", detail.errMsg))
				}
				textOutput.WriteString(fmt.Sprintf("  Status: %s\n\n", func() string {
					if success {
						return "SUCCESS"
					}
					return "FAILED"
				}()))

				if success {
					stats[level].success++
					nextLevel = append(nextLevel, pk)
				} else {
					stats[level].failed++
					if graphContinue {
						// Still add to next level to explore its neighbors
						nextLevel = append(nextLevel, pk)
					}
				}

				// Test redundant transports if requested
				if graphRedundancy && len(neighbors) > 1 {
					seenTypes := make(map[string]bool)
					seenTypes[neighbors[0].tpType] = true
					for _, n := range neighbors[1:] {
						if seenTypes[n.tpType] {
							continue
						}
						seenTypes[n.tpType] = true
						fmt.Printf("  (Redundant transport: %s via %s)\n", n.tpType, n.tpID)
					}
				}
			}

			currentLevel = nextLevel
			if len(currentLevel) == 0 {
				break
			}
		}

	done:
		// Print summary
		fmt.Printf("\n=== Summary ===\n")
		totalSuccess := 0
		totalFailed := 0
		totalSkipped := 0
		totalRedundant := 0
		for lvl := 1; lvl <= level; lvl++ {
			if s, ok := stats[lvl]; ok {
				fmt.Printf("Level %d: %d visors (%d success, %d failed, %d skipped, %d with redundant transports)\n",
					lvl, s.total, s.success, s.failed, s.skipped, s.redundant)
				totalSuccess += s.success
				totalFailed += s.failed
				totalSkipped += s.skipped
				totalRedundant += s.redundant

				// Populate level stats for JSON
				results.LevelStats[lvl] = map[string]int{
					"total":     s.total,
					"success":   s.success,
					"failed":    s.failed,
					"skipped":   s.skipped,
					"redundant": s.redundant,
				}
			}
		}
		fmt.Printf("\nTotal: %d success, %d failed, %d skipped, %d with redundant transports\n",
			totalSuccess, totalFailed, totalSkipped, totalRedundant)
		fmt.Printf("Unique visors visited: %d\n", len(visited)-1) // -1 for local visor

		// Populate total stats for JSON
		results.TotalStats = map[string]int{
			"success":   totalSuccess,
			"failed":    totalFailed,
			"skipped":   totalSkipped,
			"redundant": totalRedundant,
			"visited":   len(visited) - 1,
		}
		results.EndTime = time.Now().Format(time.RFC3339)

		// Add summary to text output
		textOutput.WriteString("\n=== Summary ===\n")
		for lvl := 1; lvl <= level; lvl++ {
			if s, ok := stats[lvl]; ok {
				textOutput.WriteString(fmt.Sprintf("Level %d: %d visors (%d success, %d failed, %d skipped, %d redundant)\n",
					lvl, s.total, s.success, s.failed, s.skipped, s.redundant))
			}
		}
		textOutput.WriteString(fmt.Sprintf("\nTotal: %d success, %d failed, %d skipped, %d redundant\n",
			totalSuccess, totalFailed, totalSkipped, totalRedundant))
		textOutput.WriteString(fmt.Sprintf("Unique visors visited: %d\n", len(visited)-1))
		textOutput.WriteString(fmt.Sprintf("End Time: %s\n", results.EndTime))

		// Write JSON output file if specified
		if graphOutputJSON != "" {
			jsonData, err := json.MarshalIndent(results, "", "  ")
			if err != nil {
				fmt.Printf("Error marshaling JSON: %v\n", err)
			} else {
				if err := os.WriteFile(graphOutputJSON, jsonData, 0644); err != nil {
					fmt.Printf("Error writing JSON file: %v\n", err)
				} else {
					fmt.Printf("Results written to JSON: %s\n", graphOutputJSON)
				}
			}
		}

		// Write text output file if specified
		if graphOutputText != "" {
			if err := os.WriteFile(graphOutputText, []byte(textOutput.String()), 0644); err != nil {
				fmt.Printf("Error writing text file: %v\n", err)
			} else {
				fmt.Printf("Results written to text: %s\n", graphOutputText)
			}
		}
	},
}
