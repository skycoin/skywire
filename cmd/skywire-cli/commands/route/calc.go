// Package cliroute cmd/skywire-cli/commands/route/calc.go
package cliroute

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/cipher"
	routeFinder "github.com/skycoin/skywire/pkg/route-finder/store"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/transport-discovery/store"
	tptypes "github.com/skycoin/skywire/pkg/transport/types"
	"github.com/skycoin/skywire/pkg/visor/rpcgrpc"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

var (
	calcEnable   bool
	calcDisable  bool
	calcTimeout  time.Duration
	tpdURL       string
	calcMinHops  uint16
	calcMaxHops  uint16
	calcCount    int
	calcSource   string
	calcQueueCap int
)

func init() {
	calcCmd.Flags().BoolVar(&calcEnable, "enable", false, "enable local route calculation in visor")
	calcCmd.Flags().BoolVar(&calcDisable, "disable", false, "disable local route calculation in visor")
	calcCmd.Flags().DurationVarP(&calcTimeout, "timeout", "t", 30*time.Second, "request timeout")
	calcCmd.Flags().StringVarP(&tpdURL, "tpd", "a", deployment.Prod.TransportDiscovery, "transport discovery URL")
	calcCmd.Flags().Uint16VarP(&calcMinHops, "min", "n", 0, "minimum hops (0 = use visor's routing.min_hops, fallback 1)")
	calcCmd.Flags().Uint16VarP(&calcMaxHops, "max", "x", 5, "maximum hops")
	calcCmd.Flags().IntVarP(&calcCount, "count", "c", 1, "max routes to return (0 = all matching)")
	calcCmd.Flags().StringVar(&calcSource, "source", "tpd", "transport graph source: tpd (HTTP), dht (visor's local DHT store), auto (DHT then TPD)")
	calcCmd.Flags().IntVar(&calcQueueCap, "queue-cap", 0, "BFS queue cap (0 = server/local default ~200K, negative = unbounded)")
	clirpc.RegisterFetchFlags(calcCmd)
}

var calcCmd = &cobra.Command{
	Use:   "calc [<src-pk>] <dst-pk>",
	Short: "Calculate routes locally or control visor's local route calculation",
	Long: `Calculate routes locally using transport discovery data

	skywire cli route calc <dst-pk>           - calculate route to destination
	skywire cli route calc <src-pk> <dst-pk>  - calculate route between two visors
	skywire cli route calc --enable           - enable local route calculation in visor
	skywire cli route calc --disable          - disable local route calculation in visor`,
	Run: func(cmd *cobra.Command, args []string) {
		if calcEnable && calcDisable {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("cannot use both --enable and --disable"))
		}

		// Handle config flags
		if calcEnable || calcDisable {
			rpcClient, err := clirpc.Client(cmd.Flags())
			if err != nil {
				internal.PrintFatalError(cmd.Flags(), err)
			}
			if calcEnable {
				err := rpcClient.SetCalculateRoutes(true)
				internal.Catch(cmd.Flags(), err)
				internal.PrintOutput(cmd.Flags(), map[string]string{"status": "enabled"}, "local route calculation enabled\n")
			} else {
				err := rpcClient.SetCalculateRoutes(false)
				internal.Catch(cmd.Flags(), err)
				internal.PrintOutput(cmd.Flags(), map[string]string{"status": "disabled"}, "local route calculation disabled\n")
			}
			return
		}

		// No flags and no args - show status
		if len(args) == 0 {
			rpcClient, err := clirpc.Client(cmd.Flags())
			if err != nil {
				internal.PrintFatalError(cmd.Flags(), err)
			}
			status, err := rpcClient.GetCalculateRoutes()
			internal.Catch(cmd.Flags(), err)
			if status {
				internal.PrintOutput(cmd.Flags(), map[string]bool{"enabled": true}, "local route calculation is enabled\n")
			} else {
				internal.PrintOutput(cmd.Flags(), map[string]bool{"enabled": false}, "local route calculation is disabled\n")
			}
			return
		}

		// Calculate route with provided PKs
		var srcPK, dstPK cipher.PubKey

		// Try the visor RPC once for both srcPK fallback and min_hops default.
		// Best-effort: if RPC isn't available we fall back to local config / 1.
		rpcClient, _ := clirpc.Client(cmd.Flags()) //nolint:errcheck

		if len(args) == 1 {
			if rpcClient != nil {
				if overview, err := rpcClient.Overview(); err == nil {
					srcPK = overview.PubKey
				}
			}
			if srcPK.Null() {
				configPath := findConfig()
				if configPath != "" {
					conf, err := visorconfig.ReadFile(configPath)
					if err == nil {
						srcPK = conf.PK
					}
				}
			}
			if srcPK.Null() {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("could not determine source public key"))
			}
			internal.Catch(cmd.Flags(), dstPK.Set(args[0]))
		} else {
			internal.Catch(cmd.Flags(), srcPK.Set(args[0]))
			internal.Catch(cmd.Flags(), dstPK.Set(args[1]))
		}

		// Resolve min_hops: 0 means "ask the visor what it would use".
		minHops := calcMinHops
		if minHops == 0 {
			if rpcClient != nil {
				if n, err := rpcClient.GetMinHops(); err == nil {
					minHops = n
				}
			}
			if minHops == 0 {
				minHops = 1
			}
		}

		ctx, cancel := context.WithTimeout(context.Background(), calcTimeout)
		defer cancel()

		// Prefer streaming via the local visor's gRPC endpoint when
		// available. The server-side BFS emits each path as soon as
		// it's found; the CLI prints them as they arrive without
		// holding the full result set in memory. This is the only
		// path that can handle --count 0 (unbounded) on dense graphs
		// without OOMing the CLI.
		if rpcClient != nil && strings.ToLower(calcSource) != "dht" {
			if err := streamRoutesViaGRPC(ctx, cmd, srcPK, dstPK, minHops, calcMaxHops, calcCount, calcQueueCap, calcSource, tpdURL); err == nil {
				return
			} else if !isFallbackEligible(err) {
				internal.PrintFatalError(cmd.Flags(), err)
			}
			// fallback to local compute below
		}

		// Local fallback path: bounded count only. The streaming path
		// above is the right tool for unbounded; this branch is for
		// offline use (no visor RPC) where memory pressure is on the
		// caller's machine but they explicitly chose this mode.
		count := calcCount
		if count <= 0 {
			count = math.MaxInt32
		}

		var entries []*transport.Entry
		var err error
		// --source dht / auto previously walked the local DHT store
		// before falling back to TPD. The DHT subsystem has been
		// removed; both modes now go straight to TPD.
		switch strings.ToLower(calcSource) {
		case "tpd", "auto", "dht":
			entries, err = fetchAllTransports(ctx, cmd.Flags(), tpdURL)
		default:
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("invalid --source %q; expected tpd|auto", calcSource))
		}
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		if len(entries) == 0 {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("no transport entries available from %s", calcSource))
		}

		memStore := newMemoryStoreFromEntries(entries)
		graph, err := routeFinder.NewGraphWithDepth(ctx, memStore, srcPK, int(calcMaxHops))
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to build graph: %w", err))
		}

		routes, err := graph.GetRoute(ctx, srcPK, dstPK, int(minHops), int(calcMaxHops), count)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("no route found: %w", err))
		}

		type routePair struct {
			Forward []routing.Hop `json:"forward"`
			Reverse []routing.Hop `json:"reverse"`
		}
		pairs := make([]routePair, len(routes))
		var textBuf strings.Builder
		for i, r := range routes {
			fwd := r.Hops
			rev := reverseHops(fwd)
			pairs[i] = routePair{Forward: fwd, Reverse: rev}
			if i > 0 {
				textBuf.WriteString("\n\n")
			}
			if calcCount != 1 {
				fmt.Fprintf(&textBuf, "[%d/%d]\n", i+1, len(routes))
			}
			fmt.Fprintf(&textBuf, "forward: %v\nreverse: %v", fwd, rev)
		}
		textBuf.WriteString("\n")

		// Preserve the single-route shape for back-compat when --count=1.
		if calcCount == 1 && len(pairs) == 1 {
			internal.PrintOutput(cmd.Flags(), pairs[0], textBuf.String())
			return
		}
		internal.PrintOutput(cmd.Flags(), pairs, textBuf.String())
	},
}

// streamRoutesViaGRPC opens the visor's StreamCalcRoutes gRPC stream and
// prints each route as it arrives. JSON mode collects routes into a slice
// and emits one final array; text mode writes one route per arrival
// directly to stdout (no full-result buffering, so --count 0 is safe).
func streamRoutesViaGRPC(
	ctx context.Context,
	cmd *cobra.Command,
	srcPK, dstPK cipher.PubKey,
	minHops, maxHops uint16,
	count, queueCap int,
	source, tpdU string,
) error {
	grpcClient, err := rpcgrpc.NewPingClient(clirpc.Addr)
	if err != nil {
		return fmt.Errorf("grpc dial: %w", err)
	}
	defer grpcClient.Close() //nolint:errcheck,gosec

	type routePair struct {
		Forward []routing.Hop `json:"forward"`
		Reverse []routing.Hop `json:"reverse"`
	}
	isJSON, _ := cmd.Flags().GetBool(internal.JSONString) //nolint:errcheck
	var collected []routePair

	emitted := 0
	cb := func(r *rpcgrpc.CalcRoute) bool {
		fwd, err := protoHopsToRouting(r.Hops)
		if err != nil {
			fmt.Fprintf(os.Stderr, "calc: skipping malformed route: %v\n", err)
			return true
		}
		rev := reverseHops(fwd)
		emitted++

		if isJSON {
			collected = append(collected, routePair{Forward: fwd, Reverse: rev})
			return count == 0 || emitted < count
		}

		// Text streaming: print as it arrives.
		if emitted > 1 {
			fmt.Println()
		}
		if count != 1 {
			fmt.Printf("[%d]\n", emitted)
		}
		fmt.Printf("forward: %v\nreverse: %v\n", fwd, rev)
		return count == 0 || emitted < count
	}

	if err := grpcClient.StreamCalcRoutes(
		ctx,
		srcPK.String(),
		dstPK.String(),
		int32(minHops),  //nolint:gosec
		int32(maxHops),  //nolint:gosec
		int32(count),    //nolint:gosec
		int32(queueCap), //nolint:gosec
		source,
		tpdU,
		cb,
	); err != nil {
		if emitted == 0 {
			return fmt.Errorf("no route found: %w", err)
		}
		// Some routes arrived before the stream errored; treat as success.
		fmt.Fprintf(os.Stderr, "calc: stream ended early: %v\n", err)
	}

	if emitted == 0 {
		return fmt.Errorf("no route found: %w", routeFinder.ErrRouteNotFound)
	}
	if isJSON {
		// Single-route back-compat shape preserved when count=1.
		if count == 1 && len(collected) == 1 {
			out, _ := json.Marshal(collected[0]) //nolint:errcheck
			fmt.Println(string(out))
		} else {
			out, _ := json.Marshal(collected) //nolint:errcheck
			fmt.Println(string(out))
		}
	}
	return nil
}

// protoHopsToRouting decodes the wire-form CalcHops back into the
// routing.Hop shape used by the rest of the CLI's printing/JSON code.
func protoHopsToRouting(hops []*rpcgrpc.CalcHop) ([]routing.Hop, error) {
	out := make([]routing.Hop, 0, len(hops))
	for _, h := range hops {
		tpID, err := uuid.Parse(h.TpId)
		if err != nil {
			return nil, fmt.Errorf("tp_id: %w", err)
		}
		var from, to cipher.PubKey
		if err := from.Set(h.From); err != nil {
			return nil, fmt.Errorf("from: %w", err)
		}
		if err := to.Set(h.To); err != nil {
			return nil, fmt.Errorf("to: %w", err)
		}
		out = append(out, routing.Hop{TpID: tpID, From: from, To: to})
	}
	return out, nil
}

// isFallbackEligible reports whether a gRPC-side calc error should
// trigger the local fallback path. A connection error (gRPC server not
// up, address invalid) is eligible; a logical error from the server
// (no route, invalid PK) is final.
func isFallbackEligible(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "grpc dial") ||
		strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "no such host") ||
		strings.Contains(msg, "transport: Error")
}

func findConfig() string {
	if _, err := os.Stat(skyenv.SkywirePath + "/" + skyenv.ConfigJSON); err == nil {
		return skyenv.SkywirePath + "/" + skyenv.ConfigJSON
	}
	if _, err := os.Stat(skyenv.ConfigName); err == nil {
		return skyenv.ConfigName
	}
	return ""
}

func reverseHops(fwd []routing.Hop) []routing.Hop {
	rev := make([]routing.Hop, len(fwd))
	for i, hop := range fwd {
		rev[len(fwd)-1-i] = routing.Hop{TpID: hop.TpID, From: hop.To, To: hop.From}
	}
	return rev
}

func fetchAllTransports(_ context.Context, cmdFlags *pflag.FlagSet, tpdAddr string) ([]*transport.Entry, error) {
	url := strings.TrimSuffix(tpdAddr, "/") + "/all-transports"
	// Use visor RPC → DMSG direct → HTTP fallback chain so the CLI picks
	// up whatever transport the visor is configured to use.
	body, err := clirpc.FetchServiceURL(cmdFlags, url)
	if err != nil {
		return nil, err
	}
	var entries []*transport.Entry
	if err := json.Unmarshal(body, &entries); err != nil {
		return nil, err
	}
	return entries, nil
}

// memoryStore wraps fetched transports to implement store.Store for route-finder's Graph
type memoryStore struct {
	entries []*transport.Entry
	byEdge  map[cipher.PubKey][]*transport.Entry
}

func newMemoryStoreFromEntries(entries []*transport.Entry) *memoryStore {
	byEdge := make(map[cipher.PubKey][]*transport.Entry)
	for _, e := range entries {
		if e != nil {
			byEdge[e.Edges[0]] = append(byEdge[e.Edges[0]], e)
			byEdge[e.Edges[1]] = append(byEdge[e.Edges[1]], e)
		}
	}
	return &memoryStore{entries: entries, byEdge: byEdge}
}

func (s *memoryStore) GetTransportsByEdgeNoLatency(ctx context.Context, pk cipher.PubKey) ([]*transport.Entry, error) {
	return s.GetTransportsByEdge(ctx, pk)
}

func (s *memoryStore) GetTransportsByEdge(_ context.Context, pk cipher.PubKey) ([]*transport.Entry, error) {
	if tps, ok := s.byEdge[pk]; ok {
		return tps, nil
	}
	return nil, store.ErrTransportNotFound
}

// Unused interface methods - stubs for store.Store compliance
func (s *memoryStore) RegisterTransport(context.Context, *transport.SignedEntry) error { return nil }
func (s *memoryStore) RegisterTransportsBatch(context.Context, []*transport.SignedEntry) error {
	return nil
}
func (s *memoryStore) DeregisterTransport(context.Context, uuid.UUID) error { return nil }
func (s *memoryStore) GetTransportByID(context.Context, uuid.UUID) (*transport.Entry, error) {
	return nil, nil
}
func (s *memoryStore) GetNumberOfTransports(context.Context) (map[tptypes.Type]int, error) {
	return nil, nil
}
func (s *memoryStore) GetAllTransports(context.Context, bool) ([]*transport.Entry, error) {
	return s.entries, nil
}
func (s *memoryStore) UpdateBandwidth(context.Context, string, cipher.PubKey, uint64, uint64) error {
	return nil
}
func (s *memoryStore) UpdateLatency(context.Context, string, float64, float64, float64) error {
	return nil
}
func (s *memoryStore) GetTransportBandwidth(context.Context, uuid.UUID, string, int) ([]store.BandwidthAggregation, error) {
	return nil, nil
}
func (s *memoryStore) GetVisorBandwidth(context.Context, cipher.PubKey, string, int) ([]store.BandwidthAggregation, error) {
	return nil, nil
}
func (s *memoryStore) GetAllVisorSummaries(context.Context, bool, bool) ([]store.VisorSummary, error) {
	return nil, nil
}
func (s *memoryStore) RecordHeartbeat(context.Context, cipher.PubKey, string) error {
	return nil
}
func (s *memoryStore) GetDailyTimeline(context.Context, string, time.Time) map[string]string {
	return nil
}
func (s *memoryStore) RecordTransportHeartbeat(context.Context, uuid.UUID, string, time.Time) error {
	return nil
}
func (s *memoryStore) IngestTransportTimeline(context.Context, uuid.UUID, string, []byte) error {
	return nil
}
func (s *memoryStore) GetTransportUptimeSummaries(context.Context, []uuid.UUID, bool, bool) ([]store.TransportUptimeSummary, error) {
	return nil, nil
}
func (s *memoryStore) GetTransportUptimeByVisor(context.Context, cipher.PubKey, bool, bool) ([]store.TransportUptimeSummary, error) {
	return nil, nil
}
func (s *memoryStore) GetTransportDailyTimeline(context.Context, string, time.Time) map[string]string {
	return nil
}
func (s *memoryStore) BackupAndCleanOldBandwidth(context.Context, string) error { return nil }
func (s *memoryStore) GetNetworkMetrics(context.Context, store.MetricsQuery) (*store.NetworkMetricResponse, error) {
	return nil, nil
}
func (s *memoryStore) GetVisorAggregateMetrics(context.Context, []cipher.PubKey, store.MetricsQuery) (map[string]*store.VisorMetricResponse, error) {
	return nil, nil
}
func (s *memoryStore) GetAllTransportMetrics(context.Context, store.MetricsQuery) ([]store.TransportMetric, error) {
	return nil, nil
}
func (s *memoryStore) GetTransportMetricsByIDs(context.Context, []uuid.UUID, store.MetricsQuery) ([]store.TransportMetric, error) {
	return nil, nil
}
func (s *memoryStore) GetTransportMetricsByVisors(context.Context, []cipher.PubKey, store.MetricsQuery) ([]store.TransportMetric, error) {
	return nil, nil
}
func (s *memoryStore) Close() {}
