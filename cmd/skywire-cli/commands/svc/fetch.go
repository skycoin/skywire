// Package clisvc cmd/skywire-cli/commands/svc/fetch.go
package clisvc

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"

	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
	"github.com/skycoin/skywire/deployment"
)

func init() {
	tpdCmd.PersistentFlags().BoolVar(&directQuery, "direct", false, "query directly instead of via visor RPC")
	tpdCmd.PersistentFlags().StringVar(&clirpc.Addr, "rpc", clirpc.DefaultRPCAddr, "RPC server address (env: SKYWIRE_RPC)")

	arCmd.PersistentFlags().BoolVar(&directQuery, "direct", false, "query directly instead of via visor RPC")
	arCmd.PersistentFlags().StringVar(&clirpc.Addr, "rpc", clirpc.DefaultRPCAddr, "RPC server address (env: SKYWIRE_RPC)")

	nmCmd.PersistentFlags().BoolVar(&directQuery, "direct", false, "query directly instead of via visor RPC")
	nmCmd.PersistentFlags().StringVar(&clirpc.Addr, "rpc", clirpc.DefaultRPCAddr, "RPC server address (env: SKYWIRE_RPC)")

	dmsgdCmd.PersistentFlags().BoolVar(&directQuery, "direct", false, "query directly instead of via visor RPC")
	dmsgdCmd.PersistentFlags().StringVar(&clirpc.Addr, "rpc", clirpc.DefaultRPCAddr, "RPC server address (env: SKYWIRE_RPC)")

	RootCmd.AddCommand(tpdCmd, dmsgdCmd, arCmd, nmCmd)
}

// fetchViaVisorOrDirect fetches data from a service, preferring visor RPC.
func fetchViaVisorOrDirect(cmd *cobra.Command, service, path, directURL string) ([]byte, error) {
	if !directQuery {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err == nil {
			data, err := rpcClient.FetchServiceData(service, path)
			if err == nil {
				return data, nil
			}
		}
	}

	// Direct fallback
	url := strings.TrimSuffix(directURL, "/") + path
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url) //nolint:gosec
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close() //nolint:errcheck
	return io.ReadAll(resp.Body)
}

func prettyJSON(data []byte) string {
	var v interface{}
	if json.Unmarshal(data, &v) == nil {
		pretty, err := json.MarshalIndent(v, "", "  ")
		if err == nil {
			return string(pretty)
		}
	}
	return string(data)
}

// --- TPD subcommands ---

var tpdCmd = &cobra.Command{
	Use:   "tpd",
	Short: "Transport Discovery endpoints",
	Long:  "\n    Query Transport Discovery service endpoints",
}

func init() {
	tpdCmd.AddCommand(
		tpdStatsCmd,
		tpdPerKeyStatsCmd,
		tpdVisorStatsCmd,
		tpdVersionsCmd,
		tpdVersionsPKCmd,
		tpdBandwidthCmd,
		tpdBandwidthTpCmd,
		tpdMetricsVisorCmd,
		tpdMetricsTpCmd,
	)
}

var tpdStatsCmd = &cobra.Command{
	Use:   "stats",
	Short: "Network-wide transport statistics",
	Run: func(cmd *cobra.Command, _ []string) {
		data, err := fetchViaVisorOrDirect(cmd, "tpd", "/all-transports/stats", deployment.Prod.TransportDiscovery)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		fmt.Println(prettyJSON(data))
	},
}

var tpdVersionsCmd = &cobra.Command{
	Use:   "versions",
	Short: "Version statistics from transport discovery",
	Run: func(cmd *cobra.Command, _ []string) {
		data, err := fetchViaVisorOrDirect(cmd, "tpd", "/version", deployment.Prod.TransportDiscovery)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		fmt.Println(prettyJSON(data))
	},
}

var tpdPerKeyStatsCmd = &cobra.Command{
	Use:   "per-key-stats",
	Short: "Per-visor transport statistics",
	Run: func(cmd *cobra.Command, _ []string) {
		data, err := fetchViaVisorOrDirect(cmd, "tpd", "/all-transports/per-key-stats", deployment.Prod.TransportDiscovery)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		fmt.Println(prettyJSON(data))
	},
}

var tpdVisorStatsPK string

func init() {
	tpdVisorStatsCmd.Flags().StringVarP(&tpdVisorStatsPK, "pk", "p", "", "visor public key (required)")
	tpdVisorStatsCmd.MarkFlagRequired("pk") //nolint:errcheck
}

var tpdVisorStatsCmd = &cobra.Command{
	Use:   "visor-stats",
	Short: "Transport count statistics for a specific visor",
	Run: func(cmd *cobra.Command, _ []string) {
		data, err := fetchViaVisorOrDirect(cmd, "tpd", "/transports/stats/"+tpdVisorStatsPK, deployment.Prod.TransportDiscovery)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		fmt.Println(prettyJSON(data))
	},
}

var tpdBandwidthVisorPK string

func init() {
	tpdBandwidthCmd.Flags().StringVarP(&tpdBandwidthVisorPK, "pk", "p", "", "visor public key (omit for network-wide)")
}

var tpdBandwidthCmd = &cobra.Command{
	Use:   "bandwidth",
	Short: "Bandwidth data (network-wide or per-visor)",
	Run: func(cmd *cobra.Command, _ []string) {
		path := "/metric"
		if tpdBandwidthVisorPK != "" {
			path = "/bandwidth/visor/" + tpdBandwidthVisorPK
		}
		data, err := fetchViaVisorOrDirect(cmd, "tpd", path, deployment.Prod.TransportDiscovery)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		fmt.Println(prettyJSON(data))
	},
}

var tpdBandwidthTpID string

func init() {
	tpdBandwidthTpCmd.Flags().StringVarP(&tpdBandwidthTpID, "id", "i", "", "transport ID (required)")
	tpdBandwidthTpCmd.MarkFlagRequired("id") //nolint:errcheck
}

var tpdBandwidthTpCmd = &cobra.Command{
	Use:   "bandwidth-tp",
	Short: "Bandwidth history for a specific transport",
	Run: func(cmd *cobra.Command, _ []string) {
		data, err := fetchViaVisorOrDirect(cmd, "tpd", "/bandwidth/transport/"+tpdBandwidthTpID, deployment.Prod.TransportDiscovery)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		fmt.Println(prettyJSON(data))
	},
}

var tpdVersionsPKs string

func init() {
	tpdVersionsPKCmd.Flags().StringVarP(&tpdVersionsPKs, "pks", "p", "", "comma-separated public keys (required)")
	tpdVersionsPKCmd.MarkFlagRequired("pks") //nolint:errcheck
}

var tpdVersionsPKCmd = &cobra.Command{
	Use:   "versions-pk",
	Short: "Version info for specific public keys",
	Run: func(cmd *cobra.Command, _ []string) {
		data, err := fetchViaVisorOrDirect(cmd, "tpd", "/versions/"+tpdVersionsPKs, deployment.Prod.TransportDiscovery)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		fmt.Println(prettyJSON(data))
	},
}

var tpdMetricsVisorPKs string

func init() {
	tpdMetricsVisorCmd.Flags().StringVarP(&tpdMetricsVisorPKs, "pks", "p", "", "comma-separated public keys (required)")
	tpdMetricsVisorCmd.MarkFlagRequired("pks") //nolint:errcheck
}

var tpdMetricsVisorCmd = &cobra.Command{
	Use:   "metrics-visor",
	Short: "Metrics for specific visor(s)",
	Run: func(cmd *cobra.Command, _ []string) {
		data, err := fetchViaVisorOrDirect(cmd, "tpd", "/metrics/visor/"+tpdMetricsVisorPKs, deployment.Prod.TransportDiscovery)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		fmt.Println(prettyJSON(data))
	},
}

var tpdMetricsTpIDs string

func init() {
	tpdMetricsTpCmd.Flags().StringVarP(&tpdMetricsTpIDs, "ids", "i", "", "comma-separated transport IDs (required)")
	tpdMetricsTpCmd.MarkFlagRequired("ids") //nolint:errcheck
}

var tpdMetricsTpCmd = &cobra.Command{
	Use:   "metrics-tp",
	Short: "Metrics for specific transport(s)",
	Run: func(cmd *cobra.Command, _ []string) {
		data, err := fetchViaVisorOrDirect(cmd, "tpd", "/metrics/"+tpdMetricsTpIDs, deployment.Prod.TransportDiscovery)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		fmt.Println(prettyJSON(data))
	},
}

// --- DMSG Discovery subcommands ---

var dmsgdCmd = &cobra.Command{
	Use:   "dmsgd",
	Short: "DMSG Discovery endpoints",
	Long:  "\n    Query DMSG Discovery service endpoints",
}

func init() {
	dmsgdCmd.AddCommand(
		dmsgdAllServersCmd,
		dmsgdServerClientsCmd,
		dmsgdServerClientsPKCmd,
	)
}

var dmsgdAllServersCmd = &cobra.Command{
	Use:   "all-servers",
	Short: "List all DMSG servers",
	Run: func(cmd *cobra.Command, _ []string) {
		data, err := fetchViaVisorOrDirect(cmd, "dmsgd", "/dmsg-discovery/all_servers", deployment.Prod.DmsgDiscovery)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		fmt.Println(prettyJSON(data))
	},
}

var dmsgdServerClientsCmd = &cobra.Command{
	Use:   "server-clients",
	Short: "List all clients grouped by server",
	Run: func(cmd *cobra.Command, _ []string) {
		data, err := fetchViaVisorOrDirect(cmd, "dmsgd", "/dmsg-discovery/servers/clients", deployment.Prod.DmsgDiscovery)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		fmt.Println(prettyJSON(data))
	},
}

var dmsgdServerClientsPK string

func init() {
	dmsgdServerClientsPKCmd.Flags().StringVarP(&dmsgdServerClientsPK, "pk", "p", "", "server public key (required)")
	dmsgdServerClientsPKCmd.MarkFlagRequired("pk") //nolint:errcheck
}

var dmsgdServerClientsPKCmd = &cobra.Command{
	Use:   "clients",
	Short: "List clients for a specific DMSG server",
	Run: func(cmd *cobra.Command, _ []string) {
		data, err := fetchViaVisorOrDirect(cmd, "dmsgd", "/dmsg-discovery/server/"+dmsgdServerClientsPK+"/clients", deployment.Prod.DmsgDiscovery)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		fmt.Println(prettyJSON(data))
	},
}

// --- AR subcommand ---

var arCmd = &cobra.Command{
	Use:   "ar",
	Short: "Address Resolver endpoints",
	Long:  "\n    Query Address Resolver service endpoints",
	Run: func(cmd *cobra.Command, _ []string) {
		data, err := fetchViaVisorOrDirect(cmd, "ar", "/transports", deployment.Prod.AddressResolver)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		fmt.Println(prettyJSON(data))
	},
}

// --- NM subcommand ---

var nmCmd = &cobra.Command{
	Use:   "nm",
	Short: "Network Monitor status",
	Long:  "\n    Query Network Monitor service status",
	Run: func(cmd *cobra.Command, _ []string) {
		// Network monitor URL isn't in the standard deployment config,
		// so we try a known path or fall back
		data, err := fetchViaVisorOrDirect(cmd, "tpd", "/health", deployment.Prod.TransportDiscovery)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		fmt.Println(prettyJSON(data))
	},
}
