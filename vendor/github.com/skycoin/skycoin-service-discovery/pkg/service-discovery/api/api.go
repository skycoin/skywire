// Package api pkg/service-disocvery/api/api.go
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math/rand"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/sirupsen/logrus"
	"github.com/skycoin/skywire/pkg/servicedisc"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/geo"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/httpauth"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/httputil"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/metricsutil"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/networkmonitor"

	"github.com/skycoin/skycoin-service-discovery/internal/sdmetrics"
	"github.com/skycoin/skycoin-service-discovery/pkg/service-discovery/store"
)

var (
	// ErrInvalidJSON is returned when json sent is invalid.
	ErrInvalidJSON = errors.New("invalid json")
	// ErrPKMismatch is returned on a PK mismatch.
	ErrPKMismatch = errors.New("using public key other than your own is forbidden")
	// ErrVisorVersionIsTooOld returned when visor version is too old
	ErrVisorVersionIsTooOld = errors.New("visor version is too old")
	// ErrMissingType is returned when there is no type in request.
	ErrMissingType = errors.New("missing service type in request")
	// ErrFailedToGetGeoData is returned when we are unable to get the geo data.
	ErrFailedToGetGeoData = errors.New("failed to get visor data")
	// ErrUnauthorizedNetworkMonitor occurs in case of invalid network monitor key
	ErrUnauthorizedNetworkMonitor = errors.New("invalid network monitor key")
	// ErrBadInput occurs in case of bad input
	ErrBadInput = errors.New("error bad input")
)

const (
	httpTimeout = 30 * time.Second
)

// HealthCheckResponse is struct of /health endpoint
type HealthCheckResponse struct {
	BuildInfo   *buildinfo.Info `json:"build_info,omitempty"`
	StartedAt   time.Time       `json:"started_at"`
	DmsgAddr    string          `json:"dmsg_address,omitempty"`
	DmsgServers []string        `json:"dmsg_servers,omitempty"`
}

// WhitelistPKs store whitelisted pks of network monitor
var WhitelistPKs = networkmonitor.GetWhitelistPKs()

// API represents the service-discovery API.
type API struct {
	log                         logrus.FieldLogger
	db                          store.Store
	metrics                     sdmetrics.Metrics
	enableMetrics               bool
	reqsInFlightCountMiddleware *metricsutil.RequestsInFlightCountMiddleware
	nonceDB                     httpauth.NonceStore
	geoFromIP                   geo.LocationDetails
	startedAt                   time.Time
	dmsgAddr                    string
	DmsgServers                 []string
}

// New creates an API.
func New(log logrus.FieldLogger, db store.Store, nonceDB httpauth.NonceStore, apiKey string,
	enableMetrics bool, m sdmetrics.Metrics, dmsgAddr string) *API {
	api := &API{
		log:                         log,
		db:                          db,
		metrics:                     m,
		enableMetrics:               enableMetrics,
		reqsInFlightCountMiddleware: metricsutil.NewRequestsInFlightCountMiddleware(),
		nonceDB:                     nonceDB,
		geoFromIP:                   geo.MakeIPDetails(log, apiKey),
		startedAt:                   time.Now(),
		dmsgAddr:                    dmsgAddr,
		DmsgServers:                 []string{},
	}
	return api
}

// ServeHTTP implements http.Handler
func (a *API) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r := chi.NewRouter()
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	if a.enableMetrics {
		r.Use(a.reqsInFlightCountMiddleware.Handle)
		r.Use(metricsutil.RequestDurationMiddleware)
	}
	r.Use(middleware.Timeout(httpTimeout))

	r.Use(cors.Handler(cors.Options{
		AllowedOrigins: []string{"*"},
		AllowedMethods: []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders: []string{"Accept", "Authorization", "Content-Type", "X-CSRF-Token"},
		ExposedHeaders: []string{"Link"},
		MaxAge:         300,
	}))

	r.Route("/api", func(r chi.Router) {
		r.Get("/services", a.getEntries)
		r.Get("/services/{addr}", a.getEntry)

		r.Group(func(r chi.Router) {
			if a.nonceDB != nil {
				r.Use(httpauth.MakeMiddleware(a.nonceDB))
			}
			r.Post("/services", a.postEntry)
			r.Delete("/services/{addr}", a.delEntry)
		})

		r.Delete("/services/deregister/{type}", a.deregisterEntry)
	})

	r.Get("/health", a.health)

	if a.nonceDB != nil {
		handler := &httpauth.NonceHandler{Store: a.nonceDB}
		r.Get("/security/nonces/{pk}", handler.ServeHTTP)
	}

	r.ServeHTTP(w, req)
}

// RunBackgroundTasks is goroutine which runs in background periodic tasks of skycoin-service-discovery.
func (a *API) RunBackgroundTasks(ctx context.Context, log logrus.FieldLogger) {
	ticker := time.NewTicker(time.Second * 10)
	defer ticker.Stop()
	a.updateInternalState(ctx, log)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.updateInternalState(ctx, log)
		}
	}
}

func (a *API) updateInternalState(ctx context.Context, logger logrus.FieldLogger) {
	serviceTypesCount, err := a.db.CountServiceTypes(ctx)
	if err != nil {
		logger.WithError(err).Errorf("failed to get service types count")
		return
	}
	vpnCount, err := a.db.CountServices(ctx, servicedisc.ServiceTypeVPN)
	if err != nil {
		logger.WithError(err).Errorf("failed to get vpn count")
		return
	}
	visorCount, err := a.db.CountServices(ctx, servicedisc.ServiceTypeVisor)
	if err != nil {
		logger.WithError(err).Errorf("failed to get visor count")
		return
	}
	proxyCount, err := a.db.CountServices(ctx, servicedisc.ServiceTypeSkysocks)
	if err != nil {
		logger.WithError(err).Errorf("failed to get proxy count")
		return
	}

	a.metrics.SetServiceTypesCount(serviceTypesCount)
	a.metrics.SetServicesRegByTypeCount(vpnCount + visorCount + proxyCount)
	a.metrics.SetServiceTypeVPNCount(vpnCount)
	a.metrics.SetServiceTypeVisorCount(visorCount)
	a.metrics.SetServiceTypeSkysocksCount(proxyCount)
}

/*
	<<< PUBLIC ENDPOINTS >>>
*/

func (a *API) getEntries(w http.ResponseWriter, r *http.Request) {
	// TODO(evanlinjin) May be needed in the future for pagination.
	//var query servicedisc.ServicesQuery
	//if err := query.Fill(r.URL.Query()); err != nil {
	//	httputil.WriteJSON(w, r, http.StatusBadRequest, err)
	//	return
	//}

	sType := r.URL.Query().Get("type")
	if sType == "" {
		a.log.Error(ErrMissingType.Error())
		a.writeError(w, r, http.StatusBadRequest, ErrMissingType.Error())
		return
	}

	if sType == servicedisc.ServiceTypeProxy {
		sType = servicedisc.ServiceTypeSkysocks
	}

	version := r.URL.Query().Get("version")
	country := r.URL.Query().Get("country")
	quantity := r.URL.Query().Get("quantity")

	services, sErr := a.db.Services(r.Context(), sType, version, country)
	if sErr != nil {
		a.writeError(w, r, sErr.HTTPStatus, sErr.Err)
		sErr.Log(a.log)
		return
	}

	if quantity != "" {
		quantity, err := strconv.Atoi(quantity)
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest, errors.New("quantity must be integer"))
			return
		}

		if quantity > 0 && len(services) > quantity {
			services = sampleRandom(services, quantity)
		}
	}

	httputil.WriteJSON(w, r, http.StatusOK, services)
}

func (a *API) getEntry(w http.ResponseWriter, r *http.Request) {
	sType := r.URL.Query().Get("type")
	if sType == "" {
		a.log.Error(ErrMissingType.Error())
		a.writeError(w, r, http.StatusBadRequest, ErrMissingType.Error())
		return
	}

	if sType == servicedisc.ServiceTypeProxy {
		sType = servicedisc.ServiceTypeSkysocks
	}

	serviceAddr, err := serviceAddrFromParam(r)
	if err != nil {
		a.log.Error(err)
		a.writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}

	service, sErr := a.db.Service(r.Context(), sType, serviceAddr)
	if sErr != nil {
		sErr.Log(a.log)
		a.writeError(w, r, sErr.HTTPStatus, sErr.Err)
		return
	}
	httputil.WriteJSON(w, r, http.StatusOK, service)
}

/*
	<<< AUTH-PROTECTED ENDPOINTS >>>
*/

func (a *API) postEntry(w http.ResponseWriter, r *http.Request) {
	var se servicedisc.Service
	var err error

	if err := httputil.ReadJSON(r, &se); err != nil {
		a.log.WithError(err).Error("Failed to parse json")
		a.writeError(w, r, http.StatusBadRequest, ErrInvalidJSON.Error())
		return
	}

	host := httpauth.GetRemoteAddr(r)

	if se.Geo == nil {
		se.Geo, err = a.geoFromIP(net.ParseIP(host))
		if err == geo.ErrIPIsNotPublic && se.Type != servicedisc.ServiceTypeVisor {
			a.log.WithField("ip", host).Infof("Unable to get geo data of a non-public IP")
		} else if err != nil {
			a.log.WithError(ErrFailedToGetGeoData).Errorf("Failed to get geo data for host %q", host)
			a.writeError(w, r, http.StatusInternalServerError, ErrFailedToGetGeoData.Error())
			return
		}
	}
	if a.nonceDB != nil {
		if pk := httpauth.PKFromCtx(r.Context()); pk != se.Addr.PubKey() {
			a.log.WithError(ErrPKMismatch).Error("Failed to get pk from request")
			a.writeError(w, r, http.StatusForbidden, ErrPKMismatch.Error())
			return
		}
	}

	if se.Type == servicedisc.ServiceTypeVPN || se.Type == servicedisc.ServiceTypeSkysocks {
		if se.Version == "" {
			a.log.Error(ErrVisorVersionIsTooOld.Error())
			a.writeError(w, r, http.StatusForbidden, ErrVisorVersionIsTooOld.Error())
			return
		}
	}

	if se.Type == servicedisc.ServiceTypeVisor {
		if !ipIsPublic(se, host) {
			a.log.Error(servicedisc.ErrVisorUnreachable.Error())
			a.writeError(w, r, http.StatusForbidden, servicedisc.ErrVisorUnreachable.Error())
			return
		}
	}

	if sErr := a.db.UpdateService(r.Context(), &se); sErr != nil {
		sErr.Log(a.log)
		a.writeError(w, r, sErr.HTTPStatus, sErr.Err)
		return
	}

	httputil.WriteJSON(w, r, http.StatusOK, &se)
}

func (a *API) delEntry(w http.ResponseWriter, r *http.Request) {
	sType := r.URL.Query().Get("type")
	if sType == "" {
		a.log.Error(ErrMissingType)
		a.writeError(w, r, http.StatusBadRequest, ErrMissingType.Error())
		return
	}

	serviceAddr, err := serviceAddrFromParam(r)
	if err != nil {
		a.log.Error(err)
		a.writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}

	if a.nonceDB != nil {
		if pk := httpauth.PKFromCtx(r.Context()); pk != serviceAddr.PubKey() {
			a.log.WithError(ErrPKMismatch).Error("service address does not have public key of auth header")
			a.writeError(w, r, http.StatusForbidden, ErrPKMismatch.Error())
			return
		}
	}

	if sErr := a.db.DeleteService(r.Context(), sType, serviceAddr); sErr != nil {
		sErr.Log(a.log)
		a.writeError(w, r, sErr.HTTPStatus, sErr.Err)
		return
	}
	httputil.WriteJSON(w, r, http.StatusOK, true)
}

func (a *API) deregisterEntry(w http.ResponseWriter, r *http.Request) {
	a.log.Info("Deregistration process started.")
	// check validation of request from network monitor
	nmPkString := r.Header.Get("NM-PK")
	if ok := WhitelistPKs.Get(nmPkString); !ok {
		a.log.WithError(ErrUnauthorizedNetworkMonitor).WithField("Step", "Checking NMs PK").Error("Deregistration process interrupt.")
		w.WriteHeader(http.StatusForbidden)
		return
	}

	nmPk := cipher.PubKey{}
	if err := nmPk.UnmarshalText([]byte(nmPkString)); err != nil {
		a.log.WithError(ErrBadInput).WithField("Step", "Reading NMs PK").Error("Deregistration process interrupt.")
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	nmSign := cipher.Sig{}
	if err := nmSign.UnmarshalText([]byte(r.Header.Get("NM-Sign"))); err != nil {
		a.log.WithError(ErrBadInput).WithField("Step", "Checking sign").Error("Deregistration process interrupt.")
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	if err := cipher.VerifyPubKeySignedPayload(nmPk, nmSign, []byte(nmPk.Hex())); err != nil {
		a.log.WithError(ErrUnauthorizedNetworkMonitor).WithField("Step", "Veryfing request").Error("Deregistration process interrupt.")
		w.WriteHeader(http.StatusForbidden)
		return
	}

	// read keys
	keys := []cipher.PubKey{}
	keysBody, err := io.ReadAll(r.Body)
	if err != nil {
		a.log.WithError(ErrBadInput).WithField("Step", "Reading keys").Error("Deregistration process interrupt.")
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	var keysSlice []string
	if err := json.Unmarshal(keysBody, &keysSlice); err != nil {
		a.log.WithError(ErrBadInput).WithField("Step", "Slicing keys").Error("Deregistration process interrupt.")
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	for _, key := range keysSlice {
		tempKey := cipher.PubKey{}
		if err := tempKey.UnmarshalText([]byte(key)); err != nil {
			a.log.WithError(ErrBadInput).WithField("Step", "Checking keys").Error("Deregistration process interrupt.")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		keys = append(keys, tempKey)
	}

	// check sType
	sType := chi.URLParam(r, "type")
	if sType == "" {
		a.log.WithError(ErrMissingType).WithField("Step", "Checking type").Error("Deregistration process interrupt.")
		a.writeError(w, r, http.StatusBadRequest, ErrMissingType.Error())
		return
	}

	// delete dead services
	for _, key := range keys {
		serviceAddr := servicedisc.NewSWAddr(key, 0)
		if sErr := a.db.DeleteService(r.Context(), sType, serviceAddr); sErr != nil {
			a.log.WithFields(logrus.Fields{"PK": key.Hex(), "Step": "Delete Service"}).Error("Deregistration process interrupt.")
			sErr.Log(a.log)
			a.writeError(w, r, sErr.HTTPStatus, sErr.Err)
			return
		}
	}
	a.log.WithFields(logrus.Fields{"Number of Keys": len(keys), "Keys": keys, "Type": sType}).Info("Deregistration process completed.")
	a.writeJSON(w, r, http.StatusOK, true)
}

func (a *API) writeError(w http.ResponseWriter, r *http.Request, status int, err string) {
	tes := &servicedisc.HTTPError{
		HTTPStatus: status,
		Err:        err,
	}
	httputil.WriteJSON(w, r, status, tes)
}

/*
	<<< HEALTH ENDPOINT >>>
*/

func (a *API) health(w http.ResponseWriter, r *http.Request) {
	info := buildinfo.Get()
	a.writeJSON(w, r, http.StatusOK, HealthCheckResponse{
		BuildInfo:   info,
		StartedAt:   a.startedAt,
		DmsgAddr:    a.dmsgAddr,
		DmsgServers: a.DmsgServers,
	})
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

func (a *API) logger(r *http.Request) logrus.FieldLogger {
	return httputil.GetLogger(r)
}

/*
	<<< HELPERS >>>
*/

func ipIsPublic(se servicedisc.Service, remoteIP string) bool {
	for _, localIP := range se.LocalIPs {
		if localIP == remoteIP {
			return true
		}
	}

	return false
}

func serviceAddrFromParam(r *http.Request) (servicedisc.SWAddr, error) {
	const key = "addr"
	var addr servicedisc.SWAddr
	err := addr.UnmarshalText([]byte(chi.URLParam(r, key)))
	return addr, err
}

func sampleRandom(services []servicedisc.Service, n int) []servicedisc.Service {
	result := make([]servicedisc.Service, len(services))
	copy(result, services)
	rand.Shuffle(len(result), func(i, j int) {
		result[i], result[j] = result[j], result[i]
	})
	return result[:n]
}
