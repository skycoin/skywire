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

	"github.com/skycoin/skywire/pkg/network-monitor/store"
	nm "github.com/skycoin/skywire/pkg/network-monitor/types"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/httputil"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport"
)

// API register all the API endpoints.
// It implements a net/http.Handler.
type API struct {
	http.Handler

	servicesURLs ServicesURLs
	logger       logging.Logger
	store        store.Store
	httpClient   *http.Client
	config       NetworkMonitorConfig

	mu            sync.RWMutex
	startedAt     time.Time
	utData        map[string]bool
	liveEntries   map[string]int
	deadEntries   map[string][]string
	pendingDeaths map[string]map[string]bool
}

// NetworkMonitorConfig contains configuration for network monitoring.
type NetworkMonitorConfig struct {
	CleaningDelay int
	PublicKey     cipher.PubKey
	SecretKey     cipher.SecKey
	Signature     cipher.Sig
}

// ServicesURLs is struct for organize URL of services
type ServicesURLs struct {
	TPD   string
	DMSGD string
	SD    string
	AR    string
	UT    string
}

const (
	httpTimeout = 10 * time.Second
)

// HealthCheckResponse is struct of /health endpoint
type HealthCheckResponse struct {
	ServiceName string          `json:"service_name,omitempty"`
	BuildInfo   *buildinfo.Info `json:"build_info,omitempty"`
	StartedAt   time.Time       `json:"started_at,omitempty"`
}

// Error is the object returned to the client when there's an error.
type Error struct {
	Error string `json:"error"`
}

var mainServices = [4]string{"tpd", "dmsgd", "ar", "sd"}
var sdSubServices = [3]string{"vpn", "visor", "skysocks"}
var arSubServices = [2]string{"sudph", "stcpr"}

// New returns a new *chi.Mux object, which can be started as a server
func New(s store.Store, logger *logging.Logger, urls ServicesURLs, config NetworkMonitorConfig) *API {

	api := &API{
		servicesURLs: urls,
		logger:       *logger,
		store:        s,
		httpClient: &http.Client{
			Timeout: httpTimeout,
		},
		startedAt:     time.Now(),
		config:        config,
		pendingDeaths: make(map[string]map[string]bool),
		deadEntries:   make(map[string][]string),
		liveEntries:   make(map[string]int),
	}
	r := chi.NewRouter()
	r.Use(
		middleware.RequestID,
		middleware.RealIP,
		middleware.Logger,
		middleware.Recoverer,
		httputil.SetLoggerMiddleware(logger),
	)
	r.Get("/status", api.getStatus)
	r.Get("/health", api.health)
	api.Handler = r

	return api
}

func (api *API) getStatus(w http.ResponseWriter, r *http.Request) {
	data, err := api.store.GetNetworkStatus()
	if err != nil {
		api.logger.WithError(err).Warnf("error getting network status")
	}
	if err := json.NewEncoder(w).Encode(data); err != nil {
		api.writeError(w, r, err)
	}
}

func (api *API) health(w http.ResponseWriter, r *http.Request) {
	info := buildinfo.Get()
	api.writeJSON(w, r, http.StatusOK, HealthCheckResponse{
		ServiceName: "network-monitor",
		BuildInfo:   info,
		StartedAt:   api.startedAt,
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

// InitCleaningLoop is function which runs periodic background tasks of API.
func (api *API) InitCleaningLoop(ctx context.Context) {
	api.initCleaning()
	for {
		select {
		case <-ctx.Done():
			return
		default:
			if err := api.cleanNetwork(ctx); err != nil {
				api.logger.WithError(err).Warn("Cleaning cycle failed")
			}
			if err := api.updateNetworkStatus(); err != nil {
				api.logger.WithError(err).Warn("Failed to update network status")
			}
			time.Sleep(30 * time.Second)
		}
	}
}

func (api *API) updateNetworkStatus() error {
	var status = nm.Status{LastUpdate: time.Now().UTC(), LastCleaning: &nm.LastCleaningSummary{}}
	// alive info
	status.Transports = api.liveEntries["tpd"]
	status.VPN = api.liveEntries["vpn"]
	status.PublicVisor = api.liveEntries["visor"]
	status.Skysocks = api.liveEntries["skysocks"]
	// last cleaning info
	status.LastCleaning.Dmsgd = len(api.deadEntries["dmsgd"])
	status.LastCleaning.Tpd = len(api.deadEntries["tpd"])
	status.LastCleaning.Ar.SUDPH = len(api.deadEntries["sudph"])
	status.LastCleaning.Ar.STCPR = len(api.deadEntries["stcpr"])
	status.LastCleaning.VPN = len(api.deadEntries["vpn"])
	status.LastCleaning.PublicVisor = len(api.deadEntries["visor"])
	status.LastCleaning.Skysocks = len(api.deadEntries["skysocks"])
	for _, service := range api.deadEntries {
		status.LastCleaning.AllDeadEntriesCleaned += len(service)
	}
	status.OnlineVisors = len(api.utData)
	return api.store.SetNetworkStatus(status)
}

func (api *API) initCleaning() {
	for _, service := range mainServices {
		if service != "ar" && service != "sd" {
			api.pendingDeaths[service] = make(map[string]bool)
		}
	}
	for _, subService := range sdSubServices {
		api.pendingDeaths[subService] = make(map[string]bool)
	}
	for _, subService := range arSubServices {
		api.pendingDeaths[subService] = make(map[string]bool)
	}
}

// cleaning use as routine to cleaning old/dead entries in the network
func (api *API) cleanNetwork(ctx context.Context) error {
	api.logger.Info("Cleanup started.")
	defer api.mu.Unlock()
	api.mu.Lock()

	// fetch uptime tracker in each itterate
	if err := api.fetchUTData(ctx); err != nil {
		api.logger.WithError(err).Warn("unable to fetch UT data")
		return err
	}
	// cleaning main services
	for _, service := range mainServices {
		api.clean(ctx, service)
		time.Sleep(time.Second * time.Duration(api.config.CleaningDelay))
	}
	api.logger.Info("Cleanup process terminated.")
	return nil
}

func (api *API) clean(ctx context.Context, service string) {
	api.logger.Infof("Service %s cleanup process start.", service)
	switch service {
	case "tpd":
		api.deadEntries[service] = []string{}
		if err := api.tpdCleaning(ctx); err != nil {
			api.logger.WithError(err).Warnf("%s cleaning interrupted.", service)
		}
	case "dmsgd":
		api.deadEntries[service] = []string{}
		if err := api.cleaningService(ctx, service, ""); err != nil {
			api.logger.WithError(err).Warnf("%s cleaning interrupted.", service)
		}
	case "ar":
		for _, sub := range arSubServices {
			api.deadEntries[sub] = []string{}
			if err := api.cleaningService(ctx, service, sub); err != nil {
				api.logger.WithError(err).Warnf("%s cleaning interrupted.", service)
			}
		}
	case "sd":
		for _, sub := range sdSubServices {
			api.deadEntries[sub] = []string{}
			if err := api.cleaningService(ctx, service, sub); err != nil {
				api.logger.WithError(err).Warnf("%s cleaning interrupted.", service)
			}
		}
	}
	api.logger.Infof("Service %s cleanup process done.", service)
}

func (api *API) cleaningService(ctx context.Context, service, sType string) error {
	var data []string
	var err error
	var target string
	switch service {
	case "dmsgd":
		data, err = api.fetchDmsgdData(ctx)
		target = service
	case "ar":
		data, err = api.fetchArData(ctx, sType)
		target = sType
	default:
		data, err = api.fetchSdData(ctx, sType)
		target = sType
	}
	if err != nil {
		api.logger.WithError(err).Warnf("unable to fetch data from %s", service)
		return err
	}

	if err := api.checkingEntries(ctx, data, service, sType); err != nil {
		api.logger.WithError(err).Errorf("unable to checking data from %s", service)
		return err
	}
	if len(api.deadEntries[target]) > 0 {
		if err := api.deregister(api.deadEntries[target], service, sType); err != nil {
			api.logger.WithError(err).Errorf("unable to deregister dead entries from %s", service)
		}
	}
	// logs
	if err := api.cleaningInfo(ctx, service, sType); err != nil {
		api.logger.WithError(err).Warn("unable to show cleaning info")
	}
	return err

}

func (api *API) fetchSdData(ctx context.Context, sType string) ([]string, error) {
	var data []string
	select {
	case <-ctx.Done():
		return data, context.DeadlineExceeded
	default:
		var sdData []struct {
			Address string `json:"address"`
		}
		res, err := api.httpClient.Get(fmt.Sprintf("%s/api/services?type=%s", api.servicesURLs.SD, sType))
		if err != nil {
			api.logger.WithError(err).Errorf("unable to fetch data from sd")
			return data, err
		}
		body, err := io.ReadAll(res.Body)
		if err != nil {
			api.logger.WithError(err).Errorf("unable to read data from sd")
			return data, err
		}
		defer func() {
			if err := res.Body.Close(); err != nil {
				api.logger.WithError(err).Warnf("error on close resp.Body")
			}
		}()
		err = json.Unmarshal(body, &sdData)
		if err != nil {
			api.logger.WithError(err).Errorf("unable to unmarshal data from sd")
			return data, err
		}
		// check entries in vpn that are available in ut or not
		for _, entry := range sdData {
			data = append(data, strings.Split(entry.Address, ":")[0])
		}
		return data, nil
	}
}
func (api *API) fetchArData(ctx context.Context, sType string) ([]string, error) {
	var data []string
	select {
	case <-ctx.Done():
		return data, context.DeadlineExceeded
	default:
		var arEntries visorTransports
		res, err := api.httpClient.Get(fmt.Sprintf("%s/transports", api.servicesURLs.AR))
		if err != nil {
			return data, err
		}
		body, err := io.ReadAll(res.Body)
		if err != nil {
			return data, err
		}
		defer func() {
			if err := res.Body.Close(); err != nil {
				api.logger.WithError(err).Warnf("error on close resp.Body")
			}
		}()
		err = json.Unmarshal(body, &arEntries)
		if err != nil {
			return data, err
		}
		data = arEntries.Stcpr
		if sType == "sudph" {
			data = arEntries.Sudph
		}
		return data, nil
	}
}
func (api *API) fetchDmsgdData(ctx context.Context) ([]string, error) {
	var data []string
	select {
	case <-ctx.Done():
		return data, context.DeadlineExceeded
	default:
		res, err := api.httpClient.Get(fmt.Sprintf("%s/dmsg-discovery/visorEntries", api.servicesURLs.DMSGD))
		if err != nil {
			api.logger.WithError(err).Errorf("unable to fetch data from dmsgd")
			return data, err
		}
		body, err := io.ReadAll(res.Body)
		if err != nil {
			api.logger.WithError(err).Errorf("unable to read data from dmsgd")
			return data, err
		}
		defer func() {
			if err := res.Body.Close(); err != nil {
				api.logger.WithError(err).Warnf("error on close resp.Body")
			}
		}()
		err = json.Unmarshal(body, &data)
		if err != nil {
			api.logger.WithError(err).Errorf("unable to unmarshal data from dmsgd")
			return data, err
		}
		return data, nil
	}
}

func (api *API) checkingEntries(ctx context.Context, data []string, service, sType string) error {
	target := service
	if sType != "" {
		target = sType
	}
	select {
	case <-ctx.Done():
		return context.DeadlineExceeded
	default:
		newPendingDeaths := make(map[string]bool)
		for _, entry := range data {
			_, online := api.utData[entry]
			if !online {
				if _, ok := api.pendingDeaths[target][entry]; ok {
					api.deadEntries[target] = append(api.deadEntries[target], entry)
					delete(api.pendingDeaths[target], entry)
					continue
				}
				newPendingDeaths[entry] = true
			}
		}
		api.pendingDeaths[target] = newPendingDeaths
		api.liveEntries[target] = len(data) - (len(api.pendingDeaths[target]) + len(api.deadEntries[target]))
		return nil
	}
}

func (api *API) cleaningInfo(ctx context.Context, service, sType string) error {
	if sType != "" {
		service = sType
	}
	select {
	case <-ctx.Done():
		return context.DeadlineExceeded
	default:
		api.logger.WithFields(logrus.Fields{
			"alive":   api.liveEntries[service],
			"pending": len(api.pendingDeaths[service]),
			"dead":    len(api.deadEntries[service]),
		}).Infof("Service %s cleanup stats:", service)
		return nil
	}
}

// tpdCleaning is a routine to clean dead entries in transport discovery
func (api *API) tpdCleaning(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return context.DeadlineExceeded
	default:
		var tpdData []*transport.Entry
		res, err := api.httpClient.Get(fmt.Sprintf("%s/all-transports", api.servicesURLs.TPD))
		if err != nil {
			api.logger.WithError(err).Errorf("unable to fetch data from tpd")
			return err
		}
		body, err := io.ReadAll(res.Body)
		if err != nil {
			api.logger.WithError(err).Errorf("unable to read data from tpd")
			return err
		}
		defer func() {
			if err := res.Body.Close(); err != nil {
				api.logger.WithError(err).Warnf("error on close resp.Body")
			}
		}()
		err = json.Unmarshal(body, &tpdData)
		if err != nil {
			api.logger.WithError(err).Errorf("unable to unmarshal data from tpd")
			return err
		}
		newPendingDeaths := make(map[string]bool)
		// check entries in tpd that are available in UT or not, based on both edges
		for _, tp := range tpdData {
			// check edge[0]
			_, online1 := api.utData[tp.Edges[0].Hex()]
			_, online2 := api.utData[tp.Edges[1].Hex()]
			if !online1 || !online2 {
				if _, ok := api.pendingDeaths["tpd"][tp.ID.String()]; ok {
					api.deadEntries["tpd"] = append(api.deadEntries["tpd"], tp.ID.String())
					delete(api.pendingDeaths["tpd"], tp.ID.String())
					continue
				}
				newPendingDeaths[tp.ID.String()] = true
			}
		}
		api.pendingDeaths["tpd"] = newPendingDeaths
		// deregister entries from tpd
		if len(api.deadEntries["tpd"]) > 0 {
			if err := api.deregister(api.deadEntries["tpd"], "tpd", ""); err != nil {
				api.logger.WithError(err).Errorf("unable to deregister dead entries from %s", "tpd")
			}
		}

		api.liveEntries["tpd"] = len(tpdData) - (len(api.deadEntries["tpd"]) + len(api.pendingDeaths["tpd"]))
		logInfo := make(logrus.Fields)
		logInfo["alive"] = api.liveEntries["tpd"]
		logInfo["pending"] = len(api.pendingDeaths["tpd"])
		logInfo["dead"] = len(api.deadEntries["tpd"])
		api.logger.WithFields(logInfo).Info("Service tpd cleanup stats:")
		return nil
	}
}

func (api *API) deregister(entries []string, service, sType string) error {
	var err error
	switch service {
	case "tpd":
		err = api.deregisterRequest(entries, fmt.Sprintf("%s/transports/deregister", api.servicesURLs.TPD), service)
	case "dmsgd":
		err = api.deregisterRequest(entries, fmt.Sprintf("%s/dmsg-discovery/deregister", api.servicesURLs.DMSGD), service)
	case "ar":
		err = api.deregisterRequest(entries, fmt.Sprintf("%s/deregister/%s", api.servicesURLs.AR, sType), fmt.Sprintf("%s [%s]", service, sType))
	case "sd":
		err = api.deregisterRequest(entries, fmt.Sprintf("%s/api/services/deregister/%s", api.servicesURLs.SD, sType), fmt.Sprintf("%s [%s]", service, sType))
	}
	if err != nil {
		return err
	}
	if sType != "" {
		api.logger.Infof("deregister request send to %s for %s entries", service, sType)
		return nil
	}
	api.logger.Infof("deregister request send to %s", service)
	return nil
}

// deregisterRequest is dereigstration handler for all services
func (api *API) deregisterRequest(keys []string, rawReqURL, service string) error {
	reqURL, err := url.Parse(rawReqURL)
	if err != nil {
		return fmt.Errorf("error on parsing deregistration URL : %v", err)
	}

	jsonData, err := json.Marshal(keys)
	if err != nil {
		return fmt.Errorf("error on marshaling deregistration keys : %v", err)
	}
	body := bytes.NewReader(jsonData)

	req := &http.Request{
		Method: "DELETE",
		URL:    reqURL,
		Header: map[string][]string{
			"NM-PK":   {api.config.PublicKey.Hex()},
			"NM-Sign": {api.config.Signature.Hex()},
		},
		Body: io.NopCloser(body),
	}

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("error on send deregistration request : %s", err)
	}
	defer func() {
		if err := res.Body.Close(); err != nil {
			api.logger.WithError(err).Warnf("error on close resp.Body")
		}
	}()

	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("error on deregister keys from %s : %s", service, err)
	}

	return nil
}

type visorTransports struct {
	Sudph []string `json:"sudph"`
	Stcpr []string `json:"stcpr"`
}

func (api *API) fetchUTData(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return context.DeadlineExceeded
	default:
		response := make(map[string]bool)
		res, err := api.httpClient.Get(fmt.Sprintf("%s/uptimes?status=on", api.servicesURLs.UT))
		if err != nil {
			return err
		}
		body, err := io.ReadAll(res.Body)
		if err != nil {
			return err
		}
		defer func() {
			if err := res.Body.Close(); err != nil {
				api.logger.WithError(err).Warnf("error on close resp.Body")
			}
		}()
		var data []uptimes
		err = json.Unmarshal(body, &data)
		if err != nil {
			return err
		}

		for _, visor := range data {
			response[visor.Key] = visor.Online
		}
		if len(response) == 0 {
			return fmt.Errorf("empty ut data fetched")
		}

		api.utData = response
		return nil
	}
}

type uptimes struct {
	Key    string `json:"key"`
	Online bool   `json:"online"`
}
