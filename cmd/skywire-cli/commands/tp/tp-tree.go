// Package clitp cmd/skywire-cli/commands/tp/tp-tree.go
package clitp

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/blang/semver/v4"
	"github.com/pterm/pterm"
	"github.com/pterm/pterm/putils"
	"github.com/spf13/cobra"
	"github.com/tidwall/pretty"

	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
	"github.com/skycoin/skywire/deployment"
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
	minVersion  string
	keysOnly    bool
	maxHops     int
	excludeSelf bool
)

func init() {
	treeCmd.Flags().StringVarP(&rootNode, "source", "k", "", "root node ; defaults to visor with most transports")
	treeCmd.Flags().StringVarP(&lastNode, "dest", "d", "", "map route between source and dest")
	treeCmd.Flags().StringVarP(&tpdURL, "tpdurl", "a", deployment.Prod.TransportDiscovery, "transport discovery url")
	treeCmd.Flags().StringVarP(&utURL, "uturl", "w", deployment.Prod.UptimeTracker, "uptime tracker url")
	treeCmd.Flags().BoolVarP(&rawData, "raw", "r", false, "print raw json data")
	treeCmd.Flags().BoolVarP(&refinedData, "pretty", "p", false, "print pretty json data")
	treeCmd.Flags().BoolVarP(&noFilterOnline, "noton", "o", false, "do not filter by online status in UT")
	treeCmd.Flags().BoolVarP(&onlyOnline, "good", "g", false, "do not display transports for offline visors")
	treeCmd.Flags().StringVar(&cacheFileTPD, "cft", os.TempDir()+"/tpd.json", "TPD cache file location")
	treeCmd.Flags().StringVar(&cacheFileUT, "cfu", os.TempDir()+"/ut.json", "UT cache file location.")
	treeCmd.Flags().IntVarP(&cacheFilesAge, "cfa", "m", 5, "update cache files if older than n minutes")
	treeCmd.Flags().IntVarP(&padSpaces, "pad", "P", 15, "padding between tree and tpid")
	treeCmd.Flags().BoolVarP(&isStats, "stats", "s", false, "return only statistics")
	treeCmd.Flags().StringVarP(&minVersion, "version", "v", "", "filter by minimum version (e.g., 1.3.34)")
	treeCmd.Flags().BoolVarP(&keysOnly, "keys", "K", false, "output only reachable public keys (requires -k source)")
	treeCmd.Flags().IntVarP(&maxHops, "hops", "H", 2, "max hops from source for --keys mode (1 or 2)")
	treeCmd.Flags().BoolVarP(&excludeSelf, "no-self", "x", false, "exclude source key from --keys output")
}

var treeCmd = &cobra.Command{
	Use:   "tree",
	Short: "tree map of transports on the skywire network",
	Long:  fmt.Sprintf("display a tree representation of transports from TPD\n\n%v/all-transports\n\nSet cache file location to \"\" to avoid using cache files", deployment.Prod.TransportDiscovery),
	Run: func(cmd *cobra.Command, _ []string) {
		// Validate source/dest flags
		if rootNode != "" {
			if err := rootnode.Set(rootNode); err != nil {
				internal.PrintFatalError(cmd.Flags(), errors.New("invalid source or root node public key"))
			}
			if lastNode != "" {
				if err := lastnode.Set(lastNode); err != nil {
					internal.PrintFatalError(cmd.Flags(), errors.New("invalid dest or last node public key"))
				}
			}
		} else if lastNode != "" {
			internal.PrintFatalError(cmd.Flags(), errors.New("-k, --source <public-key> is missing ; required with -d, --dest <public-key>"))
		}

		// Parse minimum version if specified
		var minSemver semver.Version
		var filterByVersion bool
		if minVersion != "" {
			// Strip leading 'v' if present
			vStr := strings.TrimPrefix(minVersion, "v")
			var err error
			minSemver, err = semver.Parse(vStr)
			if err != nil {
				// Try parsing as major.minor.patch without strict validation
				parts := strings.Split(vStr, ".")
				if len(parts) >= 2 {
					minSemver, err = semver.Parse(vStr + func() string {
						if len(parts) == 2 {
							return ".0"
						}
						return ""
					}())
				}
				if err != nil {
					internal.PrintFatalError(cmd.Flags(), fmt.Errorf("invalid version format: %s", minVersion))
				}
			}
			filterByVersion = true
		}

		// Fetch transport data
		tpsRaw := internal.GetData(cacheFileTPD, tpdURL+"/all-transports", cacheFilesAge)
		if rawData {
			fmt.Print(tpsRaw)
			return
		}
		if refinedData {
			fmt.Println(string(pretty.Color(pretty.Pretty([]byte(tpsRaw)), nil)))
			return
		}

		// Parse transports into Go structs
		var transports []transportEntry
		if err := json.Unmarshal([]byte(tpsRaw), &transports); err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to parse transport data: %w", err))
		}

		// Build adjacency map and count transports per key
		// adjacency[pk] = list of {otherPK, transportID, transportType}
		type neighbor struct {
			pk     string
			tpID   string
			tpType string
		}
		adjacency := make(map[string][]neighbor)
		edgeCount := make(map[string]int)

		for _, tp := range transports {
			edge0, edge1 := tp.Edges[0], tp.Edges[1]
			edgeCount[edge0]++
			edgeCount[edge1]++

			adjacency[edge0] = append(adjacency[edge0], neighbor{pk: edge1, tpID: tp.ID, tpType: tp.Type})
			if edge0 != edge1 { // Don't double-add for self-transports
				adjacency[edge1] = append(adjacency[edge1], neighbor{pk: edge0, tpID: tp.ID, tpType: tp.Type})
			}
		}

		// Fetch and parse uptime data
		onlineSet := make(map[string]bool)          // pk -> true if online
		offlineSet := make(map[string]bool)         // pk -> true if offline (in UT but not online)
		versionMap := make(map[string]string)       // pk -> version
		versionFilteredSet := make(map[string]bool) // pk -> true if passes version filter

		if !noFilterOnline || filterByVersion {
			utsRaw := internal.GetData(cacheFileUT, utURL+"/uptimes?v=v2", cacheFilesAge)
			var uptimes []uptimeEntry
			if err := json.Unmarshal([]byte(utsRaw), &uptimes); err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to parse uptime data: %w", err))
			}

			for _, ut := range uptimes {
				versionMap[ut.PK] = ut.Version
				if ut.Online {
					onlineSet[ut.PK] = true
				} else {
					offlineSet[ut.PK] = true
				}

				// Check version filter
				if filterByVersion && ut.Version != "" {
					vStr := strings.TrimPrefix(ut.Version, "v")
					// Handle various dirty version formats:
					// "1.3.34 dirty", "1.3.34+dirty", "1.3.34-dirty"
					// "1.3.34-0.20260207214945-9c1313404321 dirty" (pseudo-versions)
					// First split by space to remove " dirty" suffix
					vStr = strings.Fields(vStr)[0]
					// Remove +suffix
					vStr = strings.Split(vStr, "+")[0]
					// For pseudo-versions like "1.3.34-0.20260207...", extract base version
					// Try to parse, if fails try extracting major.minor.patch
					if v, err := semver.Parse(vStr); err == nil {
						if v.GTE(minSemver) {
							versionFilteredSet[ut.PK] = true
						}
					} else {
						// Try extracting just major.minor.patch from beginning
						parts := strings.SplitN(vStr, "-", 2)
						if len(parts) > 0 {
							if v, err := semver.Parse(parts[0]); err == nil {
								if v.GTE(minSemver) {
									versionFilteredSet[ut.PK] = true
								}
							}
						}
					}
				}
			}
		}

		// Sort edges by transport count (descending)
		type edgeWithCount struct {
			pk    string
			count int
		}
		var sortedEdges []edgeWithCount
		for pk, count := range edgeCount {
			// Apply filters
			if onlyOnline && !onlineSet[pk] {
				continue
			}
			if filterByVersion && !versionFilteredSet[pk] {
				continue
			}
			sortedEdges = append(sortedEdges, edgeWithCount{pk: pk, count: count})
		}
		sort.Slice(sortedEdges, func(i, j int) bool {
			return sortedEdges[i].count > sortedEdges[j].count
		})

		// Stats mode
		if isStats {
			fmt.Printf("Unique keys in Transport Discovery: %d\n", len(edgeCount))
			fmt.Printf("Count of transports: %d\n", len(transports))

			// Count by type
			typeCounts := make(map[string]int)
			for _, tp := range transports {
				typeCounts[tp.Type]++
			}
			fmt.Println("Types of transports:")
			for t, c := range typeCounts {
				fmt.Printf("  %s: %d\n", t, c)
			}

			if filterByVersion {
				fmt.Printf("Visors matching version >= %s: %d\n", minVersion, len(versionFilteredSet))
			}
			if onlyOnline {
				fmt.Printf("Online visors with transports: %d\n", len(sortedEdges))
			}

			// Show top visors by transport count
			fmt.Println("Top visors by transport count:")
			for i, e := range sortedEdges {
				if i >= 20 {
					break
				}
				ver := versionMap[e.pk]
				if ver == "" {
					ver = "unknown"
				}
				fmt.Printf("  %d: %s (count=%d, version=%s)\n", i+1, e.pk, e.count, ver)
			}
			return
		}

		// Keys-only mode: output reachable PKs from source within maxHops
		if keysOnly {
			if rootNode == "" {
				internal.PrintFatalError(cmd.Flags(), errors.New("--keys requires --source (-k) to specify your visor's public key"))
			}
			rootPK := rootnode.String()

			// Check source exists in transport data
			if _, exists := adjacency[rootPK]; !exists {
				internal.PrintFatalError(cmd.Flags(), errors.New("source visor has no transports in TPD"))
			}

			// BFS to find all reachable nodes within maxHops
			reachable := make(map[string]int) // pk -> hop distance
			if !excludeSelf {
				reachable[rootPK] = 0
			}

			// Queue entries: {pk, currentHop}
			type queueEntry struct {
				pk  string
				hop int
			}
			queue := []queueEntry{{pk: rootPK, hop: 0}}
			visited := map[string]bool{rootPK: true}

			for len(queue) > 0 {
				current := queue[0]
				queue = queue[1:]

				if current.hop >= maxHops {
					continue
				}

				for _, n := range adjacency[current.pk] {
					if visited[n.pk] {
						continue
					}
					// Apply filters
					if onlyOnline && !onlineSet[n.pk] {
						continue
					}
					if filterByVersion && !versionFilteredSet[n.pk] {
						continue
					}

					visited[n.pk] = true
					nextHop := current.hop + 1
					reachable[n.pk] = nextHop
					queue = append(queue, queueEntry{pk: n.pk, hop: nextHop})
				}
			}

			// Output keys (excluding self if requested)
			for pk := range reachable {
				if excludeSelf && pk == rootPK {
					continue
				}
				fmt.Println(pk)
			}
			return
		}

		if len(sortedEdges) == 0 {
			fmt.Println("No visors found matching the specified filters")
			return
		}

		// Move rootNode to front if specified
		if rootNode != "" {
			rootPK := rootnode.String()
			for i, e := range sortedEdges {
				if e.pk == rootPK {
					// Move to front
					copy(sortedEdges[1:i+1], sortedEdges[0:i])
					sortedEdges[0] = e
					break
				}
			}
			if sortedEdges[0].pk != rootPK {
				internal.PrintFatalError(cmd.Flags(), errors.New("specified source or root node public key does not have any transports (or was filtered out)"))
			}
		}

		// Move lastNode to second position if specified
		if lastNode != "" {
			lastPK := lastnode.String()
			for i, e := range sortedEdges {
				if e.pk == lastPK && i > 1 {
					// Move to position 1
					copy(sortedEdges[2:i+1], sortedEdges[1:i])
					sortedEdges[1] = e
					break
				}
			}
			if len(sortedEdges) < 2 || sortedEdges[1].pk != lastPK {
				internal.PrintFatalError(cmd.Flags(), errors.New("specified dest or last node public key does not have any transports (or was filtered out)"))
			}
		}

		// Helper to format node with status colors
		formatNode := func(pk string) string {
			display := pk
			isOnline := onlineSet[pk]
			isOffline := offlineSet[pk]

			if !noFilterOnline {
				if !isOnline && !isOffline {
					display = pterm.Red(pk) // Not in UT
				} else if isOffline {
					display = pterm.Black(pterm.BgRed.Sprint(pk)) // Offline
				}
			}
			if lastNode != "" && pk == lastnode.String() {
				display = pterm.Magenta(pterm.BgBlue.Sprint(pk)) // Dest
			}
			if rootNode != "" && pk == rootnode.String() {
				display = pterm.Blue(pterm.BgMagenta.Sprint(pk)) // Source
			}
			return display
		}

		// Print header
		fmt.Printf("Tree        *Online        %s        %s      %s      %s    TPID                                 Type\n",
			pterm.Black(pterm.BgRed.Sprint("*Offline")),
			pterm.Red("*Not in UT"),
			pterm.Blue(pterm.BgMagenta.Sprint("*source")),
			pterm.Magenta(pterm.BgBlue.Sprint("*dest")))

		if filterByVersion {
			fmt.Printf("Version filter: >= %s\n", minVersion)
		}

		// Build and render trees
		usedKeys := make(map[string]bool)

		var buildTree func(rootPK string) //nolint:staticcheck
		buildTree = func(rootPK string) {
			leveledList := pterm.LeveledList{}
			leveledList = append(leveledList, pterm.LeveledListItem{Level: 0, Text: formatNode(rootPK)})
			usedKeys[rootPK] = true

			var traverse func(pk string, level int) //nolint:staticcheck
			traverse = func(pk string, level int) {
				neighbors := adjacency[pk]
				for _, n := range neighbors {
					if usedKeys[n.pk] {
						continue
					}
					// Apply filters to neighbors
					if onlyOnline && !onlineSet[n.pk] {
						continue
					}
					if filterByVersion && !versionFilteredSet[n.pk] {
						continue
					}

					usedKeys[n.pk] = true

					// Format transport info
					tpInfo := ""
					if level > 0 {
						padding := padSpaces - 4 - level*2
						if padding < 0 {
							padding = 0
						}
						tpInfo = strings.Repeat(" ", padding) + n.tpID + " " + n.tpType
					}

					text := fmt.Sprintf("%s %s", formatNode(n.pk), tpInfo)
					leveledList = append(leveledList, pterm.LeveledListItem{Level: level, Text: text})

					traverse(n.pk, level+1)
				}
			}

			traverse(rootPK, 1)

			if len(leveledList) > 1 {
				pterm.DefaultTree.WithRoot(putils.TreeFromLeveledList(leveledList)).Render() //nolint:errcheck,gosec
			}
		}

		// Build tree starting from first (root) node
		buildTree(sortedEdges[0].pk)

		// Build trees for any disconnected components
		for _, e := range sortedEdges {
			if !usedKeys[e.pk] {
				buildTree(e.pk)
			}
			if lastNode != "" && usedKeys[lastnode.String()] {
				break // Found route to destination
			}
		}

		// If route finding mode, show the route
		if lastNode != "" && rootNode != "" {
			rootPK := rootnode.String()
			lastPK := lastnode.String()

			if !usedKeys[lastPK] {
				pterm.Println(pterm.Red("No route from source to dest"))
				return
			}

			// Check for direct route
			for _, n := range adjacency[rootPK] {
				if n.pk == lastPK {
					pterm.Println(pterm.Red("Direct route:"))
					for _, tp := range transports {
						if (tp.Edges[0] == rootPK && tp.Edges[1] == lastPK) ||
							(tp.Edges[0] == lastPK && tp.Edges[1] == rootPK) {
							b, err := json.MarshalIndent(tp, "", "  ")
							if err == nil {
								fmt.Println(string(pretty.Color(b, nil)))
							}
						}
					}
					return
				}
			}

			// BFS to find shortest path
			type pathNode struct {
				pk   string
				path []neighbor
			}
			queue := []pathNode{{pk: rootPK, path: nil}}
			visited := map[string]bool{rootPK: true}
			var foundPath []neighbor

			for len(queue) > 0 {
				current := queue[0]
				queue = queue[1:]

				for _, n := range adjacency[current.pk] {
					if visited[n.pk] {
						continue
					}
					if filterByVersion && !versionFilteredSet[n.pk] {
						continue
					}

					newPath := append([]neighbor{}, current.path...)
					newPath = append(newPath, n)

					if n.pk == lastPK {
						foundPath = newPath
						break
					}

					visited[n.pk] = true
					queue = append(queue, pathNode{pk: n.pk, path: newPath})
				}
				if foundPath != nil {
					break
				}
			}

			if foundPath != nil {
				pterm.Println(pterm.Red("Forward Route:"))
				prevPK := rootPK
				for _, hop := range foundPath {
					for _, tp := range transports {
						if tp.ID == hop.tpID {
							fmt.Printf("%s -> %s via %s (%s)\n", prevPK, hop.pk, tp.ID, tp.Type)
							prevPK = hop.pk
							break
						}
					}
				}

				pterm.Println(pterm.Red("Reverse Route:"))
				for i := len(foundPath) - 1; i >= 0; i-- {
					hop := foundPath[i]
					nextPK := rootPK
					if i > 0 {
						nextPK = foundPath[i-1].pk
					}
					fmt.Printf("%s -> %s via %s (%s)\n", hop.pk, nextPK, hop.tpID, hop.tpType)
				}
			}
		}

		// Show self-transports if no specific root
		if rootNode == "" && !onlyOnline {
			var selfTransports []transportEntry
			for _, tp := range transports {
				if tp.Edges[0] == tp.Edges[1] {
					if filterByVersion && !versionFilteredSet[tp.Edges[0]] {
						continue
					}
					selfTransports = append(selfTransports, tp)
				}
			}
			if len(selfTransports) > 0 {
				pterm.Println(pterm.Red("Self-transports"))
				for _, tp := range selfTransports {
					fmt.Printf("%s %s%s %s\n", formatNode(tp.Edges[0]), strings.Repeat(" ", padSpaces), tp.ID, tp.Type)
				}
			}
		}
	},
}
