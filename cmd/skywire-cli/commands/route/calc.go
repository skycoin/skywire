// Package cliroute cmd/skywire-cli/commands/route/calc.go
package cliroute

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
	"github.com/skycoin/skywire/deployment"
	routeFinder "github.com/skycoin/skywire/pkg/route-finder/store"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/transport-discovery/store"
	tptypes "github.com/skycoin/skywire/pkg/transport/types"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

var (
	calcEnable  bool
	calcDisable bool
	calcTimeout time.Duration
	tpdURL      string
	calcMinHops uint16
	calcMaxHops uint16
)

func init() {
	calcCmd.Flags().BoolVar(&calcEnable, "enable", false, "enable local route calculation in visor")
	calcCmd.Flags().BoolVar(&calcDisable, "disable", false, "disable local route calculation in visor")
	calcCmd.Flags().DurationVarP(&calcTimeout, "timeout", "t", 30*time.Second, "request timeout")
	calcCmd.Flags().StringVarP(&tpdURL, "tpd", "a", deployment.Prod.TransportDiscovery, "transport discovery URL")
	calcCmd.Flags().Uint16VarP(&calcMinHops, "min", "n", 1, "minimum hops")
	calcCmd.Flags().Uint16VarP(&calcMaxHops, "max", "x", 1000, "maximum hops")
}

var calcCmd = &cobra.Command{
	Use:   "calc [<src-pk>] <dst-pk>",
	Short: "Calculate routes locally or control visor's local route calculation",
	Long: func() string {
		long := `Calculate routes locally using transport discovery data

	skywire cli route calc <dst-pk>           - calculate route to destination
	skywire cli route calc <src-pk> <dst-pk>  - calculate route between two visors
	skywire cli route calc --enable           - enable local route calculation in visor
	skywire cli route calc --disable          - disable local route calculation in visor
`
		rpcClient, err := clirpc.Client(nil)
		if err != nil {
			return long
		}
		status, err := rpcClient.GetCalculateRoutes()
		if err != nil {
			if strings.Contains(err.Error(), "method") {
				return long + fmt.Sprintf("\n\tError: %v\n", err)
			}
			return long
		}
		if status {
			return long + "\n\tVisor local route calculation: enabled\n"
		}
		return long + "\n\tVisor local route calculation: disabled\n"
	}(),
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

		if len(args) == 1 {
			// Get local PK from visor or config
			rpcClient, err := clirpc.Client(cmd.Flags())
			if err == nil {
				overview, err := rpcClient.Overview()
				if err == nil {
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

		// Fetch all transports and calculate route
		ctx, cancel := context.WithTimeout(context.Background(), calcTimeout)
		defer cancel()

		entries, err := fetchAllTransports(ctx, tpdURL)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}

		memStore := newMemoryStoreFromEntries(entries)
		graph, err := routeFinder.NewGraph(ctx, memStore, srcPK)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to build graph: %w", err))
		}

		routes, err := graph.GetRoute(ctx, srcPK, dstPK, int(calcMinHops), int(calcMaxHops), 1)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("no route found: %w", err))
		}

		fwd := routes[0].Hops
		rev := reverseHops(fwd)

		output := fmt.Sprintf("forward: %v\nreverse: %v", fwd, rev)
		internal.PrintOutput(cmd.Flags(), struct {
			Forward []routing.Hop `json:"forward"`
			Reverse []routing.Hop `json:"reverse"`
		}{fwd, rev}, output)
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

func fetchAllTransports(ctx context.Context, tpdAddr string) ([]*transport.Entry, error) {
	url := strings.TrimSuffix(tpdAddr, "/") + "/all-transports"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("TPD returned status %d", resp.StatusCode)
	}
	var entries []*transport.Entry
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
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

func (s *memoryStore) GetTransportsByEdge(_ context.Context, pk cipher.PubKey) ([]*transport.Entry, error) {
	if tps, ok := s.byEdge[pk]; ok {
		return tps, nil
	}
	return nil, store.ErrTransportNotFound
}

// Unused interface methods - stubs for store.Store compliance
func (s *memoryStore) RegisterTransport(context.Context, *transport.SignedEntry) error { return nil }
func (s *memoryStore) DeregisterTransport(context.Context, uuid.UUID) error            { return nil }
func (s *memoryStore) GetTransportByID(context.Context, uuid.UUID) (*transport.Entry, error) {
	return nil, nil
}
func (s *memoryStore) GetNumberOfTransports(context.Context) (map[tptypes.Type]int, error) {
	return nil, nil
}
func (s *memoryStore) GetAllTransports(context.Context, bool) ([]*transport.Entry, error) {
	return s.entries, nil
}
func (s *memoryStore) GetTransportBandwidth(context.Context, uuid.UUID, string, int) ([]store.BandwidthAggregation, error) {
	return nil, nil
}
func (s *memoryStore) GetVisorBandwidth(context.Context, cipher.PubKey, string, int) ([]store.BandwidthAggregation, error) {
	return nil, nil
}
func (s *memoryStore) GetAllVisorSummaries(context.Context) ([]store.VisorSummary, error) {
	return nil, nil
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
