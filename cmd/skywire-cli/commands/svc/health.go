// Package clisvc cmd/skywire-cli/commands/svc/health.go c4-vis-cli
package clisvc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/direct"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsghttp"
	"github.com/skycoin/skywire/pkg/logging"
	skyvisor "github.com/skycoin/skywire/pkg/visor"
)

var (
	directQuery       bool
	healthService     string
	healthViaServer   string
	healthPort        uint16
	healthCarriers    string
	healthHoldCarrier string
)

// CarrierProbe is the result of a DIRECT per-carrier reachability probe of one
// dmsg server: a standalone dmsg client forced onto a single carrier, dialing
// the server directly (not relayed through another server).
type CarrierProbe struct {
	Carrier   string `json:"carrier"`
	Addr      string `json:"addr,omitempty"`
	Status    string `json:"status"` // OK | FAIL | not-advertised
	LatencyMs int64  `json:"latency_ms,omitempty"`
	Error     string `json:"error,omitempty"`
}

// RootCmd is the svc command
var RootCmd = &cobra.Command{
	Use:   "svc",
	Short: "Query skywire deployment services",
	Long:  "\n    Query skywire deployment services (health, stats)",
}

func init() {
	healthCmd.Flags().BoolVar(&directQuery, "direct", false, "query services directly instead of via visor RPC")
	healthCmd.Flags().StringVar(&clirpc.Addr, "rpc", clirpc.DefaultRPCAddr, "RPC server address (env: SKYWIRE_RPC)")
	healthCmd.Flags().StringVar(&healthService, "service", "", "narrow to ONE deployment service by its dmsg PK; only effective with --dmsg-server or --direct (ignored by the default all-services RPC query). For an arbitrary visor use: dmsg curl dmsg://<pk>:80/health")
	healthCmd.Flags().StringVar(&healthViaServer, "dmsg-server", "", "route the /health check through ONE specific dmsg server (PK or PK@host:port) using a standalone direct dmsg client — tests per-server reachability of --service, even for direct-client services not in discovery")
	healthCmd.Flags().Uint16Var(&healthPort, "port", 80, "service dmsg port for the /health endpoint (default: 80, the dmsghttp log-server port)")
	healthCmd.Flags().StringVar(&healthCarriers, "carriers", "", "with --dmsg-server: probe THAT dmsg server DIRECTLY over each of these carriers (comma-sep: wt,ws,quic,tcp), one clean standalone session per carrier — reports which protocols actually reach it")
	healthCmd.Flags().StringVar(&healthHoldCarrier, "hold-carrier", "", "diagnostic: before probing --carriers, open and HOLD a session on this carrier to the same server IN THIS PROCESS — reproduces in-process carrier interference (e.g. --hold-carrier quic while probing wt)")
	healthCmd.Flags().Bool(internal.JSONString, false, "print output as JSON")
	RootCmd.AddCommand(healthCmd)
}

var healthCmd = &cobra.Command{
	Use:   "health",
	Short: "Check health of the skywire deployment services",
	Long: `    Check the /health endpoint of the skywire DEPLOYMENT services
    (dmsg-discovery, transport-discovery, address-resolver, route-finder,
    service-discovery, config-bootstrap, and the dmsg servers).

    By default queries via the local visor RPC (uses the visor's configured
    URLs). Use --direct to query the services directly from the CLI.

    This is for the deployment's own services — NOT arbitrary visors. To check
    one specific visor's /health over dmsg, use:
        skywire cli dmsg curl dmsg://<visor-pk>:80/health

    --service / --dmsg-server narrow the check to ONE deployment service,
    optionally pinned through ONE dmsg server (per-server reachability).`,
	Run: func(cmd *cobra.Command, _ []string) {
		var results []skyvisor.ServiceHealthEntry

		// Per-carrier DIRECT server-reachability mode: probe ONE dmsg server
		// over each named carrier with a clean standalone client. Answers
		// "which protocols actually reach server X" — the direct analog of the
		// carrier convergence the visor does.
		if healthViaServer != "" && healthCarriers != "" {
			probeServerCarriers(cmd)
			return
		}

		// Per-server mode: pin a standalone direct dmsg client to ONE
		// server and fetch the target service's /health through it. This
		// is the only path that can test "is service X reachable VIA
		// server Y", including direct-client services (the dmsg-discovery
		// itself) that have no discovery entry.
		if healthViaServer != "" {
			printHealthResults(cmd, []skyvisor.ServiceHealthEntry{queryServiceViaServer(cmd)})
			return
		}

		// --service only narrows the --dmsg-server / --direct paths. In the default
		// (all-services RPC) path it has no effect, so say so instead of silently
		// printing the full table — that mismatch is surprising.
		if healthService != "" && !directQuery {
			fmt.Fprintln(cmd.ErrOrStderr(), "(note: --service is ignored without --dmsg-server or --direct; showing all deployment services. For one visor use: skywire cli dmsg curl dmsg://<pk>:80/health)") //nolint:errcheck
		}

		if !directQuery {
			// Try via visor RPC first
			rpcClient, err := clirpc.Client(cmd.Flags())
			if err == nil {
				results, err = rpcClient.ServiceHealth()
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "(visor RPC ServiceHealth failed: %v)\n", err) //nolint:errcheck
				} else if len(results) > 0 {
					printHealthResults(cmd, results)
					return
				}
			}
			// Fall through to direct query
			fmt.Println("(visor not available, querying services directly)")
		}

		// Direct query fallback
		results = queryServicesDirect(cmd.Flags())
		printHealthResults(cmd, results)
	},
}

// extractPK pulls the public key from a dmsg:// or http:// URL.
// For dmsg:// URLs the host is the PK (optionally with :port).
// For http:// URLs there's no PK to extract, so returns "".
func extractPK(rawURL string) string {
	if strings.HasPrefix(rawURL, "dmsg://") {
		host := strings.TrimPrefix(rawURL, "dmsg://")
		// Strip path
		if i := strings.Index(host, "/"); i >= 0 {
			host = host[:i]
		}
		// Strip port
		if i := strings.LastIndex(host, ":"); i >= 0 {
			host = host[:i]
		}
		if len(host) == 66 { // hex-encoded PK length
			return host
		}
	}
	return ""
}

func printHealthResults(cmd *cobra.Command, results []skyvisor.ServiceHealthEntry) {
	isJSON, _ := cmd.Flags().GetBool(internal.JSONString) //nolint:errcheck
	if isJSON {
		internal.PrintOutput(cmd.Flags(), results, "")
		return
	}
	tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "SERVICE\tSTATUS\tLATENCY\tPROTOCOL\tVERSION\tPK") //nolint:errcheck,gosec
	for _, r := range results {
		transport := r.Transport
		if transport == "" {
			transport = "-"
		}
		ver := r.Version
		if ver == "" {
			ver = "-"
		}
		pk := extractPK(r.URL)
		if pk == "" {
			pk = "-"
		}
		status := r.Status
		latency := fmt.Sprintf("%dms", r.LatencyMs)
		if r.Status != "OK" {
			latency = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", r.Name, status, latency, transport, ver, pk) //nolint:errcheck,gosec
	}
	tw.Flush() //nolint:errcheck,gosec
}

func queryServicesDirect(cmdFlags *pflag.FlagSet) []skyvisor.ServiceHealthEntry {
	services := map[string]string{
		"Transport Discovery": deployment.Prod.TransportDiscovery,
		"DMSG Discovery":      deployment.Prod.DmsgDiscovery,
		"Address Resolver":    deployment.Prod.AddressResolver,
		"Route Finder":        deployment.Prod.RouteFinder,
		"Service Discovery":   deployment.Prod.ServiceDiscovery,
	}

	// Query all services in parallel via visor RPC (DMSG).
	// This reduces total time from sum(per-service) to max(per-service).
	type result struct {
		entry skyvisor.ServiceHealthEntry
	}
	ch := make(chan result, len(services))
	expected := 0

	for name, baseURL := range services {
		if baseURL == "" {
			continue
		}
		expected++
		go func(name, baseURL string) {
			url := strings.TrimSuffix(baseURL, "/") + "/health"
			entry := skyvisor.ServiceHealthEntry{Name: name, URL: baseURL}

			start := time.Now()
			body, err := clirpc.FetchServiceURL(cmdFlags, url)
			entry.LatencyMs = time.Since(start).Milliseconds()

			if err != nil {
				entry.Status = "DOWN"
				ch <- result{entry: entry}
				return
			}

			var health map[string]interface{}
			if json.Unmarshal(body, &health) == nil {
				if bi, ok := health["build_info"].(map[string]interface{}); ok {
					if v, ok := bi["version"].(string); ok {
						entry.Version = v
					}
				}
				if entry.Version == "" {
					if v, ok := health["version"].(string); ok {
						entry.Version = v
					}
				}
			}
			entry.Status = "OK"
			ch <- result{entry: entry}
		}(name, baseURL)
	}

	var results []skyvisor.ServiceHealthEntry
	for i := 0; i < expected; i++ {
		r := <-ch
		results = append(results, r.entry)
	}

	return results
}

// queryServiceViaServer fetches a single service's /health over dmsg
// through ONE pinned dmsg server, using a standalone direct client.
// This is how you test whether service X is reachable VIA server Y —
// it works even for direct-client services (the dmsg-discovery itself)
// that publish no discovery entry, because the direct client carries a
// synthetic entry delegating only the pinned server.
func queryServiceViaServer(cmd *cobra.Command) skyvisor.ServiceHealthEntry {
	logging.SetLevel(logrus.ErrorLevel)
	log := logging.MustGetLogger("svc-health")

	entry := skyvisor.ServiceHealthEntry{Name: "service", Status: "DOWN"}

	if healthService == "" {
		internal.PrintFatalError(cmd.Flags(), fmt.Errorf("--dmsg-server requires --service <service-pk>"))
	}
	var svcPK cipher.PubKey
	if err := svcPK.Set(healthService); err != nil {
		internal.PrintFatalError(cmd.Flags(), fmt.Errorf("invalid --service pk %q: %w", healthService, err))
	}
	entry.URL = fmt.Sprintf("dmsg://%s:%d/health", svcPK.Hex(), healthPort)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	srv, err := resolveServerEntry(ctx, healthViaServer, log)
	if err != nil {
		internal.PrintFatalError(cmd.Flags(), err)
	}
	entry.Transport = "dmsg via " + srv.Static.String()[:8]

	myPK, mySK := cipher.GenerateKeyPair()
	dClient := direct.NewClient(direct.GetAllEntries(cipher.PubKeys{myPK, svcPK}, []*disc.Entry{srv}), log)
	dmsgC, stop, err := direct.StartDmsg(ctx, log, myPK, mySK, dClient, dmsg.DefaultConfig())
	if err != nil {
		return entry // DOWN — couldn't even connect to the pinned server
	}
	defer stop()

	httpC := &http.Client{Transport: dmsghttp.MakeHTTPTransport(ctx, dmsgC), Timeout: 15 * time.Second}
	start := time.Now()
	resp, err := httpC.Get(fmt.Sprintf("http://%s:%d/health", svcPK.Hex(), healthPort))
	entry.LatencyMs = time.Since(start).Milliseconds()
	if err != nil {
		return entry // DOWN — service not reachable via this server
	}
	defer resp.Body.Close() //nolint:errcheck

	body, rerr := io.ReadAll(io.LimitReader(resp.Body, 4096))
	var health map[string]interface{}
	if rerr == nil && json.Unmarshal(body, &health) == nil {
		if bi, ok := health["build_info"].(map[string]interface{}); ok {
			if v, ok := bi["version"].(string); ok {
				entry.Version = v
			}
		}
		if entry.Version == "" {
			if v, ok := health["version"].(string); ok {
				entry.Version = v
			}
		}
	}
	if resp.StatusCode == http.StatusOK {
		entry.Status = "OK"
	} else {
		entry.Status = fmt.Sprintf("HTTP %d", resp.StatusCode)
	}
	return entry
}

func splitCarriers(s string) []string {
	var out []string
	for _, c := range strings.Split(s, ",") {
		if c = strings.TrimSpace(c); c != "" {
			out = append(out, c)
		}
	}
	return out
}

func carrierAddr(e *disc.Entry, carrier string) string {
	if e == nil || e.Server == nil {
		return ""
	}
	switch carrier {
	case "wt":
		return e.Server.AddressWT
	case "ws":
		return e.Server.AddressWS
	case "quic":
		return e.Server.AddressUDP
	case "tcp":
		return e.Server.Address
	}
	return ""
}

// resolveServerEntryLive returns the target server's CURRENT discovery entry
// (fresh AddressWT/CertHashWT/AddressUDP/AddressWS). It prefers the running
// visor — already connected to the dmsg-discovery — and falls back to a
// standalone bootstrap client when no visor is reachable.
func resolveServerEntryLive(flags *pflag.FlagSet, srvPK cipher.PubKey, log *logging.Logger) (*disc.Entry, error) {
	url := strings.TrimSuffix(deployment.Prod.DmsgDiscoveryDmsg, "/") + "/dmsg-discovery/all_servers"
	if body, err := clirpc.FetchServiceURL(flags, url); err == nil {
		var servers []*disc.Entry
		if json.Unmarshal(body, &servers) == nil {
			for _, s := range servers {
				if s.Static == srvPK && s.Server != nil {
					return s, nil
				}
			}
		}
	}
	// No visor (or it couldn't reach the disc) — bootstrap standalone.
	return fetchLiveServerEntry(context.Background(), srvPK, log)
}

// fetchLiveServerEntry bootstraps a standalone dmsg client through the embedded
// seed servers and fetches the target server's CURRENT discovery entry (fresh
// AddressWT/CertHashWT/AddressUDP/AddressWS), which a per-carrier probe needs.
func fetchLiveServerEntry(ctx context.Context, srvPK cipher.PubKey, log *logging.Logger) (*disc.Entry, error) {
	seeds := deployment.DmsgServerEntriesToDisc(deployment.Prod.DmsgServers)
	if len(seeds) == 0 {
		return nil, fmt.Errorf("no embedded seed dmsg servers to bootstrap through")
	}
	discPKHex := dmsg.ExtractPKFromDmsgAddr(deployment.Prod.DmsgDiscoveryDmsg)
	if discPKHex == "" {
		return nil, fmt.Errorf("no dmsg-discovery dmsg address in the embedded deployment")
	}
	var discPK cipher.PubKey
	if err := discPK.Set(discPKHex); err != nil {
		return nil, fmt.Errorf("invalid embedded dmsg-discovery pk %q: %w", discPKHex, err)
	}
	myPK, mySK := cipher.GenerateKeyPair()
	conf := dmsg.DefaultConfig()
	// Connect to several seeds to raise the odds one co-hosts the disc client.
	conf.MinSessions = len(seeds)
	if conf.MinSessions > 5 {
		conf.MinSessions = 5
	}
	// Include discPK so GetAllEntries mints a synthetic entry delegating the
	// seed servers — otherwise dmsg can't route the all_servers request to it.
	dClient := direct.NewClient(direct.GetAllEntries(cipher.PubKeys{myPK, discPK}, seeds), log)
	bctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	dmsgC, stop, err := direct.StartDmsg(bctx, log, myPK, mySK, dClient, conf)
	if err != nil {
		return nil, fmt.Errorf("bootstrap dmsg client (to read live discovery): %w", err)
	}
	defer stop()
	// Server entries live in all_servers, NOT the per-PK /entry endpoint.
	httpC := &http.Client{Transport: dmsghttp.MakeHTTPTransport(ctx, dmsgC), Timeout: 15 * time.Second}
	dc := disc.NewHTTP(fmt.Sprintf("http://%s:80", discPKHex), httpC, log)
	servers, err := dc.AllServers(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetch all_servers over dmsg: %w", err)
	}
	for _, s := range servers {
		if s.Static == srvPK {
			if s.Server == nil {
				return nil, fmt.Errorf("%s has no server section in discovery", srvPK)
			}
			return s, nil
		}
	}
	return nil, fmt.Errorf("%s not found in dmsg-discovery all_servers", srvPK)
}

// probeOneCarrier dials the server over ONE carrier with a clean standalone
// client (no fallback, so a failure isolates that carrier) and reports whether
// a direct session established.
func probeOneCarrier(ctx context.Context, srvPK cipher.PubKey, entry *disc.Entry, carrier string, log *logging.Logger) CarrierProbe {
	res := CarrierProbe{Carrier: carrier, Addr: carrierAddr(entry, carrier)}
	if res.Addr == "" {
		res.Status = "not-advertised"
		return res
	}
	conf := dmsg.DefaultConfig()
	conf.Carriers = []string{carrier}
	conf.MinSessions = 1
	myPK, mySK := cipher.GenerateKeyPair()
	dClient := direct.NewClient(direct.GetAllEntries(cipher.PubKeys{myPK, srvPK}, []*disc.Entry{entry}), log)
	cctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	t0 := time.Now()
	dmsgC, stop, err := direct.StartDmsg(cctx, log, myPK, mySK, dClient, conf)
	res.LatencyMs = time.Since(t0).Milliseconds()
	if err != nil {
		res.Status = "FAIL"
		res.Error = err.Error()
		return res
	}
	defer stop()
	got := ""
	for _, s := range dmsgC.SessionCarriers() {
		if s.ServerPK == srvPK {
			got = s.Carrier
		}
	}
	switch {
	case got == carrier:
		res.Status = "OK"
	case got != "":
		res.Status = "OK(" + got + ")"
	default:
		res.Status = "FAIL"
		res.Error = "session not established on " + carrier
	}
	return res
}

func probeServerCarriers(cmd *cobra.Command) {
	logging.SetLevel(logrus.ErrorLevel)
	log := logging.MustGetLogger("svc-health")
	pkHex := healthViaServer
	if at := strings.IndexByte(pkHex, '@'); at > 0 {
		pkHex = pkHex[:at]
	}
	var srvPK cipher.PubKey
	if err := srvPK.Set(pkHex); err != nil {
		internal.PrintFatalError(cmd.Flags(), fmt.Errorf("invalid --dmsg-server pk %q: %w", pkHex, err))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	entry, err := resolveServerEntryLive(cmd.Flags(), srvPK, log)
	if err != nil {
		internal.PrintFatalError(cmd.Flags(), err)
	}
	// Diagnostic: hold a session on --hold-carrier to the same server in THIS
	// process for the duration of the probes. If probing wt fails only while a
	// quic session is held, that isolates in-process (quic-go) interference.
	if healthHoldCarrier != "" {
		if addr := carrierAddr(entry, healthHoldCarrier); addr == "" {
			fmt.Printf("(hold-carrier %s not advertised by %s; skipping hold)\n", healthHoldCarrier, srvPK.Hex()[:8])
		} else {
			hConf := dmsg.DefaultConfig()
			hConf.Carriers = []string{healthHoldCarrier}
			hConf.MinSessions = 1
			hPK, hSK := cipher.GenerateKeyPair()
			hClient := direct.NewClient(direct.GetAllEntries(cipher.PubKeys{hPK, srvPK}, []*disc.Entry{entry}), log)
			hCtx, hCancel := context.WithTimeout(ctx, 15*time.Second)
			hC, hStop, herr := direct.StartDmsg(hCtx, log, hPK, hSK, hClient, hConf)
			hCancel()
			if herr != nil {
				fmt.Printf("(could not hold a %s session to %s: %v; probing anyway)\n", healthHoldCarrier, srvPK.Hex()[:8], herr)
			} else {
				got := ""
				for _, s := range hC.SessionCarriers() {
					if s.ServerPK == srvPK {
						got = s.Carrier
					}
				}
				fmt.Printf("holding a %s session to %s (landed on %q) during probes\n", healthHoldCarrier, srvPK.Hex()[:8], got)
				defer hStop()
			}
		}
	}
	var results []CarrierProbe
	for _, c := range splitCarriers(healthCarriers) {
		results = append(results, probeOneCarrier(ctx, srvPK, entry, c, log))
	}
	if isJSON, _ := cmd.Flags().GetBool(internal.JSONString); isJSON { //nolint:errcheck
		internal.PrintOutput(cmd.Flags(), map[string]interface{}{"server": srvPK.Hex(), "carriers": results}, "")
		return
	}
	fmt.Printf("dmsg server %s — direct per-carrier reachability:\n", srvPK.Hex()[:8])
	tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "CARRIER\tSTATUS\tLATENCY\tADDR\tERROR") //nolint:errcheck,gosec
	for _, r := range results {
		lat := "-"
		if strings.HasPrefix(r.Status, "OK") {
			lat = fmt.Sprintf("%dms", r.LatencyMs)
		}
		e := r.Error
		if len(e) > 64 {
			e = e[:64]
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", r.Carrier, r.Status, lat, r.Addr, e) //nolint:errcheck,gosec
	}
	tw.Flush() //nolint:errcheck,gosec
}

// resolveServerEntry turns a "--dmsg-server" spec into a dmsg server
// entry. Accepts "<pk>" (address resolved from the discovery's
// all_servers) or "<pk>@<host:port>" (no discovery dependency).
func resolveServerEntry(ctx context.Context, spec string, log *logging.Logger) (*disc.Entry, error) {
	pkHex, addr := spec, ""
	if at := strings.IndexByte(spec, '@'); at > 0 {
		pkHex, addr = spec[:at], spec[at+1:]
	}
	var srvPK cipher.PubKey
	if err := srvPK.Set(pkHex); err != nil {
		return nil, fmt.Errorf("--dmsg-server: invalid server pk %q: %w", pkHex, err)
	}
	if addr != "" {
		return &disc.Entry{Static: srvPK, Server: &disc.Server{Address: addr}}, nil
	}
	// Resolve from the embedded deployment list first — no network call,
	// and covers every prod dmsg-server. Plain-HTTP discovery is the
	// legacy fallback for the (rare) operator running against a custom
	// dmsg-server that's only in their discovery; the embedded path
	// keeps the common production case dmsg-only.
	for _, e := range deployment.Prod.DmsgServers {
		if e.Static == srvPK.String() {
			return &disc.Entry{Static: srvPK, Server: &disc.Server{Address: e.Server.Address}}, nil
		}
	}
	discURL := deployment.Prod.DmsgDiscovery
	if discURL == "" {
		return nil, fmt.Errorf("--dmsg-server: %s not in embedded deployment set and no discovery URL available (pass <pk>@<host:port>)", srvPK)
	}
	dc := disc.NewHTTP(discURL, &http.Client{Timeout: 15 * time.Second}, log)
	servers, err := dc.AllServers(ctx)
	if err != nil {
		return nil, fmt.Errorf("--dmsg-server: resolve %s via all_servers: %w (or pass <pk>@<host:port>)", srvPK, err)
	}
	for _, s := range servers {
		if s.Static == srvPK {
			return s, nil
		}
	}
	return nil, fmt.Errorf("--dmsg-server: %s not found in discovery all_servers (pass <pk>@<host:port>)", srvPK)
}
