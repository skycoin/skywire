// Package api pkg/node-visualizer/api/api.go
package api

import (
	"context"
	"embed"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dgraph-io/badger/v3"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	json "github.com/json-iterator/go"
	"github.com/rs/cors"
	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire-utilities/pkg/httputil"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire-utilities/pkg/metricsutil"
	"github.com/skycoin/skywire/internal/tpdiscmetrics"
	"github.com/skycoin/skywire/pkg/transport"
)

//go:embed build/*
var frontendFS embed.FS

// API register all the API endpoints.
// It implements a net/http.Handler.
type API struct {
	http.Handler
	metrics                     tpdiscmetrics.Metrics
	reqsInFlightCountMiddleware *metricsutil.RequestsInFlightCountMiddleware
	startedAt                   time.Time
	cache                       *badger.DB
	uptimeTrackerURL            string
	tpdiscURL                   string
}

func (a *API) log(r *http.Request) logrus.FieldLogger {
	return httputil.GetLogger(r)
}

// HealthCheckResponse is struct of /health endpoint
type HealthCheckResponse struct {
	BuildInfo *buildinfo.Info `json:"build_info,omitempty"`
	StartedAt time.Time       `json:"started_at"`
}

// New constructs a new API instance.
func New(log logrus.FieldLogger, enableMetrics bool, m tpdiscmetrics.Metrics) *API {
	if log == nil {
		log = logging.MustGetLogger("tp_disc")
	}

	uptimeURL := os.Getenv("UT_URL")
	if uptimeURL == "" {
		uptimeURL = utURL
	}

	tpdiscURL := os.Getenv("TPD_URL")
	if tpdiscURL == "" {
		tpdiscURL = tpdURL
	}

	dbPath := filepath.Join(os.TempDir(), "db")
	db, err := badger.Open(badger.DefaultOptions(dbPath))
	if err != nil {
		log.Fatalf("unable to create file db at %s: %v", dbPath, err)
	}

	api := &API{
		metrics:                     m,
		reqsInFlightCountMiddleware: metricsutil.NewRequestsInFlightCountMiddleware(),
		cache:                       db,
		startedAt:                   time.Now(),
		uptimeTrackerURL:            uptimeURL,
		tpdiscURL:                   tpdiscURL,
	}
	c, err := api.pollUptimeTransport()
	if err != nil {
		log.Fatalf("unable to fetch initial ut and tpd visor map: %v", err)
	}
	err = api.AddToCache(c)
	if err != nil {
		log.Fatalf("unable to add to cache: %v", err)
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
	r.Use(cors.AllowAll().Handler)

	// Create a route along /files that will serve contents from
	// the ./build/ folder.
	fsys, err := fs.Sub(frontendFS, "build")
	filesDir := http.FS(fsys)
	if err != nil {
		log.Fatalf("error getting build dir: %v", err)
	}
	log.Infof("serving static directory in: %s", filesDir)
	api.FileServer(r, "/", filesDir)

	r.Get("/map", api.handleGetGraph)

	api.Handler = r

	return api
}

const (
	// this should be internal endpoint to /visor-ips, the endpoint is not public.
	// so use k8s / docker-compose host.
	utURL  = `http://uptime-tracker/visor-ips`
	tpdURL = `http://tpd.skywire.dev/all-transports`
)

type visorIPsResponse struct {
	Count      int      `json:"count"`
	PublicKeys []string `json:"public_keys"`
}

// PollResult is the return value of uptime tracker and tpd API
type PollResult struct {
	Nodes map[string]visorIPsResponse `json:"nodes"`
	Edges []*transport.Entry          `json:"edges"`
}

// pollUptimeTransport polls tpd and uptime transport for geolocation data
func (a *API) pollUptimeTransport() (*PollResult, error) {
	hc := http.Client{Timeout: 30 * time.Second}

	utReq, err := http.NewRequest(http.MethodGet, a.uptimeTrackerURL, nil)
	if err != nil {
		return nil, err
	}
	tpReq, err := http.NewRequest(http.MethodGet, a.tpdiscURL, nil)
	if err != nil {
		return nil, err
	}
	utresp, err := hc.Do(utReq)
	if err != nil {
		return nil, err
	}
	tpresp, err := hc.Do(tpReq)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = utresp.Body.Close() //nolint:errcheck
		_ = tpresp.Body.Close() //nolint:errcheck
	}()
	u, err := io.ReadAll(utresp.Body)
	if err != nil {
		return nil, err
	}
	t, err := io.ReadAll(tpresp.Body)
	if err != nil {
		return nil, err
	}
	var utResp map[string]visorIPsResponse
	var tpResp []*transport.Entry
	if err = json.Unmarshal(u, &utResp); err != nil {
		return nil, errors.New("error unmarshal uptime-tracker response")
	}
	if err = json.Unmarshal(t, &tpResp); err != nil {
		return nil, errors.New("error unmarshal transport-discovery response")
	}
	return &PollResult{
		Nodes: utResp,
		Edges: tpResp,
	}, nil
}

func (a *API) handleGetGraph(w http.ResponseWriter, r *http.Request) {
	res, err := a.GetCache()
	if err != nil {
		a.renderError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err = json.NewEncoder(w).Encode(res); err != nil {
		a.renderError(w, r, err)
		return
	}
}

// RunBackgroundTasks runs cache update every sets time interval
func (a *API) RunBackgroundTasks(ctx context.Context, logger logrus.FieldLogger) {
	cacheTicker := time.NewTicker(time.Minute * 10)
	defer cacheTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-cacheTicker.C:
			p, err := a.pollUptimeTransport()
			if err != nil {
				logger.WithError(err).Warn("unable to poll uptime-tracker and tpd")
			}
			err = a.AddToCache(p)
			if err != nil {
				logger.WithError(err).Warn("unable to poll UT and TPD")
			}
		}
	}
}

func (a *API) renderError(w http.ResponseWriter, r *http.Request, err error) {
	var status int

	if err == context.DeadlineExceeded {
		status = http.StatusRequestTimeout
	}

	// we fallback to 500
	if status == 0 {
		status = http.StatusInternalServerError
	}

	if status != http.StatusNotFound {
		a.log(r).Warnf("%d: %s", status, err)
	}

	w.WriteHeader(status)
	w.Header().Set("Content-Type", "application/json")
	if err = json.NewEncoder(w).Encode(&Error{Error: err.Error()}); err != nil {
		a.log(r).WithError(err).Warn("Failed to encode error")
	}
}

// FileServer conveniently sets up a http.FileServer handler to serve
// static files from a http.FileSystem.
func (a *API) FileServer(r chi.Router, path string, root http.FileSystem) {
	if strings.ContainsAny(path, "{}*") {
		return
	}

	if path != "/" && path[len(path)-1] != '/' {
		r.Get(path, http.RedirectHandler(path+"/", http.StatusMovedPermanently).ServeHTTP)
		path += "/"
	}
	path += "*"

	r.Get(path, func(w http.ResponseWriter, r *http.Request) {
		rctx := chi.RouteContext(r.Context())
		pathPrefix := strings.TrimSuffix(rctx.RoutePattern(), "/*")
		f := http.StripPrefix(pathPrefix, http.FileServer(root))
		f.ServeHTTP(w, r)
	})
}

// Error is the object returned to the client when there's an error.
type Error struct {
	Error string `json:"error"`
}
