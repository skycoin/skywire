// Package cliroute cmd/skywire-cli/commands/route/calc.go
package cliroute

import (
	"context"
	"encoding/hex"
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
	"github.com/skycoin/skywire/pkg/dht"
	routeFinder "github.com/skycoin/skywire/pkg/route-finder/store"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/transport-discovery/store"
	tptypes "github.com/skycoin/skywire/pkg/transport/types"
	"github.com/skycoin/skywire/pkg/visor"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

var (
	calcEnable  bool
	calcDisable bool
	calcTimeout time.Duration
	tpdURL      string
	calcMinHops uint16
	calcMaxHops uint16
	calcCount   int
	calcSource  string
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

		// 0 means "every route the BFS finds". GetRoute walks the slice
		// returned by finder until it hits the cap, so a large sentinel
		// is equivalent to "all".
		count := calcCount
		if count <= 0 {
			count = math.MaxInt32
		}

		// Fetch all transports and calculate route
		ctx, cancel := context.WithTimeout(context.Background(), calcTimeout)
		defer cancel()

		var entries []*transport.Entry
		var err error
		switch strings.ToLower(calcSource) {
		case "tpd":
			entries, err = fetchAllTransports(ctx, cmd.Flags(), tpdURL)
		case "dht":
			if rpcClient == nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("--source dht requires a running visor (RPC unavailable)"))
			}
			entries, err = fetchAllTransportsFromDHT(rpcClient)
		case "auto":
			if rpcClient != nil {
				entries, err = fetchAllTransportsFromDHT(rpcClient)
				if err != nil || len(entries) < 10 {
					// DHT had no useful data; fall back.
					entries, err = fetchAllTransports(ctx, cmd.Flags(), tpdURL)
				}
			} else {
				entries, err = fetchAllTransports(ctx, cmd.Flags(), tpdURL)
			}
		default:
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("invalid --source %q; expected tpd|dht|auto", calcSource))
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

		// Preserve the single-route shape for back-compat when --count=1.
		if calcCount == 1 && len(pairs) == 1 {
			internal.PrintOutput(cmd.Flags(), pairs[0], textBuf.String())
			return
		}
		internal.PrintOutput(cmd.Flags(), pairs, textBuf.String())
	},
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

// fetchAllTransportsFromDHT pulls every "tp" salt entry from the local
// DHT store and parses it as a list of transport.Entry. Three formats
// coexist under this salt:
//
//  1. Bare entries (TPDAdapter): []transport.Entry — used directly.
//  2. SignedEntries: []transport.SignedEntry — unwrap the .Entry.
//  3. Compact entries: [{r, t, l}] — published by deployment-side
//     mirrors, missing the source PK in the value. We recover the
//     source PK by cross-referencing the tp entry's storage-target
//     hash against the dmsg salt's index: every dmsg entry contains
//     its publisher PK in `static`, and target = SHA256(pk || salt),
//     so the dmsg salt is a known PK→target index for every visor
//     that publishes both. Targets we can't resolve via that index
//     are skipped.
//
// Synthetic transport IDs are generated for compact entries (the
// tuple is deterministic, so re-running on the same data gives the
// same IDs). Latency is preserved as Entry.Latency.
//
// On a full node the bare-entry coverage is comprehensive; on a
// non-full node the local store is sparse and this function will
// return only entries near the local node ID. For comparison
// against TPD HTTP, run on a full node after a reconcile pass.
func fetchAllTransportsFromDHT(rpcClient visor.API) ([]*transport.Entry, error) {
	tpBody, err := rpcClient.DHTListWithTargets("tp")
	if err != nil {
		return nil, fmt.Errorf("DHT ListWithTargets(tp): %w", err)
	}
	// Build the target→PK index from the dmsg salt.
	pkByTpTarget, err := buildTpTargetIndex(rpcClient)
	if err != nil {
		// Non-fatal: we still have bare entries, just no compact recovery.
		pkByTpTarget = nil
	}

	type targeted struct {
		Target string          `json:"target"`
		Value  json.RawMessage `json:"value"`
	}
	var rows []targeted
	if err := json.Unmarshal([]byte(tpBody), &rows); err != nil {
		return nil, fmt.Errorf("parse DHT tp envelope: %w", err)
	}

	var entries []*transport.Entry
	bare, signed, compact, unresolved := 0, 0, 0, 0

	for _, row := range rows {
		// Try bare-entry format first.
		var bareBatch []*transport.Entry
		if json.Unmarshal(row.Value, &bareBatch) == nil && len(bareBatch) > 0 {
			anyResolved := false
			for _, e := range bareBatch {
				if e == nil {
					continue
				}
				if !e.Edges[0].Null() && !e.Edges[1].Null() {
					entries = append(entries, e)
					anyResolved = true
					continue
				}
			}
			if anyResolved {
				bare++
				continue
			}
		}

		// Try SignedEntry format.
		var signedBatch []*transport.SignedEntry
		if json.Unmarshal(row.Value, &signedBatch) == nil && len(signedBatch) > 0 {
			anyResolved := false
			for _, se := range signedBatch {
				if se == nil || se.Entry == nil {
					continue
				}
				if !se.Entry.Edges[0].Null() && !se.Entry.Edges[1].Null() {
					entries = append(entries, se.Entry)
					anyResolved = true
				}
			}
			if anyResolved {
				signed++
				continue
			}
		}

		// Try compact format. We need the source PK from the
		// dmsg-salt index.
		var compactBatch []compactTpEntry
		if json.Unmarshal(row.Value, &compactBatch) != nil {
			continue
		}
		srcPK, ok := pkByTpTarget[row.Target]
		if !ok {
			unresolved++
			continue
		}
		for _, c := range compactBatch {
			if c.Remote == "" || c.Type == "" {
				continue
			}
			var rPK cipher.PubKey
			if err := rPK.Set(c.Remote); err != nil {
				continue
			}
			edges := transport.SortEdges(srcPK, rPK)
			tpType := tptypes.Type(c.Type)
			e := &transport.Entry{
				ID:      transport.MakeTransportID(srcPK, rPK, tpType),
				Edges:   edges,
				Type:    tpType,
				Latency: c.Latency,
			}
			entries = append(entries, e)
		}
		compact++
	}

	if len(entries) == 0 {
		return nil, fmt.Errorf("DHT tp salt empty or unparseable (rows=%d, bare=%d, signed=%d, compact=%d, unresolved=%d)",
			len(rows), bare, signed, compact, unresolved)
	}
	return entries, nil
}

// compactTpEntry is the single-letter shape some publishers emit
// under the tp salt. The struct is local to this CLI helper since
// it's not used elsewhere.
type compactTpEntry struct {
	Remote  string  `json:"r"`
	Type    string  `json:"t"`
	Latency float64 `json:"l,omitempty"`
}

// buildTpTargetIndex walks the dmsg salt to build a hex(target) → PK
// map suitable for resolving compact tp entries. Each dmsg entry
// contains the publisher PK in its `static` field; we hash that PK
// with the tp salt to compute the target where the visor's tp entry
// would live. Returned map is keyed by hex-encoded target.
func buildTpTargetIndex(rpcClient visor.API) (map[string]cipher.PubKey, error) {
	dmsgBody, err := rpcClient.DHTGetAll("dmsg")
	if err != nil {
		return nil, fmt.Errorf("DHT GetAll(dmsg): %w", err)
	}
	var raw []struct {
		Static string `json:"static"`
	}
	if err := json.Unmarshal([]byte(dmsgBody), &raw); err != nil {
		return nil, fmt.Errorf("parse DHT dmsg envelope: %w", err)
	}
	out := make(map[string]cipher.PubKey, len(raw))
	for _, e := range raw {
		if e.Static == "" {
			continue
		}
		var pk cipher.PubKey
		if err := pk.Set(e.Static); err != nil {
			continue
		}
		target := dht.MutableItemTarget(pk, []byte("tp"))
		out[hex.EncodeToString(target[:])] = pk
	}
	return out, nil
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
func (s *memoryStore) RecordTransportHeartbeat(context.Context, uuid.UUID, string) error {
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
