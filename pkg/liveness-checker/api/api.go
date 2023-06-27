// Package api pkg/liveness-checker/api.go
package api

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/url"
	"time"

	"github.com/ccding/go-stun/stun"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/sirupsen/logrus"
	"github.com/skycoin/dmsg/pkg/disc"
	"github.com/skycoin/dmsg/pkg/dmsg"

	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/httputil"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	utilenv "github.com/skycoin/skywire-utilities/pkg/skyenv"
	"github.com/skycoin/skywire/internal/lc"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/liveness-checker/store"
	"github.com/skycoin/skywire/pkg/utclient"
	"github.com/skycoin/skywire/pkg/visor"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// API register all the API endpoints.
// It implements a net/http.Handler.
type API struct {
	http.Handler

	lcPk cipher.PubKey
	lcSk cipher.SecKey

	Visor *visor.Visor

	*visorconfig.Services

	logger  *logging.Logger
	mLogger *logging.MasterLogger

	store     store.Store
	startedAt time.Time
}

// New returns a new *chi.Mux object, which can be started as a server
func New(lcPk cipher.PubKey, lcSk cipher.SecKey, s store.Store, logger *logging.Logger, mLogger *logging.MasterLogger,
	services *visorconfig.Services) *API {

	api := &API{
		lcPk:      lcPk,
		lcSk:      lcSk,
		Services:  services,
		logger:    logger,
		mLogger:   mLogger,
		store:     s,
		startedAt: time.Now(),
	}
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(httputil.SetLoggerMiddleware(logger))
	r.Get("/status", api.getStatus)
	r.Get("/health", api.health)
	api.Handler = r

	return api
}

func (api *API) getStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	data, err := api.store.GetServiceSummaries(ctx)
	if err != nil {
		api.logger.WithError(err).Warnf("Error Getting service details")
	}
	if err := json.NewEncoder(w).Encode(data); err != nil {
		api.writeError(w, r, err)
	}
}

func (api *API) health(w http.ResponseWriter, r *http.Request) {
	info := buildinfo.Get()
	api.writeJSON(w, r, http.StatusOK, httputil.HealthCheckResponse{
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
	if err := json.NewEncoder(w).Encode(&httputil.Error{Error: err.Error()}); err != nil {
		api.log(r).WithError(err).Warn("Failed to encode error")
	}
}

func (api *API) log(r *http.Request) logrus.FieldLogger {
	return httputil.GetLogger(r)
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
func InitConfig(confPath string, mLog *logging.MasterLogger) (*visorconfig.V1, *visorconfig.Services) {
	log := mLog.PackageLogger("liveness_checker:config")
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
		StunServers:        oldConf.StunServers,
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

	// following services are not needed for the visor but we still need to
	// save them in config for other background tasks
	conf.UptimeTracker = nil
	conf.StunServers = nil

	return conf, services
}

// RunBackgroundTasks is function which runs periodic background tasks of API.
func (api *API) RunBackgroundTasks(ctx context.Context, conf *visorconfig.V1) {

	// Start a visor
	api.startVisor(ctx, conf)

	ticker := time.NewTicker(time.Minute * 5)
	api.checkAddressResolver(ctx)
	api.checkServiceDiscovery(ctx)
	api.checkTransportDiscovery(ctx)
	api.checkDMSGDiscovery(ctx)
	api.checkRouteFinder(ctx)
	api.checkUptimeTracker(ctx)
	api.checkIPService(ctx)
	api.checkStunServers(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			api.checkAddressResolver(ctx)
			api.checkServiceDiscovery(ctx)
			api.checkTransportDiscovery(ctx)
			api.checkDMSGDiscovery(ctx)
			api.checkRouteFinder(ctx)
			api.checkUptimeTracker(ctx)
			api.checkIPService(ctx)
			api.checkStunServers(ctx)
			// wait full timeout no matter how long the last phase took
			ticker = time.NewTicker(time.Minute * 5)
			api.logger.Info("liveness check routine complete.")
		}
	}
}

// checkAddressResolver runs a liveness check on the address-resolver
func (api *API) checkAddressResolver(ctx context.Context) {

	online := true
	var errs []string

	cInfo, err := checkCertificate(api.AddressResolver)
	if err != nil {
		online = false
		errs = append(errs, err.Error())
	}

	health, err := httputil.GetServiceHealth(ctx, api.AddressResolver)
	if err != nil {
		online = false
		errs = append(errs, err.Error())
	}

	ss := &lc.ServiceSummary{
		Online:          online,
		Errors:          errs,
		Timestamp:       time.Now().Unix(),
		Health:          health,
		CertificateInfo: cInfo,
	}

	err = api.store.AddServiceSummary(ctx, "address-resolver", ss)
	if err != nil {
		api.logger.WithError(err).Warn("Failed to add address-resolver service summarry to store.")
	}
	api.logger.Info("address-resolver liveness check complete.")
}

// checkServiceDiscovery runs a liveness check on the service-discovery
func (api *API) checkServiceDiscovery(ctx context.Context) {
	online := true
	var errs []string

	cInfo, err := checkCertificate(api.ServiceDiscovery)
	if err != nil {
		online = false
		errs = append(errs, err.Error())
	}

	health, err := httputil.GetServiceHealth(ctx, api.ServiceDiscovery)
	if err != nil {
		online = false
		errs = append(errs, err.Error())
	}

	ss := &lc.ServiceSummary{
		Online:          online,
		Errors:          errs,
		Timestamp:       time.Now().Unix(),
		Health:          health,
		CertificateInfo: cInfo,
	}

	err = api.store.AddServiceSummary(ctx, "service-discovery", ss)
	if err != nil {
		api.logger.WithError(err).Warn("Failed to add service-discovery service summarry to store.")
	}
	api.logger.Info("service-discovery liveness check complete.")
}

// checkTransportDiscovery runs a liveness check on the transport-discovery
func (api *API) checkTransportDiscovery(ctx context.Context) {
	// will need visor for other checks

	online := true
	var errs []string

	cInfo, err := checkCertificate(api.TransportDiscovery)
	if err != nil {
		online = false
		errs = append(errs, err.Error())
	}

	health, err := httputil.GetServiceHealth(ctx, api.TransportDiscovery)
	if err != nil {
		online = false
		errs = append(errs, err.Error())
	}

	ss := &lc.ServiceSummary{
		Online:          online,
		Errors:          errs,
		Timestamp:       time.Now().Unix(),
		Health:          health,
		CertificateInfo: cInfo,
	}

	err = api.store.AddServiceSummary(ctx, "transport-discovery", ss)
	if err != nil {
		api.logger.WithError(err).Warn("Failed to add transport-discovery service summarry to store.")
	}
	api.logger.Info("transport-discovery liveness check complete.")
}

// checkDMSGDiscovery runs a liveness check on the dmsg-discovery
func (api *API) checkDMSGDiscovery(ctx context.Context) {
	// will need visor for other checks

	online := true
	var errs []string

	cInfo, err := checkCertificate(api.DmsgDiscovery)
	if err != nil {
		online = false
		errs = append(errs, err.Error())
	}

	health, err := httputil.GetServiceHealth(ctx, api.DmsgDiscovery)
	if err != nil {
		online = false
		errs = append(errs, err.Error())
	}

	disc := disc.NewHTTP(api.DmsgDiscovery, &http.Client{}, api.logger)
	servers, err := disc.AllServers(ctx)
	if err != nil {
		online = false
		errs = append(errs, err.Error())
	}

	_, err = disc.AvailableServers(ctx)
	if err != nil {
		online = false
		errs = append(errs, err.Error())
	}

	ss := &lc.ServiceSummary{
		Online:          online,
		Errors:          errs,
		Timestamp:       time.Now().Unix(),
		Health:          health,
		CertificateInfo: cInfo,
	}

	err = api.store.AddServiceSummary(ctx, "dmsg-discovery", ss)
	if err != nil {
		api.logger.WithError(err).Warn("Failed to add dmsg-discovery service summarry to store.")
	}
	api.logger.Info("dmsg-discovery liveness check complete.")

	api.checkDMSGServers(ctx, disc, servers)
}

// checkDMSGServers runs a liveness check on the servers registered in dmsg-discovery
func (api *API) checkDMSGServers(ctx context.Context, disc disc.APIClient, servers []*disc.Entry) {
	// will need visor for other checks
	online := true
	var errs []string

	dmsgConf := dmsg.DefaultConfig()
	client := dmsg.NewClient(api.lcPk, api.lcSk, disc, dmsgConf)
	for _, server := range servers {
		if err := client.EnsureSession(ctx, server); err != nil {
			online = false
			errs = append(errs, err.Error())
		}

		ss := &lc.ServiceSummary{
			Online:    online,
			Errors:    errs,
			Timestamp: time.Now().Unix(),
		}

		serviceName := "dmsg-server:" + server.Server.Address
		err := api.store.AddServiceSummary(ctx, serviceName, ss)
		if err != nil {
			api.logger.WithError(err).Warnf("Failed to add %v serviceName service summarry to store.", serviceName)
		}
		api.logger.Infof("%v liveness check complete.", serviceName)
	}
	api.logger.Info("dmsg-server liveness check complete.")
}

// checkRouteFinder runs a liveness check on the route-finder
func (api *API) checkRouteFinder(ctx context.Context) {
	// will need visor for other checks

	online := true
	var errs []string

	cInfo, err := checkCertificate(api.RouteFinder)
	if err != nil {
		online = false
		errs = append(errs, err.Error())
	}

	health, err := httputil.GetServiceHealth(ctx, api.RouteFinder)
	if err != nil {
		online = false
		errs = append(errs, err.Error())
	}

	ss := &lc.ServiceSummary{
		Online:          online,
		Errors:          errs,
		Timestamp:       time.Now().Unix(),
		Health:          health,
		CertificateInfo: cInfo,
	}

	err = api.store.AddServiceSummary(ctx, "route-finder", ss)
	if err != nil {
		api.logger.WithError(err).Warn("Failed to add route-finder service summarry to store.")
	}
	api.logger.Info("route-finder liveness check complete.")
}

// checkUptimeTracker runs a liveness check on the uptime-tracker
func (api *API) checkUptimeTracker(ctx context.Context) {

	online := true
	var errs []string

	cInfo, err := checkCertificate(api.UptimeTracker)
	if err != nil {
		online = false
		errs = append(errs, err.Error())
	}

	health, err := httputil.GetServiceHealth(ctx, api.UptimeTracker)
	if err != nil {
		online = false
		errs = append(errs, err.Error())
	}

	ut, err := utclient.NewHTTP(api.UptimeTracker, api.lcPk, api.lcSk, &http.Client{}, "", api.mLogger)
	if err != nil {
		api.logger.WithError(err).Warn("failed to create uptime tracker client.")
	}

	err = ut.UpdateVisorUptime(ctx, "")
	if err != nil {
		online = false
		errs = append(errs, err.Error())
	}

	ss := &lc.ServiceSummary{
		Online:          online,
		Errors:          errs,
		Timestamp:       time.Now().Unix(),
		Health:          health,
		CertificateInfo: cInfo,
	}

	err = api.store.AddServiceSummary(ctx, "uptime-tracker", ss)
	if err != nil {
		api.logger.WithError(err).Warn("Failed to add uptime-tracker service summarry to store.")
	}
	api.logger.Info("uptime-tracker liveness check complete.")
}

// checkIPService runs a liveness check on the ip-service https://ip.skycoin.com/
func (api *API) checkIPService(ctx context.Context) {
	online := true
	var errs []string

	cInfo, err := checkCertificate("https://ip.skycoin.com")
	if err != nil {
		online = false
		errs = append(errs, err.Error())
	}

	resp, err := http.Get("https://ip.skycoin.com")
	if err != nil {
		online = false
		errs = append(errs, err.Error())
	}

	if resp != nil {
		defer func() {
			if cErr := resp.Body.Close(); cErr != nil && err == nil {
				err = cErr
			}
		}()
	}

	if resp.StatusCode != http.StatusOK {
		var hErr httputil.HTTPError
		if err = json.NewDecoder(resp.Body).Decode(&hErr); err != nil {
			api.logger.WithError(err).Warn("Failed to decode response from ip.skycoin.com.")
			errs = append(errs, err.Error())
		}
		online = false
		errs = append(errs, hErr.Error())
	}

	ss := &lc.ServiceSummary{
		Online:          online,
		Errors:          errs,
		Timestamp:       time.Now().Unix(),
		CertificateInfo: cInfo,
	}

	err = api.store.AddServiceSummary(ctx, "ip-service", ss)
	if err != nil {
		api.logger.WithError(err).Warn("Failed to add ip-service service summarry to store.")
	}
	api.logger.Info("ip-service liveness check complete.")
}

// checkStunServers runs a liveness check on the the stun servers
func (api *API) checkStunServers(ctx context.Context) {
	online := true
	var errs []string

	for _, stunServer := range api.StunServers {
		nC := stun.NewClient()
		nC.SetServerAddr(stunServer)

		_, _, err := nC.Discover()
		if err != nil {
			api.logger.Warnf("Error %v on server: %v", err, stunServer)
			online = false
			errs = append(errs, err.Error())
		}

		ss := &lc.ServiceSummary{
			Online:    online,
			Errors:    errs,
			Timestamp: time.Now().Unix(),
		}
		serviceName := "stunserver:" + stunServer
		err = api.store.AddServiceSummary(ctx, serviceName, ss)
		if err != nil {
			api.logger.WithError(err).Warn("Failed to add ip-service service summarry to store.")
		}
		api.logger.Infof("%v liveness check complete.", serviceName)
	}
}

func checkCertificate(serviceURL string) (*lc.CertificateInfo, error) {
	u, err := url.Parse(serviceURL)
	if err != nil {
		return nil, err
	}

	conn, err := tls.Dial("tcp", u.Host+":443", nil)
	if err != nil {
		return nil, err
	}

	err = conn.VerifyHostname(u.Host)
	if err != nil {
		return nil, err
	}

	cert := &lc.CertificateInfo{
		Issuer: conn.ConnectionState().PeerCertificates[0].Issuer.String(),
		Expiry: conn.ConnectionState().PeerCertificates[0].NotAfter.Format(time.RFC850),
	}

	return cert, nil
}
