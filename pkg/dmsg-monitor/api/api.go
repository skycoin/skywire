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

	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/utilenv"
	"github.com/skycoin/skywire/pkg/visor"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// API register all the API endpoints.
// It implements a net/http.Handler.
type API struct {
	http.Handler

	Visor *visor.Visor

	dmsgURL   string
	utURL     string
	logger    logging.Logger
	dMu       sync.RWMutex
	startedAt time.Time

	nmPk           cipher.PubKey
	nmSign         cipher.Sig
	batchSize      int
	whitelistedPKs map[string]bool
}

// DMSGMonitorConfig is struct for Keys and Sign value of dmsg monitor
type DMSGMonitorConfig struct {
	PK        cipher.PubKey
	Sign      cipher.Sig
	BatchSize int
}

// ServicesURLs is struct for organize URL of services
type ServicesURLs struct {
	DMSG string
	UT   string
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
		utURL:          srvURLs.UT,
		logger:         *logger,
		startedAt:      time.Now(),
		nmPk:           monitorConfig.PK,
		nmSign:         monitorConfig.Sign,
		batchSize:      monitorConfig.BatchSize,
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
	uptimes, err := getUptimeTracker(api.utURL)
	if err != nil {
		api.logger.Warnf("Error occur during get uptime tracker status list due to %s", err)
		return
	}

	api.dmsgDeregistration(uptimes)

	api.logger.Info("Deregistration routine completed.")
}

// dmsgDeregistration is a routine to deregister dead dmsg entries in dmsg discovery
func (api *API) dmsgDeregistration(uptimes map[string]bool) {
	api.logger.Info("DMSGD Deregistration started.")

	// get list of all dmsg clients, not servers
	clients, err := getClients(api.dmsgURL)
	if err != nil {
		api.logger.Warnf("Error occur during get dmsg clients list due to %s", err)
		return
	}

	// check dmsg clients either alive or dead
	checkerConfig := dmsgCheckerConfig{
		wg:        new(sync.WaitGroup),
		locker:    new(sync.Mutex),
		uptimes:   uptimes,
		transport: "dmsg",
	}
	deadDmsg := []string{}
	var tmpBatchSize, deadDmsgCount int
	for i, client := range clients {
		if _, ok := api.whitelistedPKs[client]; !ok {
			checkerConfig.wg.Add(1)
			checkerConfig.client = client
			go api.dmsgChecker(checkerConfig, &deadDmsg)
		}
		tmpBatchSize++
		if tmpBatchSize == api.batchSize || i == len(clients)-1 {
			checkerConfig.wg.Wait()
			// deregister clients from dmsg-discovery
			if len(deadDmsg) > 0 {
				api.dmsgDeregister(deadDmsg)
				deadDmsgCount += len(deadDmsg)
			}
			deadDmsg = []string{}
			tmpBatchSize = 0
		}
	}

	api.logger.WithField("Number of dead DMSG entries", deadDmsgCount).Info("DMSGD Deregistration completed.")
}

func (api *API) dmsgChecker(cfg dmsgCheckerConfig, deadDmsg *[]string) {
	defer cfg.wg.Done()

	key := cipher.PubKey{}
	err := key.UnmarshalText([]byte(cfg.client))
	if err != nil {
		api.logger.Warnf("Error marshaling key: %s", err)
		return
	}

	var trp bool
	retrier := 3
	for retrier > 0 {
		tp, err := api.Visor.AddTransport(key, cfg.transport, time.Second*3)
		if err != nil {
			api.logger.WithField("Retry", 4-retrier).WithError(err).Warnf("Failed to establish %v transport to %v", cfg.transport, key)
			retrier--
			if strings.Contains(err.Error(), "unknown network type") {
				trp = true
				retrier = 0
			}
		} else {
			api.logger.Infof("Established %v transport to %v", cfg.transport, key)
			trp = true
			err = api.Visor.RemoveTransport(tp.ID)
			if err != nil {
				api.logger.Warnf("Error removing %v transport of %v: %v", cfg.transport, key, err)
			}
			retrier = 0
		}
	}

	if !trp {
		if status, ok := cfg.uptimes[key.Hex()]; !ok || !status {
			cfg.locker.Lock()
			*deadDmsg = append(*deadDmsg, key.Hex())
			cfg.locker.Unlock()
		}
	}
}

func (api *API) dmsgDeregister(keys []string) {
	err := api.deregisterRequest(keys, api.dmsgURL+"/dmsg-discovery/deregister", "dmsg discovery")
	if err != nil {
		api.logger.Warn(err)
		return
	}
	api.logger.Info("Deregister request send to DSMGD")
}

type dmsgCheckerConfig struct {
	client    string
	transport string
	uptimes   map[string]bool
	wg        *sync.WaitGroup
	locker    *sync.Mutex
}

// deregisterRequest is dereigstration handler for all services
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
	defer res.Body.Close() //nolint

	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("Error deregister keys from %s : %s", service, err)
	}

	return nil
}

type clientList []string

func getClients(dmsgURL string) (data clientList, err error) {
	res, err := http.Get(dmsgURL + "/dmsg-discovery/entries") //nolint

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

func getUptimeTracker(utURL string) (map[string]bool, error) {
	response := make(map[string]bool)
	res, err := http.Get(utURL) //nolint
	if err != nil {
		return response, err
	}

	body, err := io.ReadAll(res.Body)
	if err != nil {
		return response, err
	}
	var data []uptimes
	err = json.Unmarshal(body, &data)
	if err != nil {
		return response, err
	}

	for _, visor := range data {
		response[visor.Key] = visor.Online
	}

	return response, nil
}

type uptimes struct {
	Key    string `json:"key"`
	Online bool   `json:"online"`
}

func (api *API) startVisor(ctx context.Context, conf *visorconfig.V1) {
	conf.SetLogger(logging.NewMasterLogger())
	v, ok := visor.NewVisor(ctx, conf)
	if !ok {
		api.logger.Fatal("Failed to start visor.")
	}
	api.Visor = v
}

// InitConfig to initilise config
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
	// update oldconfig
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
