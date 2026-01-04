// Package clitp cmd/skywire-cli/commands/tp/tp.go
package clitp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/servicedisc"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport"
	types "github.com/skycoin/skywire/pkg/transport/types"
	"github.com/skycoin/skywire/pkg/visor"
)

var (
	filterTypes   []string
	filterPubKeys []string
	showLogs      bool
	showMore      bool
	logger        = logging.MustGetLogger("skywire-cli")
	tpTypes       bool
	utURL         string
	sdURL         string
	// RootCmd is tpCmd
	RootCmd = tpCmd
)

func init() {
	tpCmd.Flags().SortFlags = false
	addTpCmd.Flags().SortFlags = false
	rmTpCmd.Flags().SortFlags = false
	discTpCmd.Flags().SortFlags = false
	treeCmd.Flags().SortFlags = false
	tpCmd.AddCommand(
		addTpCmd,
		rmTpCmd,
		discTpCmd,
		treeCmd,
		visorListCmd,
		networkCmd,
	)
	tpCmd.Flags().StringSliceVarP(&filterTypes, "types", "t", filterTypes, "show transport(s) type(s) comma-separated")
	tpCmd.Flags().StringSliceVarP(&filterPubKeys, "pks", "p", filterPubKeys, "show transport(s) for public key(s) comma-separated")
	tpCmd.Flags().BoolVarP(&showLogs, "logs", "l", true, "show transport logs")
	tpCmd.Flags().BoolVarP(&showMore, "more", "m", false, "show more info")
	tpCmd.Flags().StringVar(&cacheFileUT, "cfu", os.TempDir()+"/ut.json", "UT cache file location.")
	tpCmd.Flags().StringVar(&cacheFileSDProxy, "cfsp", os.TempDir()+"/proxysd.json", "SD cache file location")
	tpCmd.Flags().StringVar(&cacheFileSDVPN, "cfsv", os.TempDir()+"/vpnsd.json", "SD cache file location")
	tpCmd.Flags().StringVar(&cacheFileSDVisor, "cfsvisor", os.TempDir()+"/visorsd.json", "SD cache file location")
	tpCmd.Flags().StringVarP(&sdURL, "sdurl", "a", deployment.Prod.ServiceDiscovery, "service discovery url")
	tpCmd.Flags().StringVarP(&utURL, "uturl", "w", deployment.Prod.UptimeTracker, "uptime tracker url")
	tpCmd.Flags().StringVarP(&tpID, "id", "i", "", "display transport matching ID")
	tpCmd.Flags().BoolVarP(&tpTypes, "tptypes", "u", false, "display transport types used by the local visor")
	tpCmd.Flags().StringVar(&clirpc.Addr, "rpc", "localhost:3435", "RPC server address")
}

// RootCmd contains commands that interact with the skywire-visor
var tpCmd = &cobra.Command{
	Use:   "tp",
	Short: "View and manage transports",
	Long: `Display and manage transports of the local visor

	Transports are bidirectional communication protocols
	used between two Skywire Visors (or Transport Edges)

	Each Transport is represented as a unique 16 byte (128 bit)
	UUID value called the Transport ID
	and has a Transport Type that identifies
	a specific implementation of the Transport.

	Types: stcp stcpr sudph dmsg
`,
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}

		if tpTypes {
			types, err := rpcClient.TransportTypes()
			internal.Catch(cmd.Flags(), err)
			internal.PrintOutput(cmd.Flags(), types, fmt.Sprintln(strings.Join(types, "\n")))
			return
		}

		if tpID != "" {
			tpid := internal.ParseUUID(cmd.Flags(), "transport-id", tpID)
			tp, err := rpcClient.Transport(tpid)
			internal.Catch(cmd.Flags(), err)
			PrintTransports(cmd.Flags(), tp)
			return
		}

		var pks cipher.PubKeys
		if filterPubKeys != nil {
			internal.Catch(cmd.Flags(), pks.Set(strings.Join(filterPubKeys, ",")))
		}
		transports, err := rpcClient.Transports(filterTypes, pks, showLogs)
		internal.Catch(cmd.Flags(), err)

		if showMore {
			utData = internal.GetData(cacheFileUT, utURL+"/uptimes?v=v2", cacheFilesAge)
			proxyData = internal.GetData(cacheFileSDProxy, sdURL+"/api/services?type="+servicedisc.ServiceTypeProxy, cacheFilesAge)
			vpnData = internal.GetData(cacheFileSDVPN, sdURL+"/api/services?type="+servicedisc.ServiceTypeVPN, cacheFilesAge)
		}

		PrintTransports(cmd.Flags(), transports...)
	},
}

var (
	utData    string
	vpnData   string
	proxyData string
	visorData string
)

// PrintTransports prints transports used by the visor
func PrintTransports(cmdFlags *pflag.FlagSet, tps ...*visor.TransportSummary) {
	sortTransports(tps...)

	var versionsByPK map[string]string
	if showMore && len(utData) > 0 {
		type uptimeEntry struct {
			PK      string `json:"pk"`
			Version string `json:"version"`
		}
		var utEntries []uptimeEntry
		err := json.Unmarshal([]byte(utData), &utEntries)
		internal.Catch(cmdFlags, err)

		versionsByPK = make(map[string]string, len(utEntries))
		for _, e := range utEntries {
			versionsByPK[e.PK] = e.Version
		}
	}

	type geoInfo struct {
		Country string `json:"country"`
	}

	type serviceEntry struct {
		Address string  `json:"address"`
		Geo     geoInfo `json:"geo"`
	}

	proxyByPK := make(map[string]string)
	vpnByPK := make(map[string]string)
	visorByPK := make(map[string]string)

	if showMore && len(proxyData) > 0 {
		var proxyEntries []serviceEntry
		err := json.Unmarshal([]byte(proxyData), &proxyEntries)
		internal.Catch(cmdFlags, err)
		for _, e := range proxyEntries {
			pk := strings.SplitN(e.Address, ":", 2)[0]
			proxyByPK[pk] = e.Geo.Country
		}
	}

	if showMore && len(vpnData) > 0 {
		var vpnEntries []serviceEntry
		err := json.Unmarshal([]byte(vpnData), &vpnEntries)
		internal.Catch(cmdFlags, err)
		for _, e := range vpnEntries {
			pk := strings.SplitN(e.Address, ":", 2)[0]
			vpnByPK[pk] = e.Geo.Country
		}
	}

	if showMore && len(visorData) > 0 {
		var visorEntries []serviceEntry
		err := json.Unmarshal([]byte(visorData), &visorEntries)
		internal.Catch(cmdFlags, err)
		for _, e := range visorEntries {
			pk := strings.SplitN(e.Address, ":", 2)[0]
			visorByPK[pk] = e.Geo.Country
		}
	}

	var b bytes.Buffer
	w := tabwriter.NewWriter(&b, 0, 0, 5, ' ', tabwriter.TabIndent)

	if showMore {
		_, err := fmt.Fprintln(w, "type\tid\tremote_pk\tmode\tlabel\tversion\tcountry\tservices")
		internal.Catch(cmdFlags, err)
	} else {
		_, err := fmt.Fprintln(w, "type\tid\tremote_pk\tmode\tlabel")
		internal.Catch(cmdFlags, err)
	}

	type outputTP struct {
		Type     types.Type      `json:"type"`
		ID       uuid.UUID       `json:"id"`
		Remote   cipher.PubKey   `json:"remote_pk"`
		TpMode   string          `json:"mode"`
		Label    transport.Label `json:"label"`
		Version  string          `json:"version,omitempty"`
		Country  string          `json:"country,omitempty"`
		Services string          `json:"services,omitempty"`
	}

	var outputTPS []outputTP
	for _, tp := range tps {
		if tp == nil {
			continue
		}
		tpMode := "regular"
		if tp.IsSetup {
			tpMode = "setup"
		}
		tp.Log = nil

		version := ""
		if showMore && versionsByPK != nil {
			version = versionsByPK[tp.Remote.String()]
		}

		var country, services string
		pk := tp.Remote.String()
		proxyCountry, inProxy := proxyByPK[pk]
		vpnCountry, inVPN := vpnByPK[pk]
		visorCountry, inVisor := visorByPK[pk]

		// Build services list
		var svcList []string
		var countries []string

		if inProxy {
			svcList = append(svcList, "proxy")
			countries = append(countries, proxyCountry)
		}
		if inVPN {
			svcList = append(svcList, "vpn")
			countries = append(countries, vpnCountry)
		}
		if inVisor {
			svcList = append(svcList, "visor")
			countries = append(countries, visorCountry)
		}

		if len(svcList) > 0 {
			services = strings.Join(svcList, ",")
			// Deduplicate countries
			countryMap := make(map[string]bool)
			var uniqueCountries []string
			for _, c := range countries {
				if c != "" && !countryMap[c] {
					countryMap[c] = true
					uniqueCountries = append(uniqueCountries, c)
				}
			}
			country = strings.Join(uniqueCountries, "/")
		}

		oTP := outputTP{
			Type:     tp.Type,
			ID:       tp.ID,
			Remote:   tp.Remote,
			TpMode:   tpMode,
			Label:    tp.Label,
			Version:  version,
			Country:  country,
			Services: services,
		}
		outputTPS = append(outputTPS, oTP)

		if showMore {
			_, err := fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				tp.Type, tp.ID, tp.Remote, tpMode, tp.Label, version, country, services)
			internal.Catch(cmdFlags, err)
		} else {
			_, err := fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
				tp.Type, tp.ID, tp.Remote, tpMode, tp.Label)
			internal.Catch(cmdFlags, err)
		}
	}

	internal.Catch(cmdFlags, w.Flush())
	internal.PrintOutput(cmdFlags, outputTPS, b.String())
}

func sortTransports(tps ...*visor.TransportSummary) {
	sort.Slice(tps, func(i, j int) bool {
		return tps[i].ID.String() < tps[j].ID.String()
	})
}
