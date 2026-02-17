// Package clivisor cmd/skywire-cli/commands/visor/ping-graph.go
package clivisor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/blang/semver/v4"
	"github.com/google/uuid"
	"github.com/pterm/pterm"
	"github.com/spf13/cobra"

	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
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
	graphVersion        string
	graphMaxLevel       int
	graphRedundancy     bool
	graphTimeout        time.Duration
	graphSetupTimeout   time.Duration
	graphTries          int
	graphPcktSize       int
	graphCacheTPD       string
	graphCacheUT        string
	graphCacheDMSG      string
	graphCacheAge       int
	graphTPDURL         string
	graphUTURL          string
	graphDMSGURL        string
	graphOnlineOnly     bool
	graphContinue       bool
	graphStartLevel     int
	graphUseDmsg        bool
	graphDmsgOnly       bool
	graphLocalRoute     bool
	graphShowRoute      bool
	graphHopLatency     bool
	graphOutput         string
	graphAllServers     bool
	graphDmsgServerPK   string
	graphTreeView       bool
	graphHops           uint
	graphRetries        int
	graphResume         bool
	graphMaxAge         time.Duration
	graphDryRun         bool
	graphUseTPS         bool
	graphContinuous     bool
	graphRecheckAge     time.Duration
	graphDmsgPreCheck   bool
	graphDmsgAllServers bool
	graphRemoveTp       bool
	graphRemoveRemoteTp bool
)

func init() {
	// Build tree example for help text using pterm
	treeExample := pterm.TreeNode{
		Text: "edge",
		Children: []pterm.TreeNode{
			{Text: "edge                                                             tpid                                 -     setup     ping  .....ms      avg"},
		},
	}
	treeStr, _ := pterm.DefaultTree.WithRoot(treeExample).Srender() //nolint:errcheck

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
  - -: separator (calc time shown here with --local-route)
  - setup: route setup time in ms
  - ping: ping latencies in ms (one per --tries)
  - avg: average ping latency in ms

  Failures: red text = ping failed, red background = setup/calc failed`

	pingGraphCmd.Flags().StringVarP(&graphVersion, "version", "v", "", "filter by minimum version (e.g., '1.3.34' matches 1.3.34, 1.3.34+dirty, 1.3.35, etc.)")
	pingGraphCmd.Flags().IntVarP(&graphMaxLevel, "max-level", "l", 0, "maximum hop level (0 = unlimited)")
	pingGraphCmd.Flags().BoolVar(&graphRedundancy, "redundancy", false, "test all transport types to same visor")
	pingGraphCmd.Flags().DurationVarP(&graphTimeout, "timeout", "o", 30*time.Second, "timeout per ping attempt")
	pingGraphCmd.Flags().IntVarP(&graphTries, "tries", "t", 1, "ping attempts per visor")
	pingGraphCmd.Flags().IntVarP(&graphPcktSize, "size", "s", 2, "packet size in KB")
	pingGraphCmd.Flags().StringVar(&graphCacheTPD, "cft", os.TempDir()+"/tpd.json", "TPD cache file location")
	pingGraphCmd.Flags().StringVar(&graphCacheUT, "cfu", os.TempDir()+"/ut.json", "UT cache file location")
	pingGraphCmd.Flags().StringVar(&graphCacheDMSG, "cfd", os.TempDir()+"/dmsg-clients.json", "DMSG clients cache file location")
	pingGraphCmd.Flags().IntVarP(&graphCacheAge, "cfa", "m", 5, "update cache files if older than n minutes")
	pingGraphCmd.Flags().StringVar(&graphTPDURL, "tpdurl", deployment.Prod.TransportDiscovery, "transport discovery URL")
	pingGraphCmd.Flags().StringVar(&graphUTURL, "uturl", deployment.Prod.UptimeTracker, "uptime tracker URL")
	pingGraphCmd.Flags().StringVar(&graphDMSGURL, "dmsgurl", deployment.Prod.DmsgDiscovery, "DMSG discovery URL")
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
	pingGraphCmd.Flags().StringVarP(&graphOutput, "output", "O", "", "output base filename (writes .json and .txt files)")
	pingGraphCmd.Flags().BoolVar(&graphTreeView, "tree", false, "display results as tree view with per-transport latencies")
	pingGraphCmd.Flags().UintVar(&graphHops, "hops", 0, "exact hop level to ping (0 = all levels, 1 = direct transports only, 2 = two hops, etc.)")
	pingGraphCmd.Flags().IntVar(&graphRetries, "retries", 1, "number of retry attempts if ping fails (tree mode only)")
	pingGraphCmd.Flags().BoolVarP(&graphResume, "resume", "R", false, "resume from output file if it exists (continues where left off)")
	pingGraphCmd.Flags().DurationVar(&graphMaxAge, "max-age", 0, "re-ping entries older than this duration (e.g., 1h, 30m); 0 = never re-ping")
	pingGraphCmd.Flags().BoolVar(&graphDryRun, "dry-run", false, "show tree structure without pinging (displays dataset size)")
	pingGraphCmd.Flags().BoolVar(&graphUseTPS, "tps", true, "verify/update transports via TPS (tree mode only)")
	pingGraphCmd.Flags().BoolVar(&graphRemoveTp, "remove-tp", false, "remove local transport if route ping fails")
	pingGraphCmd.Flags().BoolVar(&graphRemoveRemoteTp, "remove-remote-tp", false, "request remote visor to remove transport if route ping fails")
	pingCmd.AddCommand(pingGraphCmd)
}

var pingGraphCmd = &cobra.Command{
	Use:   "graph",
	Short: "Ping visors across the network by hop level",
	Run: func(cmd *cobra.Command, _ []string) {
		// Validate mutually exclusive flags
		if graphTreeView && graphUseDmsg {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("--tree with --dmsg is not supported; use --tree with --dmsg-only for DMSG-only tree mode"))
		}

		// Dry-run implies tree view
		if graphDryRun {
			graphTreeView = true
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

		// Check if local visor has transports (skip for DMSG-only mode)
		if !graphDmsgOnly {
			if _, exists := adjacency[localPK]; !exists {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("local visor has no transports in TPD"))
			}
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
		textOutput.WriteString("=== Ping Graph Results ===\n")
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
			if graphDmsgOnly {
				// DMSG tree mode: ping via DMSG servers
				runDmsgTreeViewMode(ctx, grpcClient, rpcClient, localPK, onlineSet, versionFilteredSet, passesFilter)
				return
			}
			// Route tree mode: ping via transports
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

		// Non-tree mode is deprecated - show warning and suggest using 'ping tree' command
		fmt.Println()
		fmt.Println("NOTE: The non-tree mode of 'ping graph' is deprecated.")
		fmt.Println("Consider using 'skywire-cli visor ping tree' instead for better transport-level visibility.")
		fmt.Println("Use 'ping graph --tree' or 'ping tree' for the recommended tree view mode.")
		fmt.Println()

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
				os.Stdout.Sync() //nolint:errcheck,gosec
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

		// Write output files if specified
		if graphOutput != "" {
			jsonFile := graphOutput + ".json"
			textFile := graphOutput + ".txt"

			jsonData, err := json.MarshalIndent(results, "", "  ")
			if err != nil {
				fmt.Printf("Error marshaling JSON: %v\n", err)
			} else {
				if err := os.WriteFile(jsonFile, jsonData, 0644); err != nil { //nolint:gosec
					fmt.Printf("Error writing JSON file: %v\n", err)
				} else {
					fmt.Printf("Results written to: %s\n", jsonFile)
				}
			}

			if err := os.WriteFile(textFile, []byte(textOutput.String()), 0644); err != nil { //nolint:gosec
				fmt.Printf("Error writing text file: %v\n", err)
			} else {
				fmt.Printf("Results written to: %s\n", textFile)
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

// dmsgServerPingData tracks DMSG ping results via a specific server
type dmsgServerPingData struct {
	serverPK    string    // DMSG server public key
	pingSamples []float64 // Ping samples in ms
	pingErr     string    // Error if ping failed
	phase       string    // "pending", "ping", "done"
	timestamp   time.Time // When the test completed
}

// transportLatencyData tracks timing for each phase of a transport ping
type transportLatencyData struct {
	tpID      string
	tpType    string
	from      string
	to        string
	gatewayPK string // First-hop visor for level 2+ entries
	level     int
	// Phase timings (ms) - 0 means not yet measured
	calcTimeMs  float64
	setupTimeMs float64
	pingSamples []float64 // All ping samples in ms
	// Phase errors - empty means success or not yet attempted
	calcErr  string
	setupErr string
	pingErr  string
	// Overall status
	phase       string    // "pending", "calc", "setup", "ping", "done", "skipped"
	timestamp   time.Time // When the test completed
	lastSuccess time.Time // Preserved on failure
	stale       bool      // Needs re-checking
	// DMSG pre-check results (children in tree view)
	dmsgServers    []*dmsgServerPingData // DMSG ping results per server
	dmsgReachable  bool                  // True if reachable via any DMSG server
	dmsgSkipReason string                // Reason for skipping route ping (if dmsg unreachable)
}

// routeSavedState represents the saved state for route tree view resume functionality
type routeSavedState struct {
	LocalPK    string                 `json:"local_pk"`
	StartTime  string                 `json:"start_time"`
	UpdateTime string                 `json:"update_time"`
	Entries    []routeSavedEntry      `json:"entries"`
	Settings   map[string]interface{} `json:"settings"`
}

// dmsgServerSavedEntry represents DMSG ping results via a specific server
type dmsgServerSavedEntry struct {
	ServerPK    string    `json:"server_pk"`
	PingSamples []float64 `json:"ping_samples,omitempty"`
	AvgLatency  float64   `json:"avg_latency_ms,omitempty"`
	PingErr     string    `json:"ping_err,omitempty"`
	Timestamp   string    `json:"timestamp,omitempty"`
}

// routeSavedEntry represents a single transport ping entry in saved state
type routeSavedEntry struct {
	TpID           string                 `json:"tp_id"`
	TpType         string                 `json:"tp_type"`
	RemotePK       string                 `json:"remote_pk"`
	ParentPK       string                 `json:"parent_pk"`
	GatewayPK      string                 `json:"gateway_pk,omitempty"` // First-hop visor for level 2+ (level 1 gateway)
	Level          int                    `json:"level"`
	CalcTimeMs     float64                `json:"calc_time_ms,omitempty"`
	SetupTimeMs    float64                `json:"setup_time_ms,omitempty"`
	PingSamples    []float64              `json:"ping_samples,omitempty"`
	AvgLatency     float64                `json:"avg_latency_ms,omitempty"`
	CalcErr        string                 `json:"calc_err,omitempty"`
	SetupErr       string                 `json:"setup_err,omitempty"`
	PingErr        string                 `json:"ping_err,omitempty"`
	Timestamp      string                 `json:"timestamp,omitempty"`
	LastSuccess    string                 `json:"last_success,omitempty"` // Preserved on failure
	Phase          string                 `json:"phase"`
	Stale          bool                   `json:"stale,omitempty"`        // Needs re-checking
	DmsgServers    []dmsgServerSavedEntry `json:"dmsg_servers,omitempty"` // DMSG ping results per server
	DmsgReachable  bool                   `json:"dmsg_reachable,omitempty"`
	DmsgSkipReason string                 `json:"dmsg_skip_reason,omitempty"`
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
	// Mutex for thread-safe access to shared data (used when DMSG and route pings run concurrently)
	var dataMu sync.Mutex

	// Track last TPD refresh time and valid transport IDs from TPD
	lastTPDRefresh := time.Now()
	const tpdRefreshInterval = 5 * time.Minute
	tpdValidTpIDs := make(map[string]bool)

	// Helper to refresh TPD data and update valid transport IDs
	refreshTPDCache := func() {
		fmt.Printf("Refreshing TPD cache...\n")
		tpdRaw := internal.GetData(graphCacheTPD, graphTPDURL+"/all-transports", graphCacheAge)
		var transports []transportEntry
		if err := json.Unmarshal([]byte(tpdRaw), &transports); err != nil {
			fmt.Printf("Warning: failed to parse TPD data: %v\n", err)
			return
		}
		// Rebuild valid transport ID set
		newValidTpIDs := make(map[string]bool)
		for _, tp := range transports {
			newValidTpIDs[tp.ID] = true
		}
		tpdValidTpIDs = newValidTpIDs
		// Also rebuild adjacency map
		newAdjacency := make(map[string][]treeNeighbor)
		for _, tp := range transports {
			edge0, edge1 := tp.Edges[0], tp.Edges[1]
			newAdjacency[edge0] = append(newAdjacency[edge0], treeNeighbor{pk: edge1, tpID: tp.ID, tpType: tp.Type})
			if edge0 != edge1 {
				newAdjacency[edge1] = append(newAdjacency[edge1], treeNeighbor{pk: edge0, tpID: tp.ID, tpType: tp.Type})
			}
		}
		// Update adjacency map in place
		for k := range adjacency {
			delete(adjacency, k)
		}
		for k, v := range newAdjacency {
			adjacency[k] = v
		}
		lastTPDRefresh = time.Now()
		fmt.Printf("TPD cache refreshed: %d transports\n", len(transports))
	}

	// Initial TPD cache population
	refreshTPDCache()

	// DMSG pre-check data: map[clientPK][]serverPK
	// Built from dmsg-discovery to know which servers each visor is connected to
	visorDmsgServers := make(map[string][]string)
	var dmsgClientsLoaded bool

	// Helper to load/refresh DMSG clients data
	loadDmsgClients := func() {
		if !graphDmsgPreCheck {
			fmt.Fprintf(os.Stderr, "DEBUG: graphDmsgPreCheck is false, skipping DMSG clients load\n")
			return
		}
		dmsgURL := graphDMSGURL + "/dmsg-discovery/servers/clients"
		fmt.Fprintf(os.Stderr, "Fetching DMSG clients from: %s\n", dmsgURL)
		dmsgClientsRaw := internal.GetData(graphCacheDMSG, dmsgURL, graphCacheAge)
		var clientsByServer map[string][]string
		if err := json.Unmarshal([]byte(dmsgClientsRaw), &clientsByServer); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to parse DMSG clients data: %v\n", err)
			return
		}
		// Invert the map: clientPK -> []serverPK
		newVisorServers := make(map[string][]string)
		for serverPK, clients := range clientsByServer {
			for _, clientPK := range clients {
				newVisorServers[clientPK] = append(newVisorServers[clientPK], serverPK)
			}
		}
		visorDmsgServers = newVisorServers
		dmsgClientsLoaded = true
		totalClients := len(visorDmsgServers)
		fmt.Fprintf(os.Stderr, "Loaded DMSG servers for %d clients\n", totalClients)
	}

	// Initial DMSG clients load
	if graphDmsgPreCheck {
		loadDmsgClients()
	}

	// Helper to get DMSG servers for a visor
	getVisorDmsgServers := func(visorPK string) []string {
		if !dmsgClientsLoaded {
			fmt.Fprintf(os.Stderr, "DEBUG: DMSG clients not loaded, skipping DMSG pre-check for %s\n", visorPK[:16])
			return nil
		}
		servers := visorDmsgServers[visorPK]
		if len(servers) == 0 {
			fmt.Fprintf(os.Stderr, "DEBUG: No DMSG servers found for visor %s\n", visorPK[:16])
		}
		return servers
	}

	// Cache for remote visor transports queried via TPS
	// Key: visor PK, Value: map of transport IDs that visor has
	remoteVisorTransports := make(map[string]map[string]bool)
	remoteVisorTransportsFetchTime := make(map[string]time.Time)
	const remoteTransportCacheAge = 2 * time.Minute // Refresh remote visor data every 2 minutes

	// Helper to get a remote visor's transports via TPS
	// Returns nil if TPS is disabled or query fails (falls back to TPD cache)
	getRemoteVisorTransports := func(visorPK string) map[string]bool {
		// Skip TPS queries if disabled
		if !graphUseTPS {
			return nil
		}

		// Check cache freshness
		if cached, ok := remoteVisorTransports[visorPK]; ok {
			if time.Since(remoteVisorTransportsFetchTime[visorPK]) < remoteTransportCacheAge {
				return cached
			}
		}

		// Query via TPS
		var pk cipher.PubKey
		if err := pk.Set(visorPK); err != nil {
			return nil
		}

		tpsTransports, err := rpcClient.TPSGetTransports(pk)
		if err != nil {
			// TPS query failed - return nil (will fall back to TPD cache)
			return nil
		}

		// Build set of transport IDs
		tpIDs := make(map[string]bool)
		for _, tp := range tpsTransports {
			tpIDs[tp.ID.String()] = true
		}
		remoteVisorTransports[visorPK] = tpIDs
		remoteVisorTransportsFetchTime[visorPK] = time.Now()
		return tpIDs
	}

	// Helper to update adjacency map with fresh TPS data for a visor
	// This adds any transports from TPS that aren't in TPD (fresher data)
	updateAdjacencyFromTPS := func(visorPK string) {
		if !graphUseTPS {
			return
		}

		var pk cipher.PubKey
		if err := pk.Set(visorPK); err != nil {
			return
		}

		tpsTransports, err := rpcClient.TPSGetTransports(pk)
		if err != nil {
			return
		}

		// Add any new transports to adjacency map
		for _, tp := range tpsTransports {
			tpID := tp.ID.String()
			remotePK := tp.Remote.String()

			// Skip if transport is already known
			if tpdValidTpIDs[tpID] {
				continue
			}

			// Add to adjacency map (bidirectional)
			tpType := tp.Type
			adjacency[visorPK] = append(adjacency[visorPK], treeNeighbor{
				pk:     remotePK,
				tpID:   tpID,
				tpType: tpType,
			})
			adjacency[remotePK] = append(adjacency[remotePK], treeNeighbor{
				pk:     visorPK,
				tpID:   tpID,
				tpType: tpType,
			})

			// Mark as valid
			tpdValidTpIDs[tpID] = true
		}
	}

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

	// Helper to remove a local transport (caller decides when to call)
	removeLocalTransport := func(tpID string) error {
		tpUUID, err := uuid.Parse(tpID)
		if err != nil {
			return fmt.Errorf("invalid transport ID: %w", err)
		}
		if err := rpcClient.RemoveTransport(tpUUID); err != nil {
			return fmt.Errorf("failed to remove local transport: %w", err)
		}
		// Remove from local tracking
		delete(localTpIDs, tpID)
		fmt.Fprintf(os.Stderr, "Removed local transport %s\n", tpID[:16])
		return nil
	}

	// Helper to request remote visor to remove a transport via TPS (caller decides when to call)
	removeRemoteTransport := func(remotePK string, tpID string) error {
		var pk cipher.PubKey
		if err := pk.Set(remotePK); err != nil {
			return fmt.Errorf("invalid remote PK: %w", err)
		}
		tpUUID, err := uuid.Parse(tpID)
		if err != nil {
			return fmt.Errorf("invalid transport ID: %w", err)
		}

		// Use a timeout to prevent hanging
		done := make(chan error, 1)
		go func() {
			done <- rpcClient.TPSRemoveTransport(pk, tpUUID)
		}()

		select {
		case err := <-done:
			if err != nil {
				return fmt.Errorf("failed to remove remote transport: %w", err)
			}
		case <-time.After(15 * time.Second):
			return fmt.Errorf("timeout removing remote transport")
		}

		fmt.Fprintf(os.Stderr, "Requested removal of transport %s from remote visor %s\n", tpID[:16], remotePK[:16])
		return nil
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

	// pathHasLoop checks if adding a new destination would create a loop in the path
	pathHasLoop := func(path []pathHop, newDest string) bool {
		// Check if newDest is the local visor
		if newDest == localPK {
			return true
		}
		// Check if newDest appears in any hop of the path
		for _, hop := range path {
			if hop.to == newDest || hop.from == newDest {
				return true
			}
		}
		return false
	}

	// routeIsValid checks if all non-first-hop transports in a route still exist
	// First hop is verified separately via local transport list
	// For intermediate hops, query the visor via TPS for fresher data, fall back to TPD cache
	routeIsValid := func(path []pathHop) bool {
		// Skip first hop (index 0) - that's verified locally
		for i := 1; i < len(path); i++ {
			hop := path[i]
			// Query the visor at hop.from for its transports
			visorTransports := getRemoteVisorTransports(hop.from)
			if visorTransports != nil {
				// TPS query succeeded - use fresh data
				if !visorTransports[hop.tpID] {
					return false
				}
			} else {
				// TPS query failed - fall back to TPD cache
				if !tpdValidTpIDs[hop.tpID] {
					return false
				}
			}
		}
		return true
	}

	// Track first-hop transports that no longer exist - skip entire subtrees using these
	deadFirstHops := make(map[string]bool)

	// Track consecutive failures per first-hop transport
	// After N consecutive failures through a first-hop, mark it as dead
	const maxFirstHopFailures = 3
	firstHopFailures := make(map[string]int)

	// Build ordered list for tree display
	// Each entry is a transport (not a visor), allowing same visor with different transports
	type treeEntry struct {
		tpID       string
		remotePK   string
		level      int
		parentPK   string
		parentTpID string
		failed     bool   // True if ping failed
		removed    bool   // True if transport was removed due to failure
		removeErr  string // Error message if removal failed
	}
	var treeEntries []treeEntry
	var treeEntriesMu sync.Mutex

	// Helper to process a failed transport - mark as failed and optionally remove
	processFailedTransport := func(tpID string, remotePK string, parentPK string, level int, isLevel1 bool) {
		treeEntriesMu.Lock()
		defer treeEntriesMu.Unlock()

		// Find entry in treeEntries and mark as failed
		entryIdx := -1
		for i := range treeEntries {
			if treeEntries[i].tpID == tpID {
				entryIdx = i
				break
			}
		}

		// If entry not found, add it (shouldn't happen normally)
		if entryIdx < 0 {
			treeEntries = append(treeEntries, treeEntry{
				tpID:     tpID,
				remotePK: remotePK,
				parentPK: parentPK,
				level:    level,
				failed:   true,
			})
			entryIdx = len(treeEntries) - 1
		} else {
			treeEntries[entryIdx].failed = true
		}

		// Try to remove transports if flags are set
		// When --remove-tp is set for level 1, remove from both local and remote
		if isLevel1 && graphRemoveTp {
			localErr := removeLocalTransport(tpID)
			remoteErr := removeRemoteTransport(remotePK, tpID)

			if localErr != nil || remoteErr != nil {
				var errParts []string
				if localErr != nil {
					errParts = append(errParts, "local: "+localErr.Error())
				}
				if remoteErr != nil {
					errParts = append(errParts, "remote: "+remoteErr.Error())
				}
				treeEntries[entryIdx].removeErr = strings.Join(errParts, "; ")
			}
			// Mark as removed if at least one succeeded
			if localErr == nil || remoteErr == nil {
				treeEntries[entryIdx].removed = true
			}
		} else if graphRemoveRemoteTp {
			// Only remove remote (for non-level-1 or when only --remove-remote-tp is set)
			if err := removeRemoteTransport(remotePK, tpID); err != nil {
				treeEntries[entryIdx].removeErr = err.Error()
			} else {
				treeEntries[entryIdx].removed = true
			}
		}
	}

	// Track which transport IDs have been pinged (for resume)
	pingedTpIDs := make(map[string]bool)

	// Start time for this scan
	startTime := time.Now()

	// Load saved state if resuming
	if graphResume && graphOutput != "" {
		resumeFile := graphOutput + ".json"
		savedData, err := os.ReadFile(resumeFile) //nolint:gosec // G304: file path is from user-provided flag
		if err == nil {
			var savedState routeSavedState
			if err := json.Unmarshal(savedData, &savedState); err == nil {
				fmt.Printf("Resuming from: %s\n", resumeFile)
				fmt.Printf("  Started: %s, Last update: %s\n", savedState.StartTime, savedState.UpdateTime)
				fmt.Printf("  Loaded %d entries\n", len(savedState.Entries))

				// Parse original start time
				if ts, err := time.Parse(time.RFC3339, savedState.StartTime); err == nil {
					startTime = ts
				}

				// Restore entries
				var staleCount int
				for _, entry := range savedState.Entries {
					// Parse timestamp
					var entryTime time.Time
					if entry.Timestamp != "" {
						if ts, err := time.Parse(time.RFC3339, entry.Timestamp); err == nil {
							entryTime = ts
						}
					}

					// Check if entry is stale (older than max-age)
					isStale := false
					if graphMaxAge > 0 && entry.Phase == "done" && !entryTime.IsZero() {
						if time.Since(entryTime) > graphMaxAge {
							isStale = true
							staleCount++
						}
					}

					// Mark transport as already pinged if done and not stale
					if entry.Phase == "done" && !isStale {
						pingedTpIDs[entry.TpID] = true
					}

					// Add to tree entries
					treeEntries = append(treeEntries, treeEntry{
						tpID:     entry.TpID,
						remotePK: entry.RemotePK,
						level:    entry.Level,
						parentPK: entry.ParentPK,
					})

					// Parse lastSuccess timestamp
					var lastSuccessTime time.Time
					if entry.LastSuccess != "" {
						if ts, err := time.Parse(time.RFC3339, entry.LastSuccess); err == nil {
							lastSuccessTime = ts
						}
					}

					// Restore latency data (but reset if stale)
					if isStale {
						// Reset stale entry - it will be re-pinged, but preserve lastSuccess
						// Also preserve dmsgReachable/dmsgSkipReason for reference
						transportLatencies[entry.TpID] = &transportLatencyData{
							tpID:           entry.TpID,
							tpType:         entry.TpType,
							from:           entry.ParentPK,
							to:             entry.RemotePK,
							gatewayPK:      entry.GatewayPK,
							level:          entry.Level,
							phase:          "pending",
							lastSuccess:    lastSuccessTime,
							stale:          true,
							dmsgReachable:  entry.DmsgReachable,
							dmsgSkipReason: entry.DmsgSkipReason,
						}
					} else {
						data := &transportLatencyData{
							tpID:           entry.TpID,
							tpType:         entry.TpType,
							from:           entry.ParentPK,
							to:             entry.RemotePK,
							gatewayPK:      entry.GatewayPK,
							level:          entry.Level,
							calcTimeMs:     entry.CalcTimeMs,
							setupTimeMs:    entry.SetupTimeMs,
							pingSamples:    entry.PingSamples,
							calcErr:        entry.CalcErr,
							setupErr:       entry.SetupErr,
							pingErr:        entry.PingErr,
							phase:          entry.Phase,
							timestamp:      entryTime,
							lastSuccess:    lastSuccessTime,
							stale:          entry.Stale,
							dmsgReachable:  entry.DmsgReachable,
							dmsgSkipReason: entry.DmsgSkipReason,
						}
						// Restore DMSG server data
						if len(entry.DmsgServers) > 0 {
							for _, savedServer := range entry.DmsgServers {
								var serverTime time.Time
								if savedServer.Timestamp != "" {
									if ts, err := time.Parse(time.RFC3339, savedServer.Timestamp); err == nil {
										serverTime = ts
									}
								}
								serverData := &dmsgServerPingData{
									serverPK:    savedServer.ServerPK,
									pingSamples: savedServer.PingSamples,
									pingErr:     savedServer.PingErr,
									phase:       "done",
									timestamp:   serverTime,
								}
								data.dmsgServers = append(data.dmsgServers, serverData)
							}
						}
						transportLatencies[entry.TpID] = data
					}

					// Restore visited level
					if entry.Phase == "done" && !isStale {
						visitedLevel[entry.RemotePK] = entry.Level

						// Restore cumulative latency
						if entry.AvgLatency > 0 {
							if existing, ok := visorCumulativeLatency[entry.RemotePK]; !ok || entry.AvgLatency < existing {
								visorCumulativeLatency[entry.RemotePK] = entry.AvgLatency
							}
						}
					}
				}
				if staleCount > 0 {
					fmt.Printf("  Found %d stale entries (older than %v) to re-ping\n", staleCount, graphMaxAge)
				}
			}
		}
		// If file doesn't exist, just start fresh (no error message needed)
	}

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

		// Timestamp (grayed, at the end)
		var tsStr string
		if data.phase == "done" && !data.timestamp.IsZero() {
			tsStr = pterm.Gray(fmt.Sprintf(" %s", data.timestamp.Format("2006-01-02 15:04:05")))
		}

		// Removal status (for failed entries)
		var removeStr string
		if entry.removed {
			removeStr = pterm.Green(" [REMOVED]")
		} else if entry.removeErr != "" {
			removeStr = pterm.Yellow(" [rm err: " + truncateErr(entry.removeErr, 20) + "]")
		}

		// Build the line: remotePK tpID calc setup pings... avg timestamp [removal_status]
		tpIDFormatted := formatTpID(data.tpID, data.tpType)

		// Format the entire line based on failure status
		var text string
		if earlyFailure {
			// Red background with white text for early failures
			line := fmt.Sprintf("%s %s %s %s %s %s",
				entry.remotePK, data.tpID, calcStr, setupStr, pingsStr, totalStr)
			text = pterm.BgRed.Sprint(pterm.White(line)) + tsStr + removeStr
		} else if pingFailure {
			// Red text for ping failures
			text = pterm.Red(fmt.Sprintf("%s %s %s %s %s %s",
				entry.remotePK, data.tpID, calcStr, setupStr, pingsStr, totalStr)) + tsStr + removeStr
		} else {
			// Normal formatting with colored tpID
			text = fmt.Sprintf("%s %s %s %s %s %s",
				entry.remotePK, tpIDFormatted, calcStr, setupStr, pingsStr, totalStr) + tsStr + removeStr
		}

		return text
	}

	// Helper to format a DMSG server ping entry as tree child
	formatDmsgServerEntry := func(serverData *dmsgServerPingData) string {
		if serverData == nil {
			return pterm.Gray("...")
		}

		// Format aligned with transport entries:
		// serverPK(64) | (dmsg)(36) | calc(-) | setup(-) | pings(8 each) | avg(9)
		var pingsStr, avgStr string

		if serverData.pingErr != "" {
			pingsStr = truncateErr(serverData.pingErr, 12)
			avgStr = fmt.Sprintf("%9s", "-")
		} else if len(serverData.pingSamples) > 0 {
			// Format ping samples
			var pingParts []string
			var pingSum float64
			for _, p := range serverData.pingSamples {
				pingParts = append(pingParts, fmt.Sprintf("%8s", fmt.Sprintf("%.1fms", p)))
				pingSum += p
			}
			pingsStr = strings.Join(pingParts, " ")
			avgPing := pingSum / float64(len(serverData.pingSamples))
			avgStr = fmt.Sprintf("%9s", fmt.Sprintf("%.1fms", avgPing))
		} else if serverData.phase != "done" {
			pingsStr = fmt.Sprintf("%8s", "...")
			avgStr = fmt.Sprintf("%9s", "...")
		} else {
			pingsStr = fmt.Sprintf("%8s", "-")
			avgStr = fmt.Sprintf("%9s", "-")
		}

		// Build line aligned with transport entries:
		// serverPK (64 chars) | "(dmsg)" padded to 34 chars (2 less to compensate for tree indent) | "-" for calc | "-" padded to 9 for setup | pings | avg | timestamp
		dmsgLabel := fmt.Sprintf("%-34s", "(dmsg)")
		calcStr := "-"
		setupStr := fmt.Sprintf("%9s", "-")

		// Timestamp (grayed, at the end) - same format as transport entries
		var tsStr string
		if serverData.phase == "done" && !serverData.timestamp.IsZero() {
			tsStr = pterm.Gray(fmt.Sprintf(" %s", serverData.timestamp.Format("2006-01-02 15:04:05")))
		}

		// Use magenta color for DMSG servers to distinguish from transports
		var text string
		if serverData.pingErr != "" {
			// Red text for failed DMSG pings
			text = pterm.Red(fmt.Sprintf("%s %s %s %s %s %s",
				serverData.serverPK, dmsgLabel, calcStr, setupStr, pingsStr, avgStr)) + tsStr
		} else {
			// Magenta server PK, gray labels
			text = fmt.Sprintf("%s %s %s %s %s %s",
				pterm.Magenta(serverData.serverPK), pterm.Gray(dmsgLabel), pterm.Gray(calcStr), pterm.Gray(setupStr), pingsStr, avgStr) + tsStr
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
		treeEntriesMu.Lock()
		localEntries := make([]treeEntry, len(treeEntries))
		copy(localEntries, treeEntries)
		treeEntriesMu.Unlock()

		bestLatency := float64(-1)
		for _, entry := range localEntries {
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

		// Make a copy of treeEntries to avoid race with concurrent writers
		treeEntriesMu.Lock()
		localTreeEntries := make([]treeEntry, len(treeEntries))
		copy(localTreeEntries, treeEntries)
		treeEntriesMu.Unlock()

		// Build label row with right-justified column headers that align with data
		// Column widths match formatEntry: remotePK(64) tpID(36) calc(1) setup(9) pings(8 each) avg(9)
		// Labels are right-justified so last char aligns with last char of data values
		var labelParts []string
		labelParts = append(labelParts, fmt.Sprintf("%-64s", pterm.Gray("edge")))  // remotePK: left-aligned
		labelParts = append(labelParts, fmt.Sprintf("%-36s", pterm.Green("tpid"))) // tpID: left-aligned
		labelParts = append(labelParts, pterm.Gray("-"))                           // calc: just hyphen
		labelParts = append(labelParts, fmt.Sprintf("%9s", pterm.Gray("setup")))   // setup: right-aligned, 'p' aligns with 's'
		// Ping columns: "ping" in first slot, ".....ms" in rest - each 8 chars right-aligned
		// 'g' in 'ping' aligns with 's' in values like '1.6ms'
		labelParts = append(labelParts, fmt.Sprintf("%8s", pterm.Gray("ping")))
		for i := 1; i < graphTries; i++ {
			labelParts = append(labelParts, fmt.Sprintf("%8s", pterm.Gray(".....ms")))
		}
		// Avg column: 'g' in 'avg' aligns with 's' in values like '1.5ms'
		labelParts = append(labelParts, fmt.Sprintf("%9s", pterm.Gray("avg")))

		labelRow := strings.Join(labelParts, " ")

		// Print label tree header
		labelTree := pterm.TreeNode{
			Text: pterm.Gray("edge"),
			Children: []pterm.TreeNode{
				{Text: labelRow},
			},
		}
		pterm.DefaultTree.WithRoot(labelTree).Render() //nolint:errcheck,gosec
		fmt.Println()

		// Group entries by level
		entriesByLevel := make(map[int][]treeEntry)
		for _, entry := range localTreeEntries {
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
		// Split into success and failure trees
		if level1Entries, ok := entriesByLevel[1]; ok && len(level1Entries) > 0 {
			fmt.Println(pterm.Yellow("=== Level 1 (direct transports) ==="))

			// Split entries into success and failed
			var successEntries, failedEntries []treeEntry
			for _, entry := range level1Entries {
				if entry.failed {
					failedEntries = append(failedEntries, entry)
				} else {
					successEntries = append(successEntries, entry)
				}
			}

			// Sort success entries by latency (lowest first)
			for i := 0; i < len(successEntries)-1; i++ {
				for j := i + 1; j < len(successEntries); j++ {
					latI := getTransportAvgLatency(successEntries[i].tpID)
					latJ := getTransportAvgLatency(successEntries[j].tpID)
					swap := false
					if latI < 0 && latJ >= 0 {
						swap = true
					} else if latI >= 0 && latJ >= 0 && latJ < latI {
						swap = true
					}
					if swap {
						successEntries[i], successEntries[j] = successEntries[j], successEntries[i]
					}
				}
			}

			// Render success tree
			if len(successEntries) > 0 {
				var children []pterm.TreeNode
				for _, entry := range successEntries {
					transportNode := pterm.TreeNode{Text: formatEntry(entry)}
					if graphDmsgPreCheck {
						data := transportLatencies[entry.tpID]
						if data != nil && len(data.dmsgServers) > 0 {
							for _, serverData := range data.dmsgServers {
								transportNode.Children = append(transportNode.Children, pterm.TreeNode{Text: formatDmsgServerEntry(serverData)})
							}
						}
					}
					children = append(children, transportNode)
				}
				rootTree := pterm.TreeNode{
					Text:     pterm.Cyan(localPK) + pterm.Gray(" (local)"),
					Children: children,
				}
				pterm.DefaultTree.WithRoot(rootTree).Render() //nolint:errcheck,gosec
			}

			// Render failure tree immediately after success tree
			if len(failedEntries) > 0 {
				var children []pterm.TreeNode
				for _, entry := range failedEntries {
					transportNode := pterm.TreeNode{Text: formatEntry(entry)}
					if graphDmsgPreCheck {
						data := transportLatencies[entry.tpID]
						if data != nil && len(data.dmsgServers) > 0 {
							for _, serverData := range data.dmsgServers {
								transportNode.Children = append(transportNode.Children, pterm.TreeNode{Text: formatDmsgServerEntry(serverData)})
							}
						}
					}
					children = append(children, transportNode)
				}
				failedTree := pterm.TreeNode{
					Text:     pterm.Cyan(localPK) + pterm.Red(" (failures)"),
					Children: children,
				}
				pterm.DefaultTree.WithRoot(failedTree).Render() //nolint:errcheck,gosec
			}
		}

		// Render level 2+ trees: separate tree per parent, ordered by parent's lowest latency
		// Success tree followed by failure tree for each parent
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
						swap = true
					} else if parents[i].latency >= 0 && parents[j].latency >= 0 {
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

				// Split entries into success and failed
				var successEntries, failedLevelEntries []treeEntry
				for _, entry := range entries {
					if entry.failed {
						failedLevelEntries = append(failedLevelEntries, entry)
					} else {
						successEntries = append(successEntries, entry)
					}
				}

				// Sort success entries by latency
				for i := 0; i < len(successEntries)-1; i++ {
					for j := i + 1; j < len(successEntries); j++ {
						latI := getTransportAvgLatency(successEntries[i].tpID)
						latJ := getTransportAvgLatency(successEntries[j].tpID)
						swap := false
						if latI < 0 && latJ >= 0 {
							swap = true
						} else if latI >= 0 && latJ >= 0 && latJ < latI {
							swap = true
						}
						if swap {
							successEntries[i], successEntries[j] = successEntries[j], successEntries[i]
						}
					}
				}

				// Helper to build parent text
				buildParentText := func(isFailed bool) string {
					latStr := ""
					if parent.latency >= 0 {
						latStr = pterm.Gray(fmt.Sprintf(" (%.1fms)", parent.latency))
					}
					firstHopStr := ""
					if path, ok := visorPath[parent.pk]; ok && len(path) > 0 {
						firstHopTpID := path[0].tpID
						tpType := path[0].tpType
						if tpType == "stcpr" {
							firstHopStr = " " + pterm.Green(firstHopTpID)
						} else if tpType == "sudph" {
							firstHopStr = " " + pterm.Blue(firstHopTpID)
						} else {
							firstHopStr = " " + pterm.Gray(firstHopTpID)
						}
					}
					suffix := ""
					if isFailed {
						suffix = pterm.Red(" (failures)")
					}
					return pterm.Cyan(parent.pk) + firstHopStr + latStr + suffix
				}

				// Render success tree
				if len(successEntries) > 0 {
					var children []pterm.TreeNode
					for _, entry := range successEntries {
						transportNode := pterm.TreeNode{Text: formatEntry(entry)}
						if graphDmsgPreCheck {
							data := transportLatencies[entry.tpID]
							if data != nil && len(data.dmsgServers) > 0 {
								for _, serverData := range data.dmsgServers {
									transportNode.Children = append(transportNode.Children, pterm.TreeNode{Text: formatDmsgServerEntry(serverData)})
								}
							}
						}
						children = append(children, transportNode)
					}
					parentTree := pterm.TreeNode{
						Text:     buildParentText(false),
						Children: children,
					}
					pterm.DefaultTree.WithRoot(parentTree).Render() //nolint:errcheck,gosec
				}

				// Render failure tree immediately after success tree
				if len(failedLevelEntries) > 0 {
					var children []pterm.TreeNode
					for _, entry := range failedLevelEntries {
						transportNode := pterm.TreeNode{Text: formatEntry(entry)}
						if graphDmsgPreCheck {
							data := transportLatencies[entry.tpID]
							if data != nil && len(data.dmsgServers) > 0 {
								for _, serverData := range data.dmsgServers {
									transportNode.Children = append(transportNode.Children, pterm.TreeNode{Text: formatDmsgServerEntry(serverData)})
								}
							}
						}
						children = append(children, transportNode)
					}
					failedTree := pterm.TreeNode{
						Text:     buildParentText(true),
						Children: children,
					}
					pterm.DefaultTree.WithRoot(failedTree).Render() //nolint:errcheck,gosec
				}
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
		dataMu.Lock()
		isDead := firstHopTpID != "" && deadFirstHops[firstHopTpID]
		dataMu.Unlock()
		if isDead {
			return
		}

		// Check for existing data to preserve (for re-pings)
		var previousGoodData *transportLatencyData
		dataMu.Lock()
		if existingData, exists := transportLatencies[tpID]; exists && len(existingData.pingSamples) > 0 {
			// Store copy of existing good data in case new ping fails
			previousGoodData = &transportLatencyData{
				tpID:        existingData.tpID,
				tpType:      existingData.tpType,
				from:        existingData.from,
				to:          existingData.to,
				gatewayPK:   existingData.gatewayPK,
				level:       existingData.level,
				calcTimeMs:  existingData.calcTimeMs,
				setupTimeMs: existingData.setupTimeMs,
				pingSamples: append([]float64{}, existingData.pingSamples...), // Copy slice
				phase:       existingData.phase,
				timestamp:   existingData.timestamp,
				lastSuccess: existingData.timestamp, // Use timestamp as lastSuccess
			}
		}
		dataMu.Unlock()

		data := &transportLatencyData{
			tpID:   tpID,
			tpType: tpType,
			from:   parentPK,
			to:     remotePK,
			level:  level,
			phase:  "pending",
		}
		// Preserve lastSuccess from previous good data
		if previousGoodData != nil {
			data.lastSuccess = previousGoodData.lastSuccess
		}
		dataMu.Lock()
		transportLatencies[tpID] = data
		dataMu.Unlock()

		// Thread-safe render helper
		safeRenderTree := func() {
			dataMu.Lock()
			renderTree()
			dataMu.Unlock()
		}

		// Render initial state
		safeRenderTree()

		// WaitGroup for concurrent DMSG and route pings
		var pingWg sync.WaitGroup

		// Get DMSG servers for pre-check (needed by both DMSG and route ping goroutines)
		var dmsgServers []string
		if graphDmsgPreCheck {
			dmsgServers = getVisorDmsgServers(remotePK)
			fmt.Fprintf(os.Stderr, "DEBUG: DMSG pre-check for %s, got %d servers\n", remotePK[:16], len(dmsgServers))
		}

		// DMSG pings goroutine (sequential through servers, concurrent with route ping)
		if graphDmsgPreCheck && len(dmsgServers) > 0 {
			servers := dmsgServers // Capture for goroutine
			if len(servers) > 0 {
				pingWg.Add(1)
				go func() {
					defer pingWg.Done()

					dataMu.Lock()
					data.phase = "dmsg"
					dataMu.Unlock()
					safeRenderTree()

					dmsgSuccess := false
					for _, serverPK := range servers {
						if ctx.Err() != nil {
							break
						}

						serverData := &dmsgServerPingData{
							serverPK: serverPK,
							phase:    "ping",
						}
						dataMu.Lock()
						data.dmsgServers = append(data.dmsgServers, serverData)
						dataMu.Unlock()
						safeRenderTree()

						// Perform DMSG ping via this server
						dmsgPingCtx, dmsgCancel := context.WithTimeout(ctx, graphTimeout+30*time.Second)

						// Callback to collect ping samples
						dmsgCallback := func(_ int32, lat time.Duration, isSetup bool, _ []rpcgrpc.RouteHopDetail, _ string, _ time.Duration, pingErr error) {
							if isSetup {
								return // Skip setup notification for DMSG
							}
							if pingErr != nil {
								dataMu.Lock()
								if serverData.pingErr == "" {
									serverData.pingErr = pingErr.Error()
								}
								dataMu.Unlock()
								return
							}
							latMs := 1000 * lat.Seconds()
							dataMu.Lock()
							serverData.pingSamples = append(serverData.pingSamples, latMs)
							dataMu.Unlock()
							safeRenderTree()
						}

						dmsgErr := grpcClient.StreamDmsgPing(dmsgPingCtx, remotePK, int32(graphTries), int32(graphPcktSize), graphTimeout, serverPK, dmsgCallback) //nolint:gosec
						dmsgCancel()

						dataMu.Lock()
						serverData.phase = "done"
						serverData.timestamp = time.Now()
						if dmsgErr != nil && serverData.pingErr == "" {
							serverData.pingErr = dmsgErr.Error()
						}
						if len(serverData.pingSamples) > 0 {
							dmsgSuccess = true
						}
						dataMu.Unlock()

						if dmsgSuccess && !graphDmsgAllServers {
							// Success - don't ping via remaining servers unless --dmsg-all-servers
							safeRenderTree()
							break
						}
						safeRenderTree()
					}

					dataMu.Lock()
					data.dmsgReachable = dmsgSuccess
					dataMu.Unlock()
				}()
			}
		}

		// Channel to signal DMSG pre-check completion
		dmsgDone := make(chan bool, 1)
		expectedServerCount := len(dmsgServers)
		if graphDmsgPreCheck && expectedServerCount > 0 {
			// Wait for DMSG pre-check in a separate goroutine and signal when done
			go func() {
				// Give DMSG pings a head start, then check periodically
				checkInterval := 500 * time.Millisecond
				maxWait := graphTimeout + 35*time.Second // Slightly longer than DMSG timeout
				waited := time.Duration(0)
				for waited < maxWait {
					select {
					case <-ctx.Done():
						dmsgDone <- false
						return
					case <-time.After(checkInterval):
						waited += checkInterval
						// Check if all DMSG pings are done
						dataMu.Lock()
						allDone := true
						for _, serverData := range data.dmsgServers {
							if serverData.phase != "done" {
								allDone = false
								break
							}
						}
						// Also check if we have enough servers tracked
						if len(data.dmsgServers) < expectedServerCount {
							allDone = false
						}
						reachable := data.dmsgReachable
						dataMu.Unlock()
						if allDone {
							dmsgDone <- reachable
							return
						}
					}
				}
				// Timed out waiting for DMSG - check final status
				dataMu.Lock()
				reachable := data.dmsgReachable
				dataMu.Unlock()
				dmsgDone <- reachable
			}()
		} else {
			// No DMSG pre-check - signal immediately
			dmsgDone <- true
		}

		// Route ping goroutine (waits for DMSG pre-check if enabled)
		pingWg.Add(1)
		go func() {
			defer pingWg.Done()

			// Wait for DMSG pre-check result if enabled
			if graphDmsgPreCheck {
				select {
				case <-ctx.Done():
					return
				case dmsgReachable := <-dmsgDone:
					if !dmsgReachable {
						// DMSG unreachable - skip route ping
						dataMu.Lock()
						data.dmsgSkipReason = "DMSG unreachable (all servers timed out)"
						data.pingErr = "skipped: DMSG unreachable"
						data.phase = "done"
						data.timestamp = time.Now()
						dataMu.Unlock()
						safeRenderTree()
						return
					}
				}
			}

			// Callback for ping results - captures setup and ping phases
			callback := func(_ int32, lat time.Duration, isSetup bool, _ []rpcgrpc.RouteHopDetail, _ string, routeCalcTime time.Duration, pingErr error) {
				if isSetup {
					// Setup phase completed
					totalSetupMs := 1000 * lat.Seconds()
					dataMu.Lock()
					if routeCalcTime > 0 {
						// Local route mode: separate calc and setup times
						data.calcTimeMs = 1000 * routeCalcTime.Seconds()
						data.setupTimeMs = totalSetupMs - data.calcTimeMs
					} else {
						// Route finder mode: no calc time, just setup
						data.setupTimeMs = totalSetupMs
					}
					data.phase = "ping"
					dataMu.Unlock()
					safeRenderTree()
					return
				}
				if pingErr != nil {
					dataMu.Lock()
					data.pingErr = pingErr.Error()
					data.phase = "done"
					data.timestamp = time.Now()
					dataMu.Unlock()
					return
				}
				// Store each ping sample
				pingLatencyMs := 1000 * lat.Seconds()
				dataMu.Lock()
				data.pingSamples = append(data.pingSamples, pingLatencyMs)
				dataMu.Unlock()
				safeRenderTree()
			}

			// Retry loop for transient failures
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
					dataMu.Lock()
					data.calcTimeMs = 0
					data.setupTimeMs = 0
					data.pingSamples = nil
					data.calcErr = ""
					data.setupErr = ""
					data.pingErr = ""
					dataMu.Unlock()
				}

				// Update phase before starting - for explicit routes, skip to setup (no calc)
				dataMu.Lock()
				if level > 1 {
					// Level 2+: we're using explicit route, no calculation needed
					data.phase = "setup"
				} else if graphLocalRoute {
					data.phase = "calc"
				} else {
					data.phase = "setup"
				}
				dataMu.Unlock()
				safeRenderTree()

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
						dataMu.Lock()
						delete(transportLatencies, tpID)
						dataMu.Unlock()
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
							dataMu.Lock()
							delete(transportLatencies, tpID)
							dataMu.Unlock()
							return
						}
						// Check if transport still exists
						if !localTpIDs[firstHopTpID] {
							// Mark this first-hop as dead so we skip entire subtree
							deadFirstHops[firstHopTpID] = true
							pingCancel()
							dataMu.Lock()
							delete(transportLatencies, tpID)
							dataMu.Unlock()
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
									dataMu.Lock()
									delete(transportLatencies, tpID)
									dataMu.Unlock()
									return
								}
							}
						}
					}

					dataMu.Lock()
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
					dataMu.Unlock()
				}

				// If successful or parent context canceled, stop retrying
				dataMu.Lock()
				hasSuccess := len(data.pingSamples) > 0
				dataMu.Unlock()
				if hasSuccess || ctx.Err() != nil {
					break
				}

				// If we have more attempts, continue
				if attempt < maxAttempts {
					continue
				}
			}

			dataMu.Lock()
			if len(data.pingSamples) > 0 {
				data.phase = "done"
				data.timestamp = time.Now()
				data.lastSuccess = time.Now() // Update lastSuccess on success
				data.stale = false            // Clear stale flag on success

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
				// Ping failed - check if we should preserve previous good data
				if previousGoodData != nil {
					// Restore previous good data but mark as stale
					transportLatencies[tpID] = previousGoodData
					previousGoodData.stale = true
					previousGoodData.phase = "done"
				} else {
					// No previous good data - keep the failed attempt
					data.phase = "done"
					data.timestamp = time.Now()
				}
			}
			dataMu.Unlock()

			// Re-render with results
			safeRenderTree()
		}() // End of route ping goroutine

		// Wait for both DMSG and route pings to complete
		pingWg.Wait()
	}

	// Helper to calculate average latency
	calcAvgLatency := func(samples []float64) float64 {
		if len(samples) == 0 {
			return 0
		}
		var sum float64
		for _, p := range samples {
			sum += p
		}
		return sum / float64(len(samples))
	}

	// Helper to save state to files
	saveState := func() {
		if graphOutput == "" {
			return
		}

		// Make a copy of treeEntries to avoid race with concurrent writers
		treeEntriesMu.Lock()
		localTreeEntries := make([]treeEntry, len(treeEntries))
		copy(localTreeEntries, treeEntries)
		treeEntriesMu.Unlock()

		// Build saved state
		state := routeSavedState{
			LocalPK:    localPK,
			StartTime:  startTime.Format(time.RFC3339),
			UpdateTime: time.Now().Format(time.RFC3339),
			Settings: map[string]interface{}{
				"tries":       graphTries,
				"timeout":     graphTimeout.String(),
				"version":     graphVersion,
				"local_route": graphLocalRoute,
			},
		}

		for _, entry := range localTreeEntries {
			data := transportLatencies[entry.tpID]
			if data == nil {
				continue
			}

			avgLatency := calcAvgLatency(data.pingSamples)

			savedEntry := routeSavedEntry{
				TpID:           entry.tpID,
				TpType:         data.tpType,
				RemotePK:       entry.remotePK,
				ParentPK:       entry.parentPK,
				GatewayPK:      data.gatewayPK,
				Level:          entry.level,
				CalcTimeMs:     data.calcTimeMs,
				SetupTimeMs:    data.setupTimeMs,
				PingSamples:    data.pingSamples,
				AvgLatency:     avgLatency,
				CalcErr:        data.calcErr,
				SetupErr:       data.setupErr,
				PingErr:        data.pingErr,
				Phase:          data.phase,
				Stale:          data.stale,
				DmsgReachable:  data.dmsgReachable,
				DmsgSkipReason: data.dmsgSkipReason,
			}
			// Save DMSG server ping results
			if len(data.dmsgServers) > 0 {
				for _, serverData := range data.dmsgServers {
					serverAvg := calcAvgLatency(serverData.pingSamples)
					savedServer := dmsgServerSavedEntry{
						ServerPK:    serverData.serverPK,
						PingSamples: serverData.pingSamples,
						AvgLatency:  serverAvg,
						PingErr:     serverData.pingErr,
					}
					if !serverData.timestamp.IsZero() {
						savedServer.Timestamp = serverData.timestamp.Format(time.RFC3339)
					}
					savedEntry.DmsgServers = append(savedEntry.DmsgServers, savedServer)
				}
			}
			if !data.timestamp.IsZero() {
				savedEntry.Timestamp = data.timestamp.Format(time.RFC3339)
			}
			if !data.lastSuccess.IsZero() {
				savedEntry.LastSuccess = data.lastSuccess.Format(time.RFC3339)
			}
			state.Entries = append(state.Entries, savedEntry)
		}

		jsonFile := graphOutput + ".json"
		textFile := graphOutput + ".txt"

		// Save JSON
		jsonData, err := json.MarshalIndent(state, "", "  ")
		if err == nil {
			os.WriteFile(jsonFile, jsonData, 0644) //nolint:errcheck,gosec
		}

		// Save text output (with ANSI codes for colors)
		var textOut strings.Builder
		textOut.WriteString("=== Route Ping Graph (Tree View) ===\n")
		textOut.WriteString(fmt.Sprintf("Local: %s\n", pterm.Cyan(localPK)))
		textOut.WriteString(fmt.Sprintf("Started: %s\n", startTime.Format(time.RFC3339)))
		textOut.WriteString(fmt.Sprintf("Updated: %s\n\n", time.Now().Format(time.RFC3339)))

		// Group entries by level
		entriesByLevel := make(map[int][]treeEntry)
		for _, entry := range localTreeEntries {
			entriesByLevel[entry.level] = append(entriesByLevel[entry.level], entry)
		}

		// Find max level
		maxLevel := 0
		for lvl := range entriesByLevel {
			if lvl > maxLevel {
				maxLevel = lvl
			}
		}

		// Write level 1
		if level1Entries, ok := entriesByLevel[1]; ok && len(level1Entries) > 0 {
			textOut.WriteString(pterm.Yellow("=== Level 1 (direct transports) ===\n"))
			for _, entry := range level1Entries {
				data := transportLatencies[entry.tpID]
				if data == nil {
					continue
				}
				var latStr string
				if len(data.pingSamples) > 0 {
					avg := calcAvgLatency(data.pingSamples)
					latStr = fmt.Sprintf("%.1fms", avg)
				} else if data.pingErr != "" {
					latStr = data.pingErr
				} else if data.setupErr != "" {
					latStr = data.setupErr
				} else if data.calcErr != "" {
					latStr = data.calcErr
				} else {
					latStr = "..."
				}
				textOut.WriteString(fmt.Sprintf("  %s %s %s\n", entry.remotePK, entry.tpID, latStr))
			}
		}

		// Write level 2+
		for lvl := 2; lvl <= maxLevel; lvl++ {
			levelEntries, ok := entriesByLevel[lvl]
			if !ok || len(levelEntries) == 0 {
				continue
			}
			textOut.WriteString(fmt.Sprintf("\n%s\n", pterm.Yellow(fmt.Sprintf("=== Level %d ===", lvl))))
			for _, entry := range levelEntries {
				data := transportLatencies[entry.tpID]
				if data == nil {
					continue
				}
				var latStr string
				if len(data.pingSamples) > 0 {
					avg := calcAvgLatency(data.pingSamples)
					latStr = fmt.Sprintf("%.1fms", avg)
				} else if data.pingErr != "" {
					latStr = data.pingErr
				} else if data.setupErr != "" {
					latStr = data.setupErr
				} else if data.calcErr != "" {
					latStr = data.calcErr
				} else {
					latStr = "..."
				}
				// Show first hop transport ID for multi-hop routes
				firstHopInfo := ""
				if path, ok := visorPath[entry.parentPK]; ok && len(path) > 0 {
					firstHopInfo = fmt.Sprintf(" (1st: %s)", path[0].tpID[:8])
				}
				textOut.WriteString(fmt.Sprintf("  %s via %s %s%s %s\n", entry.remotePK, entry.parentPK[:16], entry.tpID, firstHopInfo, latStr))
			}
		}

		os.WriteFile(textFile, []byte(textOut.String()), 0644) //nolint:errcheck,gosec
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

	// Per-subtree concurrent exploration
	// Each first-hop transport gets its own goroutine that explores its entire subtree sequentially
	// Different subtrees (different first-hops) run concurrently

	type transportTarget struct {
		remotePK string
		parentPK string
		tpID     string
		tpType   string
	}

	// Collect level 1 targets
	var level1Targets []transportTarget
	var filteredCount, tpdMissingCount int
	for _, tp := range localTransports {
		remotePK := tp.Remote.String()
		if !passesFilter(remotePK) {
			filteredCount++
			continue
		}
		tpID := tp.ID.String()
		if !verifyTransportExists(tpID) {
			tpdMissingCount++
			continue
		}
		level1Targets = append(level1Targets, transportTarget{
			remotePK: remotePK,
			parentPK: localPK,
			tpID:     tpID,
			tpType:   string(tp.Type),
		})
	}
	if filteredCount > 0 || tpdMissingCount > 0 {
		fmt.Printf("Level 1: %d transports, %d filtered (version/online), %d not in TPD, %d targets\n",
			len(localTransports), filteredCount, tpdMissingCount, len(level1Targets))
	}

	if graphDryRun {
		// Dry-run mode: level-by-level sequential processing (no actual pings)
		currentLevel := []string{localPK}
		seenVisors := make(map[string]bool)
		seenVisors[localPK] = true

		for level := 1; graphMaxLevel == 0 || level <= graphMaxLevel; level++ {
			if graphHops > 0 && uint(level) > graphHops { //nolint:gosec
				break
			}

			var targets []transportTarget
			if level == 1 {
				targets = level1Targets
			} else {
				for _, parentPK := range currentLevel {
					updateAdjacencyFromTPS(parentPK)
					for _, n := range adjacency[parentPK] {
						if seenVisors[n.pk] {
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

			var nextLevel []string
			for _, target := range targets {
				if seenVisors[target.remotePK] {
					continue
				}

				pathToParent := visorPath[target.parentPK]
				treeEntries = append(treeEntries, treeEntry{
					tpID:       target.tpID,
					remotePK:   target.remotePK,
					level:      level,
					parentPK:   target.parentPK,
					parentTpID: "",
				})

				transportLatencies[target.tpID] = &transportLatencyData{
					tpID:   target.tpID,
					tpType: target.tpType,
					from:   target.parentPK,
					to:     target.remotePK,
					level:  level,
					phase:  "dry-run",
				}
				renderTree()

				newPath := make([]pathHop, len(pathToParent)+1)
				copy(newPath, pathToParent)
				newPath[len(pathToParent)] = pathHop{
					tpID:   target.tpID,
					tpType: target.tpType,
					from:   target.parentPK,
					to:     target.remotePK,
				}
				visorPath[target.remotePK] = newPath
				visitedLevel[target.remotePK] = level
				seenVisors[target.remotePK] = true
				nextLevel = append(nextLevel, target.remotePK)
			}

			currentLevel = nextLevel
		}
	} else {
		// Live mode: per-subtree concurrent exploration
		// Each level 1 transport spawns a goroutine that explores its entire subtree sequentially

		var subtreeWg sync.WaitGroup
		var globalMu sync.Mutex // protects shared state not covered by dataMu

		// Add all level 1 tree entries first
		for _, target := range level1Targets {
			treeEntries = append(treeEntries, treeEntry{
				tpID:       target.tpID,
				remotePK:   target.remotePK,
				level:      1,
				parentPK:   target.parentPK,
				parentTpID: "",
			})
		}

		// Spawn a goroutine for each first-hop (level 1) transport
		for _, firstHopTarget := range level1Targets {
			if ctx.Err() != nil {
				break
			}

			subtreeWg.Add(1)
			go func(fhTarget transportTarget) {
				defer subtreeWg.Done()

				if ctx.Err() != nil {
					return
				}

				// Skip if below start level or not at exact hop level
				skipLevel1 := graphStartLevel > 1 || (graphHops > 0 && graphHops != 1)

				// First, ping the level 1 target
				if !skipLevel1 {
					pingTransport(fhTarget.remotePK, fhTarget.parentPK, fhTarget.tpID, fhTarget.tpType, 1, nil)
				}

				// Check if level 1 ping succeeded
				dataMu.Lock()
				data := transportLatencies[fhTarget.tpID]
				var success bool
				if skipLevel1 {
					success = true // Skip means we treat it as success for subtree exploration
				} else {
					success = data != nil && data.calcErr == "" && data.setupErr == "" && data.pingErr == "" && len(data.pingSamples) > 0
				}

				if success || skipLevel1 {
					visorPath[fhTarget.remotePK] = []pathHop{{
						tpID:   fhTarget.tpID,
						tpType: fhTarget.tpType,
						from:   fhTarget.parentPK,
						to:     fhTarget.remotePK,
					}}
					visitedLevel[fhTarget.remotePK] = 1
				} else {
					// Track failure
					firstHopFailures[fhTarget.tpID]++
					if firstHopFailures[fhTarget.tpID] >= maxFirstHopFailures {
						deadFirstHops[fhTarget.tpID] = true
					}
					visitedLevel[fhTarget.remotePK] = 1
				}
				dataMu.Unlock()

				if !success && !skipLevel1 {
					// First hop failed - move to failed entries and optionally remove transport
					processFailedTransport(fhTarget.tpID, fhTarget.remotePK, localPK, 1, true)
					saveState()
					return
				}

				// Now explore level 2, 3, etc. within this subtree (BFS, sequential)
				subtreeCurrentLevel := []string{fhTarget.remotePK}
				firstHopTpID := fhTarget.tpID

				for subtreeLevel := 2; graphMaxLevel == 0 || subtreeLevel <= graphMaxLevel; subtreeLevel++ {
					if ctx.Err() != nil {
						break
					}
					if graphHops > 0 && uint(subtreeLevel) > graphHops { //nolint:gosec
						break
					}

					// Check if first-hop is now dead
					dataMu.Lock()
					isDead := deadFirstHops[firstHopTpID]
					dataMu.Unlock()
					if isDead {
						break
					}

					// Verify first-hop transport still exists
					if !verifyTransportExists(firstHopTpID) {
						dataMu.Lock()
						deadFirstHops[firstHopTpID] = true
						dataMu.Unlock()
						break
					}

					// Refresh TPD cache if needed
					if time.Since(lastTPDRefresh) >= tpdRefreshInterval {
						refreshTPDCache()
					}

					// Find targets at this level within this subtree
					var subtreeTargets []transportTarget
					for _, parentPK := range subtreeCurrentLevel {
						var neighbors []treeNeighbor
						if graphUseTPS {
							var pk cipher.PubKey
							if err := pk.Set(parentPK); err == nil {
								tpsTransports, tpsErr := rpcClient.TPSGetTransports(pk)
								if tpsErr == nil {
									for _, tp := range tpsTransports {
										neighbors = append(neighbors, treeNeighbor{
											pk:     tp.Remote.String(),
											tpID:   tp.ID.String(),
											tpType: tp.Type,
										})
									}
								}
							}
						}
						if len(neighbors) == 0 {
							updateAdjacencyFromTPS(parentPK)
							neighbors = adjacency[parentPK]
						}

						for _, n := range neighbors {
							// Check if already visited at lower level
							globalMu.Lock()
							prevLevel, visited := visitedLevel[n.pk]
							globalMu.Unlock()
							if visited && prevLevel < subtreeLevel {
								continue
							}
							if !passesFilter(n.pk) {
								continue
							}

							pathToParent := visorPath[parentPK]
							if pathHasLoop(pathToParent, n.pk) {
								continue
							}
							if len(pathToParent) > 0 && !routeIsValid(pathToParent) {
								continue
							}

							subtreeTargets = append(subtreeTargets, transportTarget{
								remotePK: n.pk,
								parentPK: parentPK,
								tpID:     n.tpID,
								tpType:   n.tpType,
							})
						}
					}

					if len(subtreeTargets) == 0 {
						break
					}

					// Ping each target sequentially (they all share the same first-hop)
					var nextSubtreeLevel []string
					skipPing := subtreeLevel < graphStartLevel || (graphHops > 0 && uint(subtreeLevel) != graphHops) //nolint:gosec

					for _, target := range subtreeTargets {
						if ctx.Err() != nil {
							break
						}

						// Check if first-hop became dead
						dataMu.Lock()
						isDead := deadFirstHops[firstHopTpID]
						dataMu.Unlock()
						if isDead {
							break
						}

						// Check if already visited
						globalMu.Lock()
						prevLevel, alreadyVisited := visitedLevel[target.remotePK]
						globalMu.Unlock()
						if alreadyVisited && prevLevel < subtreeLevel {
							continue
						}

						pathToParent := visorPath[target.parentPK]

						// Add tree entry (use treeEntriesMu for treeEntries to avoid race with processFailedTransport)
						treeEntriesMu.Lock()
						treeEntries = append(treeEntries, treeEntry{
							tpID:       target.tpID,
							remotePK:   target.remotePK,
							level:      subtreeLevel,
							parentPK:   target.parentPK,
							parentTpID: "",
						})
						treeEntriesMu.Unlock()

						if skipPing {
							// Just add to next level without pinging
							globalMu.Lock()
							if _, visited := visitedLevel[target.remotePK]; !visited {
								newPath := make([]pathHop, len(pathToParent)+1)
								copy(newPath, pathToParent)
								newPath[len(pathToParent)] = pathHop{
									tpID:   target.tpID,
									tpType: target.tpType,
									from:   target.parentPK,
									to:     target.remotePK,
								}
								visorPath[target.remotePK] = newPath
								visitedLevel[target.remotePK] = subtreeLevel
								nextSubtreeLevel = append(nextSubtreeLevel, target.remotePK)
							}
							globalMu.Unlock()
							continue
						}

						// Ping this transport
						pingTransport(target.remotePK, target.parentPK, target.tpID, target.tpType, subtreeLevel, pathToParent)

						// Check result
						dataMu.Lock()
						data := transportLatencies[target.tpID]
						hasFailed := data != nil && (data.calcErr != "" || data.setupErr != "" || data.pingErr != "")

						// Track first-hop failures
						if hasFailed {
							firstHopFailures[firstHopTpID]++
							if firstHopFailures[firstHopTpID] >= maxFirstHopFailures {
								deadFirstHops[firstHopTpID] = true
							}
							dataMu.Unlock()

							// Move to failed entries and optionally remove transport
							processFailedTransport(target.tpID, target.remotePK, target.parentPK, subtreeLevel, false)
						} else {
							if data != nil && len(data.pingSamples) > 0 {
								firstHopFailures[firstHopTpID] = 0
							}
							dataMu.Unlock()
						}

						// Add to next level if successful
						globalMu.Lock()
						if _, visited := visitedLevel[target.remotePK]; !visited {
							dataMu.Lock()
							data := transportLatencies[target.tpID]
							hasFailed := data != nil && (data.calcErr != "" || data.setupErr != "" || data.pingErr != "")
							if data != nil && !hasFailed {
								newPath := make([]pathHop, len(pathToParent)+1)
								copy(newPath, pathToParent)
								newPath[len(pathToParent)] = pathHop{
									tpID:   target.tpID,
									tpType: target.tpType,
									from:   target.parentPK,
									to:     target.remotePK,
								}
								visorPath[target.remotePK] = newPath
								nextSubtreeLevel = append(nextSubtreeLevel, target.remotePK)
							}
							dataMu.Unlock()
							visitedLevel[target.remotePK] = subtreeLevel
						}
						globalMu.Unlock()

						// Save state periodically
						saveState()
					}

					subtreeCurrentLevel = nextSubtreeLevel
					if len(subtreeCurrentLevel) == 0 {
						break
					}
				}

				// Final save for this subtree
				saveState()
			}(firstHopTarget)
		}

		// Wait for all subtrees to complete
		subtreeWg.Wait()
		saveState()
	}

	// Add any transport IDs from this session to the pinged set
	for tpID := range transportLatencies {
		pingedTpIDs[tpID] = true
	}

	// In dry-run mode, print summary and exit
	if graphDryRun {
		// Count unique visors and transports per level
		levelCounts := make(map[int]int)
		visorsByLevel := make(map[int]map[string]bool)
		for _, entry := range treeEntries {
			levelCounts[entry.level]++
			if visorsByLevel[entry.level] == nil {
				visorsByLevel[entry.level] = make(map[string]bool)
			}
			visorsByLevel[entry.level][entry.remotePK] = true
		}

		fmt.Println()
		fmt.Println("=== Dry Run Summary ===")
		fmt.Printf("Total transports: %d\n", len(treeEntries))
		fmt.Printf("Total unique visors: %d\n", len(visitedLevel)-1) // -1 for local

		// Print per-level breakdown
		maxLevel := 0
		for level := range levelCounts {
			if level > maxLevel {
				maxLevel = level
			}
		}
		for level := 1; level <= maxLevel; level++ {
			fmt.Printf("  Level %d: %d transports, %d unique visors\n",
				level, levelCounts[level], len(visorsByLevel[level]))
		}
		return
	}

	// Continuous monitoring loop - check for new transports after each pass
	for {
		if ctx.Err() != nil {
			break
		}

		// Refresh TPD cache if enough time has passed
		if time.Since(lastTPDRefresh) >= tpdRefreshInterval {
			refreshTPDCache()
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
			// No new transports found
			if !graphContinuous {
				// Non-continuous mode - scan complete
				break
			}

			// Continuous mode - check for stale entries to re-ping
			var staleEntries []struct {
				tpID     string
				remotePK string
				parentPK string
				tpType   string
				level    int
			}

			recheckAge := graphRecheckAge
			if recheckAge == 0 {
				recheckAge = 24 * time.Hour // Default recheck age
			}

			for tpID, data := range transportLatencies {
				if data == nil {
					continue
				}
				// Check if entry needs pinging
				shouldReping := false
				if data.phase == "pending" {
					// Never pinged - needs to be pinged
					shouldReping = true
				} else if data.phase == "done" {
					// Check if stale (needs re-ping)
					if data.stale {
						shouldReping = true
					} else if !data.timestamp.IsZero() && time.Since(data.timestamp) > recheckAge {
						shouldReping = true
					}
				}

				if shouldReping {
					// Find the tree entry for this transport
					for _, entry := range treeEntries {
						if entry.tpID == tpID {
							staleEntries = append(staleEntries, struct {
								tpID     string
								remotePK string
								parentPK string
								tpType   string
								level    int
							}{
								tpID:     tpID,
								remotePK: entry.remotePK,
								parentPK: entry.parentPK,
								tpType:   data.tpType,
								level:    entry.level,
							})
							break
						}
					}
				}
			}

			if len(staleEntries) == 0 {
				// No pending or stale entries - wait a bit before next check
				fmt.Printf("No pending or stale entries, waiting %v before next check...\n", recheckAge/10)
				select {
				case <-ctx.Done():
					break
				case <-time.After(recheckAge / 10): // Wait 1/10th of recheck age
				}
				continue
			}

			fmt.Printf("Re-pinging %d stale entries...\n", len(staleEntries))

			// Re-ping stale entries (sorted by level, lowest first)
			for i := 0; i < len(staleEntries)-1; i++ {
				for j := i + 1; j < len(staleEntries); j++ {
					if staleEntries[j].level < staleEntries[i].level {
						staleEntries[i], staleEntries[j] = staleEntries[j], staleEntries[i]
					}
				}
			}

			for _, entry := range staleEntries {
				if ctx.Err() != nil {
					break
				}

				// Get path to parent for multi-hop routes
				var pathToParent []pathHop
				if entry.level > 1 {
					pathToParent = visorPath[entry.parentPK]
				}

				// Skip if first-hop is dead
				var firstHopTpID string
				if entry.level == 1 {
					firstHopTpID = entry.tpID
				} else if len(pathToParent) > 0 {
					firstHopTpID = pathToParent[0].tpID
				}
				if firstHopTpID != "" && deadFirstHops[firstHopTpID] {
					continue
				}

				// Verify first-hop still exists
				if firstHopTpID != "" && !verifyTransportExists(firstHopTpID) {
					deadFirstHops[firstHopTpID] = true
					continue
				}

				// Re-ping this transport
				pingTransport(entry.remotePK, entry.parentPK, entry.tpID, entry.tpType, entry.level, pathToParent)
				saveState()
			}

			// Loop back to check for new transports
			continue
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

			// Skip if this transport is already marked dead
			if deadFirstHops[target.tpID] {
				continue
			}

			// Ping it
			pingTransport(target.remotePK, localPK, target.tpID, target.tpType, 1, nil)
			pingedTpIDs[target.tpID] = true
			saveState() // Save after each ping for resume support

			// Check if ping succeeded and track failures
			data := transportLatencies[target.tpID]
			hasFailed := data != nil && (data.calcErr != "" || data.setupErr != "" || data.pingErr != "")
			if hasFailed {
				firstHopFailures[target.tpID]++
				if firstHopFailures[target.tpID] >= maxFirstHopFailures {
					deadFirstHops[target.tpID] = true
				}
			} else if data != nil && len(data.pingSamples) > 0 {
				firstHopFailures[target.tpID] = 0
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
				// For level 2+, query TPS for fresh transport data if enabled
				var neighbors []treeNeighbor
				if graphUseTPS {
					var pk cipher.PubKey
					if err := pk.Set(parentPK); err == nil {
						tpsTransports, tpsErr := rpcClient.TPSGetTransports(pk)
						if tpsErr == nil {
							// Use TPS transports - only include non-user labeled transports
							for _, tp := range tpsTransports {
								// Skip "user" labeled transports (only use automatic/skycoin)
								// Label is in tp.Type field or we can check if it's a system transport
								neighbors = append(neighbors, treeNeighbor{
									pk:     tp.Remote.String(),
									tpID:   tp.ID.String(),
									tpType: tp.Type,
								})
							}
						}
					}
				}
				// If TPS failed or disabled, fall back to TPD adjacency
				if len(neighbors) == 0 {
					neighbors = adjacency[parentPK]
				}
				if len(neighbors) == 0 {
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

					// Determine first-hop transport ID
					var firstHopTpID string
					if len(pathToParent) > 0 {
						firstHopTpID = pathToParent[0].tpID
					}

					// Skip if first-hop is marked dead
					if firstHopTpID != "" && deadFirstHops[firstHopTpID] {
						continue
					}

					// Verify first-hop transport still exists locally before pinging
					if firstHopTpID != "" && !verifyTransportExists(firstHopTpID) {
						// First-hop transport is gone - mark as dead and skip
						deadFirstHops[firstHopTpID] = true
						continue
					}

					// Verify all intermediate transports (except first hop) still exist in TPD
					if len(pathToParent) > 0 && !routeIsValid(pathToParent) {
						// Some transport in the route is no longer in TPD - skip this route
						continue
					}

					// Ping it
					pingTransport(n.pk, parentPK, n.tpID, n.tpType, level, pathToParent)
					pingedTpIDs[n.tpID] = true
					saveState() // Save after each ping for resume support

					// Check if ping succeeded and track first-hop failures
					data := transportLatencies[n.tpID]
					hasFailed := data != nil && (data.calcErr != "" || data.setupErr != "" || data.pingErr != "")
					if firstHopTpID != "" {
						if hasFailed {
							firstHopFailures[firstHopTpID]++
							if firstHopFailures[firstHopTpID] >= maxFirstHopFailures {
								deadFirstHops[firstHopTpID] = true
							}
						} else if data != nil && len(data.pingSamples) > 0 {
							firstHopFailures[firstHopTpID] = 0
						}
					}

					// Add to next level if not seen
					if !seenVisors[n.pk] {
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

	// Final render and save
	renderTree()
	saveState()
	fmt.Println(pterm.Green("Scan complete!"))
}

// dmsgLatencyData tracks timing for DMSG pings (no transport ID, no setup time)
type dmsgLatencyData struct {
	serverPK    string    // DMSG server used for this ping
	remotePK    string    // Remote visor being pinged
	pingSamples []float64 // Ping samples in ms
	pingErr     string    // Error message if ping failed
	phase       string    // "pending", "ping", "done"
	timestamp   time.Time // When the test completed
}

// dmsgSavedState represents the saved state for resume functionality
type dmsgSavedState struct {
	LocalPK    string                 `json:"local_pk"`
	StartTime  string                 `json:"start_time"`
	UpdateTime string                 `json:"update_time"`
	Entries    []dmsgSavedEntry       `json:"entries"`
	Servers    []string               `json:"servers"`
	Settings   map[string]interface{} `json:"settings"`
}

// dmsgSavedEntry represents a single ping entry in saved state
type dmsgSavedEntry struct {
	ServerPK    string    `json:"server_pk"`
	RemotePK    string    `json:"remote_pk"`
	Level       int       `json:"level"`
	PingSamples []float64 `json:"ping_samples,omitempty"`
	AvgLatency  float64   `json:"avg_latency_ms,omitempty"`
	PingErr     string    `json:"ping_err,omitempty"`
	Timestamp   string    `json:"timestamp,omitempty"`
	Phase       string    `json:"phase"`
}

// runDmsgTreeViewMode executes the ping graph in DMSG tree view mode
func runDmsgTreeViewMode(
	ctx context.Context,
	grpcClient *rpcgrpc.PingClient,
	rpcClient visor.API,
	localPK string,
	_ map[string]bool, // onlineSet - unused, passesFilter is used instead
	_ map[string]bool, // versionFilteredSet - unused, passesFilter is used instead
	passesFilter func(string) bool,
) {
	// Mutex for thread-safe access to shared data structures
	var mu sync.Mutex

	// Data structure to track DMSG ping latencies
	// Key: serverPK:remotePK, Value: latency data
	dmsgLatencies := make(map[string]*dmsgLatencyData)

	// Track server latencies from level 1 (self-ping)
	serverLatencies := make(map[string]float64) // serverPK -> avg latency to self

	// Start time for this scan
	startTime := time.Now()

	// Get local visor's connected DMSG servers
	summary, err := rpcClient.Summary()
	if err != nil {
		fmt.Printf("Error getting visor summary: %v\n", err)
		return
	}
	connectedServers := summary.ConnectedDmsgServers
	if len(connectedServers) == 0 {
		fmt.Println("No connected DMSG servers found")
		return
	}
	fmt.Printf("Found %d connected DMSG server(s)\n", len(connectedServers))

	// Fetch DMSG clients by server from dmsg-discovery
	fmt.Printf("Fetching DMSG clients from discovery...\n")
	dmsgClientsRaw := internal.GetData(graphCacheDMSG, graphDMSGURL+"/dmsg-discovery/servers/clients", graphCacheAge)
	var clientsByServer map[string][]string
	if err := json.Unmarshal([]byte(dmsgClientsRaw), &clientsByServer); err != nil {
		fmt.Printf("Warning: failed to parse DMSG clients data: %v\n", err)
		clientsByServer = make(map[string][]string)
	} else {
		totalClients := 0
		for _, clients := range clientsByServer {
			totalClients += len(clients)
		}
		fmt.Printf("Loaded %d servers with %d total clients from DMSG discovery\n", len(clientsByServer), totalClients)
	}

	// Build list of tree entries for display
	type dmsgTreeEntry struct {
		serverPK string
		remotePK string
		level    int
	}
	var treeEntries []dmsgTreeEntry

	// Track which visors we've already pinged (for resume)
	pingedVisors := make(map[string]bool)
	pingedVisors[localPK] = true

	// Load saved state if resuming
	if graphResume && graphOutput != "" {
		resumeFile := graphOutput + ".json"
		savedData, err := os.ReadFile(resumeFile) //nolint:gosec // G304: file path is from user-provided flag
		if err == nil {
			var savedState dmsgSavedState
			if err := json.Unmarshal(savedData, &savedState); err == nil {
				fmt.Printf("Resuming from: %s\n", resumeFile)
				fmt.Printf("  Started: %s, Last update: %s\n", savedState.StartTime, savedState.UpdateTime)
				fmt.Printf("  Loaded %d entries\n", len(savedState.Entries))

				// Restore entries
				var staleCount int
				for _, entry := range savedState.Entries {
					// Parse timestamp
					var entryTime time.Time
					if entry.Timestamp != "" {
						if ts, err := time.Parse(time.RFC3339, entry.Timestamp); err == nil {
							entryTime = ts
						}
					}

					// Check if entry is stale (older than max-age)
					isStale := false
					if graphMaxAge > 0 && entry.Phase == "done" && !entryTime.IsZero() {
						if time.Since(entryTime) > graphMaxAge {
							isStale = true
							staleCount++
						}
					}

					// Mark as already pinged if done and not stale
					if entry.Phase == "done" && !isStale {
						pingedVisors[entry.RemotePK] = true
					}

					// Add to tree entries
					treeEntries = append(treeEntries, dmsgTreeEntry{
						serverPK: entry.ServerPK,
						remotePK: entry.RemotePK,
						level:    entry.Level,
					})

					// Restore latency data (but reset if stale)
					key := entry.ServerPK + ":" + entry.RemotePK
					if isStale {
						// Reset stale entry - it will be re-pinged
						dmsgLatencies[key] = &dmsgLatencyData{
							serverPK: entry.ServerPK,
							remotePK: entry.RemotePK,
							phase:    "pending",
						}
					} else {
						dmsgLatencies[key] = &dmsgLatencyData{
							serverPK:    entry.ServerPK,
							remotePK:    entry.RemotePK,
							pingSamples: entry.PingSamples,
							pingErr:     entry.PingErr,
							phase:       entry.Phase,
							timestamp:   entryTime,
						}
					}

					// Restore server latencies for level 1 (only if not stale)
					if entry.Level == 1 && entry.AvgLatency > 0 && !isStale {
						serverLatencies[entry.ServerPK] = entry.AvgLatency
					}
				}
				if staleCount > 0 {
					fmt.Printf("  Found %d stale entries (older than %v) to re-ping\n", staleCount, graphMaxAge)
				}
			}
		}
		// If file doesn't exist, just start fresh (no error message needed)
	}

	// Helper to get unique key for latency map
	latencyKey := func(serverPK, remotePK string) string {
		return serverPK + ":" + remotePK
	}

	// Helper to format a DMSG tree entry as text (with optional timestamp)
	// If lockMu is true, acquires mu.Lock; if false, assumes caller holds the lock
	formatDmsgEntryInternal := func(entry dmsgTreeEntry, lockMu bool) string {
		if lockMu {
			mu.Lock()
		}
		key := latencyKey(entry.serverPK, entry.remotePK)
		data := dmsgLatencies[key]
		if lockMu {
			mu.Unlock()
		}

		// For level 1 (self-ping), display serverPK as the child node
		// For level 2+, display remotePK (the client being pinged)
		displayPK := entry.remotePK
		if entry.level == 1 {
			displayPK = entry.serverPK
		}

		if data == nil {
			return fmt.Sprintf("%s ...", displayPK)
		}

		// Build ping samples string (each 8 chars)
		var pingsStr string
		if data.pingErr != "" {
			pingsStr = pterm.Red(fmt.Sprintf("%8s", truncateErrDmsg(data.pingErr, 8)))
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

		// Average ping time (9 chars, right-aligned)
		var avgStr string
		if data.pingErr == "" && len(data.pingSamples) > 0 {
			var pingSum float64
			for _, p := range data.pingSamples {
				pingSum += p
			}
			avgPing := pingSum / float64(len(data.pingSamples))
			avgStr = fmt.Sprintf("%9s", fmt.Sprintf("%.1fms", avgPing))
		} else {
			avgStr = fmt.Sprintf("%9s", "-")
		}

		// Timestamp (if done)
		var tsStr string
		if data.phase == "done" && !data.timestamp.IsZero() {
			tsStr = pterm.Gray(fmt.Sprintf(" %s", data.timestamp.Format("2006-01-02 15:04:05")))
		}

		// Format: displayPK pings... avg timestamp
		if data.pingErr != "" {
			return pterm.Red(fmt.Sprintf("%s %s %s", displayPK, pingsStr, avgStr)) + tsStr
		}
		return fmt.Sprintf("%s %s %s", displayPK, pingsStr, avgStr) + tsStr
	}

	// Wrapper that always acquires the lock
	formatDmsgEntry := func(entry dmsgTreeEntry) string {
		return formatDmsgEntryInternal(entry, true)
	}

	// Helper to get average latency for a server (from self-ping)
	getServerLatency := func(serverPK string) float64 {
		mu.Lock()
		defer mu.Unlock()
		if lat, ok := serverLatencies[serverPK]; ok {
			return lat
		}
		return -1
	}

	// Helper to save state to files
	saveState := func() {
		mu.Lock()
		defer mu.Unlock()

		// Build saved state
		state := dmsgSavedState{
			LocalPK:    localPK,
			StartTime:  startTime.Format(time.RFC3339),
			UpdateTime: time.Now().Format(time.RFC3339),
			Servers:    connectedServers,
			Settings: map[string]interface{}{
				"tries":   graphTries,
				"timeout": graphTimeout.String(),
				"version": graphVersion,
			},
		}

		for _, entry := range treeEntries {
			key := latencyKey(entry.serverPK, entry.remotePK)
			data := dmsgLatencies[key]
			if data == nil {
				continue
			}

			var avgLatency float64
			if len(data.pingSamples) > 0 {
				var sum float64
				for _, p := range data.pingSamples {
					sum += p
				}
				avgLatency = sum / float64(len(data.pingSamples))
			}

			savedEntry := dmsgSavedEntry{
				ServerPK:    entry.serverPK,
				RemotePK:    entry.remotePK,
				Level:       entry.level,
				PingSamples: data.pingSamples,
				AvgLatency:  avgLatency,
				PingErr:     data.pingErr,
				Phase:       data.phase,
			}
			if !data.timestamp.IsZero() {
				savedEntry.Timestamp = data.timestamp.Format(time.RFC3339)
			}
			state.Entries = append(state.Entries, savedEntry)
		}

		// Save files if output specified
		if graphOutput != "" {
			jsonFile := graphOutput + ".json"
			textFile := graphOutput + ".txt"

			// Save JSON
			jsonData, err := json.MarshalIndent(state, "", "  ")
			if err == nil {
				os.WriteFile(jsonFile, jsonData, 0644) //nolint:errcheck,gosec
			}

			// Save text (with ANSI codes)
			var textOut strings.Builder
			textOut.WriteString("=== DMSG Ping Graph ===\n")
			textOut.WriteString(fmt.Sprintf("Local: %s\n", pterm.Cyan(localPK)))
			textOut.WriteString(fmt.Sprintf("Started: %s\n", startTime.Format(time.RFC3339)))
			textOut.WriteString(fmt.Sprintf("Updated: %s\n\n", time.Now().Format(time.RFC3339)))

			// Level 1 - sort servers by latency
			textOut.WriteString(pterm.Yellow("=== Level 1 (DMSG servers) ===\n"))
			type serverWithLatency struct {
				pk      string
				latency float64
			}
			var sortedServersForSave []serverWithLatency
			for _, entry := range treeEntries {
				if entry.level == 1 {
					lat, ok := serverLatencies[entry.serverPK]
					if !ok {
						lat = -1
					}
					sortedServersForSave = append(sortedServersForSave, serverWithLatency{pk: entry.serverPK, latency: lat})
				}
			}
			// Sort servers by latency
			for i := 0; i < len(sortedServersForSave)-1; i++ {
				for j := i + 1; j < len(sortedServersForSave); j++ {
					si, sj := sortedServersForSave[i], sortedServersForSave[j]
					if si.latency < 0 && sj.latency >= 0 {
						sortedServersForSave[i], sortedServersForSave[j] = sortedServersForSave[j], sortedServersForSave[i]
					} else if si.latency >= 0 && sj.latency >= 0 && sj.latency < si.latency {
						sortedServersForSave[i], sortedServersForSave[j] = sortedServersForSave[j], sortedServersForSave[i]
					}
				}
			}
			for _, srv := range sortedServersForSave {
				entry := dmsgTreeEntry{serverPK: srv.pk, remotePK: localPK, level: 1}
				textOut.WriteString(fmt.Sprintf("  %s\n", formatDmsgEntryInternal(entry, false)))
			}

			// Level 2 grouped by server
			entriesByServer := make(map[string][]dmsgTreeEntry)
			for _, entry := range treeEntries {
				if entry.level > 1 {
					entriesByServer[entry.serverPK] = append(entriesByServer[entry.serverPK], entry)
				}
			}
			if len(entriesByServer) > 0 {
				textOut.WriteString(pterm.Yellow("\n=== Level 2 (visors via DMSG servers) ===\n"))
				// Iterate over sorted servers
				for _, srv := range sortedServersForSave {
					entries, ok := entriesByServer[srv.pk]
					if !ok || len(entries) == 0 {
						continue
					}
					serverPK := srv.pk
					lat := serverLatencies[serverPK]
					latStr := ""
					if lat > 0 {
						latStr = pterm.Gray(fmt.Sprintf(" (%.1fms)", lat))
					}
					textOut.WriteString(fmt.Sprintf("%s%s\n", pterm.Magenta(serverPK), latStr))

					// Sort entries by average latency (already hold mu.Lock)
					type entryWithLatency struct {
						entry   dmsgTreeEntry
						avgLat  float64
						hasData bool
					}
					var sortableEntries []entryWithLatency
					for _, entry := range entries {
						key := latencyKey(entry.serverPK, entry.remotePK)
						data := dmsgLatencies[key]
						var avgLat float64
						var hasData bool
						if data != nil && len(data.pingSamples) > 0 {
							var sum float64
							for _, p := range data.pingSamples {
								sum += p
							}
							avgLat = sum / float64(len(data.pingSamples))
							hasData = true
						}
						sortableEntries = append(sortableEntries, entryWithLatency{entry: entry, avgLat: avgLat, hasData: hasData})
					}
					// Sort: entries with data by latency, entries without data at the end
					for i := 0; i < len(sortableEntries)-1; i++ {
						for j := i + 1; j < len(sortableEntries); j++ {
							ei, ej := sortableEntries[i], sortableEntries[j]
							if !ei.hasData && ej.hasData {
								sortableEntries[i], sortableEntries[j] = sortableEntries[j], sortableEntries[i]
							} else if ei.hasData && ej.hasData && ej.avgLat < ei.avgLat {
								sortableEntries[i], sortableEntries[j] = sortableEntries[j], sortableEntries[i]
							}
						}
					}

					for _, se := range sortableEntries {
						textOut.WriteString(fmt.Sprintf("  %s\n", formatDmsgEntryInternal(se.entry, false)))
					}
				}
			}

			os.WriteFile(textFile, []byte(textOut.String()), 0644) //nolint:errcheck,gosec
		}
	}

	// Helper to render the DMSG tree
	renderDmsgTree := func() {
		mu.Lock()
		localTreeEntries := make([]dmsgTreeEntry, len(treeEntries))
		copy(localTreeEntries, treeEntries)
		localConnectedServers := make([]string, len(connectedServers))
		copy(localConnectedServers, connectedServers)
		mu.Unlock()

		// Clear screen
		fmt.Print("\033[H\033[2J\033[3J")

		// Build label row for DMSG mode (no tpid, no setup)
		var labelParts []string
		labelParts = append(labelParts, fmt.Sprintf("%-64s", pterm.Gray("edge")))
		labelParts = append(labelParts, fmt.Sprintf("%8s", pterm.Gray("ping")))
		for i := 1; i < graphTries; i++ {
			labelParts = append(labelParts, fmt.Sprintf("%8s", pterm.Gray(".....ms")))
		}
		labelParts = append(labelParts, fmt.Sprintf("%9s", pterm.Gray("avg")))
		labelParts = append(labelParts, fmt.Sprintf("%9s", pterm.Gray("time")))
		labelRow := strings.Join(labelParts, " ")

		// Print label tree header
		labelTree := pterm.TreeNode{
			Text: pterm.Gray("root"),
			Children: []pterm.TreeNode{
				{Text: labelRow},
			},
		}
		pterm.DefaultTree.WithRoot(labelTree).Render() //nolint:errcheck,gosec
		fmt.Println()

		// Level 1: Self-ping via each server
		fmt.Println(pterm.Yellow("=== Level 1 (DMSG servers) ==="))

		// Sort servers by latency
		type serverWithLatency struct {
			pk      string
			latency float64
		}
		var sortedServers []serverWithLatency
		for _, serverPK := range localConnectedServers {
			lat := getServerLatency(serverPK)
			sortedServers = append(sortedServers, serverWithLatency{pk: serverPK, latency: lat})
		}
		// Sort: lowest latency first
		for i := 0; i < len(sortedServers)-1; i++ {
			for j := i + 1; j < len(sortedServers); j++ {
				if sortedServers[i].latency < 0 && sortedServers[j].latency >= 0 {
					sortedServers[i], sortedServers[j] = sortedServers[j], sortedServers[i]
				} else if sortedServers[i].latency >= 0 && sortedServers[j].latency >= 0 && sortedServers[j].latency < sortedServers[i].latency {
					sortedServers[i], sortedServers[j] = sortedServers[j], sortedServers[i]
				}
			}
		}

		// Build level 1 tree
		var level1Children []pterm.TreeNode
		for _, srv := range sortedServers {
			entry := dmsgTreeEntry{serverPK: srv.pk, remotePK: localPK, level: 1}
			level1Children = append(level1Children, pterm.TreeNode{Text: formatDmsgEntry(entry)})
		}
		level1Tree := pterm.TreeNode{
			Text:     pterm.Cyan(localPK) + pterm.Gray(" (local)"),
			Children: level1Children,
		}
		pterm.DefaultTree.WithRoot(level1Tree).Render() //nolint:errcheck,gosec

		// Level 2+: Clients per server
		entriesByServer := make(map[string][]dmsgTreeEntry)
		for _, entry := range localTreeEntries {
			if entry.level > 1 {
				entriesByServer[entry.serverPK] = append(entriesByServer[entry.serverPK], entry)
			}
		}

		if len(entriesByServer) > 0 {
			fmt.Println()
			fmt.Println(pterm.Yellow("=== Level 2 (visors via DMSG servers) ==="))

			for _, srv := range sortedServers {
				entries, ok := entriesByServer[srv.pk]
				if !ok || len(entries) == 0 {
					continue
				}

				// Sort entries by average latency
				type entryWithLatency struct {
					entry   dmsgTreeEntry
					avgLat  float64
					hasData bool
				}
				var sortableEntries []entryWithLatency
				mu.Lock()
				for _, entry := range entries {
					key := latencyKey(entry.serverPK, entry.remotePK)
					data := dmsgLatencies[key]
					var avgLat float64
					var hasData bool
					if data != nil && len(data.pingSamples) > 0 {
						var sum float64
						for _, p := range data.pingSamples {
							sum += p
						}
						avgLat = sum / float64(len(data.pingSamples))
						hasData = true
					}
					sortableEntries = append(sortableEntries, entryWithLatency{entry: entry, avgLat: avgLat, hasData: hasData})
				}
				mu.Unlock()

				// Sort: entries with data by latency, entries without data at the end
				for i := 0; i < len(sortableEntries)-1; i++ {
					for j := i + 1; j < len(sortableEntries); j++ {
						ei, ej := sortableEntries[i], sortableEntries[j]
						// No data goes to the end
						if !ei.hasData && ej.hasData {
							sortableEntries[i], sortableEntries[j] = sortableEntries[j], sortableEntries[i]
						} else if ei.hasData && ej.hasData && ej.avgLat < ei.avgLat {
							sortableEntries[i], sortableEntries[j] = sortableEntries[j], sortableEntries[i]
						}
					}
				}

				var children []pterm.TreeNode
				for _, se := range sortableEntries {
					children = append(children, pterm.TreeNode{Text: formatDmsgEntry(se.entry)})
				}

				latStr := ""
				if srv.latency >= 0 {
					latStr = pterm.Gray(fmt.Sprintf(" (%.1fms)", srv.latency))
				}
				serverTree := pterm.TreeNode{
					Text:     pterm.Magenta(srv.pk) + latStr,
					Children: children,
				}
				pterm.DefaultTree.WithRoot(serverTree).Render() //nolint:errcheck,gosec
			}
		}

		fmt.Println()
		fmt.Println(pterm.Gray("Press Ctrl+C to stop"))
	}

	// Helper to ping a visor via DMSG
	pingDmsg := func(serverPK, remotePK string, level int) {

		key := latencyKey(serverPK, remotePK)

		mu.Lock()
		data := &dmsgLatencyData{
			serverPK: serverPK,
			remotePK: remotePK,
			phase:    "pending",
		}
		dmsgLatencies[key] = data
		mu.Unlock()

		renderDmsgTree()

		mu.Lock()
		data.phase = "ping"
		mu.Unlock()

		renderDmsgTree()

		// Callback for DMSG ping results - updates screen after each sample
		callback := func(_ int32, lat time.Duration, isSetup bool, _ []rpcgrpc.RouteHopDetail, _ string, _ time.Duration, pingErr error) {
			if isSetup {
				return
			}
			mu.Lock()
			if pingErr != nil {
				data.pingErr = pingErr.Error()
			} else {
				pingLatencyMs := 1000 * lat.Seconds()
				data.pingSamples = append(data.pingSamples, pingLatencyMs)
			}
			mu.Unlock()
			// Update screen after each ping sample
			renderDmsgTree()
		}

		// Perform DMSG ping via specific server
		err := grpcClient.StreamDmsgPing(ctx, remotePK, int32(graphTries), int32(graphPcktSize), graphTimeout, serverPK, callback) //nolint:gosec // G115: graphTries/graphPcktSize are bounded by CLI flags
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			mu.Lock()
			if data.pingErr == "" {
				data.pingErr = err.Error()
			}
			mu.Unlock()
		}

		mu.Lock()
		data.phase = "done"
		data.timestamp = time.Now()

		// Update server latency for level 1 (self-ping)
		if level == 1 && len(data.pingSamples) > 0 {
			var sum float64
			for _, p := range data.pingSamples {
				sum += p
			}
			serverLatencies[serverPK] = sum / float64(len(data.pingSamples))
		}
		mu.Unlock()

		// Save state after each ping
		saveState()

		renderDmsgTree()
	}

	// === Level 1: Self-ping via each connected server ===
	fmt.Println("\n=== Level 1: Self-ping via DMSG servers ===")

	// Check which servers are already done (from resume)
	var level1ToDo []string
	for _, serverPK := range connectedServers {
		key := latencyKey(serverPK, localPK)
		mu.Lock()
		data := dmsgLatencies[key]
		mu.Unlock()
		if data == nil || data.phase != "done" {
			level1ToDo = append(level1ToDo, serverPK)
			// Add entry if not already present
			found := false
			for _, e := range treeEntries {
				if e.serverPK == serverPK && e.remotePK == localPK {
					found = true
					break
				}
			}
			if !found {
				mu.Lock()
				treeEntries = append(treeEntries, dmsgTreeEntry{
					serverPK: serverPK,
					remotePK: localPK,
					level:    1,
				})
				mu.Unlock()
			}
		}
	}

	// Level 1: Self-ping via each DMSG server (sequential)
	for _, serverPK := range level1ToDo {
		if ctx.Err() != nil {
			return
		}
		pingDmsg(serverPK, localPK, 1)
	}

	// === Level 2: Ping clients via each server ===
	var workingServers []string
	for _, serverPK := range connectedServers {
		key := latencyKey(serverPK, localPK)
		mu.Lock()
		data := dmsgLatencies[key]
		mu.Unlock()
		if data != nil && len(data.pingSamples) > 0 {
			workingServers = append(workingServers, serverPK)
		}
	}

	if len(workingServers) == 0 {
		fmt.Println("\nNo servers responded to self-ping, cannot proceed to level 2")
		renderDmsgTree()
		fmt.Println(pterm.Green("Scan complete!"))
		return
	}

	// Sort workingServers by latency
	mu.Lock()
	for i := 0; i < len(workingServers)-1; i++ {
		for j := i + 1; j < len(workingServers); j++ {
			latI := serverLatencies[workingServers[i]]
			latJ := serverLatencies[workingServers[j]]
			if latJ < latI {
				workingServers[i], workingServers[j] = workingServers[j], workingServers[i]
			}
		}
	}
	mu.Unlock()

	fmt.Printf("\n=== Level 2: Pinging clients via %d working server(s) ===\n", len(workingServers))

	// Build list of all clients to ping (each client via ONE server - the first working one)
	type pingJob struct {
		serverPK string
		clientPK string
	}
	var jobs []pingJob

	for _, serverPK := range workingServers {
		clients, ok := clientsByServer[serverPK]
		if !ok || len(clients) == 0 {
			continue
		}

		for _, clientPK := range clients {
			if clientPK == localPK {
				continue
			}
			mu.Lock()
			alreadyPinged := pingedVisors[clientPK]
			mu.Unlock()
			if alreadyPinged {
				continue
			}
			if !passesFilter(clientPK) {
				continue
			}
			jobs = append(jobs, pingJob{serverPK: serverPK, clientPK: clientPK})
			mu.Lock()
			pingedVisors[clientPK] = true
			mu.Unlock()
		}
	}

	fmt.Printf("Total clients to ping: %d\n", len(jobs))

	// Run pings sequentially
	for _, job := range jobs {
		if ctx.Err() != nil {
			break
		}

		mu.Lock()
		treeEntries = append(treeEntries, dmsgTreeEntry{
			serverPK: job.serverPK,
			remotePK: job.clientPK,
			level:    2,
		})
		mu.Unlock()

		pingDmsg(job.serverPK, job.clientPK, 2)
	}

	// Final render and save
	renderDmsgTree()
	saveState()
	fmt.Println(pterm.Green("DMSG scan complete!"))
}

// truncateErrDmsg truncates error messages for DMSG tree display
func truncateErrDmsg(err string, maxLen int) string {
	if len(err) <= maxLen {
		return err
	}
	if strings.Contains(err, "timeout") {
		return "timeout"
	}
	if strings.Contains(err, "deadline") {
		return "deadline"
	}
	if strings.Contains(err, "refused") {
		return "refused"
	}
	if strings.Contains(err, "EOF") {
		return "EOF"
	}
	return err[:maxLen-2] + ".."
}
