// Package clisd root.go
package clisd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/pterm/pterm"
	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/servicedisc"
)

// isTestEnv checks if test environment is enabled via SKYWIRETEST env var
func isTestEnv() bool {
	return os.Getenv("SKYWIRETEST") == "1"
}

// cacheDirPath returns a cache directory path based on the service URL host
func cacheDirPath(serviceURL string) string {
	u, err := url.Parse(serviceURL)
	if err != nil || u.Host == "" {
		return ""
	}
	return filepath.Join(os.TempDir(), u.Host)
}

// cacheFile returns the full cache file path for a given URL.
// If cacheDir is empty, returns "" (disables caching).
// Creates the cache directory if it doesn't exist.
// Generates simple, descriptive filenames (e.g., "visor.json", "uptimes.json").
func cacheFile(cacheDir, fullURL string) string {
	if cacheDir == "" {
		return ""
	}

	u, err := url.Parse(fullURL)
	if err != nil {
		return ""
	}

	// Create cache directory if it doesn't exist
	if err := os.MkdirAll(cacheDir, 0750); err != nil {
		return ""
	}

	// Extract a simple, meaningful name from the URL
	var name string

	// Check for service type in query (e.g., ?type=visor -> visor.json)
	if typeVal := u.Query().Get("type"); typeVal != "" {
		name = typeVal
	} else {
		// Use the last path segment (e.g., /all-transports -> all-transports.json)
		path := strings.TrimSuffix(u.Path, "/")
		if idx := strings.LastIndex(path, "/"); idx >= 0 {
			name = path[idx+1:]
		} else {
			name = strings.TrimPrefix(path, "/")
		}
	}

	if name == "" {
		name = "cache"
	}

	return filepath.Join(cacheDir, name+".json")
}

// getDeployment returns the appropriate deployment config based on test env
func getDeployment() deployment.Services {
	if isTestEnv() {
		return deployment.Test
	}
	return deployment.Prod
}

var (
	sdURL          string
	tpdURL         string
	utURL          string
	cacheDirSD     string
	cacheDirTPD    string
	cacheDirUT     string
	cacheFilesAge  int
	country        string
	version        string
	minTransports  int
	noFilterOnline bool
	jsonOutput     bool
	testEnv        bool
)

func init() {
	dep := getDeployment()
	defaultTestEnv := isTestEnv()

	RootCmd.Flags().BoolVar(&testEnv, "testenv", defaultTestEnv, "use test deployment")
	RootCmd.Flags().StringVarP(&sdURL, "sdurl", "a", dep.ServiceDiscovery, "service discovery url")
	RootCmd.Flags().StringVarP(&tpdURL, "tpdurl", "b", dep.TransportDiscovery, "transport discovery url")
	RootCmd.Flags().StringVarP(&utURL, "uturl", "w", dep.UptimeTracker, "uptime tracker url")
	RootCmd.Flags().StringVar(&cacheDirSD, "cds", cacheDirPath(dep.ServiceDiscovery), "SD cache dir (\"\" to disable)")
	RootCmd.Flags().StringVar(&cacheDirTPD, "cdt", cacheDirPath(dep.TransportDiscovery), "TPD cache dir (\"\" to disable)")
	RootCmd.Flags().StringVar(&cacheDirUT, "cdu", cacheDirPath(dep.UptimeTracker), "UT cache dir (\"\" to disable)")
	RootCmd.Flags().IntVarP(&cacheFilesAge, "cfa", "m", 5, "update cache files if older than n minutes")
	RootCmd.Flags().StringVarP(&country, "country", "c", "", "filter by country code")
	RootCmd.Flags().StringVarP(&version, "version", "e", "", "filter by version")
	RootCmd.Flags().IntVarP(&minTransports, "min", "n", 0, "filter by minimum transport count")
	RootCmd.Flags().BoolVarP(&noFilterOnline, "noton", "o", false, "do not filter by online status in UT")
	RootCmd.Flags().BoolVar(&jsonOutput, internal.JSONString, false, "print output in json")
}

// RootCmd contains commands that interact with service discovery
var RootCmd = &cobra.Command{
	Use:   "sd",
	Short: "Service discovery network statistics",
	Long: fmt.Sprintf(`Display combined service discovery and transport statistics

Combines data from:
- Service Discovery: %v/api/services
- Transport Discovery: %v/all-transports

Shows public keys with their services and transport counts by type.

Use --testenv or SKYWIRETEST=1 to use test deployment services.`,
		getDeployment().ServiceDiscovery,
		getDeployment().TransportDiscovery),
	Run: func(cmd *cobra.Command, _ []string) {
		// Handle --testenv flag: override URLs and cache dirs that weren't explicitly set
		if testEnv && !isTestEnv() {
			// --testenv was specified at runtime (not via SKYWIRETEST env)
			// Override values that weren't explicitly changed by user
			if !cmd.Flags().Changed("sdurl") {
				sdURL = deployment.Test.ServiceDiscovery
			}
			if !cmd.Flags().Changed("tpdurl") {
				tpdURL = deployment.Test.TransportDiscovery
			}
			if !cmd.Flags().Changed("uturl") {
				utURL = deployment.Test.UptimeTracker
			}
			if !cmd.Flags().Changed("cds") {
				cacheDirSD = cacheDirPath(deployment.Test.ServiceDiscovery)
			}
			if !cmd.Flags().Changed("cdt") {
				cacheDirTPD = cacheDirPath(deployment.Test.TransportDiscovery)
			}
			if !cmd.Flags().Changed("cdu") {
				cacheDirUT = cacheDirPath(deployment.Test.UptimeTracker)
			}
		}

		// Build full URLs with endpoints
		sdProxyURL := sdURL + "/api/services?type=" + servicedisc.ServiceTypeProxy
		sdVpnURL := sdURL + "/api/services?type=" + servicedisc.ServiceTypeVPN
		sdVisorURL := sdURL + "/api/services?type=" + servicedisc.ServiceTypeVisor
		tpdFullURL := tpdURL + "/all-transports"
		utFullURL := utURL + "/uptimes?v=v2"

		// Fetch service discovery data for all service types
		proxyData := internal.GetData(cacheFile(cacheDirSD, sdProxyURL), sdProxyURL, cacheFilesAge)
		vpnData := internal.GetData(cacheFile(cacheDirSD, sdVpnURL), sdVpnURL, cacheFilesAge)
		visorData := internal.GetData(cacheFile(cacheDirSD, sdVisorURL), sdVisorURL, cacheFilesAge)

		// Fetch transport discovery data
		tpdData := internal.GetData(cacheFile(cacheDirTPD, tpdFullURL), tpdFullURL, cacheFilesAge)

		// Parse service discovery data
		type sdEntry struct {
			Address string `json:"address"`
			Geo     struct {
				Country string `json:"country"`
			} `json:"geo"`
			Version string `json:"version"`
		}

		var proxyEntries, vpnEntries, visorEntries []sdEntry
		json.Unmarshal([]byte(proxyData), &proxyEntries) //nolint:errcheck,gosec
		json.Unmarshal([]byte(vpnData), &vpnEntries)     //nolint:errcheck,gosec
		json.Unmarshal([]byte(visorData), &visorEntries) //nolint:errcheck,gosec

		// Build service map by public key
		type serviceInfo struct {
			PK        string
			DisplayPK string
			Country   string
			Version   string
			Services  []string
		}
		serviceMap := make(map[string]*serviceInfo)

		for _, e := range proxyEntries {
			pk := strings.Split(e.Address, ":")[0]
			if serviceMap[pk] == nil {
				serviceMap[pk] = &serviceInfo{PK: pk, Country: e.Geo.Country, Version: e.Version}
			}
			serviceMap[pk].Services = append(serviceMap[pk].Services, "proxy")
		}

		for _, e := range vpnEntries {
			pk := strings.Split(e.Address, ":")[0]
			if serviceMap[pk] == nil {
				serviceMap[pk] = &serviceInfo{PK: pk, Country: e.Geo.Country, Version: e.Version}
			}
			serviceMap[pk].Services = append(serviceMap[pk].Services, "vpn")
			if serviceMap[pk].Country == "" {
				serviceMap[pk].Country = e.Geo.Country
			}
			if serviceMap[pk].Version == "" {
				serviceMap[pk].Version = e.Version
			}
		}

		for _, e := range visorEntries {
			pk := strings.Split(e.Address, ":")[0]
			if serviceMap[pk] == nil {
				serviceMap[pk] = &serviceInfo{PK: pk, DisplayPK: e.Address, Country: e.Geo.Country, Version: e.Version}
			} else if serviceMap[pk].DisplayPK == "" {
				serviceMap[pk].DisplayPK = e.Address
			}
			serviceMap[pk].Services = append(serviceMap[pk].Services, "visor")
			if serviceMap[pk].Country == "" {
				serviceMap[pk].Country = e.Geo.Country
			}
			if serviceMap[pk].Version == "" {
				serviceMap[pk].Version = e.Version
			}
		}

		// Always fetch UT data for status tracking
		utData := internal.GetData(cacheFile(cacheDirUT, utFullURL), utFullURL, cacheFilesAge)

		type utEntry struct {
			PK string `json:"pk"`
			On bool   `json:"on"`
		}

		var utEntries []utEntry
		json.Unmarshal([]byte(utData), &utEntries) //nolint:errcheck,gosec

		// Build online/offline status maps
		// utStatus: "online", "offline", or "" (not in UT)
		utStatus := make(map[string]string)
		for _, ut := range utEntries {
			if ut.On {
				utStatus[ut.PK] = "online"
			} else {
				utStatus[ut.PK] = "offline"
			}
		}

		// Filter by online status if requested
		if !noFilterOnline {
			for pk := range serviceMap {
				if utStatus[pk] != "online" {
					delete(serviceMap, pk)
				}
			}
		}

		// Parse transport data and count transports per key
		type tpEntry struct {
			Edges []string `json:"edges"`
			Type  string   `json:"type"`
		}

		var tpEntries []tpEntry
		json.Unmarshal([]byte(tpdData), &tpEntries) //nolint:errcheck,gosec

		// Count transports per key by type
		type transportCounts struct {
			STCPR int
			SUDPH int
			DMSG  int
			STCP  int
			Total int
		}
		tpCountMap := make(map[string]*transportCounts)

		for _, tp := range tpEntries {
			for _, edge := range tp.Edges {
				if tpCountMap[edge] == nil {
					tpCountMap[edge] = &transportCounts{}
				}
				switch tp.Type {
				case "stcpr":
					tpCountMap[edge].STCPR++
				case "sudph":
					tpCountMap[edge].SUDPH++
				case "dmsg":
					tpCountMap[edge].DMSG++
				case "stcp":
					tpCountMap[edge].STCP++
				}
				tpCountMap[edge].Total++
			}
		}

		// Combine data
		type networkEntry struct {
			PK       string `json:"pk"`
			Country  string `json:"country,omitempty"`
			Version  string `json:"version,omitempty"`
			Services string `json:"services,omitempty"`
			STCPR    int    `json:"stcpr"`
			SUDPH    int    `json:"sudph"`
			DMSG     int    `json:"dmsg"`
			STCP     int    `json:"stcp"`
			Total    int    `json:"total"`
			UTStatus string `json:"ut_status,omitempty"` // "online", "offline", or "" (not in UT)
		}

		var entries []networkEntry

		// Add all keys from service discovery
		for pk, info := range serviceMap {
			services := strings.Join(info.Services, ",")
			counts := tpCountMap[pk]
			if counts == nil {
				counts = &transportCounts{}
			}

			// Apply filters
			if country != "" && info.Country != country {
				continue
			}
			if version != "" && info.Version != version {
				continue
			}
			if minTransports > 0 && counts.Total < minTransports {
				continue
			}

			displayPK := pk
			if info.DisplayPK != "" {
				displayPK = info.DisplayPK
			}

			entries = append(entries, networkEntry{
				PK:       displayPK,
				Country:  info.Country,
				Version:  info.Version,
				Services: services,
				STCPR:    counts.STCPR,
				SUDPH:    counts.SUDPH,
				DMSG:     counts.DMSG,
				STCP:     counts.STCP,
				Total:    counts.Total,
				UTStatus: utStatus[pk],
			})
		}

		// Add keys that have transports but not in service discovery
		for pk, counts := range tpCountMap {
			if serviceMap[pk] != nil {
				continue // Already added
			}

			// Filter by online status if requested (for non-SD entries too)
			if !noFilterOnline && utStatus[pk] != "online" {
				continue
			}

			// Apply filters
			if minTransports > 0 && counts.Total < minTransports {
				continue
			}

			entries = append(entries, networkEntry{
				PK:       pk,
				STCPR:    counts.STCPR,
				SUDPH:    counts.SUDPH,
				DMSG:     counts.DMSG,
				STCP:     counts.STCP,
				Total:    counts.Total,
				UTStatus: utStatus[pk],
			})
		}

		// Sort by total transport count (descending)
		sort.Slice(entries, func(i, j int) bool {
			return entries[i].Total > entries[j].Total
		})

		// Output
		if jsonOutput {
			internal.PrintOutput(cmd.Flags(), entries, "")
			return
		}

		// Print legend
		fmt.Printf("Legend: %s %s %s\n\n",
			pterm.Red("*offline"),
			pterm.BgRed.Sprint(pterm.White("*not in UT")),
			pterm.BgRed.Sprint(pterm.Black("*online <2 transports")))

		// Use tabwriter for alignment
		var b bytes.Buffer
		w := tabwriter.NewWriter(&b, 0, 0, 3, ' ', tabwriter.TabIndent)

		// Write all entries to tabwriter first
		fmt.Fprintln(w, "pk\tcountry\tversion\tservices\tstcpr\tsudph\tdmsg\tstcp\ttotal") //nolint:errcheck,gosec

		for _, e := range entries {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%d\t%d\t%d\t%d\n", //nolint:errcheck,gosec
				e.PK, e.Country, e.Version, e.Services,
				e.STCPR, e.SUDPH, e.DMSG, e.STCP, e.Total)
		}

		w.Flush() //nolint:errcheck,gosec

		// Now print each line with color coding
		lines := strings.Split(b.String(), "\n")
		for i, line := range lines {
			if line == "" {
				continue
			}

			// First line is header - print without color
			if i == 0 {
				fmt.Println(line)
				continue
			}

			// Get the corresponding entry (i-1 because of header)
			entryIdx := i - 1
			if entryIdx >= len(entries) {
				fmt.Println(line)
				continue
			}

			e := entries[entryIdx]
			realTransports := e.STCPR + e.SUDPH // Only count stcpr and sudph, not dmsg

			// Color coding:
			// - Red text: offline visors
			// - White text on red background: visors not in UT
			// - Black text on red background: online but fewer than 2 stcpr+sudph transports
			switch {
			case e.UTStatus == "":
				// Not in UT: white text on red background
				fmt.Println(pterm.BgRed.Sprint(pterm.White(line)))
			case e.UTStatus == "offline":
				// Offline: red text
				fmt.Println(pterm.Red(line))
			case e.UTStatus == "online" && realTransports < 2:
				// Online but fewer than 2 real transports: black text on red background
				fmt.Println(pterm.BgRed.Sprint(pterm.Black(line)))
			default:
				// Normal: no color
				fmt.Println(line)
			}
		}
	},
}
