// Package api pkg/config-bootstrapper/api/api.go
package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/sirupsen/logrus"
	"github.com/skycoin/skywire"
	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/httputil"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// API represents the api of the stun-list service.
type API struct {
	http.Handler

	log *logging.Logger

	startedAt time.Time

	services *visorconfig.Services

	dmsghttpConf   httputil.DMSGHTTPConf
	dmsghttpConfTs time.Time

	closeOnce sync.Once
	closeC    chan struct{}

	dmsgAddr string
}

// HealthCheckResponse is struct of /health endpoint
type HealthCheckResponse struct {
	BuildInfo *buildinfo.Info `json:"build_info,omitempty"`
	StartedAt time.Time       `json:"started_at"`
	DmsgAddr  string          `json:"dmsg_address,omitempty"`
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
func New(log *logging.Logger, conf Config, domain, dmsgAddr string) *API {
	var envServices skywire.EnvServices
	var svcs skywire.Services
	json.Unmarshal([]byte(skywire.ServicesJSON), &envServices) //nolint
	json.Unmarshal(envServices.Prod, &svcs)                    //nolint

	sd := strings.Replace(svcs.ServiceDiscovery, "skycoin.com", domain, -1)
	if domain == "skywire.skycoin.com" {
		sd = svcs.ServiceDiscovery
	}

	services := &visorconfig.Services{
		DmsgDiscovery:      strings.Replace(svcs.DmsgDiscovery, "skywire.skycoin.com", domain, -1),
		TransportDiscovery: strings.Replace(svcs.TransportDiscovery, "skywire.skycoin.com", domain, -1),
		AddressResolver:    strings.Replace(svcs.AddressResolver, "skywire.skycoin.com", domain, -1),
		RouteFinder:        strings.Replace(svcs.RouteFinder, "skywire.skycoin.com", domain, -1),
		RouteSetupNodes:    conf.SetupNodes,
		UptimeTracker:      strings.Replace(svcs.UptimeTracker, "skywire.skycoin.com", domain, -1),
		ServiceDiscovery:   sd,
		StunServers:        conf.StunServers,
		DNSServer:          svcs.DNSServer,
		SurveyWhitelist:    conf.SurveyWhitelist,
		TransportSetupPKs:  conf.TransportSetupPKs,
	}

	api := &API{
		log:            log,
		startedAt:      time.Now(),
		services:       services,
		dmsghttpConfTs: time.Now().Add(-5 * time.Minute),
		closeC:         make(chan struct{}),
		dmsgAddr:       dmsgAddr,
	}

	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(httputil.SetLoggerMiddleware(log))
	r.Get("/health", api.health)
	r.Get("/", api.config)
	r.Get("/dmsghttp", api.dmsghttp)

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
		DmsgAddr:  a.dmsgAddr,
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

func (a *API) dmsghttp(w http.ResponseWriter, r *http.Request) {
	if time.Now().Add(-5 * time.Minute).After(a.dmsghttpConfTs) {
		a.dmsghttpConf = a.dmsghttpConfGen()
		a.dmsghttpConfTs = time.Now()
	}
	a.writeJSON(w, r, http.StatusOK, a.dmsghttpConf)
}

func (a *API) dmsghttpConfGen() httputil.DMSGHTTPConf {
	var dmsghttpConf httputil.DMSGHTTPConf
	dmsghttpConf.DMSGServers = fetchDMSGServers(a.services.DmsgDiscovery)
	dmsghttpConf.AddressResolver = fetchDMSGAddress(a.services.AddressResolver)
	dmsghttpConf.DMSGDiscovery = fetchDMSGAddress(a.services.DmsgDiscovery)
	dmsghttpConf.RouteFinder = fetchDMSGAddress(a.services.RouteFinder)
	dmsghttpConf.ServiceDiscovery = fetchDMSGAddress(a.services.ServiceDiscovery)
	dmsghttpConf.TranspordDiscovery = fetchDMSGAddress(a.services.TransportDiscovery)
	dmsghttpConf.UptimeTracker = fetchDMSGAddress(a.services.UptimeTracker)

	return dmsghttpConf
}

func fetchDMSGAddress(url string) string {
	resp, err := http.Get(fmt.Sprintf("%s/health", url))
	if err != nil {
		return ""
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return ""
	}
	var healthResponse httputil.HealthCheckResponse
	err = json.Unmarshal(body, &healthResponse)
	if err != nil {
		return ""
	}
	return healthResponse.DmsgAddr
}

func fetchDMSGServers(url string) []httputil.DMSGServersConf {
	var dmsgServersList []httputil.DMSGServersConf
	resp, err := http.Get(fmt.Sprintf("%s/dmsg-discovery/all_servers", url))
	if err != nil {
		return dmsgServersList
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return dmsgServersList
	}
	err = json.Unmarshal(body, &dmsgServersList)
	if err != nil {
		return dmsgServersList
	}
	return dmsgServersList
}
