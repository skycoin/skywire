// Package clidmsg cmd/skywire-cli/commands/dmsg/probe.go
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
)

var (
	standalone        bool
	probeVerbose      bool
	probeVerboseLevel string
	probePorts        string
	probeParallel     int
)

func init() {
	probeCmd.Flags().BoolVarP(&standalone, "standalone", "s", false, "use a standalone dmsg client (no running visor needed)")
	probeCmd.Flags().BoolVarP(&probeVerbose, "verbose", "v", false, "stream visor's dmsg-layer logs to stderr while probing (single-port mode only)")
	probeCmd.Flags().StringVar(&probeVerboseLevel, "verbose-level", "debug", "minimum log level when --verbose is set: trace|debug|info|warn|error")
	probeCmd.Flags().StringVar(&probePorts, "ports", "", "multi-port sweep — comma list with optional ranges, e.g. '22,80,1000-1010,5000'. When set, the positional <port> arg is ignored and one row per port is printed.")
	probeCmd.Flags().IntVar(&probeParallel, "parallel", 8, "parallel probes when --ports is set (caps simultaneous DialStreams)")
	RootCmd.AddCommand(probeCmd)
}

var probeCmd = &cobra.Command{
	Use:   "probe <public-key> <port>",
	Short: "Probe a remote visor's dmsg port reachability",
	Long: `Probe a remote visor on a specific dmsg port via dmsg.

The probe performs a full DialStream (noise handshake) through the dmsg server
bridge to the destination. If a listener is active on the specified port, the
handshake completes and the probe reports success. If nothing is listening,
the probe reports failure.

By default, the probe uses the local visor's dmsg client via RPC. Use -s to
bootstrap a standalone dmsg client (no running visor required).

Common ports:
  80   - dmsghttp log server (/health, /ping)
  136  - route setup await port (used by RSN for route establishment)
  22   - dmsgpty (remote terminal)
  7    - dmsg ctrl
  8    - dmsg ping

Examples:
  skywire cli dmsg probe <pk> 136        # via visor RPC
  skywire cli dmsg probe -s <pk> 136     # standalone (no visor needed)
  skywire cli dmsg probe -s <pk> 80      # check if log server is up`,
	Args: func(cmd *cobra.Command, args []string) error {
		// --ports drives a multi-port sweep, so the positional <port>
		// is optional in that mode. Without --ports we require both
		// <pk> and <port>.
		if probePorts != "" {
			if len(args) < 1 {
				return fmt.Errorf("requires at least <pk> when --ports is set")
			}
			return nil
		}
		return cobra.ExactArgs(2)(cmd, args)
	},
	Run: func(cmd *cobra.Command, args []string) {
		var pk cipher.PubKey
		if err := pk.Set(args[0]); err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("invalid public key: %w", err))
		}

		// Multi-port mode short-circuits before the positional-port
		// parse. Standalone vs RPC dispatch still applies; sweep
		// fans out parallel probes capped at --parallel.
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

		if standalone {
			runStandaloneProbe(cmd, pk, uint16(port))
			return
		}

		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}

		// --verbose: subscribe to dmsg-layer logs for the duration of
		// the probe so DialStream activity is visible in real time.
		if probeVerbose {
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
		reachable, err := rpcClient.DmsgProbe(pk, uint16(port))
		latency := time.Since(start)

		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("probe failed: %w", err))
		}

		if reachable {
			fmt.Printf("dmsg://%s:%d — reachable (%s)\n", pk, port, latency.Round(time.Millisecond))
		} else {
			fmt.Printf("dmsg://%s:%d — unreachable (%s)\n", pk, port, latency.Round(time.Millisecond))
		}
	},
}

// parsePortSpec turns a "22,80,1000-1010,5000" spec into a
// deduplicated sorted []uint16. Range endpoints are inclusive.
// Invalid tokens / out-of-range values return an error rather
// than silently dropping — operators get an explicit complaint.
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
				port := uint16(p)
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

// portProbeResult is the per-port record emitted by the multi-port
// sweep. Kept compact for the table renderer; the JSON output uses
// the same struct.
type portProbeResult struct {
	Port      uint16        `json:"port"`
	Reachable bool          `json:"reachable"`
	LatencyMS int64         `json:"latency_ms"`
	Latency   time.Duration `json:"-"`
	Err       string        `json:"error,omitempty"`
}

// runMultiPortProbe fans out parallel probes against a single pk
// across a port set. Standalone-dmsg uses one client for all
// probes (cheaper than spinning N clients). RPC path reuses the
// local visor's dmsg client via DmsgProbe.
func runMultiPortProbe(cmd *cobra.Command, pk cipher.PubKey, ports []uint16) {
	results := make([]portProbeResult, len(ports))

	if standalone {
		runMultiPortProbeStandalone(cmd, pk, ports, results)
	} else {
		runMultiPortProbeRPC(cmd, pk, ports, results)
	}

	jsonMode, _ := cmd.Flags().GetBool(internal.JSONString) //nolint:errcheck
	if jsonMode {
		// Populate the int latency-ms field before marshaling so
		// the JSON consumer doesn't see a "0ns" stringified
		// time.Duration.
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
	fmt.Fprintln(w, "PORT\tREACHABLE\tLATENCY\tERROR")
	for _, r := range results {
		state := "no"
		if r.Reachable {
			state = "yes"
		}
		fmt.Fprintf(w, "%d\t%s\t%s\t%s\n",
			r.Port, state, r.Latency.Round(time.Millisecond).String(), r.Err)
	}
	_ = w.Flush() //nolint:errcheck
}

// runMultiPortProbeRPC pumps probes through the local visor's
// DmsgProbe RPC. Capped concurrency via probeParallel; the visor's
// own dmsg client multiplexes requests but we don't want to fire
// a few hundred goroutines if --ports has a wide range.
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
			reachable, perr := rpcClient.DmsgProbe(pk, p)
			results[i] = portProbeResult{
				Port:      p,
				Reachable: reachable && perr == nil,
				Latency:   time.Since(start),
			}
			if perr != nil {
				results[i].Err = perr.Error()
			}
		}()
	}
	wg.Wait()
}

// runMultiPortProbeStandalone bootstraps a single standalone dmsg
// client and uses dmsgC.Probe for each port. Cheaper than the
// RPC path when no visor is up; identical semantics otherwise.
func runMultiPortProbeStandalone(cmd *cobra.Command, pk cipher.PubKey, ports []uint16, results []portProbeResult) {
	log := logging.MustGetLogger("dmsg-probe")
	myPK, mySK := cipher.GenerateKeyPair()
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
			reachable := dmsgC.Probe(ctx, pk, p)
			results[i] = portProbeResult{
				Port:      p,
				Reachable: reachable,
				Latency:   time.Since(start),
			}
		}()
	}
	wg.Wait()
}

func runStandaloneProbe(cmd *cobra.Command, pk cipher.PubKey, port uint16) {
	log := logging.MustGetLogger("dmsg-probe")

	// Generate ephemeral identity for this probe.
	myPK, mySK := cipher.GenerateKeyPair()
	log.WithField("pk", myPK).Debug("Starting standalone dmsg client")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	dmsgC, stop, err := startDmsgClient(ctx, log, myPK, mySK)
	if err != nil {
		internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to start dmsg client: %w", err))
	}
	defer stop()

	start := time.Now()
	reachable := dmsgC.Probe(ctx, pk, port)
	latency := time.Since(start)

	if reachable {
		fmt.Printf("dmsg://%s:%d — reachable (%s)\n", pk, port, latency.Round(time.Millisecond))
	} else {
		fmt.Printf("dmsg://%s:%d — unreachable (%s)\n", pk, port, latency.Round(time.Millisecond))
	}
}
