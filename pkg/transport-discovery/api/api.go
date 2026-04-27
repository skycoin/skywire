// Package api pkg/transport-discovery/api.go
package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/httpauth"
	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/metricsutil"
	"github.com/skycoin/skywire/pkg/networkmonitor"
	"github.com/skycoin/skywire/pkg/transport"
	tpdiscmetrics "github.com/skycoin/skywire/pkg/transport-discovery/metrics"
	"github.com/skycoin/skywire/pkg/transport-discovery/store"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

const (
	transportsCacheDelay = 30 * time.Second
	uptimesCacheDelay    = 5 * time.Minute
	backupTickerDelay    = 1 * time.Hour
)

var (
	// ErrEmptyPubKey indicates that provided public key is empty.
	ErrEmptyPubKey = errors.New("public key cannot be empty")
	// ErrInvalidPubKey indicates that provided public key is invalid.
	ErrInvalidPubKey = errors.New("public key is invalid")
	// ErrEmptyTransportID indicates that provided transport ID is empty.
	ErrEmptyTransportID = errors.New("transport ID cannot be empty")
	// ErrInvalidTransportID indicates that provided transport ID is invalid.
	ErrInvalidTransportID = errors.New("transport ID is invalid")
	// ErrUnauthorizedNetworkMonitor occurs in case of invalid network monitor key
	ErrUnauthorizedNetworkMonitor = errors.New("invalid network monitor key")
	// ErrBadInput occurs in case of bad input
	ErrBadInput = errors.New("error bad input")
	// WhitelistPKs store whitelisted pks of network monitor
	WhitelistPKs = networkmonitor.GetWhitelistPKs()
)

// API register all the API endpoints.
// It implements a net/http.Handler.
type API struct {
	http.Handler
	metrics                     tpdiscmetrics.Metrics
	reqsInFlightCountMiddleware *metricsutil.RequestsInFlightCountMiddleware
	rateLimiter                 *PubKeyRateLimiter
	store                       store.Store
	startedAt                   time.Time
	dmsgAddr                    string
	DmsgServers                 []string
	backupPath                  string

	transportsCache         []*transport.Entry
	transportsCacheFiltered []*transport.Entry // excludes self-transports
	transportsMu            sync.RWMutex

	uptimesCache   []store.VisorSummary
	uptimesV2Cache []store.VisorSummary
	uptimesMu      sync.RWMutex

	// cxoPublisher is an optional CXO publisher for distributing transport data.
	// When set, transport register/deregister operations publish to CXO subscribers.
	// dhtMirror mirrors transport lists to the DHT under each edge visor's PK.
	// The value written is the FULL list of transports an edge is part of
	// (matching GET /transports/edge:{pk} semantics), so the DHT is a
	// consistent view of discovery rather than the most-recently-written
	// single transport. Delete is called when an edge no longer has any
	// transports to drop the stale target.
	dhtMirror interface {
		Mirror(subjectPK cipher.PubKey, entry interface{}, seq uint64)
		MirrorMany(subjectPKs []cipher.PubKey, entry interface{}, seq uint64)
		Delete(subjectPK cipher.PubKey)
	}
}

// HealthCheckResponse is struct of /health endpoint
type HealthCheckResponse struct {
	ServiceName string          `json:"service_name,omitempty"`
	BuildInfo   *buildinfo.Info `json:"build_info,omitempty"`
	StartedAt   time.Time       `json:"started_at"`
	DmsgAddr    string          `json:"dmsg_address,omitempty"`
	DmsgServers []string        `json:"dmsg_servers,omitempty"`
}

// New constructs a new API instance.
func New(log logrus.FieldLogger, s store.Store, nonceStore httpauth.NonceStore,
	enableMetrics bool, m tpdiscmetrics.Metrics, dmsgAddr string, backupPath string) *API {
	if log == nil {
		log = logging.MustGetLogger("tp_disc")
	}

	api := &API{
		metrics:                     m,
		reqsInFlightCountMiddleware: metricsutil.NewRequestsInFlightCountMiddleware(),
		rateLimiter:                 NewPubKeyRateLimiter(30, 10), // 30 req/min with burst of 10
		store:                       s,
		startedAt:                   time.Now(),
		dmsgAddr:                    dmsgAddr,
		DmsgServers:                 []string{},
		backupPath:                  backupPath,
	}

	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	if enableMetrics {
		r.Use(api.reqsInFlightCountMiddleware.Handle)
		r.Use(metricsutil.RequestDurationMiddleware)
	}
	r.Use(httputil.SetLoggerMiddleware(log))

	// Authenticated endpoints (rate limited + auth)
	r.Group(func(r chi.Router) {
		r.Use(api.rateLimiter.Middleware())
		r.Use(httpauth.MakeMiddleware(nonceStore))

		r.Get("/transports/id:{id}", api.getTransportByID)
		r.Get("/transports/edge:{edge}", api.getTransportByEdge)
		r.Post("/transports/", api.registerTransport)
		r.Delete("/transports/id:{id}", api.deleteTransport)
		r.Post("/transports/delete-batch", api.deleteTransportsBatch)
		r.Get("/v4/update", api.visorHeartbeat)

		// v3: DHT-aligned registration. Accepts []*transport.Entry (bare
		// entries — no per-entry signatures, since the request is already
		// authenticated via SW-Sig). Matches the shape stored in the DHT
		// under SHA256(visorPK || "tp") and returned by GET /v3/transports
		// below. Reverse-compatible: /transports/ (v2) still works for
		// older visors.
		r.Post("/v3/transports/", api.registerTransportV3)
	})

	// Public data endpoints (rate limited, no auth)
	r.Group(func(r chi.Router) {
		r.Use(api.rateLimiter.Middleware())

		r.Post("/transports/edges", api.getTransportsByEdges)
		r.Get("/all-transports", api.getAllTransports)

		// v3: DHT-aligned read. Returns []*transport.Entry for the given
		// edge PK — identical shape to the DHT value under
		// SHA256(edgePK || "tp") so a visor with a full DHT node can
		// consume either source interchangeably.
		r.Get("/v3/transports/edge:{edge}", api.getTransportsByEdgeV3)
		r.Get("/all-transports/stats", api.getAllTransportsStats)
		r.Get("/all-transports/per-key-stats", api.getAllTransportsPerKeyStats)
		r.Get("/transports/stats/{edge}", api.getTransportStats)
		r.Delete("/transports/deregister", api.deregisterTransport)

		// Bandwidth endpoints (legacy)
		r.Get("/bandwidth/transport/{id}", api.getTransportBandwidth)
		r.Get("/bandwidth/visor/{pk}", api.getVisorBandwidth)

		// Metrics endpoints (new consolidated API)
		r.Get("/metric", api.getNetworkMetric)
		r.Get("/metric/visor/{pks}", api.getVisorAggregateMetric)
		r.Get("/metrics", api.getAllTransportMetrics)
		r.Get("/metrics/{ids}", api.getTransportMetricsByIDs)
		r.Get("/metrics/visor/{pks}", api.getTransportMetricsByVisors)

		r.Get("/uptimes", api.getUptimes)
		r.Post("/uptimes", api.postUptimes)
		r.Get("/uptimes/transports", api.getTransportUptimes)
		r.Post("/uptimes/transports", api.postTransportUptimes)
		r.Get("/metrics/uptime", api.getNetworkTransportUptime)
		r.Get("/metrics/uptime/{ids}", api.getTransportUptimeByIDs)
		r.Get("/metrics/uptime/visor/{pks}", api.getTransportUptimeByVisors)
		r.Get("/version", api.getVersionStats)
		r.Get("/versions", api.getVersions)
		r.Get("/versions/{pks}", api.getVersionsByPKs)
	})

	// Infrastructure endpoints (no rate limiting, no auth)
	r.Get("/health", api.health)
	r.Post("/statuses", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusGone)
	})

	nonceHandler := &httpauth.NonceHandler{Store: nonceStore}
	r.Get("/security/nonces/{pk}", nonceHandler.ServeHTTP)

	api.Handler = r

	return api
}

// SetDHTMirror sets a mirror that publishes transport lists to the DHT.
func (api *API) SetDHTMirror(m interface {
	Mirror(subjectPK cipher.PubKey, entry interface{}, seq uint64)
	MirrorMany(subjectPKs []cipher.PubKey, entry interface{}, seq uint64)
	Delete(subjectPK cipher.PubKey)
}) {
	api.dhtMirror = m
}

// mirrorEdges re-publishes the full transport list (as stored in the
// discovery) for each edge PK in the given set. Call this after any
// store mutation (register, deregister, delete-batch) so the DHT view
// stays consistent with HTTP discovery. Edges with no remaining
// transports have their DHT target deleted.
//
// Fetching per-edge lists from the store on every mutation costs an
// extra Redis round-trip per touched edge but is necessary: the DHT
// item's value is the complete list, and the batch we're processing
// may not contain all of an edge's transports (the other edge, or
// prior state, can carry more).
func (api *API) mirrorEdges(ctx context.Context, edges map[cipher.PubKey]struct{}) {
	if api.dhtMirror == nil || len(edges) == 0 {
		return
	}
	seq := uint64(time.Now().UnixNano()) //nolint:gosec
	for edge := range edges {
		entries, err := api.store.GetTransportsByEdge(ctx, edge)
		if err != nil || len(entries) == 0 {
			api.dhtMirror.Delete(edge)
			continue
		}
		api.dhtMirror.Mirror(edge, entries, seq)
	}
}

// BackfillDHTMirror mirrors the full transport list for every edge PK
// seen in the discovery on startup. Producing one DHT item per edge
// (value = all that edge's transports) matches the semantics of
// GET /transports/edge:{pk} and avoids the pre-#2333 data loss where
// per-transport mirroring overwrote each other's target.
func (api *API) BackfillDHTMirror(ctx context.Context, log logrus.FieldLogger) {
	if api.dhtMirror == nil {
		return
	}
	entries, err := api.store.GetAllTransports(ctx, false)
	if err != nil {
		log.WithError(err).Warn("DHT backfill: failed to list transports")
		return
	}
	byEdge := make(map[cipher.PubKey][]*transport.Entry)
	for _, entry := range entries {
		if entry == nil {
			continue
		}
		for _, edge := range entry.Edges {
			byEdge[edge] = append(byEdge[edge], entry)
		}
	}
	mirrored := 0
	for edge, list := range byEdge {
		if ctx.Err() != nil {
			break
		}
		seq := uint64(time.Now().UnixNano()) //nolint:gosec
		api.dhtMirror.Mirror(edge, list, seq)
		mirrored++
	}
	log.WithField("edges", mirrored).WithField("transports", len(entries)).Info("DHT backfill complete")
}

// RunBackgroundTasks is function which runs periodic background tasks of API.
func (api *API) RunBackgroundTasks(ctx context.Context, logger logrus.FieldLogger) {
	tpTicker := time.NewTicker(transportsCacheDelay)
	defer tpTicker.Stop()

	uptimesTicker := time.NewTicker(uptimesCacheDelay)
	defer uptimesTicker.Stop()

	backupTicker := time.NewTicker(backupTickerDelay)
	defer backupTicker.Stop()

	api.refreshTransportsCache(ctx, logger)
	api.refreshUptimesCache(ctx, logger)

	for {
		select {
		case <-ctx.Done():
			return
		case <-tpTicker.C:
			api.refreshTransportsCache(ctx, logger)
		case <-uptimesTicker.C:
			api.refreshUptimesCache(ctx, logger)
		case <-backupTicker.C:
			if err := api.store.BackupAndCleanOldBandwidth(ctx, api.backupPath); err != nil {
				logger.WithError(err).Error("failed to backup old bandwidth data")
			}
		}
	}
}

// refreshUptimesCache fetches visor data from the store and caches both v1 and v2 formats.
func (api *API) refreshUptimesCache(ctx context.Context, logger logrus.FieldLogger) {
	uptimes, err := api.store.GetAllVisorSummaries(ctx, false, false)
	if err != nil {
		logger.WithError(err).Error("failed to refresh uptimes cache")
		return
	}
	uptimesV2, err := api.store.GetAllVisorSummaries(ctx, true, false)
	if err != nil {
		logger.WithError(err).Error("failed to refresh uptimes v2 cache")
		uptimesV2 = uptimes // fall back to v1
	}
	api.uptimesMu.Lock()
	api.uptimesCache = uptimes
	api.uptimesV2Cache = uptimesV2
	api.uptimesMu.Unlock()
}

// getUptimesFromCache returns the cached v1 uptimes data.
func (api *API) getUptimesFromCache() []store.VisorSummary {
	api.uptimesMu.RLock()
	defer api.uptimesMu.RUnlock()
	return api.uptimesCache
}

// getUptimesV2FromCache returns the cached v2 uptimes data (with version and daily).
func (api *API) getUptimesV2FromCache() []store.VisorSummary {
	api.uptimesMu.RLock()
	defer api.uptimesMu.RUnlock()
	return api.uptimesV2Cache
}

func (api *API) log(r *http.Request) logrus.FieldLogger {
	return httputil.GetLogger(r)
}

// Error is the object returned to the client when there's an error.
type Error struct {
	Error string `json:"error"`
}

func (api *API) renderError(w http.ResponseWriter, r *http.Request, code int, err error) {
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(&Error{Error: err.Error()}); err != nil {
		api.log(r).WithError(err).Warn("Failed to encode error")
	}
}

// ServeHTTP implements http.Handler.
func (api *API) writeError(w http.ResponseWriter, r *http.Request, err error) {
	var status int

	switch err {
	case ErrEmptyPubKey, ErrEmptyTransportID, ErrInvalidTransportID, ErrInvalidPubKey:
		status = http.StatusBadRequest
	case store.ErrTransportNotFound:
		status = http.StatusNotFound
	case store.ErrAlreadyRegistered:
		status = http.StatusConflict
	case context.DeadlineExceeded:
		status = http.StatusRequestTimeout
	}

	// we still haven't found the error
	if status == 0 {
		if _, ok := err.(*json.SyntaxError); ok {
			status = http.StatusBadRequest
		}
	}

	// we fallback to 500
	if status == 0 {
		status = http.StatusInternalServerError
	}

	if status != http.StatusNotFound {
		api.log(r).WithError(err).WithField("status", http.StatusText(status)).Warn()
	}

	api.renderError(w, r, status, err)
}

// refreshTransportsCache loads all transports from Redis into memory and updates metrics.
func (api *API) refreshTransportsCache(ctx context.Context, logger logrus.FieldLogger) {
	entries, err := api.store.GetAllTransports(ctx, true)
	if err != nil {
		logger.WithError(err).Error("failed to refresh transports cache")
		return
	}

	// Pre-compute the filtered variant (no self-transports)
	var filtered []*transport.Entry
	counts := make(map[types.Type]int)
	for _, e := range entries {
		counts[e.Type]++
		if e.Edges[0] != e.Edges[1] {
			filtered = append(filtered, e)
		}
	}

	api.transportsMu.Lock()
	api.transportsCache = entries
	api.transportsCacheFiltered = filtered
	api.transportsMu.Unlock()

	api.metrics.SetTPCounts(counts)
}

// getTransportsFromCache returns the cached transports, optionally filtering self-transports.
// Returns nil if the cache has not been initialized yet (caller should fall back to the store).
func (api *API) getTransportsFromCache(selfTransports bool) []*transport.Entry {
	api.transportsMu.RLock()
	defer api.transportsMu.RUnlock()
	if api.transportsCache == nil {
		return nil
	}
	if selfTransports {
		return api.transportsCache
	}
	return api.transportsCacheFiltered
}
