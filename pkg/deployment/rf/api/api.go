// Package api pkg/deployment/rf/api/api.go c2-net-routing
package api

import (
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/cipher"
	routeFinder "github.com/skycoin/skywire/pkg/deployment/rf/store"
	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/metricsutil"
	"github.com/skycoin/skywire/pkg/rfclient"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport-discovery/store"
)

// maxNumberOfRoutes is the default per-edge route count when the
// request doesn't specify RouteOptions.NumRoutes (back-compat with
// pre-NumRoutes clients).
const maxNumberOfRoutes = 3

// routesCeiling bounds RouteOptions.NumRoutes so a client can't make
// the finder collect an unbounded number of routes (BFS work scales
// with the count). Sized comfortably above any realistic mux degree.
const routesCeiling = 20

// API represents the api of the route-finder service.
type API struct {
	http.Handler
	reqsInFlightCountMiddleware *metricsutil.RequestsInFlightCountMiddleware
	store                       store.Store
	startedAt                   time.Time
	dmsgAddr                    string
	DmsgServers                 []string
}

// HealthCheckResponse is struct of /health endpoint
type HealthCheckResponse struct {
	ServiceName string          `json:"service_name,omitempty"`
	BuildInfo   *buildinfo.Info `json:"build_info,omitempty"`
	StartedAt   time.Time       `json:"started_at"`
	DmsgAddr    string          `json:"dmsg_address,omitempty"`
	DmsgServers []string        `json:"dmsg_servers,omitempty"`
}

// New creates a new api
func New(s store.Store, logger logrus.FieldLogger, enableMetrics bool, dmsgAddr string) *API {
	api := &API{
		reqsInFlightCountMiddleware: metricsutil.NewRequestsInFlightCountMiddleware(),
		store:                       s,
		startedAt:                   time.Now(),
		dmsgAddr:                    dmsgAddr,
		DmsgServers:                 []string{},
	}

	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP) //nolint:staticcheck
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	// gzip route-finder JSON responses on the wire (skips small bodies,
	// honors Vary; net/http clients get transparent gzip).
	r.Use(middleware.Compress(5))
	if enableMetrics {
		r.Use(api.reqsInFlightCountMiddleware.Handle)
		r.Use(metricsutil.RequestDurationMiddleware)
	}
	r.Use(httputil.SetLoggerMiddleware(logger))

	// routes
	r.Post("/routes", api.getPairedRoutes)
	r.Get("/health", api.health)

	api.Handler = r

	return api
}

func (a *API) log(r *http.Request) logrus.FieldLogger {
	return httputil.GetLogger(r)
}

// getPairedRoutes Obtains the available routes for a specific source and destination public key and
// the available reverse routes from destination to source.
// Optionally with custom min and max hop parameters.
// URI: /routes
// Method: POST
// Body:
//
//	{
//	  "edges": ["<src-pk>", "<dst-pk>"],
//	  "opts": {
//	    "min_hops": 0,
//	    "max_hops": 0
//	  }
//	}
func (a *API) getPairedRoutes(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		a.handleError(w, r, http.StatusBadRequest, err)
		return
	}

	var grr rfclient.FindRoutesRequest
	if err := json.Unmarshal(body, &grr); err != nil {
		a.handleError(w, r, http.StatusBadRequest, err)
		return
	}

	defer func() {
		if err := r.Body.Close(); err != nil {
			a.log(r).WithError(err).Warn("Failed to close HTTP response body")
		}
	}()

	var minHops, maxHops int
	numRoutes := maxNumberOfRoutes
	if grr.Opts != nil {
		minHops = int(grr.Opts.MinHops)
		maxHops = int(grr.Opts.MaxHops)
		// Honor the caller's requested route count (mux degree +
		// headroom), bounded by routesCeiling. Zero keeps the default.
		if grr.Opts.NumRoutes > 0 {
			numRoutes = int(grr.Opts.NumRoutes)
			if numRoutes > routesCeiling {
				numRoutes = routesCeiling
			}
		}
	}

	// Limit graph exploration depth to prevent OOM on large networks.
	graphDepth := maxHops
	if graphDepth <= 0 || graphDepth > 10 {
		graphDepth = 10
	}

	graphs := make(map[cipher.PubKey]*routeFinder.Graph)
	for _, edge := range grr.Edges {
		srcPK := edge[0]
		if _, ok := graphs[srcPK]; !ok {
			graph, err := routeFinder.NewGraphWithDepth(r.Context(), a.store, srcPK, graphDepth)
			if err != nil {
				if err == store.ErrTransportNotFound {
					a.handleError(w, r, http.StatusNotFound, err)
					return
				}
				a.log(r).WithError(err).Errorf("Error creating graph for src %s", srcPK)
				a.handleError(w, r, http.StatusInternalServerError, err)
				return
			}
			graphs[srcPK] = graph
		}
	}

	routes := make(map[routing.PathEdges][][]routing.Hop)
	for _, edge := range grr.Edges {
		srcPK := edge[0]
		dstPK := edge[1]
		graph := graphs[srcPK]

		// Latency-weighted by default: the graph is already built from
		// GetTransportsByEdge, which overlays each edge's measured latency, so
		// returning lowest-total-latency routes (with a per-hop penalty for
		// unmeasured edges) costs nothing extra and replaces the old
		// shortest-hop-but-arbitrary ordering.
		forwardRoutes, err := graph.GetRouteWeighted(r.Context(), srcPK, dstPK, minHops, maxHops, numRoutes, true)
		if err != nil {
			a.handleError(w, r, http.StatusNotFound, err)
			return
		}

		forwardPaths := make([][]routing.Hop, 0, len(forwardRoutes))
		for _, route := range forwardRoutes {
			forwardPaths = append(forwardPaths, route.Hops)
		}

		routes[edge] = forwardPaths
	}

	a.writeJSON(w, r, http.StatusOK, routes)
}

func (a *API) health(w http.ResponseWriter, r *http.Request) {
	info := buildinfo.Get()
	a.writeJSON(w, r, http.StatusOK, HealthCheckResponse{
		ServiceName: "route-finder",
		BuildInfo:   info,
		StartedAt:   a.startedAt,
		DmsgAddr:    a.dmsgAddr,
		DmsgServers: a.DmsgServers,
	})
}

func (a *API) handleError(w http.ResponseWriter, r *http.Request, code int, e error) {
	if code != http.StatusNotFound {
		a.log(r).Warnf("%d: %s", code, e)
	}

	a.writeJSON(w, r, code, rfclient.HTTPResponse{Error: &rfclient.HTTPError{Code: code, Message: e.Error()}})
}

// writeJSON writes a json object on a http.ResponseWriter with the given code
func (a *API) writeJSON(w http.ResponseWriter, r *http.Request, code int, object interface{}) {
	jsonObject, err := json.Marshal(object)
	if err != nil {
		a.log(r).WithError(err).Errorf("failed to encode json response")
		w.WriteHeader(http.StatusInternalServerError)

		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)

	_, err = w.Write(jsonObject)
	if err != nil {
		a.log(r).WithError(err).Errorf("failed to write json response")
	}
}
