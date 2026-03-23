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

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/httpauth"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/httputil"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/metricsutil"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/networkmonitor"
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

	uptimesCache []store.VisorSummary
	uptimesMu    sync.RWMutex
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
	})

	// Public data endpoints (rate limited, no auth)
	r.Group(func(r chi.Router) {
		r.Use(api.rateLimiter.Middleware())

		r.Get("/all-transports", api.getAllTransports)
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

// refreshUptimesCache fetches visor data from the store and caches it.
func (api *API) refreshUptimesCache(ctx context.Context, logger logrus.FieldLogger) {
	uptimes, err := api.store.GetAllVisorSummaries(ctx)
	if err != nil {
		logger.WithError(err).Error("failed to refresh uptimes cache")
		return
	}
	api.uptimesMu.Lock()
	api.uptimesCache = uptimes
	api.uptimesMu.Unlock()
}

// getUptimesFromCache returns the cached uptimes data.
func (api *API) getUptimesFromCache() []store.VisorSummary {
	api.uptimesMu.RLock()
	defer api.uptimesMu.RUnlock()
	return api.uptimesCache
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
