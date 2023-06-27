// Package api pkg/config-bootstrapper/api/api.go
package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// API represents the api of the stun-list service.
type API struct {
	http.Handler

	log *logging.Logger

	startedAt time.Time

	services *visorconfig.Services

	closeOnce sync.Once
	closeC    chan struct{}
}

// HealthCheckResponse is struct of /health endpoint
type HealthCheckResponse struct {
	BuildInfo *buildinfo.Info `json:"build_info,omitempty"`
	StartedAt time.Time       `json:"started_at"`
}

// Error is the object returned to the client when there's an error.
type Error struct {
	Error string `json:"error"`
}

// Config contains the list of stun servers and setup-nodes
type Config struct {
	StunServers       []string        `json:"stun_servers"`
	SetupNodes        []cipher.PubKey `json:"route_setup_nodes"`
	SurveyWhitelist   []cipher.PubKey `json:"survey_whitelist"`
	TransportSetupPKs []cipher.PubKey `json:"transport_setup"`
}

// New creates a new api.
func New(log *logging.Logger, conf Config, domain string) *API {

	sd := strings.Replace(skyenv.ServiceDiscAddr, "skycoin.com", domain, -1)
	if domain == "skywire.skycoin.com" {
		sd = skyenv.ServiceDiscAddr
	}

	services := &visorconfig.Services{
		DmsgDiscovery:      strings.Replace(skyenv.DmsgDiscAddr, "skywire.skycoin.com", domain, -1),
		TransportDiscovery: strings.Replace(skyenv.TpDiscAddr, "skywire.skycoin.com", domain, -1),
		AddressResolver:    strings.Replace(skyenv.AddressResolverAddr, "skywire.skycoin.com", domain, -1),
		RouteFinder:        strings.Replace(skyenv.RouteFinderAddr, "skywire.skycoin.com", domain, -1),
		RouteSetupNodes:    conf.SetupNodes,
		UptimeTracker:      strings.Replace(skyenv.UptimeTrackerAddr, "skywire.skycoin.com", domain, -1),
		ServiceDiscovery:   sd,
		StunServers:        conf.StunServers,
		DNSServer:          skyenv.DNSServer,
		SurveyWhitelist:    conf.SurveyWhitelist,
		TransportSetupPKs:  conf.TransportSetupPKs,
	}

	api := &API{
		log:       log,
		startedAt: time.Now(),
		services:  services,
		closeC:    make(chan struct{}),
	}

	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(httputil.SetLoggerMiddleware(log))
	r.Get("/health", api.health)
	r.Get("/", api.config)

	api.Handler = r

	return api
}

// Close stops API.
func (a *API) Close() {
	a.closeOnce.Do(func() {
		close(a.closeC)
	})
}

func (a *API) logger(r *http.Request) logrus.FieldLogger {
	return httputil.GetLogger(r)
}

func (a *API) health(w http.ResponseWriter, r *http.Request) {
	info := buildinfo.Get()
	a.writeJSON(w, r, http.StatusOK, HealthCheckResponse{
		BuildInfo: info,
		StartedAt: a.startedAt,
	})
}

func (a *API) config(w http.ResponseWriter, r *http.Request) {

	a.writeJSON(w, r, http.StatusOK, a.services)
}

func (a *API) writeJSON(w http.ResponseWriter, r *http.Request, code int, object interface{}) {
	jsonObject, err := json.Marshal(object)
	if err != nil {
		a.logger(r).WithError(err).Errorf("failed to encode json response")
		w.WriteHeader(http.StatusInternalServerError)

		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)

	_, err = w.Write(jsonObject)
	if err != nil {
		a.logger(r).WithError(err).Errorf("failed to write json response")
	}
}
