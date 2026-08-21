// Package clisvc cmd/skywire-cli/commands/svc/fetch.go c4-vis-cli
package clisvc

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/cliout"
	"github.com/skycoin/skywire/pkg/serviceuptime"
)

// Deployment-selection state, shared by every svc subcommand. Registered
// as persistent flags on RootCmd (see registerDeploymentFlags) so the
// whole `svc` tree can point at a non-prod deployment consistently —
// matching what `sd` / `ut` already offer. All default empty / prod so
// existing invocations behave exactly as before.
var (
	svcTestEnv  bool
	svcTPDURL   string
	svcDmsgdURL string
	svcARURL    string
)

// svcIsTestEnv mirrors sd/ut: SKYWIRETEST=1 selects the test deployment.
func svcIsTestEnv() bool { return os.Getenv("SKYWIRETEST") == "1" }

// svcDeployment returns the deployment.Services set the svc commands
// resolve URLs from, honoring --testenv (or SKYWIRETEST=1).
func svcDeployment() deployment.Services {
	if svcTestEnv {
		return deployment.Test
	}
	return deployment.Prod
}

// baseURLFor resolves the deployment base URL for a svc service key,
// honoring --testenv and the per-service --tpdurl / --dmsgdurl / --arurl
// overrides. An explicit override always wins; otherwise the selected
// deployment's URL is used.
func baseURLFor(service string) string {
	dep := svcDeployment()
	switch service {
	case "tpd":
		if svcTPDURL != "" {
			return svcTPDURL
		}
		return dep.TransportDiscovery
	case "dmsgd":
		if svcDmsgdURL != "" {
			return svcDmsgdURL
		}
		return dep.DmsgDiscovery
	case "ar":
		if svcARURL != "" {
			return svcARURL
		}
		return dep.AddressResolver
	}
	return ""
}

// registerDeploymentFlags installs the deployment-selection + fetch-chain
// flags on RootCmd's persistent set, so every svc subcommand inherits one
// consistent vocabulary (--testenv, --tpdurl/--dmsgdurl/--arurl, and the
// --no-cxo/--no-rpc/--no-dmsg/--sk/--config fetch-chain controls).
func registerDeploymentFlags() {
	pf := RootCmd.PersistentFlags()
	pf.BoolVar(&svcTestEnv, "testenv", svcIsTestEnv(), "use the test deployment services (or set SKYWIRETEST=1)")
	pf.StringVar(&svcTPDURL, "tpdurl", "", "override the transport-discovery base URL (used by tpd / nm)")
	pf.StringVar(&svcDmsgdURL, "dmsgdurl", "", "override the dmsg-discovery base URL (used by dmsgd)")
	pf.StringVar(&svcARURL, "arurl", "", "override the address-resolver base URL (used by ar)")
	// Fetch-chain controls (--no-cxo/--no-rpc/--no-dmsg/--sk/--config) so
	// svc can steer the CXO→RPC→DMSG fetch chain like the other groups.
	// Registered on a throwaway command then merged into the persistent
	// set, since clirpc.RegisterFetchFlags targets cmd.Flags().
	fetchFlags := &cobra.Command{}
	clirpc.RegisterFetchFlags(fetchFlags)
	pf.AddFlagSet(fetchFlags.Flags())
}

func init() {
	registerDeploymentFlags()

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
// The direct/HTTP fallback base URL is resolved from the service key via
// baseURLFor, so --testenv and the per-service URL overrides apply.
func fetchViaVisorOrDirect(cmd *cobra.Command, service, path string) ([]byte, error) {
	if !directQuery {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err == nil {
			data, err := rpcClient.FetchServiceData(service, path)
			if err == nil {
				return data, nil
			}
		}
	}

	// Fallback via FetchServiceURL (CXO → direct DMSG HTTP chain)
	url := strings.TrimSuffix(baseURLFor(service), "/") + path
	return clirpc.FetchServiceURL(cmd.Flags(), url)
}

// emitPretty renders a raw JSON response. --json / --jq / --shape are
// routed through the flag-aware cliout printer so they work uniformly;
// the default human view stays pretty-indented JSON (unchanged).
func emitPretty(cmd *cobra.Command, data []byte) {
	var v interface{}
	if err := json.Unmarshal(data, &v); err != nil {
		// Not JSON — emit the raw body (still honors --json as a string).
		internal.Catch(cmd.Flags(), cliout.Print(cmd, string(data)))
		return
	}
	// Any structured-output flag: let cliout handle it.
	if cliout.JSONMode(cmd) || cliout.JQFilter(cmd) != "" || cliout.ShapeMode(cmd) {
		internal.Catch(cmd.Flags(), cliout.Print(cmd, v))
		return
	}
	// Default human view: pretty-indented JSON.
	if pretty, err := json.MarshalIndent(v, "", "  "); err == nil {
		fmt.Fprintln(cmd.OutOrStdout(), string(pretty)) //nolint:errcheck,gosec
		return
	}
	fmt.Fprintln(cmd.OutOrStdout(), string(data)) //nolint:errcheck,gosec
}

// --- TPD subcommands ---

var tpdCmd = &cobra.Command{
	Use:   "tpd",
	Short: "Transport Discovery endpoints",
	Long: `Query Transport Discovery (TPD) read endpoints over the mesh.

By default each subcommand fetches via the local visor's RPC (DMSG-HTTP);
pass --direct to fetch with a CLI-owned client instead. All output honors
--json. Network-wide views (stats, versions, bandwidth without --pk) need no
arguments; per-visor / per-transport views take --pk / --id / --pks / --ids.`,
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
		tpdUptimeCmd,
	)
	tpdUptimeCmd.Flags().IntVarP(&svcUptimeLimit, "limit", "n", 0, "show only the most-recent N session rows (0 = all)")
	tpdUptimeCmd.Flags().DurationVar(&svcUptimeGapThreshold, "gap-threshold", 30*time.Second,
		"render a (down: ...) row between sessions whose gap exceeds this")
	tpdUptimeCmd.Flags().DurationVar(&svcUptimeRestartLoopMax, "restart-loop-threshold", 30*time.Second,
		"sessions shorter than this get a (restart loop?) tag")
}

var (
	svcUptimeLimit          int
	svcUptimeGapThreshold   time.Duration
	svcUptimeRestartLoopMax time.Duration
)

var tpdStatsCmd = &cobra.Command{
	Use:   "stats",
	Short: "Network-wide transport statistics",
	Run: func(cmd *cobra.Command, _ []string) {
		data, err := fetchViaVisorOrDirect(cmd, "tpd", "/all-transports/stats")
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		emitPretty(cmd, data)
	},
}

var tpdVersionsCmd = &cobra.Command{
	Use:   "versions",
	Short: "Version statistics from transport discovery",
	Run: func(cmd *cobra.Command, _ []string) {
		data, err := fetchViaVisorOrDirect(cmd, "tpd", "/version")
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		emitPretty(cmd, data)
	},
}

var tpdPerKeyStatsCmd = &cobra.Command{
	Use:   "per-key-stats",
	Short: "Per-visor transport statistics",
	Run: func(cmd *cobra.Command, _ []string) {
		data, err := fetchViaVisorOrDirect(cmd, "tpd", "/all-transports/per-key-stats")
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		emitPretty(cmd, data)
	},
}

var tpdVisorStatsPK string

func init() {
	tpdVisorStatsCmd.Flags().StringVarP(&tpdVisorStatsPK, "pk", "p", "", "visor public key (required)")
	tpdVisorStatsCmd.MarkFlagRequired("pk") //nolint:errcheck,gosec
}

var tpdVisorStatsCmd = &cobra.Command{
	Use:     "visor-stats",
	Short:   "Transport count statistics for a specific visor",
	Long:    "Transport count statistics for one visor (TPD /transports/stats/<pk>).\n\n  skywire cli svc tpd visor-stats -p <visor-pk>",
	Example: "  skywire cli svc tpd visor-stats -p 02b3...",
	Run: func(cmd *cobra.Command, _ []string) {
		data, err := fetchViaVisorOrDirect(cmd, "tpd", "/transports/stats/"+tpdVisorStatsPK)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		emitPretty(cmd, data)
	},
}

var tpdBandwidthVisorPK string

func init() {
	tpdBandwidthCmd.Flags().StringVarP(&tpdBandwidthVisorPK, "pk", "p", "", "visor public key (omit for network-wide)")
}

var tpdBandwidthCmd = &cobra.Command{
	Use:   "bandwidth",
	Short: "Bandwidth data (network-wide or per-visor)",
	Long:  "Bandwidth data from TPD. With no --pk: the network-wide /metric rollup.\nWith --pk <visor-pk>: that visor's /bandwidth/visor/<pk> history.",
	Run: func(cmd *cobra.Command, _ []string) {
		path := "/metric"
		if tpdBandwidthVisorPK != "" {
			path = "/bandwidth/visor/" + tpdBandwidthVisorPK
		}
		data, err := fetchViaVisorOrDirect(cmd, "tpd", path)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		emitPretty(cmd, data)
	},
}

var tpdBandwidthTpID string

func init() {
	tpdBandwidthTpCmd.Flags().StringVarP(&tpdBandwidthTpID, "id", "i", "", "transport ID (required)")
	tpdBandwidthTpCmd.MarkFlagRequired("id") //nolint:errcheck,gosec
}

var tpdBandwidthTpCmd = &cobra.Command{
	Use:   "bandwidth-tp",
	Short: "Bandwidth history for a specific transport",
	Long:  "Bandwidth history for one transport (TPD /bandwidth/transport/<id>).\n\n  skywire cli svc tpd bandwidth-tp -i <transport-uuid>",
	Run: func(cmd *cobra.Command, _ []string) {
		data, err := fetchViaVisorOrDirect(cmd, "tpd", "/bandwidth/transport/"+tpdBandwidthTpID)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		emitPretty(cmd, data)
	},
}

var tpdVersionsPKs string

func init() {
	tpdVersionsPKCmd.Flags().StringVarP(&tpdVersionsPKs, "pks", "p", "", "comma-separated public keys (required)")
	tpdVersionsPKCmd.MarkFlagRequired("pks") //nolint:errcheck,gosec
}

var tpdVersionsPKCmd = &cobra.Command{
	Use:   "versions-pk",
	Short: "Version info for specific public keys",
	Long:  "Version info for one or more visors (TPD /versions/<pks>).\n\n  skywire cli svc tpd versions-pk -p <pk1>,<pk2>",
	Run: func(cmd *cobra.Command, _ []string) {
		data, err := fetchViaVisorOrDirect(cmd, "tpd", "/versions/"+tpdVersionsPKs)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		emitPretty(cmd, data)
	},
}

var tpdMetricsVisorPKs string

func init() {
	tpdMetricsVisorCmd.Flags().StringVarP(&tpdMetricsVisorPKs, "pks", "p", "", "comma-separated public keys (required)")
	tpdMetricsVisorCmd.MarkFlagRequired("pks") //nolint:errcheck,gosec
}

var tpdMetricsVisorCmd = &cobra.Command{
	Use:   "metrics-visor",
	Short: "Metrics for specific visor(s)",
	Long:  "Transport metrics for one or more visors (TPD /metrics/visor/<pks>).\n\n  skywire cli svc tpd metrics-visor -p <pk1>,<pk2>",
	Run: func(cmd *cobra.Command, _ []string) {
		data, err := fetchViaVisorOrDirect(cmd, "tpd", "/metrics/visor/"+tpdMetricsVisorPKs)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		emitPretty(cmd, data)
	},
}

var tpdMetricsTpIDs string

func init() {
	tpdMetricsTpCmd.Flags().StringVarP(&tpdMetricsTpIDs, "ids", "i", "", "comma-separated transport IDs (required)")
	tpdMetricsTpCmd.MarkFlagRequired("ids") //nolint:errcheck,gosec
}

var tpdMetricsTpCmd = &cobra.Command{
	Use:   "metrics-tp",
	Short: "Metrics for specific transport(s)",
	Long:  "Metrics for one or more transports (TPD /metrics/<ids>).\n\n  skywire cli svc tpd metrics-tp -i <id1>,<id2>",
	Run: func(cmd *cobra.Command, _ []string) {
		data, err := fetchViaVisorOrDirect(cmd, "tpd", "/metrics/"+tpdMetricsTpIDs)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		emitPretty(cmd, data)
	},
}

// --- DMSG Discovery subcommands ---

var dmsgdCmd = &cobra.Command{
	Use:   "dmsgd",
	Short: "DMSG Discovery endpoints",
	Long: `Query DMSG Discovery read endpoints over the mesh.

  all-servers     every registered dmsg server entry
  server-clients  clients grouped by the server they session with
  clients --pk    clients currently sessioned with one dmsg server

Fetches via the local visor's RPC by default; --direct uses a CLI-owned
client. Output honors --json.`,
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
		data, err := fetchViaVisorOrDirect(cmd, "dmsgd", "/dmsg-discovery/all_servers")
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		emitPretty(cmd, data)
	},
}

var dmsgdServerClientsCmd = &cobra.Command{
	Use:   "server-clients",
	Short: "List all clients grouped by server",
	Run: func(cmd *cobra.Command, _ []string) {
		data, err := fetchViaVisorOrDirect(cmd, "dmsgd", "/dmsg-discovery/servers/clients")
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		emitPretty(cmd, data)
	},
}

var dmsgdServerClientsPK string

func init() {
	dmsgdServerClientsPKCmd.Flags().StringVarP(&dmsgdServerClientsPK, "pk", "p", "", "server public key (required)")
	dmsgdServerClientsPKCmd.MarkFlagRequired("pk") //nolint:errcheck,gosec
}

var dmsgdServerClientsPKCmd = &cobra.Command{
	Use:   "clients",
	Short: "List clients for a specific DMSG server",
	Long:  "List the clients currently sessioned with one dmsg server\n(DMSG-discovery /dmsg-discovery/server/<pk>/clients).\n\n  skywire cli svc dmsgd clients -p <server-pk>",
	Run: func(cmd *cobra.Command, _ []string) {
		data, err := fetchViaVisorOrDirect(cmd, "dmsgd", "/dmsg-discovery/server/"+dmsgdServerClientsPK+"/clients")
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		emitPretty(cmd, data)
	},
}

// --- AR subcommand ---

var arCmd = &cobra.Command{
	Use:   "ar",
	Short: "Address Resolver endpoints",
	Long: `Query the Address Resolver (AR) over the mesh.

Bare 'ar' fetches /transports — the AR's view of stcpr/sudph-registered
edges. 'ar check <pk>' asks whether one visor is registered (without
revealing its IP). Fetches via the local visor's RPC by default; --direct
uses a CLI-owned client. Output honors --json.`,
	Run: func(cmd *cobra.Command, _ []string) {
		data, err := fetchViaVisorOrDirect(cmd, "ar", "/transports")
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		emitPretty(cmd, data)
	},
}

// tpdUptimeCmd renders TPD's own session history (one row per process
// incarnation, version-tagged) — recorded by pkg/serviceuptime in the
// service's local bbolt store. Gaps between sessions = TPD was actually
// down. Sessions shorter than --restart-loop-threshold get tagged so
// an operator can spot a TPD that's bouncing.
var tpdUptimeCmd = &cobra.Command{
	Use:   "uptime",
	Short: "TPD session history (version-tagged, with restart-loop / down-window detection)",
	Long: `
Render the TPD service's own session history recorded locally by
pkg/serviceuptime. Each session is one process incarnation: started_at,
last_seen, and the running binary's Version. Gaps between adjacent
sessions show as "(down: <duration>)"; sessions shorter than
--restart-loop-threshold get a "(restart loop?)" tag.

	skywire cli svc tpd uptime
	skywire cli svc tpd uptime -n 20
	skywire cli svc tpd uptime --json | jq '.[].version' | sort -u
`,
	Run: func(cmd *cobra.Command, _ []string) {
		path := "/uptime/sessions"
		if svcUptimeLimit > 0 {
			path = fmt.Sprintf("%s?limit=%d", path, svcUptimeLimit)
		}
		data, err := fetchViaVisorOrDirect(cmd, "tpd", path)
		if err != nil {
			// A 503 here means the TPD is up but has no session recorder
			// enabled (pkg/serviceuptime not wired on this deployment's
			// build), or the endpoint predates it. That's an expected,
			// operator-actionable state — not a CLI failure — so report it
			// plainly instead of dumping a raw RPC/HTTP error + exit 1.
			if strings.Contains(err.Error(), "503") || strings.Contains(err.Error(), "404") {
				fmt.Fprintln(cmd.ErrOrStderr(), "svc tpd uptime: the transport-discovery reports no session history (HTTP 503/404 from /uptime/sessions).") //nolint:errcheck
				fmt.Fprintln(cmd.ErrOrStderr(), "This deployment's TPD build does not record its own session history (pkg/serviceuptime not enabled).")     //nolint:errcheck
				return
			}
			internal.PrintFatalError(cmd.Flags(), err)
		}
		var sessions []serviceuptime.SessionRecord
		if jErr := json.Unmarshal(data, &sessions); jErr != nil {
			// Service responded but with something other than a session
			// list — most likely a 503 (recorder not configured) or an
			// older binary without /uptime. Surface the raw body.
			emitPretty(cmd, data)
			return
		}
		if jsonOut, _ := cmd.Flags().GetBool("json"); jsonOut { //nolint:errcheck
			b, _ := json.MarshalIndent(sessions, "", "  ") //nolint:errcheck
			internal.PrintOutput(cmd.Flags(), sessions, string(b)+"\n")
			return
		}
		human := serviceuptime.FormatHistory(sessions, svcUptimeGapThreshold, svcUptimeRestartLoopMax)
		internal.PrintOutput(cmd.Flags(), sessions, human)
	},
}

// --- NM subcommand ---

// nmCmd is a stand-in: the network-monitor has no URL in the embedded
// deployment.Services set (only keyring PKs exist for it), so there is no
// dedicated endpoint to query. Until one is wired in, this probes the
// transport-discovery /health as the closest liveness signal and says so.
// FOLLOW-UP: give the network monitor a real deployment URL + endpoint, or
// drop this subcommand.
var nmCmd = &cobra.Command{
	Use:   "nm",
	Short: "Network Monitor status (currently probes TPD /health — no NM endpoint deployed)",
	Long: `Query network-monitor liveness.

NOTE: the network monitor has no URL in the embedded deployment set (only
keyring public keys), so there is no dedicated endpoint to hit yet. This
command probes the transport-discovery /health as the closest available
liveness signal — treat the result as "is the discovery tier up", not as a
network-monitor-specific status.`,
	Run: func(cmd *cobra.Command, _ []string) {
		data, err := fetchViaVisorOrDirect(cmd, "tpd", "/health")
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		emitPretty(cmd, data)
	},
}
