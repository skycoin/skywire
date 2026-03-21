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

	RootCmd.AddCommand(tpdCmd, arCmd, nmCmd)
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
		tpdVersionsCmd,
		tpdBandwidthCmd,
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

var tpdBandwidthVisorPK string

func init() {
	tpdBandwidthCmd.Flags().StringVarP(&tpdBandwidthVisorPK, "pk", "p", "", "visor public key")
}

var tpdBandwidthCmd = &cobra.Command{
	Use:   "bandwidth",
	Short: "Bandwidth data from transport discovery",
	Long:  "\n    Query bandwidth data. Use --pk for a specific visor.",
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
