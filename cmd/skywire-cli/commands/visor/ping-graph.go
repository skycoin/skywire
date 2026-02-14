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
	"github.com/pterm/pterm"
	"github.com/spf13/cobra"

	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/visor"
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
	graphAllServers   bool
	graphDmsgServerPK string
	graphTreeView     bool
	graphHops         uint
	graphRetries      int
)

func init() {
	// Build tree example for help text using pterm
	treeExample := pterm.TreeNode{
		Text: "edge",
		Children: []pterm.TreeNode{
			{Text: "edge tpid calc setup ping... avg"},
		},
	}
	treeStr, _ := pterm.DefaultTree.WithRoot(treeExample).Srender()

	pingGraphCmd.Long = `Ping visors reachable from this visor, organized by hop distance.

Level 1: Visors with direct transports to local visor
Level 2: Visors connected to Level 1 visors
Level 3: Visors connected to Level 2 visors
...and so on until no new visors are found.

Uses cached TPD and UT data to build the network graph, then pings
each visor at each level. Skips visors already pinged at earlier levels.

Tree view (--tree) output format:

` + treeStr + `
  - edge: visor public key
  - tpid: transport ID (green=stcpr, blue=sudph)
  - calc: route calculation time in ms (--local-route only)
  - setup: route setup time in ms
  - ping...: ping latencies in ms (one per --tries)
  - total: calc + setup + avg(ping)

  Failures: red text = ping failed, red background = setup/calc failed`

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
	pingGraphCmd.Flags().BoolVar(&graphAllServers, "all-servers", false, "ping through all DMSG servers the remote visor is connected to (only with --dmsg or --dmsg-only)")
	pingGraphCmd.Flags().StringVar(&graphDmsgServerPK, "via-server", "", "ping through specific DMSG server (only with --dmsg or --dmsg-only)")
	pingGraphCmd.Flags().BoolVar(&graphLocalRoute, "local-route", false, "calculate routes locally using cached TPD data instead of querying route finder")
	pingGraphCmd.Flags().BoolVar(&graphShowRoute, "show-route", false, "show the route hops used for the ping")
	pingGraphCmd.Flags().BoolVar(&graphHopLatency, "hop-latency", false, "measure per-hop latency for multi-hop routes (requires --show-route)")
	pingGraphCmd.Flags().DurationVar(&graphSetupTimeout, "setup-timeout", 30*time.Second, "timeout for route setup phase")
	pingGraphCmd.Flags().StringVar(&graphOutputJSON, "output-json", "", "write results to JSON file (with timestamp)")
	pingGraphCmd.Flags().StringVar(&graphOutputText, "output-text", "", "write results to text file (with timestamp)")
	pingGraphCmd.Flags().BoolVar(&graphTreeView, "tree", false, "display results as tree view with per-transport latencies")
	pingGraphCmd.Flags().UintVar(&graphHops, "hops", 0, "exact hop level to ping (0 = all levels, 1 = direct transports only, 2 = two hops, etc.)")
	pingGraphCmd.Flags().IntVar(&graphRetries, "retries", 1, "number of retry attempts if ping fails (tree mode only)")
	pingCmd.AddCommand(pingGraphCmd)
}

var pingGraphCmd = &cobra.Command{
	Use:   "graph",
	Short: "Ping visors across the network by hop level",
	Run: func(cmd *cobra.Command, _ []string) {
		// Validate mutually exclusive flags
		if graphTreeView && (graphUseDmsg || graphDmsgOnly) {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("--tree is mutually exclusive with --dmsg and --dmsg-only"))
		}

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

		// Add local visor's transports to adjacency map (may not be in TPD)
		localTransports, err := rpcClient.Transports(nil, nil, false)
		if err != nil {
			fmt.Printf("Warning: could not fetch local transports: %v\n", err)
		} else {
			for _, tp := range localTransports {
				remotePK := tp.Remote.String()
				tpID := tp.ID.String()
				tpType := string(tp.Type)
				// Check if this neighbor already exists for localPK
				found := false
				for _, n := range adjacency[localPK] {
					if n.pk == remotePK && n.tpType == tpType {
						found = true
						break
					}
				}
				if !found {
					adjacency[localPK] = append(adjacency[localPK], neighbor{pk: remotePK, tpID: tpID, tpType: tpType})
					// Also add reverse edge
					foundReverse := false
					for _, n := range adjacency[remotePK] {
						if n.pk == localPK && n.tpType == tpType {
							foundReverse = true
							break
						}
					}
					if !foundReverse {
						adjacency[remotePK] = append(adjacency[remotePK], neighbor{pk: localPK, tpID: tpID, tpType: tpType})
					}
				}
			}
			if len(localTransports) > 0 {
				fmt.Printf("Added %d local transports to graph\n", len(localTransports))
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

		// Tree view mode: separate execution path
		if graphTreeView {
			// Convert adjacency map to use treeNeighbor type
			treeAdjacency := make(map[string][]treeNeighbor)
			for pk, neighbors := range adjacency {
				for _, n := range neighbors {
					treeAdjacency[pk] = append(treeAdjacency[pk], treeNeighbor(n)) //nolint:staticcheck
				}
			}
			runTreeViewMode(ctx, grpcClient, rpcClient, localPK, treeAdjacency, localTransports, passesFilter)
			return
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
			dmsgCallback := func(seq int32, lat time.Duration, isSetup bool, routeHops []rpcgrpc.RouteHopDetail, serverPK string, _ time.Duration, pingErr error) {
				_ = routeHops // unused for dmsg
				if isSetup {
					msg := fmt.Sprintf("  Dmsg dial: %0.2f ms", 1000*lat.Seconds())
					if serverPK != "" {
						msg += fmt.Sprintf(" (via server %s)", serverPK)
					}
					fmt.Println(msg)
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
			routeCallback := func(seq int32, lat time.Duration, isSetup bool, routeHops []rpcgrpc.RouteHopDetail, _ string, routeCalcTime time.Duration, pingErr error) {
				if isSetup {
					// Print route calculation time if available (local route mode)
					if routeCalcTime > 0 {
						fmt.Printf(" %0.2f ms (calc: %0.2f ms, setup: %0.2f ms)\n",
							1000*lat.Seconds(), 1000*routeCalcTime.Seconds(), 1000*(lat-routeCalcTime).Seconds())
					} else {
						fmt.Printf(" %0.2f ms\n", 1000*lat.Seconds())
					}
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
				if graphAllServers {
					// Ping through all DMSG servers the remote visor is connected to
					servers, err := grpcClient.GetRemoteDmsgServers(ctx, pk)
					if err != nil {
						fmt.Printf("  Failed to get DMSG servers: %v\n", err)
						errors = append(errors, fmt.Sprintf("dmsg servers: %v", err))
					} else if len(servers) == 0 {
						fmt.Printf("  Remote visor not connected to any DMSG servers\n")
						errors = append(errors, "no dmsg servers")
					} else {
						fmt.Printf("  Testing %d DMSG server(s)...\n", len(servers))
						for _, server := range servers {
							err := grpcClient.StreamDmsgPing(ctx, pk, int32(graphTries), int32(graphPcktSize), graphTimeout, server, dmsgCallback)
							if err != nil {
								if ctx.Err() != nil {
									return false, detail
								}
								fmt.Printf("    Server %s: error: %v\n", server[:16]+"...", err)
							}
						}
					}
				} else {
					// Normal DMSG ping (optionally via specific server)
					err := grpcClient.StreamDmsgPing(ctx, pk, int32(graphTries), int32(graphPcktSize), graphTimeout, graphDmsgServerPK, dmsgCallback)
					if err != nil {
						if ctx.Err() != nil {
							return false, detail
						}
						fmt.Printf("  Dmsg Error: %v\n", err)
						errors = append(errors, fmt.Sprintf("dmsg stream: %v", err))
					}
				}
			}

			// Ping over skywire route (unless dmsg-only)
			if !graphDmsgOnly {
				if graphLocalRoute {
					fmt.Printf("  Calculating route locally...")
				} else {
					fmt.Printf("  Querying route finder...")
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
						hopCallback := func(seq int32, lat time.Duration, isSetup bool, _ []rpcgrpc.RouteHopDetail, _ string, _ time.Duration, pingErr error) {
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
			// Stop if we've passed the exact hop level (when --hops is set)
			if graphHops > 0 && uint(level) > graphHops {
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

				// Skip if below start level or not at exact hop level (when --hops is set)
				skipPing := level < graphStartLevel || (graphHops > 0 && uint(level) != graphHops)
				if skipPing {
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

// treeNeighbor represents a transport neighbor for tree view
type treeNeighbor struct {
	pk     string
	tpID   string
	tpType string
}

// transportLatencyData tracks timing for each phase of a transport ping
type transportLatencyData struct {
	tpID   string
	tpType string
	from   string
	to     string
	level  int
	// Phase timings (ms) - 0 means not yet measured
	calcTimeMs  float64
	setupTimeMs float64
	pingSamples []float64 // All ping samples in ms
	// Phase errors - empty means success or not yet attempted
	calcErr  string
	setupErr string
	pingErr  string
	// Overall status
	phase string // "pending", "calc", "setup", "ping", "done"
}

// runTreeViewMode executes the ping graph in tree view mode
func runTreeViewMode(
	ctx context.Context,
	grpcClient *rpcgrpc.PingClient,
	rpcClient visor.API,
	localPK string,
	adjacency map[string][]treeNeighbor,
	localTransports []*visor.TransportSummary,
	passesFilter func(string) bool,
) {
	// Build set of valid local transport IDs for level 1 verification
	localTpIDs := make(map[string]bool)
	localTpByRemote := make(map[string]treeNeighbor) // remotePK -> transport info
	for _, tp := range localTransports {
		tpID := tp.ID.String()
		localTpIDs[tpID] = true
		remotePK := tp.Remote.String()
		// Store the transport info keyed by remote PK for quick lookup
		localTpByRemote[remotePK] = treeNeighbor{
			pk:     remotePK,
			tpID:   tpID,
			tpType: string(tp.Type),
		}
	}
	// Data structure to track transport latencies
	// Key: tpID, Value: latency data
	transportLatencies := make(map[string]*transportLatencyData)

	// Track which visors we've visited and at what level
	visitedLevel := make(map[string]int)
	visitedLevel[localPK] = 0

	// Track cumulative latency to each visor (for multi-hop derivation)
	visorCumulativeLatency := make(map[string]float64)
	visorCumulativeLatency[localPK] = 0

	// Track the path (sequence of hops) to reach each visor
	// Key: visor PK, Value: slice of (tpID, tpType, from, to) representing path from local
	type pathHop struct {
		tpID   string
		tpType string
		from   string
		to     string
	}
	visorPath := make(map[string][]pathHop)
	visorPath[localPK] = nil // Local visor has no path

	// Track first-hop transports that no longer exist - skip entire subtrees using these
	deadFirstHops := make(map[string]bool)

	// Build ordered list for tree display
	// Each entry is a transport (not a visor), allowing same visor with different transports
	type treeEntry struct {
		tpID       string
		remotePK   string
		level      int
		parentPK   string
		parentTpID string
	}
	var treeEntries []treeEntry

	// Helper to format transport ID with color based on type
	formatTpID := func(tpID, tpType string) string {
		switch tpType {
		case "stcpr":
			return pterm.Green(tpID)
		case "sudph":
			return pterm.Blue(tpID)
		default:
			return tpID
		}
	}

	// Helper to truncate error messages to a reasonable length
	truncateErr := func(err string, maxLen int) string {
		if len(err) <= maxLen {
			return err
		}
		// Try to find a meaningful short form
		if strings.Contains(err, "route setup timeout") {
			return "setup-tmo"
		}
		if strings.Contains(err, "route calculation timeout") || strings.Contains(err, "context deadline exceeded") {
			return "calc-tmo"
		}
		if strings.Contains(err, "timeout") {
			return "timeout"
		}
		if strings.Contains(err, "deadline exceeded") {
			return "deadline"
		}
		if strings.Contains(err, "noise handshake") {
			return "handshake"
		}
		if strings.Contains(err, "maximum retries") {
			return "max-retry"
		}
		if strings.Contains(err, "connection refused") {
			return "conn-ref"
		}
		if strings.Contains(err, "stream error") {
			return "stream-err"
		}
		// Generic truncation
		return err[:maxLen-3] + "..."
	}

	// Helper to format a tree entry as text
	formatEntry := func(entry treeEntry) string {
		data := transportLatencies[entry.tpID]
		if data == nil {
			return fmt.Sprintf("%s %s ...", entry.remotePK, entry.tpID)
		}

		// Format timing columns with fixed widths for alignment
		// Column widths: calc=variable, setup=9, pings=variable, total=9
		var calcStr, setupStr, pingsStr, totalStr string

		// Determine if we have an early failure (calc or setup)
		earlyFailure := data.calcErr != "" || data.setupErr != ""
		pingFailure := data.pingErr != "" && !earlyFailure

		// Calc time/error (minimal width, expands for errors/values)
		if data.calcErr != "" {
			calcStr = truncateErr(data.calcErr, 9)
		} else if data.calcTimeMs > 0 {
			calcStr = fmt.Sprintf("%.1fms", data.calcTimeMs)
		} else if data.phase == "pending" || data.phase == "calc" {
			calcStr = "..."
		} else {
			calcStr = "-"
		}

		// Setup time/error (9 chars, right-aligned)
		if data.setupErr != "" {
			setupStr = fmt.Sprintf("%9s", truncateErr(data.setupErr, 9))
		} else if data.setupTimeMs > 0 {
			setupStr = fmt.Sprintf("%9s", fmt.Sprintf("%.1fms", data.setupTimeMs))
		} else if data.phase == "pending" || data.phase == "calc" || data.phase == "setup" {
			setupStr = fmt.Sprintf("%9s", "...")
		} else {
			setupStr = fmt.Sprintf("%9s", "-")
		}

		// Ping times/error - show all samples (space-separated, each 8 chars for alignment)
		if data.pingErr != "" {
			pingsStr = truncateErr(data.pingErr, 12)
		} else if len(data.pingSamples) > 0 {
			var pingParts []string
			for _, p := range data.pingSamples {
				pingParts = append(pingParts, fmt.Sprintf("%8s", fmt.Sprintf("%.1fms", p)))
			}
			pingsStr = strings.Join(pingParts, " ")
		} else if data.phase != "done" {
			pingsStr = fmt.Sprintf("%8s", "...")
		} else {
			pingsStr = fmt.Sprintf("%8s", "-")
		}

		// Average ping time (9 chars, right-aligned) - excludes setup time
		if !earlyFailure && !pingFailure && len(data.pingSamples) > 0 {
			var pingSum float64
			for _, p := range data.pingSamples {
				pingSum += p
			}
			avgPing := pingSum / float64(len(data.pingSamples))
			totalStr = fmt.Sprintf("%9s", fmt.Sprintf("%.1fms", avgPing))
		} else {
			totalStr = fmt.Sprintf("%9s", "-")
		}

		// Build the line: remotePK tpID calc setup pings... avg
		tpIDFormatted := formatTpID(data.tpID, data.tpType)

		// Format the entire line based on failure status
		var text string
		if earlyFailure {
			// Red background with white text for early failures
			line := fmt.Sprintf("%s %s %s %s %s %s",
				entry.remotePK, data.tpID, calcStr, setupStr, pingsStr, totalStr)
			text = pterm.BgRed.Sprint(pterm.White(line))
		} else if pingFailure {
			// Red text for ping failures
			text = pterm.Red(fmt.Sprintf("%s %s %s %s %s %s",
				entry.remotePK, data.tpID, calcStr, setupStr, pingsStr, totalStr))
		} else {
			// Normal formatting with colored tpID
			text = fmt.Sprintf("%s %s %s %s %s %s",
				entry.remotePK, tpIDFormatted, calcStr, setupStr, pingsStr, totalStr)
		}

		return text
	}

	// Helper to get average latency for a specific transport (by tpID)
	getTransportAvgLatency := func(tpID string) float64 {
		data := transportLatencies[tpID]
		if data == nil || len(data.pingSamples) == 0 {
			return -1
		}
		var pingSum float64
		for _, p := range data.pingSamples {
			pingSum += p
		}
		return pingSum / float64(len(data.pingSamples))
	}

	// Helper to get best average latency for a visor (across all its transports)
	getVisorAvgLatency := func(pk string) float64 {
		// Find the best (lowest) average ping latency transport to this visor
		bestLatency := float64(-1)
		for _, entry := range treeEntries {
			if entry.remotePK != pk {
				continue
			}
			avgPing := getTransportAvgLatency(entry.tpID)
			if avgPing >= 0 && (bestLatency < 0 || avgPing < bestLatency) {
				bestLatency = avgPing
			}
		}
		return bestLatency
	}

	// Helper to render the tree with current data - separate trees per level
	renderTree := func() {
		// Clear screen and scrollback buffer, move cursor to top-left
		fmt.Print("\033[H\033[2J\033[3J")

		// Print label tree header
		labelTree := pterm.TreeNode{
			Text: pterm.Gray("edge"),
			Children: []pterm.TreeNode{
				{Text: pterm.Gray("edge") + " " + pterm.Green("tpid") + " " + pterm.Gray("calc setup ping... avg")},
			},
		}
		pterm.DefaultTree.WithRoot(labelTree).Render() //nolint:errcheck,gosec
		fmt.Println()

		// Group entries by level
		entriesByLevel := make(map[int][]treeEntry)
		for _, entry := range treeEntries {
			entriesByLevel[entry.level] = append(entriesByLevel[entry.level], entry)
		}

		// Find max level
		maxLevel := 0
		for lvl := range entriesByLevel {
			if lvl > maxLevel {
				maxLevel = lvl
			}
		}

		// Render level 1 tree: local visor as root, level 1 entries as children (sorted by latency)
		if level1Entries, ok := entriesByLevel[1]; ok && len(level1Entries) > 0 {
			fmt.Println(pterm.Yellow("=== Level 1 (direct transports) ==="))
			// Sort level 1 entries by latency (lowest first)
			for i := 0; i < len(level1Entries)-1; i++ {
				for j := i + 1; j < len(level1Entries); j++ {
					latI := getTransportAvgLatency(level1Entries[i].tpID)
					latJ := getTransportAvgLatency(level1Entries[j].tpID)
					swap := false
					if latI < 0 && latJ >= 0 {
						// i has no latency, j does - j should come first
						swap = true
					} else if latI >= 0 && latJ >= 0 && latJ < latI {
						// Both have latency - lower is better
						swap = true
					}
					if swap {
						level1Entries[i], level1Entries[j] = level1Entries[j], level1Entries[i]
					}
				}
			}

			var children []pterm.TreeNode
			for _, entry := range level1Entries {
				children = append(children, pterm.TreeNode{Text: formatEntry(entry)})
			}
			rootTree := pterm.TreeNode{
				Text:     pterm.Cyan(localPK) + pterm.Gray(" (local)"),
				Children: children,
			}
			pterm.DefaultTree.WithRoot(rootTree).Render() //nolint:errcheck,gosec
		}

		// Render level 2+ trees: separate tree per parent, ordered by parent's lowest latency
		for lvl := 2; lvl <= maxLevel; lvl++ {
			levelEntries, ok := entriesByLevel[lvl]
			if !ok || len(levelEntries) == 0 {
				continue
			}

			// Group entries by parent
			entriesByParent := make(map[string][]treeEntry)
			for _, entry := range levelEntries {
				entriesByParent[entry.parentPK] = append(entriesByParent[entry.parentPK], entry)
			}

			// Sort parents by lowest latency (from previous level)
			type parentLatency struct {
				pk      string
				latency float64
			}
			var parents []parentLatency
			for parentPK := range entriesByParent {
				lat := getVisorAvgLatency(parentPK)
				parents = append(parents, parentLatency{pk: parentPK, latency: lat})
			}
			// Sort: lowest latency first, then by PK for stability
			for i := 0; i < len(parents)-1; i++ {
				for j := i + 1; j < len(parents); j++ {
					swap := false
					if parents[i].latency < 0 && parents[j].latency >= 0 {
						// i has no latency, j does - j should come first
						swap = true
					} else if parents[i].latency >= 0 && parents[j].latency >= 0 {
						// Both have latency - lower is better
						if parents[j].latency < parents[i].latency {
							swap = true
						}
					}
					if swap {
						parents[i], parents[j] = parents[j], parents[i]
					}
				}
			}

			// Render a separate tree for each parent
			fmt.Println() // Blank line between levels
			fmt.Println(pterm.Yellow(fmt.Sprintf("=== Level %d ===", lvl)))

			for _, parent := range parents {
				entries := entriesByParent[parent.pk]
				if len(entries) == 0 {
					continue
				}

				// Sort entries within this parent by latency
				for i := 0; i < len(entries)-1; i++ {
					for j := i + 1; j < len(entries); j++ {
						latI := getTransportAvgLatency(entries[i].tpID)
						latJ := getTransportAvgLatency(entries[j].tpID)
						swap := false
						if latI < 0 && latJ >= 0 {
							swap = true
						} else if latI >= 0 && latJ >= 0 && latJ < latI {
							swap = true
						}
						if swap {
							entries[i], entries[j] = entries[j], entries[i]
						}
					}
				}

				var children []pterm.TreeNode
				for _, entry := range entries {
					children = append(children, pterm.TreeNode{Text: formatEntry(entry)})
				}

				// Root is the parent visor with its latency
				latStr := ""
				if parent.latency >= 0 {
					latStr = pterm.Gray(fmt.Sprintf(" (%.1fms)", parent.latency))
				}
				parentTree := pterm.TreeNode{
					Text:     pterm.Cyan(parent.pk) + latStr,
					Children: children,
				}
				pterm.DefaultTree.WithRoot(parentTree).Render() //nolint:errcheck,gosec
			}
		}

		fmt.Println()
		fmt.Println(pterm.Gray("Press Ctrl+C to stop"))
	}

	// Helper to ping a transport and update data
	// pathToParent contains the path from local to parentPK (empty for level 1)
	pingTransport := func(remotePK string, parentPK string, tpID string, tpType string, level int, pathToParent []pathHop) {
		// Early check: for level 1, the transport itself is the first hop
		// For level 2+, check if the first hop is already known dead
		var firstHopTpID string
		if level == 1 {
			firstHopTpID = tpID
		} else if len(pathToParent) > 0 {
			firstHopTpID = pathToParent[0].tpID
		}

		// If first hop is dead, skip entirely - don't even show in tree
		if firstHopTpID != "" && deadFirstHops[firstHopTpID] {
			return
		}

		data := &transportLatencyData{
			tpID:   tpID,
			tpType: tpType,
			from:   parentPK,
			to:     remotePK,
			level:  level,
			phase:  "pending",
		}
		transportLatencies[tpID] = data

		// Render initial state
		renderTree()

		// Callback for ping results - captures setup and ping phases
		callback := func(_ int32, lat time.Duration, isSetup bool, _ []rpcgrpc.RouteHopDetail, _ string, routeCalcTime time.Duration, pingErr error) {
			if isSetup {
				// Setup phase completed
				totalSetupMs := 1000 * lat.Seconds()
				if routeCalcTime > 0 {
					// Local route mode: separate calc and setup times
					data.calcTimeMs = 1000 * routeCalcTime.Seconds()
					data.setupTimeMs = totalSetupMs - data.calcTimeMs
				} else {
					// Route finder mode: no calc time, just setup
					data.setupTimeMs = totalSetupMs
				}
				data.phase = "ping"
				renderTree()
				return
			}
			if pingErr != nil {
				data.pingErr = pingErr.Error()
				data.phase = "done"
				return
			}
			// Store each ping sample
			pingLatencyMs := 1000 * lat.Seconds()
			data.pingSamples = append(data.pingSamples, pingLatencyMs)
			renderTree()
		}

		// Retry loop for transient failures
		maxAttempts := graphRetries
		if maxAttempts < 1 {
			maxAttempts = 1
		}

		// Client-side timeout: setup timeout + single ping timeout + buffer
		// Keep it short - if the server hangs or routing rules expire, move on quickly.
		// The server has its own per-ping timeouts; this is just a safety net.
		perTransportTimeout := graphSetupTimeout + graphTimeout + 30*time.Second

		for attempt := 1; attempt <= maxAttempts; attempt++ {
			// Reset state for retry
			if attempt > 1 {
				data.calcTimeMs = 0
				data.setupTimeMs = 0
				data.pingSamples = nil
				data.calcErr = ""
				data.setupErr = ""
				data.pingErr = ""
			}

			// Update phase before starting - for explicit routes, skip to setup (no calc)
			if level > 1 {
				// Level 2+: we're using explicit route, no calculation needed
				data.phase = "setup"
			} else if graphLocalRoute {
				data.phase = "calc"
			} else {
				data.phase = "setup"
			}
			renderTree()

			// Create per-transport timeout context to prevent hanging
			pingCtx, pingCancel := context.WithTimeout(ctx, perTransportTimeout)

			// Perform ping
			var err error
			if level == 1 {
				// Level 1: direct transport, use transport ID to skip route calculation
				// First verify the transport still exists locally
				if !localTpIDs[tpID] {
					// Transport no longer exists - mark as dead and skip
					deadFirstHops[tpID] = true
					pingCancel()
					delete(transportLatencies, tpID)
					return
				}
				err = grpcClient.StreamPingWithTransport(pingCtx, remotePK, int32(graphTries), int32(graphPcktSize), graphLocalRoute, graphTimeout, graphSetupTimeout, tpID, callback) //nolint:gosec
			} else {
				// Level 2+: use explicit route (path to parent + current hop)
				// First verify the first hop transport still exists locally
				if len(pathToParent) > 0 {
					firstHopTpID := pathToParent[0].tpID
					// Quick check: is this first-hop already known to be dead?
					if deadFirstHops[firstHopTpID] {
						pingCancel()
						delete(transportLatencies, tpID)
						return
					}
					// Check if transport still exists
					if !localTpIDs[firstHopTpID] {
						// Mark this first-hop as dead so we skip entire subtree
						deadFirstHops[firstHopTpID] = true
						pingCancel()
						delete(transportLatencies, tpID)
						return
					}
				}

				// Build forward hops: path to parent + current hop
				var forwardHops []rpcgrpc.RouteHopDetail
				for _, h := range pathToParent {
					forwardHops = append(forwardHops, rpcgrpc.RouteHopDetail{
						TpID:   h.tpID,
						From:   h.from,
						To:     h.to,
						TpType: h.tpType,
					})
				}
				// Add current hop
				forwardHops = append(forwardHops, rpcgrpc.RouteHopDetail{
					TpID:   tpID,
					From:   parentPK,
					To:     remotePK,
					TpType: tpType,
				})

				// Build reverse hops (reverse order, swap from/to)
				var reverseHops []rpcgrpc.RouteHopDetail
				// First add current hop reversed
				reverseHops = append(reverseHops, rpcgrpc.RouteHopDetail{
					TpID:   tpID,
					From:   remotePK,
					To:     parentPK,
					TpType: tpType,
				})
				// Then add path to parent in reverse order
				for i := len(pathToParent) - 1; i >= 0; i-- {
					h := pathToParent[i]
					reverseHops = append(reverseHops, rpcgrpc.RouteHopDetail{
						TpID:   h.tpID,
						From:   h.to, // Swap from/to for reverse
						To:     h.from,
						TpType: h.tpType,
					})
				}

				err = grpcClient.StreamPingWithRoute(pingCtx, remotePK, int32(graphTries), int32(graphPcktSize), graphLocalRoute, graphTimeout, graphSetupTimeout, forwardHops, reverseHops, callback) //nolint:gosec
			}
			pingCancel() // Always clean up the context

			if err != nil {
				if ctx.Err() != nil {
					// Parent context canceled - stop everything
					break
				}

				// For level 2+, check if first hop transport still exists on error
				if level > 1 && len(pathToParent) > 0 {
					firstHopTpID := pathToParent[0].tpID
					if !localTpIDs[firstHopTpID] {
						// First hop gone - refresh localTpIDs and verify
						currentTps, tpErr := rpcClient.Transports(nil, nil, false)
						if tpErr == nil {
							localTpIDs = make(map[string]bool)
							for _, tp := range currentTps {
								localTpIDs[tp.ID.String()] = true
							}
							if !localTpIDs[firstHopTpID] {
								// Confirmed gone - mark as dead and skip
								deadFirstHops[firstHopTpID] = true
								delete(transportLatencies, tpID)
								return
							}
						}
					}
				}

				if pingCtx.Err() == context.DeadlineExceeded {
					// Per-transport timeout - move on
					if data.setupTimeMs == 0 && data.calcTimeMs == 0 {
						data.setupErr = "client-tmo"
					} else {
						data.pingErr = "client-tmo"
					}
				} else {
					// Determine which phase failed based on current state
					if data.setupTimeMs == 0 && data.calcTimeMs == 0 {
						// Failed during calc/setup
						if graphLocalRoute && data.calcTimeMs == 0 {
							data.calcErr = err.Error()
						} else {
							data.setupErr = err.Error()
						}
					} else {
						// Failed during ping
						data.pingErr = err.Error()
					}
				}
			}

			// If successful or parent context canceled, stop retrying
			if len(data.pingSamples) > 0 || ctx.Err() != nil {
				break
			}

			// If we have more attempts, continue
			if attempt < maxAttempts {
				continue
			}
		}

		if len(data.pingSamples) > 0 {
			data.phase = "done"

			// Update cumulative latency for this visor (use average ping)
			var pingSum float64
			for _, p := range data.pingSamples {
				pingSum += p
			}
			avgPing := pingSum / float64(len(data.pingSamples))
			if existing, ok := visorCumulativeLatency[remotePK]; !ok || avgPing < existing {
				visorCumulativeLatency[remotePK] = avgPing
			}
		} else {
			data.phase = "done"
		}

		// Re-render with results
		renderTree()
	}

	// Helper to verify transport exists by refreshing local transport list
	verifyTransportExists := func(tpID string) bool {
		currentTransports, err := rpcClient.Transports(nil, nil, false)
		if err != nil {
			return false
		}
		for _, tp := range currentTransports {
			if tp.ID.String() == tpID {
				return true
			}
		}
		return false
	}

	// BFS through the network
	currentLevel := []string{localPK}
	level := 0

	for {
		level++
		// Stop if we've exceeded max level
		if graphMaxLevel > 0 && level > graphMaxLevel {
			break
		}
		// Stop if we've passed the exact hop level (when --hops is set)
		if graphHops > 0 && uint(level) > graphHops {
			break
		}

		// Find all transports at this level (per-transport, not per-visor)
		type transportTarget struct {
			remotePK string
			parentPK string
			tpID     string
			tpType   string
		}
		var targets []transportTarget

		// For level 1, use ONLY verified local transports from the visor
		if level == 1 {
			for _, tp := range localTransports {
				remotePK := tp.Remote.String()
				if !passesFilter(remotePK) {
					continue
				}
				// Verify transport still exists before adding
				tpID := tp.ID.String()
				if !verifyTransportExists(tpID) {
					continue
				}
				targets = append(targets, transportTarget{
					remotePK: remotePK,
					parentPK: localPK,
					tpID:     tpID,
					tpType:   string(tp.Type),
				})
			}
		} else {
			// For level 2+, use adjacency map from TPD
			for _, parentPK := range currentLevel {
				neighbors, ok := adjacency[parentPK]
				if !ok {
					continue
				}
				for _, n := range neighbors {
					// Skip if we've already visited this visor at a lower level
					if prevLevel, visited := visitedLevel[n.pk]; visited && prevLevel < level {
						continue
					}
					if !passesFilter(n.pk) {
						continue
					}

					targets = append(targets, transportTarget{
						remotePK: n.pk,
						parentPK: parentPK,
						tpID:     n.tpID,
						tpType:   n.tpType,
					})
				}
			}
		}

		if len(targets) == 0 {
			break
		}

		// Ping each transport
		var nextLevelVisors []string
		seenVisors := make(map[string]bool)

		for _, target := range targets {
			if ctx.Err() != nil {
				return
			}

			// Skip if below start level or not at exact hop level (when --hops is set)
			skipPing := level < graphStartLevel || (graphHops > 0 && uint(level) != graphHops)
			if skipPing {
				if !seenVisors[target.remotePK] {
					nextLevelVisors = append(nextLevelVisors, target.remotePK)
					seenVisors[target.remotePK] = true
					visitedLevel[target.remotePK] = level
				}
				continue
			}

			// Add tree entry just before pinging (so we don't populate entries too far ahead)
			treeEntries = append(treeEntries, treeEntry{
				tpID:       target.tpID,
				remotePK:   target.remotePK,
				level:      level,
				parentPK:   target.parentPK,
				parentTpID: "",
			})

			// Get path to parent (empty for level 1, since parent is local)
			pathToParent := visorPath[target.parentPK]

			pingTransport(target.remotePK, target.parentPK, target.tpID, target.tpType, level, pathToParent)

			// Add to next level if not seen
			if !seenVisors[target.remotePK] {
				data := transportLatencies[target.tpID]
				hasFailed := data != nil && (data.calcErr != "" || data.setupErr != "" || data.pingErr != "")
				if data != nil && !hasFailed {
					nextLevelVisors = append(nextLevelVisors, target.remotePK)
					// Store path to this visor for future levels
					newPath := make([]pathHop, len(pathToParent)+1)
					copy(newPath, pathToParent)
					newPath[len(pathToParent)] = pathHop{
						tpID:   target.tpID,
						tpType: target.tpType,
						from:   target.parentPK,
						to:     target.remotePK,
					}
					visorPath[target.remotePK] = newPath
				} else if graphContinue {
					nextLevelVisors = append(nextLevelVisors, target.remotePK)
				}
				seenVisors[target.remotePK] = true
				visitedLevel[target.remotePK] = level
			}
		}

		// Sort nextLevelVisors by cumulative latency (lowest first) for optimal exploration order
		for i := 0; i < len(nextLevelVisors)-1; i++ {
			for j := i + 1; j < len(nextLevelVisors); j++ {
				latI := visorCumulativeLatency[nextLevelVisors[i]]
				latJ := visorCumulativeLatency[nextLevelVisors[j]]
				// Lower latency should come first
				if latJ < latI || (latI == 0 && latJ > 0) {
					nextLevelVisors[i], nextLevelVisors[j] = nextLevelVisors[j], nextLevelVisors[i]
				}
			}
		}

		currentLevel = nextLevelVisors
		if len(currentLevel) == 0 {
			break
		}
	}

	// Track which transport IDs we've already pinged
	pingedTpIDs := make(map[string]bool)
	for tpID := range transportLatencies {
		pingedTpIDs[tpID] = true
	}

	// Continuous monitoring loop - check for new transports after each pass
	for {
		if ctx.Err() != nil {
			break
		}

		// Re-fetch local transports to detect new ones
		newLocalTransports, err := rpcClient.Transports(nil, nil, false)
		if err != nil {
			break // Can't continue without transport info
		}

		// Update localTpIDs with current state
		localTpIDs = make(map[string]bool)
		for _, tp := range newLocalTransports {
			localTpIDs[tp.ID.String()] = true
		}

		// Find new level 1 transports that pass filtering
		var newLevel1Targets []struct {
			remotePK string
			tpID     string
			tpType   string
		}
		for _, tp := range newLocalTransports {
			tpID := tp.ID.String()
			if pingedTpIDs[tpID] {
				continue // Already pinged
			}
			remotePK := tp.Remote.String()
			if !passesFilter(remotePK) {
				continue // Doesn't pass version/online filter
			}
			newLevel1Targets = append(newLevel1Targets, struct {
				remotePK string
				tpID     string
				tpType   string
			}{
				remotePK: remotePK,
				tpID:     tpID,
				tpType:   string(tp.Type),
			})
		}

		if len(newLevel1Targets) == 0 {
			// No new transports found - scan complete
			break
		}

		// Ping new level 1 transports
		var newLevel1Visors []string
		for _, target := range newLevel1Targets {
			if ctx.Err() != nil {
				break
			}

			// Add tree entry
			treeEntries = append(treeEntries, treeEntry{
				tpID:       target.tpID,
				remotePK:   target.remotePK,
				level:      1,
				parentPK:   localPK,
				parentTpID: "",
			})

			// Ping it
			pingTransport(target.remotePK, localPK, target.tpID, target.tpType, 1, nil)
			pingedTpIDs[target.tpID] = true

			// Check if ping succeeded
			data := transportLatencies[target.tpID]
			hasFailed := data != nil && (data.calcErr != "" || data.setupErr != "" || data.pingErr != "")
			if data != nil && !hasFailed {
				newLevel1Visors = append(newLevel1Visors, target.remotePK)
				// Store path to this visor
				visorPath[target.remotePK] = []pathHop{{
					tpID:   target.tpID,
					tpType: target.tpType,
					from:   localPK,
					to:     target.remotePK,
				}}
				visitedLevel[target.remotePK] = 1
			}

			// After each level 1 ping, check for more new transports
			// This handles transports that appear while we're pinging
		}

		// Now explore level 2+ for new level 1 visors
		currentLevel := newLevel1Visors
		for level := 2; len(currentLevel) > 0; level++ {
			if ctx.Err() != nil {
				break
			}

			var nextLevelVisors []string
			seenVisors := make(map[string]bool)

			for _, parentPK := range currentLevel {
				neighbors, ok := adjacency[parentPK]
				if !ok {
					continue
				}
				for _, n := range neighbors {
					// Skip if already visited at lower level
					if prevLevel, visited := visitedLevel[n.pk]; visited && prevLevel < level {
						continue
					}
					if !passesFilter(n.pk) {
						continue
					}
					// Skip if this transport already pinged
					if pingedTpIDs[n.tpID] {
						continue
					}

					// Add tree entry
					treeEntries = append(treeEntries, treeEntry{
						tpID:       n.tpID,
						remotePK:   n.pk,
						level:      level,
						parentPK:   parentPK,
						parentTpID: "",
					})

					// Get path to parent
					pathToParent := visorPath[parentPK]

					// Ping it
					pingTransport(n.pk, parentPK, n.tpID, n.tpType, level, pathToParent)
					pingedTpIDs[n.tpID] = true

					// Check if ping succeeded and add to next level
					if !seenVisors[n.pk] {
						data := transportLatencies[n.tpID]
						hasFailed := data != nil && (data.calcErr != "" || data.setupErr != "" || data.pingErr != "")
						if data != nil && !hasFailed {
							nextLevelVisors = append(nextLevelVisors, n.pk)
							// Store path to this visor
							newPath := make([]pathHop, len(pathToParent)+1)
							copy(newPath, pathToParent)
							newPath[len(pathToParent)] = pathHop{
								tpID:   n.tpID,
								tpType: n.tpType,
								from:   parentPK,
								to:     n.pk,
							}
							visorPath[n.pk] = newPath
						}
						seenVisors[n.pk] = true
						visitedLevel[n.pk] = level
					}
				}
			}

			currentLevel = nextLevelVisors
		}

		// Loop back to check for any new level 1 transports that appeared
	}

	// Final render
	renderTree()
	fmt.Println(pterm.Green("Scan complete!"))
}
