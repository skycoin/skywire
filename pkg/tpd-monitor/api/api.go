// Package api pkg/dmsg-monitor/api.go
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
	utilenv "github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/transport/network"
	"github.com/skycoin/skywire/pkg/visor"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// API register all the API endpoints.
// It implements a net/http.Handler.
type API struct {
	http.Handler

	Visor *visor.Visor

	dmsgURL   string
	arURL     string
	tpdURL    string
	startedAt time.Time
	logger    logging.Logger
	dMu       sync.RWMutex

	nmPk           cipher.PubKey
	nmSign         cipher.Sig
	whitelistedPKs map[string]bool
}

// DMSGMonitorConfig is struct for Keys and Sign value of dmsg monitor
type DMSGMonitorConfig struct {
	PK   cipher.PubKey
	Sign cipher.Sig
}

// ServicesURLs is struct for organize URL of services
type ServicesURLs struct {
	AR   string
	DMSG string
	TPD  string
}

// HealthCheckResponse is struct of /health endpoint
type HealthCheckResponse struct {
	BuildInfo *buildinfo.Info `json:"build_info,omitempty"`
	StartedAt time.Time       `json:"started_at,omitempty"`
}

// Error is the object returned to the client when there's an error.
type Error struct {
	Error string `json:"error"`
}

// New returns a new *chi.Mux object, which can be started as a server
func New(logger *logging.Logger, srvURLs ServicesURLs, monitorConfig DMSGMonitorConfig) *API {

	api := &API{
		dmsgURL:        srvURLs.DMSG,
		tpdURL:         srvURLs.TPD,
		arURL:          srvURLs.AR,
		logger:         *logger,
		startedAt:      time.Now(),
		nmPk:           monitorConfig.PK,
		nmSign:         monitorConfig.Sign,
		whitelistedPKs: whitelistedPKs(),
	}
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(httputil.SetLoggerMiddleware(logger))
	r.Get("/health", api.health)
	api.Handler = r

	return api
}

func (api *API) health(w http.ResponseWriter, r *http.Request) {
	info := buildinfo.Get()
	api.writeJSON(w, r, http.StatusOK, HealthCheckResponse{
		BuildInfo: info,
		StartedAt: api.startedAt,
	})
}

func (api *API) writeJSON(w http.ResponseWriter, r *http.Request, code int, object interface{}) {
	jsonObject, err := json.Marshal(object)
	if err != nil {
		api.log(r).WithError(err).Errorf("failed to encode json response")
		w.WriteHeader(http.StatusInternalServerError)

		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)

	_, err = w.Write(jsonObject)
	if err != nil {
		api.log(r).WithError(err).Errorf("failed to write json response")
	}
}

func (api *API) log(r *http.Request) logrus.FieldLogger {
	return httputil.GetLogger(r)
}

// InitDeregistrationLoop is function which runs periodic background tasks of API.
func (api *API) InitDeregistrationLoop(ctx context.Context, conf *visorconfig.V1, sleepDeregistration time.Duration) {
	// Start a visor
	api.startVisor(ctx, conf)

	for {
		select {
		case <-ctx.Done():
			return
		default:
			api.deregister()
			time.Sleep(sleepDeregistration * time.Minute)
		}
	}
}

// deregister use as routine to deregister old/dead entries in the network
func (api *API) deregister() {
	api.logger.Info("Deregistration routine start.")
	defer api.dMu.Unlock()
	api.dMu.Lock()

	// get uptimes data to check online/offline of visor based on uptime tracker
	arData, err := getARData(api.arURL)
	if err != nil {
		api.logger.Warnf("Error occur during get address resolver data due to %s", err)
		return
	}
	dmsgData, err := getDMSGData(api.dmsgURL)
	if err != nil {
		api.logger.Warnf("Error occur during get dmsg discovery entries list due to %s", err)
		return
	}

	api.tpdDeregistration(arData, dmsgData)

	api.logger.Info("Deregistration routine completed.")
}

// tpdDeregistration is a routine to deregister dead transports entries in transport discovery
func (api *API) tpdDeregistration(arData map[string]map[string]bool, dmsgData map[string]bool) {
	api.logger.Info("TPD Deregistration started.")

	// get list of all transports entries
	tps, err := api.getTransports()
	if err != nil {
		api.logger.Warnf("Error occur during get transports entries list due to %s", err)
		return
	}

	// check transports either alive or dead
	var deadTps []string
	for _, tp := range tps {
		if !api.tpChecker(tp, arData, dmsgData) {
			deadTps = append(deadTps, tp.ID.String())
		}
	}
	if len(deadTps) > 0 {
		api.tpdDeregister(deadTps)
	}
	api.logger.WithField("Transports", deadTps).WithField("Number of dead tp entries", len(deadTps)).Info("TPD Deregistration completed.")
}

func (api *API) tpChecker(tp transport.Entry, arData map[string]map[string]bool, dmsgData map[string]bool) bool {
	switch tp.Type {
	case network.STCPR:
		if _, ok := arData["stcpr"][tp.Edges[0].String()]; ok {
			if _, ok := arData["sudph"][tp.Edges[1].String()]; ok {
				return true
			}
		}
		if _, ok := arData["stcpr"][tp.Edges[1].String()]; ok {
			if _, ok := arData["sudph"][tp.Edges[0].String()]; ok {
				return true
			}
		}
		return false
	case network.SUDPH:
		if _, ok := arData["sudph"][tp.Edges[0].String()]; ok {
			if _, ok := arData["sudph"][tp.Edges[1].String()]; ok {
				return true
			}
		}
		return false
	default:
		if _, ok := dmsgData[tp.Edges[0].String()]; ok {
			if _, ok := dmsgData[tp.Edges[1].String()]; ok {
				return true
			}
		}
		return false
	}
}

func (api *API) getTransports() ([]transport.Entry, error) {
	res, err := http.Get(api.tpdURL + "/all-transports") //nolint
	var data []transport.Entry
	if err != nil {
		return nil, err
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal(body, &data)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func getDMSGData(url string) (map[string]bool, error) {
	res, err := http.Get(url + "/dmsg-discovery/entries") //nolint
	var data []string
	if err != nil {
		return nil, err
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal(body, &data)
	if err != nil {
		return nil, err
	}
	response := make(map[string]bool)
	for _, key := range data {
		response[key] = true
	}
	return response, nil
}

type arData struct {
	SUDPH []string
	STCPR []string
}

func getARData(url string) (map[string]map[string]bool, error) {
	res, err := http.Get(url + "/transports") //nolint
	var data arData
	if err != nil {
		return nil, err
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal(body, &data)
	if err != nil {
		return nil, err
	}
	response := make(map[string]map[string]bool)
	response["stcpr"] = make(map[string]bool)
	response["sudph"] = make(map[string]bool)
	for _, key := range data.STCPR {
		response["stcpr"][key] = true
	}
	for _, key := range data.SUDPH {
		response["sudph"][key] = true
	}
	return response, nil
}

func (api *API) tpdDeregister(tps []string) {
	err := api.deregisterRequest(tps, api.dmsgURL+"/deregister", "tp discovery")
	if err != nil {
		api.logger.Warn(err)
		return
	}
	api.logger.Info("Deregister request send to tpd")
}

// deregisterRequest is deregistration handler for all services
func (api *API) deregisterRequest(keys []string, rawReqURL, service string) error {
	reqURL, err := url.Parse(rawReqURL)
	if err != nil {
		return fmt.Errorf("Error on parsing deregistration URL : %v", err)
	}

	jsonData, err := json.Marshal(keys)
	if err != nil {
		return fmt.Errorf("Error on parsing deregistration keys : %v", err)
	}
	body := bytes.NewReader(jsonData)

	req := &http.Request{
		Method: "DELETE",
		URL:    reqURL,
		Header: map[string][]string{
			"NM-PK":   {api.nmPk.Hex()},
			"NM-Sign": {api.nmSign.Hex()},
		},
		Body: io.NopCloser(body),
	}

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("Error on send deregistration request : %s", err)
	}
	defer func(Body io.ReadCloser) {
		_ = Body.Close() // nolint
	}(res.Body)

	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("Error deregister keys from %s : %s", service, err)
	}

	return nil
}

func (api *API) startVisor(ctx context.Context, conf *visorconfig.V1) {
	conf.SetLogger(logging.NewMasterLogger())
	v, ok := visor.NewVisor(ctx, conf)
	if !ok {
		api.logger.Fatal("Failed to start visor.")
	}
	api.Visor = v
}

// InitConfig to initialize config
func InitConfig(confPath string, mLog *logging.MasterLogger) *visorconfig.V1 {
	log := mLog.PackageLogger("network_monitor:config")
	log.Info("Reading config from file.")
	log.WithField("filepath", confPath).Info()

	oldConf, err := visorconfig.ReadFile(confPath)
	if err != nil {
		log.WithError(err).Fatal("Failed to read config file.")
	}
	var testEnv bool
	if oldConf.Dmsg.Discovery == utilenv.TestDmsgDiscAddr {
		testEnv = true
	}
	// have same services as old config
	services := &visorconfig.Services{
		DmsgDiscovery:      oldConf.Dmsg.Discovery,
		TransportDiscovery: oldConf.Transport.Discovery,
		AddressResolver:    oldConf.Transport.AddressResolver,
		RouteFinder:        oldConf.Routing.RouteFinder,
		RouteSetupNodes:    oldConf.Routing.RouteSetupNodes,
		UptimeTracker:      oldConf.UptimeTracker.Addr,
		ServiceDiscovery:   oldConf.Launcher.ServiceDisc,
	}
	// update old config
	conf, err := visorconfig.MakeDefaultConfig(mLog, &oldConf.SK, false, false, testEnv, false, false, confPath, "", services)
	if err != nil {
		log.WithError(err).Fatal("Failed to create config.")
	}

	// have the same apps that the old config had
	var newConfLauncherApps []appserver.AppConfig
	for _, app := range conf.Launcher.Apps {
		for _, oldApp := range oldConf.Launcher.Apps {
			if app.Name == oldApp.Name {
				newConfLauncherApps = append(newConfLauncherApps, app)
			}
		}
	}
	conf.Launcher.Apps = newConfLauncherApps

	conf.Version = oldConf.Version
	conf.LocalPath = oldConf.LocalPath
	conf.Launcher.BinPath = oldConf.Launcher.BinPath
	conf.Launcher.ServerAddr = oldConf.Launcher.ServerAddr
	conf.CLIAddr = oldConf.CLIAddr

	// following services are not needed
	conf.STCP = nil
	conf.Dmsgpty = nil
	conf.Transport.PublicAutoconnect = false

	// save the config file
	if err := conf.Flush(); err != nil {
		log.WithError(err).Fatal("Failed to flush config to file.")
	}

	return conf
}

func whitelistedPKs() map[string]bool {
	whitelistedPKs := make(map[string]bool)
	for _, pk := range strings.Split(utilenv.NetworkMonitorPKs, ",") {
		whitelistedPKs[pk] = true
	}
	for _, pk := range strings.Split(utilenv.TestNetworkMonitorPKs, ",") {
		whitelistedPKs[pk] = true
	}
	whitelistedPKs[utilenv.RouteSetupPKs] = true
	whitelistedPKs[utilenv.TestRouteSetupPKs] = true
	return whitelistedPKs
}
