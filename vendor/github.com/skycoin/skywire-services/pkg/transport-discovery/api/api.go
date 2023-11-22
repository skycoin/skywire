// Package api pkg/transport-discovery/api.go
package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/sirupsen/logrus"
	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire-utilities/pkg/httpauth"
	"github.com/skycoin/skywire-utilities/pkg/httputil"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire-utilities/pkg/metricsutil"
	"github.com/skycoin/skywire-utilities/pkg/networkmonitor"

	"github.com/skycoin/skywire-services/internal/tpdiscmetrics"
	"github.com/skycoin/skywire-services/pkg/transport-discovery/store"
)

const (
	transportsNumberDelay = time.Second * 10
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
	store                       store.Store
	startedAt                   time.Time
	dmsgAddr                    string
}

// HealthCheckResponse is struct of /health endpoint
type HealthCheckResponse struct {
	BuildInfo *buildinfo.Info `json:"build_info,omitempty"`
	StartedAt time.Time       `json:"started_at"`
	DmsgAddr  string          `json:"dmsg_address,omitempty"`
}

// New constructs a new API instance.
func New(log logrus.FieldLogger, s store.Store, nonceStore httpauth.NonceStore,
	enableMetrics bool, m tpdiscmetrics.Metrics, dmsgAddr string) *API {
	if log == nil {
		log = logging.MustGetLogger("tp_disc")
	}

	api := &API{
		metrics:                     m,
		reqsInFlightCountMiddleware: metricsutil.NewRequestsInFlightCountMiddleware(),
		store:                       s,
		startedAt:                   time.Now(),
		dmsgAddr:                    dmsgAddr,
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

	r.Group(func(r chi.Router) {
		r.Use(httpauth.MakeMiddleware(nonceStore))

		r.Get("/transports/id:{id}", api.getTransportByID)
		r.Get("/transports/edge:{edge}", api.getTransportByEdge)
		r.Post("/transports/", api.registerTransport)
		r.Delete("/transports/id:{id}", api.deleteTransport)
		r.Delete("/transports/deregister", api.deregisterTransport)
	})

	r.Get("/health", api.health)
	r.Get("/all-transports", api.getAllTransports)
	r.Post("/statuses", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusGone)
	})

	nonceHandler := &httpauth.NonceHandler{Store: nonceStore}
	r.Get("/security/nonces/{pk}", nonceHandler.ServeHTTP)

	api.Handler = r

	return api
}

// RunBackgroundTasks is function which runs periodic background tasks of API.
func (api *API) RunBackgroundTasks(ctx context.Context, logger logrus.FieldLogger) {
	ticker := time.NewTicker(transportsNumberDelay)
	defer ticker.Stop()
	api.updateTransportsNumber(ctx, logger)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			api.updateTransportsNumber(ctx, logger)
		}
	}
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

// updateTransportsNumber is background function which updates number of registered transports
func (api *API) updateTransportsNumber(ctx context.Context, logger logrus.FieldLogger) {
	transports, err := api.store.GetNumberOfTransports(ctx)
	if err != nil {
		logger.WithError(err).Errorf("failed to get transports count")
		return
	}
	api.metrics.SetTPCounts(transports)
}
