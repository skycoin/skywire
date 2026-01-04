// Package clisd root.go
package clisd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/servicedisc"
)

var (
	sdURL         string
	tpdURL        string
	cacheFileSD   string
	cacheFileTPD  string
	cacheFilesAge int
	country       string
	version       string
	minTransports int
	jsonOutput    bool
)

func init() {
	RootCmd.Flags().StringVarP(&sdURL, "sdurl", "a", deployment.Prod.ServiceDiscovery, "service discovery url")
	RootCmd.Flags().StringVarP(&tpdURL, "tpdurl", "b", deployment.Prod.TransportDiscovery, "transport discovery url")
	RootCmd.Flags().StringVar(&cacheFileSD, "cfs", os.TempDir()+"/networksd.json", "SD cache file location")
	RootCmd.Flags().StringVar(&cacheFileTPD, "cft", os.TempDir()+"/networktpd.json", "TPD cache file location")
	RootCmd.Flags().IntVarP(&cacheFilesAge, "cfa", "m", 5, "update cache files if older than n minutes")
	RootCmd.Flags().StringVarP(&country, "country", "c", "", "filter by country code")
	RootCmd.Flags().StringVarP(&version, "version", "e", "", "filter by version")
	RootCmd.Flags().IntVarP(&minTransports, "min", "n", 0, "filter by minimum transport count")
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

Shows public keys with their services and transport counts by type.`,
		deployment.Prod.ServiceDiscovery,
		deployment.Prod.TransportDiscovery),
	Run: func(cmd *cobra.Command, _ []string) {
		// Fetch service discovery data for all service types
		proxyData := internal.GetData(cacheFileSD+"_proxy", sdURL+"/api/services?type="+servicedisc.ServiceTypeProxy, cacheFilesAge)
		vpnData := internal.GetData(cacheFileSD+"_vpn", sdURL+"/api/services?type="+servicedisc.ServiceTypeVPN, cacheFilesAge)
		visorData := internal.GetData(cacheFileSD+"_visor", sdURL+"/api/services?type="+servicedisc.ServiceTypeVisor, cacheFilesAge)

		// Fetch transport discovery data
		tpdData := internal.GetData(cacheFileTPD, tpdURL+"/all-transports", cacheFilesAge)

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
			PK       string
			Country  string
			Version  string
			Services []string
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
				serviceMap[pk] = &serviceInfo{PK: pk, Country: e.Geo.Country, Version: e.Version}
			}
			serviceMap[pk].Services = append(serviceMap[pk].Services, "visor")
			if serviceMap[pk].Country == "" {
				serviceMap[pk].Country = e.Geo.Country
			}
			if serviceMap[pk].Version == "" {
				serviceMap[pk].Version = e.Version
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

			entries = append(entries, networkEntry{
				PK:       pk,
				Country:  info.Country,
				Version:  info.Version,
				Services: services,
				STCPR:    counts.STCPR,
				SUDPH:    counts.SUDPH,
				DMSG:     counts.DMSG,
				STCP:     counts.STCP,
				Total:    counts.Total,
			})
		}

		// Add keys that have transports but not in service discovery
		for pk, counts := range tpCountMap {
			if serviceMap[pk] != nil {
				continue // Already added
			}

			// Apply filters
			if minTransports > 0 && counts.Total < minTransports {
				continue
			}

			entries = append(entries, networkEntry{
				PK:    pk,
				STCPR: counts.STCPR,
				SUDPH: counts.SUDPH,
				DMSG:  counts.DMSG,
				STCP:  counts.STCP,
				Total: counts.Total,
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

		var b bytes.Buffer
		w := tabwriter.NewWriter(&b, 0, 0, 3, ' ', tabwriter.TabIndent)

		fmt.Fprintln(w, "pk\tcountry\tversion\tservices\tstcpr\tsudph\tdmsg\tstcp\ttotal") //nolint:errcheck,gosec

		for _, e := range entries {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%d\t%d\t%d\t%d\n", //nolint:errcheck,gosec
				e.PK, e.Country, e.Version, e.Services,
				e.STCPR, e.SUDPH, e.DMSG, e.STCP, e.Total)
		}

		w.Flush() //nolint:errcheck,gosec
		fmt.Print(b.String())
	},
}
