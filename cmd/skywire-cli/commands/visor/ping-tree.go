// Package clivisor cmd/skywire-cli/commands/visor/ping-tree.go
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
	"github.com/skycoin/skywire/pkg/visor/rpcgrpc"
)

// Flags for ping tree command (separate from ping graph for cleaner interface)
var (
	treeVersion        string
	treeMaxLevel       int
	treeTimeout        time.Duration
	treeSetupTimeout   time.Duration
	treeTries          int
	treePcktSize       int
	treeCacheTPD       string
	treeCacheUT        string
	treeCacheDMSG      string
	treeCacheAge       int
	treeTPDURL         string
	treeUTURL          string
	treeDMSGURL        string
	treeOnlineOnly     bool
	treeOutput         string
	treeHops           uint
	treeRetries        int
	treeResume         bool
	treeMaxAge         time.Duration
	treeDryRun         bool
	treeDmsgOnly       bool
	treeUseTPS         bool
	treeContinuous     bool
	treeRecheckAge     time.Duration
	treeDmsgPreCheck   bool
	treeDmsgAllServer  bool
	treeRemoveTp       bool
	treeRemoveRemoteTp bool
	treeRemakeTp       bool
	treeRemakeRemoteTp bool
)

func init() {
	// Build tree example for help text
	treeExample := pterm.TreeNode{
		Text: "local_visor",
		Children: []pterm.TreeNode{
			{Text: "remote_visor                                           tpid                                 -     setup     ping  .....ms      avg"},
		},
	}
	treeStr, _ := pterm.DefaultTree.WithRoot(treeExample).Srender() //nolint:errcheck

	pingTreeCmd.Long = `Ping visors via transport routes, displayed as a tree structure.

This command discovers reachable visors through transports and pings each one,
showing per-transport latencies in a hierarchical tree view.

Output format:
` + treeStr + `
  - remote_visor: visor public key (first 16 chars)
  - tpid: transport ID (green=stcpr, blue=sudph)
  - setup: route setup time in ms
  - ping: ping latencies in ms (one per --tries)
  - avg: average ping latency in ms

  Colors: red text = ping failed, red background = setup failed

Use --tps to verify transports via Transport Setup Node (fresher data than TPD).
Use --dmsg-only to ping via DMSG servers instead of transport routes.
Use --dmsg to pre-check visor reachability over DMSG before route ping (skips unreachable).
Use --dmsg-all-servers to ping via all DMSG servers (not just first success).`

	pingTreeCmd.Flags().StringVarP(&treeVersion, "version", "v", "", "filter by minimum version")
	pingTreeCmd.Flags().IntVarP(&treeMaxLevel, "max-level", "l", 0, "maximum hop level (0 = unlimited)")
	pingTreeCmd.Flags().DurationVarP(&treeTimeout, "timeout", "o", 30*time.Second, "timeout per ping attempt")
	pingTreeCmd.Flags().DurationVar(&treeSetupTimeout, "setup-timeout", 30*time.Second, "timeout for route setup phase")
	pingTreeCmd.Flags().IntVarP(&treeTries, "tries", "t", 1, "ping attempts per transport")
	pingTreeCmd.Flags().IntVarP(&treePcktSize, "size", "s", 2, "packet size in KB")
	pingTreeCmd.Flags().StringVar(&treeCacheTPD, "cft", os.TempDir()+"/tpd.json", "TPD cache file location")
	pingTreeCmd.Flags().StringVar(&treeCacheUT, "cfu", os.TempDir()+"/ut.json", "UT cache file location")
	pingTreeCmd.Flags().StringVar(&treeCacheDMSG, "cfd", os.TempDir()+"/dmsg-clients.json", "DMSG clients cache file location")
	pingTreeCmd.Flags().IntVarP(&treeCacheAge, "cfa", "m", 5, "update cache files if older than n minutes")
	pingTreeCmd.Flags().StringVar(&treeTPDURL, "tpdurl", deployment.Prod.TransportDiscovery, "transport discovery URL")
	pingTreeCmd.Flags().StringVar(&treeUTURL, "uturl", deployment.Prod.UptimeTracker, "uptime tracker URL")
	pingTreeCmd.Flags().StringVar(&treeDMSGURL, "dmsgurl", deployment.Prod.DmsgDiscovery, "DMSG discovery URL")
	pingTreeCmd.Flags().BoolVarP(&treeOnlineOnly, "online", "g", false, "only ping visors marked online in UT")
	pingTreeCmd.Flags().StringVarP(&treeOutput, "output", "O", "", "output base filename (writes .json file)")
	pingTreeCmd.Flags().UintVar(&treeHops, "hops", 0, "exact hop level to ping (0 = all levels)")
	pingTreeCmd.Flags().IntVar(&treeRetries, "retries", 1, "retry attempts if ping fails")
	pingTreeCmd.Flags().BoolVarP(&treeResume, "resume", "R", false, "resume from output file if it exists")
	pingTreeCmd.Flags().DurationVar(&treeMaxAge, "max-age", 0, "re-ping entries older than this duration")
	pingTreeCmd.Flags().BoolVar(&treeDryRun, "dry-run", false, "show tree structure without pinging")
	pingTreeCmd.Flags().BoolVar(&treeDmsgOnly, "dmsg-only", false, "ping via DMSG servers instead of routes")
	pingTreeCmd.Flags().BoolVar(&treeDmsgPreCheck, "dmsg", false, "pre-check visor reachability over DMSG before route ping")
	pingTreeCmd.Flags().BoolVar(&treeDmsgAllServer, "dmsg-all-servers", false, "ping via all DMSG servers (not just first success)")
	pingTreeCmd.Flags().BoolVar(&treeUseTPS, "tps", true, "verify/update transports via TPS (default: true)")
	pingTreeCmd.Flags().BoolVar(&treeContinuous, "continuous", false, "run continuously, re-checking and expanding trees")
	pingTreeCmd.Flags().DurationVar(&treeRecheckAge, "recheck-age", 24*time.Hour, "re-ping entries older than this in continuous mode")
	pingTreeCmd.Flags().BoolVar(&treeRemoveTp, "remove-tp", false, "remove local transport if route ping fails")
	pingTreeCmd.Flags().BoolVar(&treeRemoveRemoteTp, "remove-remote-tp", false, "request remote visor to remove transport if route ping fails")
	pingTreeCmd.Flags().BoolVar(&treeRemakeTp, "remake-tp", false, "remake local transport after removing failed one (retry once)")
	pingTreeCmd.Flags().BoolVar(&treeRemakeRemoteTp, "remake-remote-tp", false, "remake transport on remote side after failure (retry once)")

	pingCmd.AddCommand(pingTreeCmd)
}

var pingTreeCmd = &cobra.Command{
	Use:   "tree",
	Short: "Ping visors via transport routes (tree view)",
	Run: func(cmd *cobra.Command, _ []string) {
		// Copy flags to the shared graph variables for compatibility with existing code
		graphVersion = treeVersion
		graphMaxLevel = treeMaxLevel
		graphTimeout = treeTimeout
		graphSetupTimeout = treeSetupTimeout
		graphTries = treeTries
		graphPcktSize = treePcktSize
		graphCacheTPD = treeCacheTPD
		graphCacheUT = treeCacheUT
		graphCacheDMSG = treeCacheDMSG
		graphCacheAge = treeCacheAge
		graphTPDURL = treeTPDURL
		graphUTURL = treeUTURL
		graphDMSGURL = treeDMSGURL
		graphOnlineOnly = treeOnlineOnly
		graphOutput = treeOutput
		graphHops = treeHops
		graphRetries = treeRetries
		graphResume = treeResume
		graphMaxAge = treeMaxAge
		graphDryRun = treeDryRun
		graphDmsgOnly = treeDmsgOnly
		graphDmsgPreCheck = treeDmsgPreCheck
		graphDmsgAllServers = treeDmsgAllServer
		graphTreeView = true // Always tree mode
		graphUseTPS = treeUseTPS
		graphContinuous = treeContinuous
		graphRecheckAge = treeRecheckAge
		graphRemoveTp = treeRemoveTp
		graphRemoveRemoteTp = treeRemoveRemoteTp
		graphRemakeTp = treeRemakeTp
		graphRemakeRemoteTp = treeRemakeRemoteTp

		// Set up signal handling for graceful shutdown
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
		go func() {
			<-sigChan
			fmt.Println("\nReceived interrupt, saving state...")
			cancel()
		}()

		// Create RPC client
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}

		// Create gRPC client for ping operations
		grpcClient, err := rpcgrpc.NewPingClient(clirpc.Addr)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to connect to gRPC server: %w", err))
		}
		defer grpcClient.Close() //nolint:errcheck

		// Get local visor info
		overview, err := rpcClient.Overview()
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to get visor overview: %w", err))
		}
		localPK := overview.PubKey.String()

		// Fetch TPD data for initial adjacency map
		tpdRaw := internal.GetData(graphCacheTPD, graphTPDURL+"/all-transports", graphCacheAge)
		var transports []transportEntry
		if err := json.Unmarshal([]byte(tpdRaw), &transports); err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to parse TPD data: %w", err))
		}

		// Build adjacency map from TPD
		adjacency := make(map[string][]treeNeighbor)
		for _, tp := range transports {
			edge0, edge1 := tp.Edges[0], tp.Edges[1]
			adjacency[edge0] = append(adjacency[edge0], treeNeighbor{pk: edge1, tpID: tp.ID, tpType: tp.Type})
			if edge0 != edge1 {
				adjacency[edge1] = append(adjacency[edge1], treeNeighbor{pk: edge0, tpID: tp.ID, tpType: tp.Type})
			}
		}

		// Get local transports to supplement TPD data
		localTransports, err := rpcClient.Transports(nil, nil, false)
		if err != nil {
			fmt.Printf("Warning: failed to get local transports: %v\n", err)
		}

		// Add local transports not in TPD
		for _, tp := range localTransports {
			tpID := tp.ID.String()
			remotePK := tp.Remote.String()
			tpType := string(tp.Type)

			// Check if already exists
			found := false
			for _, n := range adjacency[localPK] {
				if n.tpID == tpID {
					found = true
					break
				}
			}
			if !found {
				adjacency[localPK] = append(adjacency[localPK], treeNeighbor{pk: remotePK, tpID: tpID, tpType: tpType})
				// Add reverse direction too
				reverseFound := false
				for _, n := range adjacency[remotePK] {
					if n.tpID == tpID {
						reverseFound = true
						break
					}
				}
				if !reverseFound {
					adjacency[remotePK] = append(adjacency[remotePK], treeNeighbor{pk: localPK, tpID: tpID, tpType: tpType})
				}
			}
		}

		// Fetch UT data for online/version filtering
		utRaw := internal.GetData(graphCacheUT, graphUTURL+"/uptimes?v=v2", graphCacheAge)
		var utEntries []uptimeEntry
		_ = json.Unmarshal([]byte(utRaw), &utEntries) //nolint:errcheck

		// Build filter sets
		onlineSet := make(map[string]bool)
		versionFilteredSet := make(map[string]bool)
		filterByVersion := graphVersion != ""

		var minVersion semver.Version
		if filterByVersion {
			var err error
			// Strip "v" prefix if present for semver parsing
			cleanVersion := strings.TrimPrefix(graphVersion, "v")
			minVersion, err = semver.Parse(cleanVersion)
			if err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("invalid version format: %w", err))
			}
		}

		for _, entry := range utEntries {
			if entry.Online {
				onlineSet[entry.PK] = true
			}
			if filterByVersion && entry.Version != "" {
				cleanVer := entry.Version
				// Strip "v" prefix
				cleanVer = strings.TrimPrefix(cleanVer, "v")
				// Remove dirty suffix variations for semver parsing
				cleanVer = strings.Fields(cleanVer)[0]         // "1.3.34 dirty" -> "1.3.34"
				cleanVer = strings.Split(cleanVer, "+")[0]     // "1.3.34+dirty" -> "1.3.34"
				cleanVer = strings.SplitN(cleanVer, "-", 2)[0] // "1.3.34-dirty" -> "1.3.34"
				if v, err := semver.Parse(cleanVer); err == nil && v.GTE(minVersion) {
					versionFilteredSet[entry.PK] = true
				}
			}
		}

		// Print filter stats
		if filterByVersion {
			fmt.Printf("Version filter >= %s: %d visors in UT match\n", graphVersion, len(versionFilteredSet))
		}

		// Build passes filter function
		passesFilter := func(pk string) bool {
			if graphOnlineOnly && !onlineSet[pk] {
				return false
			}
			if filterByVersion && !versionFilteredSet[pk] {
				return false
			}
			return true
		}

		// Run tree view mode
		if graphDmsgOnly {
			// DMSG tree mode
			runDmsgTreeViewMode(ctx, grpcClient, rpcClient, localPK, onlineSet, versionFilteredSet, passesFilter)
		} else {
			// Route tree mode
			runTreeViewMode(ctx, grpcClient, rpcClient, localPK, adjacency, localTransports, passesFilter)
		}
	},
}
