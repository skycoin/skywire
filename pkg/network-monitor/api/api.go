// Package api pkg/network-monitor/api.go
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
	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/httputil"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire-utilities/pkg/metricsutil"
	utilenv "github.com/skycoin/skywire-utilities/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/transport/network"
	"github.com/skycoin/skywire/pkg/visor"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"

	"github.com/skycoin/skywire/internal/nm"
	"github.com/skycoin/skywire/internal/nmmetrics"
	"github.com/skycoin/skywire/pkg/network-monitor/store"
)

// API register all the API endpoints.
// It implements a net/http.Handler.
type API struct {
	http.Handler

	Visor          *visor.Visor
	VisorSummaries map[cipher.PubKey]nm.VisorSummary

	visorDetails map[cipher.PubKey]visorDetails
	sdURL        string
	arURL        string
	utURL        string
	logger       logging.Logger
	store        store.Store
	mu           sync.RWMutex
	dMu          sync.RWMutex
	startedAt    time.Time

	reqsInFlightCountMiddleware *metricsutil.RequestsInFlightCountMiddleware
	metrics                     nmmetrics.Metrics
	nmPk                        cipher.PubKey
	nmSk                        cipher.SecKey
	nmSign                      cipher.Sig
	batchSize                   int
	whitelistedPKs              map[string]bool
}

type visorDetails struct {
	IsOnline bool
	IsStcpr  bool
}

// NetworkMonitorConfig is struct for Keys and Sign value of NM
type NetworkMonitorConfig struct {
	PK        cipher.PubKey
	SK        cipher.SecKey
	Sign      cipher.Sig
	BatchSize int
}

// ServicesURLs is struct for organize URL of services
type ServicesURLs struct {
	SD string
	AR string
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
func New(s store.Store, logger *logging.Logger, srvURLs ServicesURLs, enableMetrics bool, m nmmetrics.Metrics, nmConfig NetworkMonitorConfig) *API {

	api := &API{
		VisorSummaries:              make(map[cipher.PubKey]nm.VisorSummary),
		visorDetails:                make(map[cipher.PubKey]visorDetails),
		sdURL:                       srvURLs.SD,
		arURL:                       srvURLs.AR,
		utURL:                       srvURLs.UT,
		logger:                      *logger,
		store:                       s,
		startedAt:                   time.Now(),
		reqsInFlightCountMiddleware: metricsutil.NewRequestsInFlightCountMiddleware(),
		metrics:                     m,
		nmPk:                        nmConfig.PK,
		nmSk:                        nmConfig.SK,
		nmSign:                      nmConfig.Sign,
		batchSize:                   nmConfig.BatchSize,
		whitelistedPKs:              whitelistedPKs(),
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
	r.Use(httputil.SetLoggerMiddleware(logger))
	r.Get("/status", api.getStatus)
	r.Get("/health", api.health)
	api.Handler = r

	return api
}

func (api *API) getStatus(w http.ResponseWriter, r *http.Request) {
	data, err := api.store.GetAllSummaries()
	if err != nil {
		api.logger.WithError(err).Warnf("Error Getting all summaries")
	}
	if err := json.NewEncoder(w).Encode(data); err != nil {
		api.writeError(w, r, err)
	}
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

// ServeHTTP implements http.Handler.
func (api *API) writeError(w http.ResponseWriter, r *http.Request, err error) {
	var status int

	if err == context.DeadlineExceeded {
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
		api.log(r).Warnf("%d: %s", status, err)
	}

	w.WriteHeader(status)
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(&Error{Error: err.Error()}); err != nil {
		api.log(r).WithError(err).Warn("Failed to encode error")
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
			api.deregister(ctx)
			time.Sleep(sleepDeregistration * time.Minute)
		}
	}
}

// deregister use as routine to deregister old/dead entries in the network
func (api *API) deregister(ctx context.Context) {
	api.logger.Info("Deregistration routine start.")
	defer api.dMu.Unlock()
	api.dMu.Lock()

	// reload keys
	api.getVisorKeys()

	// get uptimes data to check online/offline of visor based on uptime tracker
	uptimes, err := getUptimeTracker(api.utURL)
	if err != nil {
		api.logger.Warnf("Error occur during get uptime tracker status list due to %s", err)
		return
	}

	api.arDeregistration(ctx, uptimes)

	api.logger.Info("Deregistration routine completed.")

	// reload keys
	api.getVisorKeys()
}

// arDeregistration is a routine to deregister dead entries in address resolver transports
func (api *API) arDeregistration(ctx context.Context, uptimes map[string]bool) {
	api.logger.Info("AR Deregistration started.")
	allSudphCount, allStcprCount := 0, 0
	arKeys := make(map[cipher.PubKey]visorDetails)
	for key, details := range api.visorDetails {
		arKeys[key] = details

		if details.IsStcpr {
			allStcprCount++
		}
		if details.IsOnline {
			allSudphCount++
		}
	}
	if len(arKeys) == 0 {
		api.logger.Warn("No visor keys found")
		return
	}

	checkRes := arCheckerResult{
		deadStcpr: &[]string{},
		deadSudph: &[]string{},
	}

	checkConf := arChekerConfig{
		ctx:     ctx,
		wg:      new(sync.WaitGroup),
		uptimes: uptimes,
	}

	tmpBatchSize := 0
	for key, details := range arKeys {
		if _, ok := api.whitelistedPKs[key.Hex()]; ok {
			continue
		}
		tmpBatchSize++
		checkConf.wg.Add(1)
		checkConf.key = key
		checkConf.details = details
		go api.arChecker(checkConf, &checkRes)
		if tmpBatchSize == api.batchSize {
			time.Sleep(time.Minute)
			tmpBatchSize = 0
		}
	}
	checkConf.wg.Wait()

	stcprCounter := int64(allStcprCount - len(*checkRes.deadStcpr))
	sudphCounter := int64(allSudphCount - len(*checkRes.deadSudph))

	api.logger.WithField("sudph", sudphCounter).WithField("stcpr", stcprCounter).Info("Transports online.")
	api.metrics.SetTpCount(stcprCounter, sudphCounter)

	if len(*checkRes.deadStcpr) > 0 {
		api.arDeregister(*checkRes.deadStcpr, "stcpr")
	}
	api.logger.WithField("Number of dead Stcpr", len(*checkRes.deadStcpr)).WithField("PKs", checkRes.deadStcpr).Info("STCPR deregistration complete.")

	if len(*checkRes.deadSudph) > 0 {
		api.arDeregister(*checkRes.deadSudph, "sudph")
	}
	api.logger.WithField("Number of dead Sudph", len(*checkRes.deadSudph)).WithField("PKs", checkRes.deadSudph).Info("SUDPH deregistration complete.")

	api.logger.Info("AR Deregistration completed.")
}

func (api *API) arChecker(cfg arChekerConfig, res *arCheckerResult) {
	defer cfg.wg.Done()
	visorSum, err := api.store.GetVisorByPk(cfg.key.String())
	if err != nil {
		api.logger.WithError(err).Debugf("Failed to fetch visor summary of PK %s in AR deregister procces.", cfg.key.Hex())
		if err != store.ErrVisorSumNotFound {
			return
		}
	}

	stcprC := make(chan bool)
	sudphC := make(chan bool)
	if cfg.details.IsStcpr {
		go api.testTransport(cfg.key, network.STCPR, stcprC)
	}
	if cfg.details.IsOnline {
		go api.testTransport(cfg.key, network.SUDPH, sudphC)
	}

	if cfg.details.IsStcpr {
		visorSum.Stcpr = <-stcprC
	}
	if cfg.details.IsOnline {
		visorSum.Sudph = <-sudphC
	}
	visorSum.Timestamp = time.Now().Unix()
	api.mu.Lock()
	err = api.store.AddVisorSummary(cfg.ctx, cfg.key, visorSum)
	if err != nil {
		api.logger.WithError(err).Warnf("Failed to save Visor summary of %v", cfg.key)
	}

	if cfg.details.IsStcpr && !visorSum.Stcpr {
		*res.deadStcpr = append(*res.deadStcpr, cfg.key.Hex())
	}

	if cfg.details.IsOnline && !visorSum.Sudph {
		*res.deadSudph = append(*res.deadSudph, cfg.key.Hex())
	}

	api.mu.Unlock()
}

func (api *API) testTransport(key cipher.PubKey, transport network.Type, ch chan bool) {
	var isUp bool
	retrier := 3
	for retrier > 0 {
		tp, err := api.Visor.AddTransport(key, string(transport), time.Second*3)
		if err != nil {
			api.logger.WithField("Retry", 4-retrier).WithError(err).Warnf("Failed to establish %v transport to %v", transport, key)
			retrier--
			continue
		} else {
			api.logger.Infof("Established %v transport to %v", transport, key)
			isUp = true
			err = api.Visor.RemoveTransport(tp.ID)
			if err != nil {
				api.logger.Warnf("Error removing %v transport of %v: %v", transport, key, err)
			}
			retrier = 0
		}
	}
	ch <- isUp
}

func (api *API) arDeregister(keys []string, transport string) {
	err := api.deregisterRequest(keys, fmt.Sprintf(api.arURL+"/deregister/%s", transport), "address resolver")
	if err != nil {
		api.logger.Warn(err)
		return
	}
	api.logger.Info("Deregister request send to AR")
}

type arChekerConfig struct {
	ctx     context.Context
	wg      *sync.WaitGroup
	key     cipher.PubKey
	details visorDetails
	uptimes map[string]bool
}

type arCheckerResult struct {
	deadStcpr *[]string
	deadSudph *[]string
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

type visorTransports struct {
	Sudph []cipher.PubKey `json:"sudph"`
	Stcpr []cipher.PubKey `json:"stcpr"`
}

func getVisors(arURL string) (data visorTransports, err error) {
	res, err := http.Get(arURL + "/transports") //nolint

	if err != nil {
		return visorTransports{}, err
	}

	body, err := io.ReadAll(res.Body)

	if err != nil {
		return visorTransports{}, err
	}
	err = json.Unmarshal(body, &data)
	if err != nil {
		return visorTransports{}, err
	}
	return data, err
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

func (api *API) getVisorKeys() {
	api.visorDetails = make(map[cipher.PubKey]visorDetails)
	visorTs, err := getVisors(api.arURL)
	if err != nil {
		api.logger.Warnf("Error while fetching visors: %v", err)
		return
	}
	if len(visorTs.Stcpr) == 0 && len(visorTs.Sudph) == 0 {
		api.logger.Warn("No visors found... Will try again")
	}
	for _, visorPk := range visorTs.Stcpr {
		if visorPk != api.nmPk {
			detail := api.visorDetails[visorPk]
			detail.IsStcpr = true
			api.visorDetails[visorPk] = detail
		}
	}
	for _, visorPk := range visorTs.Sudph {
		if visorPk != api.nmPk {
			detail := api.visorDetails[visorPk]
			detail.IsOnline = true
			api.visorDetails[visorPk] = detail
		}
	}

	api.logger.WithField("visors", len(api.visorDetails)).Info("Visor keys updated.")
	api.metrics.SetTotalVisorCount(int64(len(api.visorDetails)))
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
