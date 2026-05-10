// Package visor pkg/visor/hypervisor_handlers_routing_tools.go
//
// HTTP handlers powering the Routing tab's Find + Calculate forms.
// route-find queries the route-finder service via the visor's
// configured rfClient. route-calc enumerates routes locally using
// the same graph machinery the gRPC StreamCalcRoutes uses, but in
// a bounded-count one-shot form so it fits a JSON HTTP response.
package visor

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/rfclient"
	routeFinder "github.com/skycoin/skywire/pkg/route-finder/store"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport"
	tpdstore "github.com/skycoin/skywire/pkg/transport-discovery/store"
	tptypes "github.com/skycoin/skywire/pkg/transport/types"
)

// routeFindReq is the JSON body for POST /visors/{pk}/route-find.
// MinHops/MaxHops match the route-finder service options. SrcPK
// defaults to the visor's local PK when empty.
type routeFindReq struct {
	SrcPK   string `json:"src_pk"`
	DstPK   string `json:"dst_pk"`
	MinHops uint16 `json:"min_hops"`
	MaxHops uint16 `json:"max_hops"`
}

// routeFindResp wraps the route-finder result in a UI-friendly shape.
// Each entry is a forward+reverse pair of hop sequences keyed by the
// underlying PathEdges; the Edges map flattens those for transport
// over JSON (PathEdges is a [2]cipher.PubKey tuple, not a JSON map
// key in its native form).
type routeFindResp struct {
	Routes []routeFindEntry `json:"routes"`
}
type routeFindEntry struct {
	SrcPK string         `json:"src_pk"`
	DstPK string         `json:"dst_pk"`
	Hops  []routeFindHop `json:"hops"`
}
type routeFindHop struct {
	TpID string `json:"tp_id"`
	From string `json:"from"`
	To   string `json:"to"`
}

// postRouteFind handles POST /visors/{pk}/route-find.
func (hv *Hypervisor) postRouteFind() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		var req routeFindReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest,
				map[string]string{"error": "bad json: " + err.Error()})
			return
		}
		if hv.visor == nil || hv.visor.rfClient == nil {
			httputil.WriteJSON(w, r, http.StatusServiceUnavailable,
				map[string]string{"error": "route-finder client not initialized"})
			return
		}
		var dstPK cipher.PubKey
		if err := dstPK.Set(req.DstPK); err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest,
				map[string]string{"error": "bad dst_pk: " + err.Error()})
			return
		}
		var srcPK cipher.PubKey
		if req.SrcPK != "" {
			if err := srcPK.Set(req.SrcPK); err != nil {
				httputil.WriteJSON(w, r, http.StatusBadRequest,
					map[string]string{"error": "bad src_pk: " + err.Error()})
				return
			}
		} else {
			srcPK = hv.visor.conf.PK
		}
		if req.MaxHops == 0 {
			req.MaxHops = 5
		}
		if req.MinHops == 0 {
			req.MinHops = 1
		}
		opts := &rfclient.RouteOptions{MinHops: req.MinHops, MaxHops: req.MaxHops}
		edges := []routing.PathEdges{{srcPK, dstPK}}
		findCtx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		results, err := hv.visor.rfClient.FindRoutes(findCtx, edges, opts)
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusBadGateway,
				map[string]string{"error": "FindRoutes: " + err.Error()})
			return
		}
		out := routeFindResp{Routes: []routeFindEntry{}}
		for edge, paths := range results {
			for _, hops := range paths {
				entry := routeFindEntry{
					SrcPK: edge[0].Hex(),
					DstPK: edge[1].Hex(),
					Hops:  make([]routeFindHop, 0, len(hops)),
				}
				for _, h := range hops {
					entry.Hops = append(entry.Hops, routeFindHop{
						TpID: h.TpID.String(),
						From: h.From.String(),
						To:   h.To.String(),
					})
				}
				out.Routes = append(out.Routes, entry)
			}
		}
		httputil.WriteJSON(w, r, http.StatusOK, out)
	})
}

// routeCalcReq is the JSON body for POST /visors/{pk}/route-calc.
// Count caps the number of routes returned (0 = use the server-side
// default, currently 200). MinHops/MaxHops constrain path length.
type routeCalcReq struct {
	SrcPK    string `json:"src_pk"`
	DstPK    string `json:"dst_pk"`
	MinHops  int    `json:"min_hops"`
	MaxHops  int    `json:"max_hops"`
	Count    int    `json:"count"`
	QueueCap int    `json:"queue_cap"`
}

// postRouteCalc handles POST /visors/{pk}/route-calc. Replicates
// the gRPC StreamCalcRoutes machinery in a non-streaming HTTP
// response: fetch the TPD transport graph, BFS-enumerate routes,
// return the first `count` results. Same defaults the gRPC path
// uses (max=5, count cap=200, queue=DefaultMaxBFSQueue).
func (hv *Hypervisor) postRouteCalc() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		var req routeCalcReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest,
				map[string]string{"error": "bad json: " + err.Error()})
			return
		}
		if hv.visor == nil {
			httputil.WriteJSON(w, r, http.StatusServiceUnavailable,
				map[string]string{"error": "visor unavailable"})
			return
		}
		var dstPK cipher.PubKey
		if err := dstPK.Set(req.DstPK); err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest,
				map[string]string{"error": "bad dst_pk: " + err.Error()})
			return
		}
		srcPK := hv.visor.conf.PK
		if req.SrcPK != "" {
			if err := srcPK.Set(req.SrcPK); err != nil {
				httputil.WriteJSON(w, r, http.StatusBadRequest,
					map[string]string{"error": "bad src_pk: " + err.Error()})
				return
			}
		}
		if req.MaxHops <= 0 {
			req.MaxHops = 5
		}
		if req.MinHops < 0 {
			req.MinHops = 0
		}
		// 200 default cap matches operator expectation: a useful set
		// of distinct paths without OOMing on dense graphs.
		if req.Count <= 0 {
			req.Count = 200
		}
		if req.QueueCap == 0 {
			req.QueueCap = routeFinder.DefaultMaxBFSQueue
		}
		calcCtx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
		defer cancel()
		entries, err := hv.visor.FetchAllTransportEntries(calcCtx)
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusBadGateway,
				map[string]string{"error": fmt.Sprintf("fetch transports: %v", err)})
			return
		}
		if len(entries) == 0 {
			httputil.WriteJSON(w, r, http.StatusOK, routeFindResp{Routes: []routeFindEntry{}})
			return
		}
		memStore := newCalcMemStore(entries)
		graph, err := routeFinder.NewGraphWithDepth(calcCtx, memStore, srcPK, req.MaxHops)
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusInternalServerError,
				map[string]string{"error": fmt.Sprintf("build graph: %v", err)})
			return
		}
		out := routeFindResp{Routes: []routeFindEntry{}}
		streamErr := graph.StreamRoutesWithCap(calcCtx, srcPK, dstPK, req.MinHops, req.MaxHops, req.QueueCap, func(rt routing.Route) bool {
			entry := routeFindEntry{
				SrcPK: srcPK.Hex(),
				DstPK: dstPK.Hex(),
				Hops:  make([]routeFindHop, 0, len(rt.Hops)),
			}
			for _, h := range rt.Hops {
				entry.Hops = append(entry.Hops, routeFindHop{
					TpID: h.TpID.String(),
					From: h.From.String(),
					To:   h.To.String(),
				})
			}
			out.Routes = append(out.Routes, entry)
			return len(out.Routes) < req.Count
		})
		if streamErr != nil && len(out.Routes) == 0 && streamErr != routeFinder.ErrRouteNotFound {
			httputil.WriteJSON(w, r, http.StatusBadGateway,
				map[string]string{"error": fmt.Sprintf("calc: %v", streamErr)})
			return
		}
		httputil.WriteJSON(w, r, http.StatusOK, out)
	})
}

// hvCalcStore is a minimal route-finder store.Store impl that
// answers GetTransportsByEdge over an in-memory edge → entries
// map. Mirrors the gRPC server's calcMemStore but lives in the
// visor package so this handler doesn't import the rpcgrpc subpkg.
// All other methods on the Store interface are stubbed — the
// route-finder graph traversal only ever invokes the by-edge
// lookups.
type hvCalcStore struct {
	byEdge map[cipher.PubKey][]*transport.Entry
}

func newCalcMemStore(entries []*transport.Entry) *hvCalcStore {
	byEdge := make(map[cipher.PubKey][]*transport.Entry)
	for _, e := range entries {
		if e == nil {
			continue
		}
		byEdge[e.Edges[0]] = append(byEdge[e.Edges[0]], e)
		if e.Edges[0] != e.Edges[1] {
			byEdge[e.Edges[1]] = append(byEdge[e.Edges[1]], e)
		}
	}
	return &hvCalcStore{byEdge: byEdge}
}

// Used by the BFS walker.
func (s *hvCalcStore) GetTransportsByEdgeNoLatency(ctx context.Context, pk cipher.PubKey) ([]*transport.Entry, error) {
	return s.GetTransportsByEdge(ctx, pk)
}

func (s *hvCalcStore) GetTransportsByEdge(_ context.Context, pk cipher.PubKey) ([]*transport.Entry, error) {
	if tps, ok := s.byEdge[pk]; ok {
		return tps, nil
	}
	return nil, tpdstore.ErrTransportNotFound
}

// Stubs to satisfy tpdstore.Store. None of these are reached during
// route enumeration; they exist only to make the type assignable to
// the route-finder's parameter type.
func (s *hvCalcStore) RegisterTransport(context.Context, *transport.SignedEntry) error {
	return nil
}
func (s *hvCalcStore) RegisterTransportsBatch(context.Context, []*transport.SignedEntry) error {
	return nil
}
func (s *hvCalcStore) DeregisterTransport(context.Context, uuid.UUID) error { return nil }
func (s *hvCalcStore) GetTransportByID(context.Context, uuid.UUID) (*transport.Entry, error) {
	return nil, tpdstore.ErrTransportNotFound
}
func (s *hvCalcStore) GetNumberOfTransports(context.Context) (map[tptypes.Type]int, error) {
	return nil, nil
}
func (s *hvCalcStore) GetAllTransports(context.Context, bool) ([]*transport.Entry, error) {
	return nil, nil
}
func (s *hvCalcStore) UpdateBandwidth(context.Context, string, cipher.PubKey, uint64, uint64) error {
	return nil
}
func (s *hvCalcStore) UpdateLatency(context.Context, string, float64, float64, float64) error {
	return nil
}
func (s *hvCalcStore) GetTransportBandwidth(context.Context, uuid.UUID, string, int) ([]tpdstore.BandwidthAggregation, error) {
	return nil, nil
}
func (s *hvCalcStore) GetVisorBandwidth(context.Context, cipher.PubKey, string, int) ([]tpdstore.BandwidthAggregation, error) {
	return nil, nil
}
func (s *hvCalcStore) GetAllVisorSummaries(context.Context, bool, bool) ([]tpdstore.VisorSummary, error) {
	return nil, nil
}
func (s *hvCalcStore) RecordHeartbeat(context.Context, cipher.PubKey, string) error { return nil }
func (s *hvCalcStore) GetDailyTimeline(context.Context, string, time.Time) map[string]string {
	return nil
}
func (s *hvCalcStore) RecordTransportHeartbeat(context.Context, uuid.UUID, string, time.Time) error {
	return nil
}
func (s *hvCalcStore) IngestTransportTimeline(context.Context, uuid.UUID, string, []byte) error {
	return nil
}
func (s *hvCalcStore) GetTransportUptimeSummaries(context.Context, []uuid.UUID, bool, bool) ([]tpdstore.TransportUptimeSummary, error) {
	return nil, nil
}
func (s *hvCalcStore) GetTransportUptimeByVisor(context.Context, cipher.PubKey, bool, bool) ([]tpdstore.TransportUptimeSummary, error) {
	return nil, nil
}
func (s *hvCalcStore) GetTransportDailyTimeline(context.Context, string, time.Time) map[string]string {
	return nil
}
func (s *hvCalcStore) BackupAndCleanOldBandwidth(context.Context, string) error { return nil }
func (s *hvCalcStore) GetNetworkMetrics(context.Context, tpdstore.MetricsQuery) (*tpdstore.NetworkMetricResponse, error) {
	return nil, nil
}
func (s *hvCalcStore) GetVisorAggregateMetrics(context.Context, []cipher.PubKey, tpdstore.MetricsQuery) (map[string]*tpdstore.VisorMetricResponse, error) {
	return nil, nil
}
func (s *hvCalcStore) GetAllTransportMetrics(context.Context, tpdstore.MetricsQuery) ([]tpdstore.TransportMetric, error) {
	return nil, nil
}
func (s *hvCalcStore) GetTransportMetricsByIDs(context.Context, []uuid.UUID, tpdstore.MetricsQuery) ([]tpdstore.TransportMetric, error) {
	return nil, nil
}
func (s *hvCalcStore) GetTransportMetricsByVisors(context.Context, []cipher.PubKey, tpdstore.MetricsQuery) ([]tpdstore.TransportMetric, error) {
	return nil, nil
}
func (s *hvCalcStore) Close() {}
