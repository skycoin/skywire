// Package commands cmd/dmsg/conf/commands/probe.go c1-net-dmsg
package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/direct"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
)

var (
	probeTimeout  time.Duration
	probeEmbedded bool
	probeServerPK string
	probeCarriers []string
	probeJSON     bool
	probeParallel int
)

func init() {
	probeCmd.Flags().DurationVar(&probeTimeout, "timeout", 20*time.Second, "per-probe timeout (one carrier of one server)")
	probeCmd.Flags().BoolVar(&probeEmbedded, "embedded", false, "probe the embedded server set instead of the live discovery list")
	probeCmd.Flags().StringVar(&probeServerPK, "server", "", "probe only the server with this public key")
	probeCmd.Flags().StringSliceVar(&probeCarriers, "carrier", nil, "carriers to probe (tcp,quic,ws,wt); default: every carrier the server advertises")
	probeCmd.Flags().BoolVar(&probeJSON, "json", false, "emit results as JSON")
	probeCmd.Flags().IntVar(&probeParallel, "parallel", 8, "concurrent probes")
	RootCmd.AddCommand(probeCmd)
}

// probeResult is one carrier probe of one server.
type probeResult struct {
	PK        string `json:"pk"`
	Carrier   string `json:"carrier"` // tcp | quic | ws | wss | wt
	Addr      string `json:"addr"`
	OK        bool   `json:"ok"`
	LatencyMs int64  `json:"latency_ms"`
	Err       string `json:"error,omitempty"`
}

var probeCmd = &cobra.Command{
	Use:   "probe",
	Short: "Probe every advertised carrier of every dmsg server with a real session",
	Long: `Establish a REAL dmsg session (Noise handshake included) with each dmsg
server over each carrier its discovery entry advertises — tcp, quic,
ws/wss (the browser WebSocket carrier), and wt (WebTransport) — and
report per-carrier reachability and session-establishment latency.

This dials exactly what a client of that carrier would dial: the wss
probe validates the TLS front + WebSocket upgrade + Noise handshake a
browser wasm-visor performs, not just a TCP connect.

Server entries come from the live discovery (fetched over dmsg, like
'conf pull') unless --embedded selects the set embedded in this binary.`,
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableFlagsInUseLine: true,
	RunE: func(_ *cobra.Command, _ []string) error {
		entries, err := probeEntrySet()
		if err != nil {
			return err
		}
		if probeServerPK != "" {
			var pk cipher.PubKey
			if err := pk.Set(probeServerPK); err != nil {
				return fmt.Errorf("invalid --server public key: %w", err)
			}
			filtered := entries[:0]
			for _, e := range entries {
				if e.Static == pk {
					filtered = append(filtered, e)
				}
			}
			entries = filtered
			if len(entries) == 0 {
				return fmt.Errorf("server %s not in the %s set", pk, probeSourceName())
			}
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Static.Hex() < entries[j].Static.Hex() })

		type job struct {
			entry   *disc.Entry
			carrier string
		}
		var jobs []job
		for _, e := range entries {
			for _, c := range carriersOf(e) {
				if len(probeCarriers) > 0 && !carrierRequested(c) {
					continue
				}
				jobs = append(jobs, job{entry: e, carrier: c})
			}
		}
		if len(jobs) == 0 {
			return fmt.Errorf("nothing to probe (no advertised carrier matches the filter)")
		}

		results := make([]probeResult, len(jobs))
		sem := make(chan struct{}, probeParallel)
		var wg sync.WaitGroup
		for i, j := range jobs {
			wg.Add(1)
			go func(i int, j job) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				results[i] = probeOne(j.entry, j.carrier)
			}(i, j)
		}
		wg.Wait()

		if probeJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(results)
		}
		printProbeTable(entries, results)
		for _, r := range results {
			if !r.OK {
				os.Exit(1)
			}
		}
		return nil
	},
}

func probeSourceName() string {
	if probeEmbedded {
		return "embedded"
	}
	return "discovery"
}

// probeEntrySet returns the server entries to probe: the live discovery list
// (over dmsg, the same fetch 'conf pull' does) or the embedded set.
func probeEntrySet() ([]*disc.Entry, error) {
	if probeEmbedded {
		entries := deployment.Prod.ToDiscEntries()
		if len(entries) == 0 {
			return nil, fmt.Errorf("no embedded dmsg servers")
		}
		return entries, nil
	}
	// Reaching the discovery over dmsg means guessing a relay server it is
	// connected to (it publishes no client entry of its own), so a fetch can
	// fail with "202 - cannot connect to delegated server" purely by drawing an
	// unlucky relay. Each retry bootstraps fresh sessions and redraws.
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		entries, ferr := fetchAllServersOverDmsg(ctx)
		cancel()
		if ferr == nil {
			return entries, nil
		}
		err = ferr
	}
	return nil, fmt.Errorf("fetch all_servers over dmsg (3 attempts; use --embedded to skip): %w", err)
}

// carriersOf lists the carriers entry advertises, in probe order. The ws
// carrier is reported as wss when the advertised URL is TLS-fronted.
func carriersOf(e *disc.Entry) []string {
	var cs []string
	if e.Server.Address != "" {
		cs = append(cs, dmsg.CarrierTCP)
	}
	if e.Protocol == "quic" && e.Server.AddressUDP != "" {
		cs = append(cs, dmsg.CarrierQUIC)
	}
	if e.Server.AddressWS != "" {
		cs = append(cs, dmsg.CarrierWS)
	}
	if e.Server.AddressWT != "" {
		cs = append(cs, dmsg.CarrierWT)
	}
	return cs
}

func carrierRequested(c string) bool {
	for _, want := range probeCarriers {
		w := strings.ToLower(strings.TrimSpace(want))
		if w == c || (w == "wss" && c == dmsg.CarrierWS) {
			return true
		}
	}
	return false
}

// probeOne establishes one session with entry over exactly the given carrier
// (Carriers=[c] has no cross-carrier fallback) and reports the outcome.
func probeOne(entry *disc.Entry, carrier string) probeResult {
	e := *entry // copy: the client mutates nothing, but keep probes independent
	label := dmsg.ProtocolLabel(carrier, carrierAddr(&e, carrier))
	res := probeResult{PK: e.Static.Hex(), Carrier: label, Addr: carrierAddr(&e, carrier)}

	log := logging.MustGetLogger("dmsg-conf-probe")
	pk, sk := cipher.GenerateKeyPair()
	dClient := direct.NewClient(direct.GetAllEntries(cipher.PubKeys{pk}, []*disc.Entry{&e}), log)

	// Surface the real dial failure: StartDmsg retries until ctx expires and
	// then returns a generic error, so capture the per-dial error instead.
	var mu sync.Mutex
	var lastErr error
	conf := dmsg.DefaultConfig()
	conf.MinSessions = 1
	conf.Carriers = []string{carrier}
	conf.Callbacks = &dmsg.ClientCallbacks{
		OnSessionDisconnect: func(_, _ string, err error) {
			if err != nil {
				mu.Lock()
				lastErr = err
				mu.Unlock()
			}
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	start := time.Now()
	_, stop, err := direct.StartDmsg(ctx, log, pk, sk, dClient, conf)
	if err != nil {
		mu.Lock()
		if lastErr != nil {
			err = lastErr
		}
		mu.Unlock()
		res.Err = err.Error()
		return res
	}
	res.OK = true
	res.LatencyMs = time.Since(start).Milliseconds()
	stop()
	return res
}

func carrierAddr(e *disc.Entry, carrier string) string {
	switch carrier {
	case dmsg.CarrierTCP:
		return e.Server.Address
	case dmsg.CarrierQUIC:
		return e.Server.AddressUDP
	case dmsg.CarrierWS:
		return e.Server.AddressWS
	case dmsg.CarrierWT:
		return e.Server.AddressWT
	}
	return ""
}

// printProbeTable renders one row per server with a column per carrier.
func printProbeTable(entries []*disc.Entry, results []probeResult) {
	byPK := map[string]map[string]probeResult{}
	carrierSet := map[string]bool{}
	for _, r := range results {
		if byPK[r.PK] == nil {
			byPK[r.PK] = map[string]probeResult{}
		}
		byPK[r.PK][r.Carrier] = r
		carrierSet[r.Carrier] = true
	}
	// Stable column order.
	var cols []string
	for _, c := range []string{"tcp", "quic", "ws", "wss", "webtransport"} {
		if carrierSet[c] {
			cols = append(cols, c)
		}
	}
	fmt.Printf("%-66s", "server")
	for _, c := range cols {
		fmt.Printf(" %-12s", c)
	}
	fmt.Println()
	for _, e := range entries {
		pk := e.Static.Hex()
		row, ok := byPK[pk]
		if !ok {
			continue
		}
		fmt.Printf("%-66s", pk)
		for _, c := range cols {
			r, ok := row[c]
			switch {
			case !ok:
				fmt.Printf(" %-12s", "-")
			case r.OK:
				fmt.Printf(" %-12s", fmt.Sprintf("ok %dms", r.LatencyMs))
			default:
				fmt.Printf(" %-12s", "FAIL")
			}
		}
		fmt.Println()
	}
	// Failure details below the table so rows stay scannable.
	for _, r := range results {
		if !r.OK {
			fmt.Printf("FAIL %s %s (%s): %s\n", r.PK, r.Carrier, r.Addr, r.Err)
		}
	}
}
