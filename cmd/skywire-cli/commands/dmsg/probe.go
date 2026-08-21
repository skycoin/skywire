// Package clidmsg cmd/skywire-cli/commands/dmsg/probe.go c4-vis-cli
package clidmsg

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/skywire/tcpnoise"
)

var (
	standalone        bool
	probeVerbose      bool
	probeVerboseLevel string
	probePorts        string
	probeParallel     int
	probeSK           string
	probeServer       string
	probeSkynet       bool
	probeVia          string
)

func init() {
	probeCmd.Flags().BoolVarP(&standalone, "standalone", "s", false, "use a standalone dmsg client with an ephemeral key (no running visor needed)")
	probeCmd.Flags().StringVar(&probeSK, "sk", "", "standalone dmsg client using THIS secret key (hex); implies --standalone, no visor RPC")
	probeCmd.Flags().StringVar(&probeServer, "server", "", "dmsg: force the probe through this specific dmsg server (pk hex) — per-server reachability")
	probeCmd.Flags().BoolVar(&probeSkynet, "skynet", false, "probe over skynet (skywire's routed p2p transports) instead of dmsg — uses the visor's router")
	probeCmd.Flags().StringVar(&probeVia, "via", "", "probe a direct noise-TCP connection instead of dmsg: tcp://<pk>@host:port (uses --sk or an ephemeral key)")
	probeCmd.Flags().BoolVarP(&probeVerbose, "verbose", "v", false, "stream visor's dmsg-layer logs to stderr while probing (single-port mode only)")
	probeCmd.Flags().StringVar(&probeVerboseLevel, "verbose-level", "debug", "minimum log level when --verbose is set: trace|debug|info|warn|error")
	probeCmd.Flags().StringVar(&probePorts, "ports", "", "multi-port sweep — comma list with optional ranges, e.g. '22,80,1000-1010,5000'. When set, the positional <port> arg is ignored and one row per port is printed.")
	probeCmd.Flags().IntVar(&probeParallel, "parallel", 8, "parallel probes when --ports is set (caps simultaneous DialStreams)")
	RootCmd.AddCommand(probeCmd)
}

var probeCmd = &cobra.Command{
	Use:   "probe <public-key> <port>",
	Short: "Probe a remote port's reachability over dmsg, skynet, or a direct TCP connection",
	Long: `Probe a remote port's reachability. The probe performs a full noise
handshake to the destination; if a listener is active, the handshake completes
and the probe reports success.

TRANSPORTS:
  (default)   dmsg — DialStream through a dmsg server bridge to <pk>:<port>.
  --skynet    skynet — dial a route over skywire's p2p transports to <pk>:<port>
              (uses the local visor's router; RPC only).
  --via tcp://<pk>@host:port
              a direct noise-TCP connection (no dmsg, no router). Reports
              whether the noise handshake to that endpoint completes for <pk>.

IDENTITY (dmsg / tcp):
  By default dmsg probes use the local visor's dmsg client via RPC. Use -s for a
  standalone client with an ephemeral key, or --sk <hex> to run standalone with
  a SPECIFIC identity (no visor needed). --via tcp uses --sk or an ephemeral key.

  --server <server-pk> forces a dmsg probe through a specific dmsg server, to ask
  "is <pk> reachable VIA this server?" — works in both RPC and standalone modes.

Common dmsg ports:
  80   - dmsghttp log server (/health, /ping)
  136  - route setup await port (used by RSN for route establishment)
  22   - pty (remote terminal)
  7    - dmsg ctrl
  8    - dmsg ping

Examples:
  skywire cli dmsg probe <pk> 136                    # dmsg via visor RPC
  skywire cli dmsg probe -s <pk> 136                 # dmsg standalone (ephemeral key)
  skywire cli dmsg probe --sk <hex> <pk> 80          # dmsg standalone with a specific key
  skywire cli dmsg probe --server <srv> <pk> 136     # dmsg via a specific server
  skywire cli dmsg probe --skynet <pk> 136           # over skynet (routed transports)
  skywire cli dmsg probe --via tcp://<pk>@host:8801  # direct noise-TCP connection`,
	Args: func(cmd *cobra.Command, args []string) error {
		internal.CheckDirectViaScheme(cmd.Flags(), probeVia, "a direct noise-TCP target (tcp://<pk>@host:port)")
		// --via tcp carries the pk in its URL, so no positional args are
		// required in that mode.
		if probeVia != "" {
			return nil
		}
		// --ports drives a multi-port sweep, so the positional <port>
		// is optional in that mode (but <pk> is still required).
		if probePorts != "" {
			if len(args) < 1 {
				return fmt.Errorf("requires at least <pk> when --ports is set")
			}
			return nil
		}
		return cobra.ExactArgs(2)(cmd, args)
	},
	Run: func(cmd *cobra.Command, args []string) {
		// Direct noise-TCP connection probe — fully self-contained.
		if probeVia != "" {
			runTCPProbe(cmd)
			return
		}

		// Mode validation: skynet needs the visor's router; it can't run
		// standalone.
		if probeSkynet && (standalone || probeSK != "") {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("--skynet uses the visor's router and cannot run standalone (-s/--sk)"))
		}
		if probeSkynet && probeServer != "" {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("--server only applies to dmsg probes, not --skynet"))
		}

		var pk cipher.PubKey
		if err := pk.Set(args[0]); err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("invalid public key: %w", err))
		}

		if probePorts != "" {
			ports, perr := parsePortSpec(probePorts)
			if perr != nil {
				internal.PrintFatalError(cmd.Flags(), perr)
			}
			runMultiPortProbe(cmd, pk, ports)
			return
		}

		port, err := strconv.ParseUint(args[1], 10, 16)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("invalid port: %w", err))
		}

		// Standalone dmsg (ephemeral or provided key).
		if standalone || probeSK != "" {
			runStandaloneProbe(cmd, pk, uint16(port))
			return
		}

		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}

		if probeVerbose && !probeSkynet {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			vs, vErr := clirpc.OpenVerbose(ctx, clirpc.Addr, clirpc.VerboseFilter{
				Modules: []string{"dmsgC", "dmsg_grpc", "dmsg_disc", "dmsg_tracker"},
				Level:   probeVerboseLevel,
			})
			if vErr != nil {
				internal.PrintFatalError(cmd.Flags(), vErr)
			}
			_ = vs.WaitSubscribed(ctx, 2*time.Second) //nolint:errcheck
			defer vs.Close()
		}

		start := time.Now()
		reachable, err := rpcProbeOne(rpcClient, pk, uint16(port))
		latency := time.Since(start)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("probe failed: %w", err))
		}

		emitProbeResult(cmd, pk, uint16(port), reachable, latency)
	},
}

// emitProbeResult prints one single-port probe result, honoring the
// global --json flag (the multi-port sweep already does). Keeps the
// human line identical to what the command has always printed.
func emitProbeResult(cmd *cobra.Command, pk cipher.PubKey, port uint16, reachable bool, latency time.Duration) {
	scheme, suffix := probeSchemeSuffix()
	if jsonMode, _ := cmd.Flags().GetBool(internal.JSONString); jsonMode { //nolint:errcheck
		b, _ := json.MarshalIndent(struct { //nolint:errcheck
			PK        string `json:"pk"`
			Port      uint16 `json:"port"`
			Transport string `json:"transport"`
			Server    string `json:"server,omitempty"`
			Reachable bool   `json:"reachable"`
			LatencyMS int64  `json:"latency_ms"`
		}{pk.String(), port, scheme, probeServer, reachable, latency.Milliseconds()}, "", "  ")
		fmt.Println(string(b))
		return
	}
	state := "unreachable"
	if reachable {
		state = "reachable"
	}
	fmt.Printf("%s://%s:%d%s — %s (%s)\n", scheme, pk, port, suffix, state, latency.Round(time.Millisecond))
}

// rpcProbeOne dispatches a single RPC-mode probe by the selected transport.
func rpcProbeOne(rpcClient interface {
	DmsgProbe(cipher.PubKey, uint16) (bool, error)
	DmsgProbeViaServer(cipher.PubKey, uint16, cipher.PubKey) (bool, error)
	SkynetProbe(cipher.PubKey, uint16) (bool, error)
}, pk cipher.PubKey, port uint16) (bool, error) {
	if probeSkynet {
		return rpcClient.SkynetProbe(pk, port)
	}
	if probeServer != "" {
		srv, err := parsePK(probeServer)
		if err != nil {
			return false, fmt.Errorf("invalid --server pk: %w", err)
		}
		return rpcClient.DmsgProbeViaServer(pk, port, srv)
	}
	return rpcClient.DmsgProbe(pk, port)
}

// probeSchemeSuffix returns the URL scheme + an optional suffix for the
// reachability print line, reflecting the chosen transport.
func probeSchemeSuffix() (string, string) {
	if probeSkynet {
		return "skynet", ""
	}
	if probeServer != "" {
		return "dmsg", " (via " + probeServer[:min(8, len(probeServer))] + "…)"
	}
	return "dmsg", ""
}

// parsePK parses a hex public key.
func parsePK(s string) (cipher.PubKey, error) {
	var pk cipher.PubKey
	err := pk.Set(s)
	return pk, err
}

// probeIdentity returns the (pk, sk) to use for standalone dmsg / tcp
// probes: the --sk key if provided, else a fresh ephemeral pair.
func probeIdentity() (cipher.PubKey, cipher.SecKey, error) {
	if probeSK != "" {
		var sk cipher.SecKey
		if err := sk.Set(probeSK); err != nil {
			return cipher.PubKey{}, cipher.SecKey{}, fmt.Errorf("invalid --sk: %w", err)
		}
		pk, err := sk.PubKey()
		if err != nil {
			return cipher.PubKey{}, cipher.SecKey{}, fmt.Errorf("derive pk from --sk: %w", err)
		}
		return pk, sk, nil
	}
	pk, sk := cipher.GenerateKeyPair()
	return pk, sk, nil
}

// runTCPProbe dials a direct noise-TCP connection to tcp://<pk>@host:port
// and reports whether the noise handshake completed (a listener answered).
func runTCPProbe(cmd *cobra.Command) {
	rPK, hostPort, err := parseViaTCP(probeVia)
	if err != nil {
		internal.PrintFatalError(cmd.Flags(), err)
	}
	myPK, mySK, err := probeIdentity()
	if err != nil {
		internal.PrintFatalError(cmd.Flags(), err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	start := time.Now()
	conn, err := tcpnoise.Dial(ctx, hostPort, myPK, mySK, rPK)
	latency := time.Since(start)
	reachable := err == nil
	if conn != nil {
		_ = conn.Close() //nolint:errcheck
	}
	if jsonMode, _ := cmd.Flags().GetBool(internal.JSONString); jsonMode { //nolint:errcheck
		b, _ := json.MarshalIndent(struct { //nolint:errcheck
			PK        string `json:"pk"`
			Transport string `json:"transport"`
			Addr      string `json:"addr"`
			Reachable bool   `json:"reachable"`
			LatencyMS int64  `json:"latency_ms"`
		}{rPK.String(), "tcp", hostPort, reachable, latency.Milliseconds()}, "", "  ")
		fmt.Println(string(b))
		return
	}
	state := "unreachable"
	if reachable {
		state = "reachable"
	}
	fmt.Printf("tcp://%s@%s — %s (%s)\n", rPK, hostPort, state, latency.Round(time.Millisecond))
}

// parseViaTCP turns "tcp://<66-hex-pk>@host:port" into (pk, "host:port").
func parseViaTCP(spec string) (cipher.PubKey, string, error) {
	const prefix = "tcp://"
	if !strings.HasPrefix(spec, prefix) {
		return cipher.PubKey{}, "", fmt.Errorf("--via must start with %q", prefix)
	}
	rest := spec[len(prefix):]
	at := strings.IndexByte(rest, '@')
	if at <= 0 || at >= len(rest)-1 {
		return cipher.PubKey{}, "", fmt.Errorf("--via tcp: expected tcp://<pk>@host:port, got %q", spec)
	}
	var pk cipher.PubKey
	if err := pk.Set(rest[:at]); err != nil {
		return cipher.PubKey{}, "", fmt.Errorf("--via tcp: invalid pk: %w", err)
	}
	return pk, rest[at+1:], nil
}

// parsePortSpec turns a "22,80,1000-1010,5000" spec into a
// deduplicated sorted []uint16. Range endpoints are inclusive.
func parsePortSpec(spec string) ([]uint16, error) {
	seen := map[uint16]struct{}{}
	out := make([]uint16, 0, 16)
	for _, raw := range strings.Split(spec, ",") {
		tok := strings.TrimSpace(raw)
		if tok == "" {
			continue
		}
		if dash := strings.IndexByte(tok, '-'); dash >= 0 {
			lo, err := strconv.ParseUint(strings.TrimSpace(tok[:dash]), 10, 16)
			if err != nil {
				return nil, fmt.Errorf("invalid range start in %q: %w", tok, err)
			}
			hi, err := strconv.ParseUint(strings.TrimSpace(tok[dash+1:]), 10, 16)
			if err != nil {
				return nil, fmt.Errorf("invalid range end in %q: %w", tok, err)
			}
			if hi < lo {
				return nil, fmt.Errorf("range %q has end < start", tok)
			}
			for p := lo; p <= hi; p++ {
				port := uint16(p) //nolint:gosec // ParseUint(..., 16) already bounds p
				if _, dup := seen[port]; !dup {
					seen[port] = struct{}{}
					out = append(out, port)
				}
			}
			continue
		}
		p, err := strconv.ParseUint(tok, 10, 16)
		if err != nil {
			return nil, fmt.Errorf("invalid port %q: %w", tok, err)
		}
		port := uint16(p)
		if _, dup := seen[port]; !dup {
			seen[port] = struct{}{}
			out = append(out, port)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("--ports parsed to empty list")
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}

// portProbeResult is the per-port record emitted by the multi-port sweep.
type portProbeResult struct {
	Port      uint16        `json:"port"`
	Reachable bool          `json:"reachable"`
	LatencyMS int64         `json:"latency_ms"`
	Latency   time.Duration `json:"-"`
	Err       string        `json:"error,omitempty"`
}

// runMultiPortProbe fans out parallel probes against a single pk across a
// port set, by the selected transport / identity.
func runMultiPortProbe(cmd *cobra.Command, pk cipher.PubKey, ports []uint16) {
	results := make([]portProbeResult, len(ports))

	if standalone || probeSK != "" {
		runMultiPortProbeStandalone(cmd, pk, ports, results)
	} else {
		runMultiPortProbeRPC(cmd, pk, ports, results)
	}

	jsonMode, _ := cmd.Flags().GetBool(internal.JSONString) //nolint:errcheck
	if jsonMode {
		for i := range results {
			results[i].LatencyMS = results[i].Latency.Milliseconds()
		}
		b, _ := json.MarshalIndent(struct { //nolint:errcheck
			PK      string            `json:"pk"`
			Results []portProbeResult `json:"results"`
		}{PK: pk.String(), Results: results}, "", "  ")
		fmt.Println(string(b))
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "PORT\tREACHABLE\tLATENCY\tERROR") //nolint:errcheck
	for _, r := range results {
		state := "no"
		if r.Reachable {
			state = "yes"
		}
		fmt.Fprintf(w, "%d\t%s\t%s\t%s\n", //nolint:errcheck
			r.Port, state, r.Latency.Round(time.Millisecond).String(), r.Err)
	}
	_ = w.Flush() //nolint:errcheck
}

// runMultiPortProbeRPC pumps probes through the local visor (dmsg /
// dmsg-via-server / skynet), capped at --parallel.
func runMultiPortProbeRPC(cmd *cobra.Command, pk cipher.PubKey, ports []uint16, results []portProbeResult) {
	rpcClient, err := clirpc.Client(cmd.Flags())
	if err != nil {
		internal.PrintFatalError(cmd.Flags(), err)
	}
	sem := make(chan struct{}, probeParallel)
	var wg sync.WaitGroup
	for i, p := range ports {
		i, p := i, p
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			start := time.Now()
			reachable, perr := rpcProbeOne(rpcClient, pk, p)
			results[i] = portProbeResult{Port: p, Reachable: reachable && perr == nil, Latency: time.Since(start)}
			if perr != nil {
				results[i].Err = perr.Error()
			}
		}()
	}
	wg.Wait()
}

// runMultiPortProbeStandalone bootstraps a single standalone dmsg client
// (ephemeral or --sk key) and probes each port, optionally via --server.
func runMultiPortProbeStandalone(cmd *cobra.Command, pk cipher.PubKey, ports []uint16, results []portProbeResult) {
	log := logging.MustGetLogger("dmsg-probe")
	myPK, mySK, err := probeIdentity()
	if err != nil {
		internal.PrintFatalError(cmd.Flags(), err)
	}
	var serverPK cipher.PubKey
	serverSet := probeServer != ""
	if serverSet {
		if serverPK, err = parsePK(probeServer); err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("invalid --server pk: %w", err))
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	dmsgC, stop, err := startDmsgClient(ctx, log, myPK, mySK)
	if err != nil {
		internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to start dmsg client: %w", err))
	}
	defer stop()

	sem := make(chan struct{}, probeParallel)
	var wg sync.WaitGroup
	for i, p := range ports {
		i, p := i, p
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			start := time.Now()
			var reachable bool
			if serverSet {
				reachable = dmsgC.ProbeViaServer(ctx, pk, p, serverPK)
			} else {
				reachable = dmsgC.Probe(ctx, pk, p)
			}
			results[i] = portProbeResult{Port: p, Reachable: reachable, Latency: time.Since(start)}
		}()
	}
	wg.Wait()
}

// runStandaloneProbe bootstraps a standalone dmsg client (ephemeral or
// --sk key) and probes a single port, optionally via --server.
func runStandaloneProbe(cmd *cobra.Command, pk cipher.PubKey, port uint16) {
	log := logging.MustGetLogger("dmsg-probe")
	myPK, mySK, err := probeIdentity()
	if err != nil {
		internal.PrintFatalError(cmd.Flags(), err)
	}
	var serverPK cipher.PubKey
	serverSet := probeServer != ""
	if serverSet {
		if serverPK, err = parsePK(probeServer); err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("invalid --server pk: %w", err))
		}
	}
	log.WithField("pk", myPK).Debug("Starting standalone dmsg client")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dmsgC, stop, err := startDmsgClient(ctx, log, myPK, mySK)
	if err != nil {
		internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to start dmsg client: %w", err))
	}
	defer stop()

	start := time.Now()
	var reachable bool
	if serverSet {
		reachable = dmsgC.ProbeViaServer(ctx, pk, port, serverPK)
	} else {
		reachable = dmsgC.Probe(ctx, pk, port)
	}
	latency := time.Since(start)

	emitProbeResult(cmd, pk, port, reachable, latency)
}
