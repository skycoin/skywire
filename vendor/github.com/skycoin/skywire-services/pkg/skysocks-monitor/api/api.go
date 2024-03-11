// Package api pkg/skysocks-monitor/api/api.go
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/sirupsen/logrus"
	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/httputil"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	utilenv "github.com/skycoin/skywire-utilities/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/servicedisc"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/transport/network"
	"github.com/skycoin/skywire/pkg/visor"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"

	"github.com/skycoin/skywire-services/internal/vpn"
)

// API register all the API endpoints.
// It implements a net/http.Handler.
type API struct {
	http.Handler
	Config
	ServicesURLs

	Visor *visor.Visor

	skysocksKeys  []cipher.PubKey
	deadSkysockss []string
	logger        logging.Logger
	startedAt     time.Time
}

// Config is struct for keys and sign value of VM
type Config struct {
	PK   cipher.PubKey
	SK   cipher.SecKey
	Sign cipher.Sig
}

// ServicesURLs is struct for organizing URLs of services
type ServicesURLs struct {
	SD string
	UT string
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
func New(logger *logging.Logger, srvURLs ServicesURLs, vmConfig Config) *API {

	api := &API{
		Config:       vmConfig,
		ServicesURLs: srvURLs,
		logger:       *logger,
		startedAt:    time.Now(),
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

// deregister dead Skysockss entries in service discovery
func (api *API) deregister() {
	api.logger.Info("Skysocks Deregistration started.")

	// reload keys
	api.getSkysocksKeys()

	// monitoring Skysockss
	onlineSkysocksCount := int64(0)
	api.deadSkysockss = []string{}
	var allDeadSkysockss []string
	if len(api.skysocksKeys) == 0 {
		api.logger.Warn("No Skysocks keys found")
	} else {
		for _, key := range api.skysocksKeys {
			api.testSkysocks(key, &onlineSkysocksCount)
			if len(api.deadSkysockss) >= 10 {
				api.skysocksDeregister(api.deadSkysockss)
				allDeadSkysockss = append(allDeadSkysockss, api.deadSkysockss...)
				api.deadSkysockss = []string{}
			}
		}
		api.logger.WithField("count", onlineSkysocksCount).Info("Skysockss online.")

		// deregister dead Skysockss
		if len(api.deadSkysockss) > 0 {
			api.skysocksDeregister(api.deadSkysockss)
		}
	}

	api.logger.WithField("Number of dead Skysockss", len(allDeadSkysockss)).WithField("PKs", allDeadSkysockss).Info("Skysocks Deregistration completed.")
}

func (api *API) testSkysocks(key cipher.PubKey, onlineSkysocksCount *int64) {

	online := api.isOnline(key)

	if online {
		atomic.AddInt64(onlineSkysocksCount, 1)
	}

	if !online {
		api.deadSkysockss = append(api.deadSkysockss, key.Hex())
	}
}

func (api *API) isOnline(key cipher.PubKey) (ok bool) {
	transport := network.DMSG

	tp, err := api.Visor.AddTransport(key, string(transport), time.Second*10)
	if err != nil {
		api.logger.WithError(err).Warnf("Failed to establish %v transport", transport)
		return false
	}

	var latency time.Duration
	api.logger.Infof("Established %v transport to %v", transport, key)
	// We use the name skysocks-client and not skysocks-lite-client here to get around the constraint that
	// -srv flag can only be set for skysocks-client and skysocks-client.
	// And due to this the binary should also be named as skysocks-client and not skysocks-client-lite
	sum, skysocksErr := RunSkysocksClient(api.Visor, key, skyenv.SkysocksClientName)
	time.Sleep(time.Second * 4)
	ok = true

	switch skysocksErr {
	case nil:
		if len(sum) > 0 {
			latency = sum[0].Latency
		}
	case vpn.ErrNotPermitted:
		api.logger.WithError(skysocksErr).Infof("Skysocks error on %v transport of %v.", transport, key)
	default:
		api.logger.WithError(skysocksErr).Infof("Skysocks error on %v transport of %v.", transport, key)
		ok = false
	}

	err = api.Visor.RemoveTransport(tp.ID)
	if err != nil {
		api.logger.Warnf("Error removing %v transport of %v: %v", transport, key, err)
	}

	if ok && latency != 0 {
		return ok
	}

	return ok
}

func (api *API) skysocksDeregister(keys []string) {
	err := api.deregisterRequest(keys, fmt.Sprintf(api.ServicesURLs.SD+"/api/services/deregister/skysocks"))
	if err != nil {
		api.logger.Warn(err)
		return
	}
	api.logger.Info("Deregister request send to SD")
}

// deregisterRequest is deregistration handler for all services
func (api *API) deregisterRequest(keys []string, rawReqURL string) error {
	reqURL, err := url.Parse(rawReqURL)
	if err != nil {
		return fmt.Errorf("error on parsing deregistration URL : %v", err)
	}

	jsonData, err := json.Marshal(keys)
	if err != nil {
		return fmt.Errorf("error on parsing deregistration keys : %v", err)
	}
	body := bytes.NewReader(jsonData)

	req := &http.Request{
		Method: "DELETE",
		URL:    reqURL,
		Header: map[string][]string{
			"NM-PK":   {api.Config.PK.Hex()},
			"NM-Sign": {api.Config.Sign.Hex()},
		},
		Body: io.NopCloser(body),
	}

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("error on send deregistration request : %s", err)
	}
	defer func(Body io.ReadCloser) {
		_ = Body.Close() //nolint
	}(res.Body)

	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("error deregistering skysocks keys: status code %v", res.StatusCode)
	}

	return nil
}

type skysocksList []servicedisc.Service

func getSkysockss(sdURL string) (data skysocksList, err error) {
	res, err := http.Get(sdURL + "/api/services?type=skysocks") //nolint

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

func (api *API) getSkysocksKeys() {
	skysockss, err := getSkysockss(api.ServicesURLs.SD)
	if err != nil {
		api.logger.Warn("Error while fetching skysockss: %v", err)
		return
	}
	if len(skysockss) == 0 {
		api.logger.Warn("No skysockss found... Trying again")
	}
	//randomize the order of the skysocks entries
	rand.Shuffle(len(skysockss), func(i, j int) {
		skysockss[i], skysockss[j] = skysockss[j], skysockss[i]
	})
	api.skysocksKeys = []cipher.PubKey{}
	for _, skysocksEntry := range skysockss {
		api.skysocksKeys = append(api.skysocksKeys, skysocksEntry.Addr.PubKey())
	}

	api.logger.WithField("skysockss", len(skysockss)).Info("Skysocks keys updated.")
}

func (api *API) startVisor(ctx context.Context, conf *visorconfig.V1) {
	conf.SetLogger(logging.NewMasterLogger())
	v, ok := visor.NewVisor(ctx, conf)
	if !ok {
		api.logger.Fatal("Failed to start visor.")
	}
	api.Visor = v
}

// RunSkysocksClient runs a skysocks-client which connects to skysocks server
func RunSkysocksClient(v *visor.Visor, serverPK cipher.PubKey, appName string) ([]appserver.ConnectionSummary, error) {
	err := v.SetAppPK(appName, serverPK)
	if err != nil {
		return []appserver.ConnectionSummary{}, err
	}
	err = v.StartApp(appName)
	if err != nil {
		return []appserver.ConnectionSummary{}, err
	}

	time.Sleep(time.Second * 15)
	appErr, _ := v.GetAppError(appName) //nolint
	if appErr == vpn.ErrNotPermitted.Error() {
		return []appserver.ConnectionSummary{}, vpn.ErrNotPermitted
	}
	if appErr == vpn.ErrServerOffline.Error() {
		return []appserver.ConnectionSummary{}, vpn.ErrServerOffline
	}
	sum, err := v.GetAppConnectionsSummary(appName)
	if err != nil {
		return []appserver.ConnectionSummary{}, err
	}
	time.Sleep(time.Second * 2)
	err = v.StopApp(appName)
	if err != nil {
		return []appserver.ConnectionSummary{}, err
	}

	return sum, nil
}

// InitConfig to initialize config
func InitConfig(confPath string, mLog *logging.MasterLogger) *visorconfig.V1 {
	log := mLog.PackageLogger("skysocks_monitor:config")
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
				newConfLauncherApps = append(newConfLauncherApps, oldApp)
			}
		}
	}
	conf.Launcher.Apps = newConfLauncherApps

	conf.Version = oldConf.Version
	conf.LocalPath = oldConf.LocalPath
	conf.Launcher.BinPath = oldConf.Launcher.BinPath
	conf.Launcher.ServerAddr = oldConf.Launcher.ServerAddr
	conf.CLIAddr = oldConf.CLIAddr
	conf.Transport.TransportSetupPKs = oldConf.Transport.TransportSetupPKs

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
