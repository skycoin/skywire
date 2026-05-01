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

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/logging"
)

// API represents the api of the stun-list service.
type API struct {
	http.Handler

	log *logging.Logger

	startedAt time.Time

	services *deployment.Services
	// servicesMu guards services.DmsgServers, which is refreshed in the
	// background from dmsg-discovery so the served bootstrap list reflects
	// the actual current addresses of each known dmsg-server PK rather
	// than the addresses baked into services-config.json at build time.
	servicesMu sync.RWMutex

	dmsghttpConf   httputil.DMSGHTTPConf
	dmsghttpConfTs time.Time

	closeOnce sync.Once
	closeC    chan struct{}

	dmsgAddr string
}

// dmsgServersRefreshInterval is how often the bootstrap dmsg_servers list is
// refreshed from dmsg-discovery. 5m matches the dmsghttp endpoint cache.
const dmsgServersRefreshInterval = 5 * time.Minute

// HealthCheckResponse is struct of /health endpoint
type HealthCheckResponse struct {
	ServiceName string          `json:"service_name,omitempty"`
	BuildInfo   *buildinfo.Info `json:"build_info,omitempty"`
	StartedAt   time.Time       `json:"started_at"`
	DmsgAddr    string          `json:"dmsg_address,omitempty"`
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

// New creates a new api. By default, serves the embedded deployment config
// (deployment.Prod). The domain parameter applies HTTP URL replacements for
// custom deployments; DMSG addresses are PK-based and don't change.
func New(log *logging.Logger, conf Config, domain, dmsgAddr string) *API {
	// Start from the full embedded deployment config (includes DMSG fields)
	svcs := deployment.Prod

	// Apply domain replacement to HTTP URLs for custom deployments
	if domain != "" && domain != "skywire.skycoin.com" {
		svcs.DmsgDiscovery = strings.Replace(svcs.DmsgDiscovery, "skywire.skycoin.com", domain, -1)
		svcs.TransportDiscovery = strings.Replace(svcs.TransportDiscovery, "skywire.skycoin.com", domain, -1)
		svcs.AddressResolver = strings.Replace(svcs.AddressResolver, "skywire.skycoin.com", domain, -1)
		svcs.RouteFinder = strings.Replace(svcs.RouteFinder, "skywire.skycoin.com", domain, -1)
		svcs.UptimeTracker = strings.Replace(svcs.UptimeTracker, "skywire.skycoin.com", domain, -1)
		svcs.ServiceDiscovery = strings.Replace(svcs.ServiceDiscovery, "skycoin.com", domain, -1)
	}
	// Override from config file if provided
	if conf.SetupNodes != nil {
		svcs.RouteSetupNodes = conf.SetupNodes
	}
	if conf.StunServers != nil {
		svcs.StunServers = conf.StunServers
	}
	if conf.SurveyWhitelist != nil {
		svcs.SurveyWhitelist = conf.SurveyWhitelist
	}
	if conf.TransportSetupPKs != nil {
		svcs.TransportSetupPKs = conf.TransportSetupPKs
	}

	api := &API{
		log:            log,
		startedAt:      time.Now(),
		services:       &svcs,
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

	go api.refreshDmsgServersLoop()

	return api
}

// Close stops API.
func (a *API) Close() {
	a.closeOnce.Do(func() {
		close(a.closeC)
	})
}

// refreshDmsgServersLoop refreshes services.DmsgServers from dmsg-discovery
// at startup and then on a fixed interval. If dmsgd is unreachable or returns
// no entries, the previous in-memory list is kept (which on first attempt is
// the embedded list from services-config.json). This means the served
// bootstrap list converges on the live state without ever falling back to a
// stale-at-build-time response.
func (a *API) refreshDmsgServersLoop() {
	a.refreshDmsgServers()
	t := time.NewTicker(dmsgServersRefreshInterval)
	defer t.Stop()
	for {
		select {
		case <-a.closeC:
			return
		case <-t.C:
			a.refreshDmsgServers()
		}
	}
}

func (a *API) refreshDmsgServers() {
	a.servicesMu.RLock()
	dmsgdURL := a.services.DmsgDiscovery
	a.servicesMu.RUnlock()
	if dmsgdURL == "" {
		return
	}
	live := fetchDMSGServers(dmsgdURL)
	if len(live) == 0 {
		// Either dmsgd unreachable or no servers registered — keep the
		// previous list (embedded on first call, last-known-good after).
		a.log.Debug("dmsg_servers refresh returned 0 entries; keeping previous list")
		return
	}
	entries := make([]deployment.DmsgServerEntry, 0, len(live))
	for _, s := range live {
		var e deployment.DmsgServerEntry
		e.Static = s.Static
		e.Server.Address = s.Server.Address
		entries = append(entries, e)
	}
	a.servicesMu.Lock()
	a.services.DmsgServers = entries
	a.servicesMu.Unlock()
	a.log.WithField("count", len(entries)).Debug("dmsg_servers refreshed from dmsg-discovery")
}

func (a *API) logger(r *http.Request) logrus.FieldLogger {
	return httputil.GetLogger(r)
}

func (a *API) health(w http.ResponseWriter, r *http.Request) {
	info := buildinfo.Get()
	a.writeJSON(w, r, http.StatusOK, HealthCheckResponse{
		ServiceName: "config-bootstrapper",
		BuildInfo:   info,
		StartedAt:   a.startedAt,
		DmsgAddr:    a.dmsgAddr,
	})
}

func (a *API) config(w http.ResponseWriter, r *http.Request) {
	a.servicesMu.RLock()
	// Snapshot under the lock so the JSON encoder doesn't see a torn
	// state if refreshDmsgServers swaps the slice mid-encode.
	resp := *a.services
	a.servicesMu.RUnlock()
	a.writeJSON(w, r, http.StatusOK, &resp)
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
