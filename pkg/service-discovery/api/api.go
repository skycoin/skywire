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
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/geo"
	"github.com/skycoin/skywire/pkg/httpauth"
	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/metricsutil"
	"github.com/skycoin/skywire/pkg/networkmonitor"
	sdmetrics "github.com/skycoin/skywire/pkg/service-discovery/metrics"
	"github.com/skycoin/skywire/pkg/service-discovery/store"
	"github.com/skycoin/skywire/pkg/servicedisc"
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
	httpTimeout       = 30 * time.Second
	uptimesCacheDelay = 5 * time.Minute
)

// HealthCheckResponse is struct of /health endpoint
type HealthCheckResponse struct {
	ServiceName string          `json:"service_name,omitempty"`
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

	// uptimes caches are refreshed every uptimesCacheDelay.
	uptimesCache   []store.VisorSummary
	uptimesV2Cache []store.VisorSummary
	uptimesMu      sync.RWMutex

	// dhtMirror mirrors the list of services registered by each visor PK
	// to the DHT under SHA256(pk || "svc"). The value is the full list so
	// visors with both vpn and skysocks don't overwrite each other's
	// entry. Delete drops the target when a visor has no services left.
	dhtMirror interface {
		Mirror(subjectPK cipher.PubKey, entry interface{}, seq uint64)
		Delete(subjectPK cipher.PubKey)
	}

	// cxoPublisher mirrors register / deregister events into a CXO
	// TreeStore feed. Started automatically whenever SD runs with a
	// configured SecKey + dmsg-capable mode. Nil if the publisher
	// failed to bind or DMSG isn't enabled; the calling sites
	// null-check via pub == nil so the HTTP path always works
	// regardless.
	cxoPublisher *ServicesCXOPublisher
}

// SetDHTMirror sets a mirror that publishes per-visor service lists to
// the DHT on every registration and clears them on full deregistration.
func (a *API) SetDHTMirror(m interface {
	Mirror(subjectPK cipher.PubKey, entry interface{}, seq uint64)
	Delete(subjectPK cipher.PubKey)
}) {
	a.dhtMirror = m
}

// SetServicesCXOPublisher installs the CXO publisher that mirrors
// register / deregister events into a TreeStore feed. Pass nil to
// disable. Wired by cmd/svc/service-discovery/commands/root.go after
// the publisher is built; no-op until then.
func (a *API) SetServicesCXOPublisher(p *ServicesCXOPublisher) {
	a.cxoPublisher = p
}

// WarmCXOFromStore pre-populates the CXO publisher's tree from the
// current SD store. Without this, the publisher only Puts on
// register/heartbeat events — so right after an SD restart the
// publisher's tree is empty until the first event lands, and any
// subscriber that connects in the gap times out at firstSyncTimeout
// (10s) with "timeout waiting for Root".
//
// Best-effort: per-type listing failures are logged and skipped, but
// the returned count is the actual number of entries pushed into the
// publisher's queue. Caller should run this once after
// SetServicesCXOPublisher and before declaring SD ready to serve.
func (a *API) WarmCXOFromStore(ctx context.Context) int {
	if a == nil || a.cxoPublisher == nil {
		return 0
	}
	types := []string{
		servicedisc.ServiceTypeVisor,
		servicedisc.ServiceTypeVPN,
		servicedisc.ServiceTypeProxy,
		servicedisc.ServiceTypeSkysocks,
	}
	var n int
	for _, sType := range types {
		services, herr := a.db.Services(ctx, sType, "", "")
		if herr != nil {
			a.log.WithField("type", sType).WithError(herr).Warn("CXO warm: failed to list services from store")
			continue
		}
		for i := range services {
			a.cxoPublisher.PutEntry(&services[i])
			n++
		}
	}
	if n > 0 {
		a.log.WithField("entries", n).Info("CXO warm: pre-populated publisher tree from store")
	}
	return n
}

// mirrorVisorServices re-publishes the current full list of services
// for a visor PK. Called after any service registration or deletion
// so the DHT target holds the same set as HTTP discovery. An empty
// list triggers Delete so the DHT doesn't hold a stale record past
// the visor's last active service.
func (a *API) mirrorVisorServices(ctx context.Context, pk cipher.PubKey) {
	if a.dhtMirror == nil {
		return
	}
	services, herr := a.db.ServicesByPK(ctx, pk)
	if herr != nil || len(services) == 0 {
		a.dhtMirror.Delete(pk)
		return
	}
	a.dhtMirror.Mirror(pk, services, uint64(time.Now().UnixNano())) //nolint:gosec
}

// BackfillDHTMirror mirrors the full per-visor service list for every
// visor that currently has at least one service registered. Producing
// one DHT item per visor (value = all that visor's services) matches
// HTTP discovery and replaces the pre-#2333 per-service mirror which
// overwrote the same target for visors offering multiple service types.
func (a *API) BackfillDHTMirror(ctx context.Context, log logrus.FieldLogger) {
	if a.dhtMirror == nil {
		return
	}
	byPK := make(map[cipher.PubKey][]servicedisc.Service)
	var total int
	for _, sType := range []string{"vpn", "visor", "skysocks"} {
		services, sErr := a.db.Services(ctx, sType, "", "")
		if sErr != nil {
			log.WithError(sErr).WithField("type", sType).Warn("DHT backfill: failed to list services")
			continue
		}
		total += len(services)
		for i := range services {
			pk := services[i].Addr.PubKey()
			byPK[pk] = append(byPK[pk], services[i])
		}
	}
	mirrored := 0
	for pk, list := range byPK {
		if ctx.Err() != nil {
			break
		}
		a.dhtMirror.Mirror(pk, list, uint64(time.Now().UnixNano())) //nolint:gosec
		mirrored++
	}
	log.WithField("visors", mirrored).WithField("services", total).Info("DHT backfill complete")
}

// New creates an API.
func New(log logrus.FieldLogger, db store.Store, nonceDB httpauth.NonceStore,
	enableMetrics bool, m sdmetrics.Metrics, dmsgAddr, geoipURL string) *API {
	api := &API{
		log:                         log,
		db:                          db,
		metrics:                     m,
		enableMetrics:               enableMetrics,
		reqsInFlightCountMiddleware: metricsutil.NewRequestsInFlightCountMiddleware(),
		nonceDB:                     nonceDB,
		geoFromIP:                   geo.MakeIPDetails(log, geoipURL),
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

	r.Get("/uptimes", a.getUptimes)
	r.Post("/uptimes", a.postUptimes)
	r.Get("/health", a.health)

	if a.nonceDB != nil {
		handler := &httpauth.NonceHandler{Store: a.nonceDB}
		r.Get("/security/nonces/{pk}", handler.ServeHTTP)
	}

	r.ServeHTTP(w, req)
}

// RunBackgroundTasks is goroutine which runs in background periodic tasks of skycoin-service-discovery.
func (a *API) RunBackgroundTasks(ctx context.Context, log logrus.FieldLogger) {
	stateTicker := time.NewTicker(time.Second * 10)
	defer stateTicker.Stop()

	uptimesTicker := time.NewTicker(uptimesCacheDelay)
	defer uptimesTicker.Stop()

	a.updateInternalState(ctx, log)
	a.refreshUptimesCache(ctx, log)

	for {
		select {
		case <-ctx.Done():
			return
		case <-stateTicker.C:
			a.updateInternalState(ctx, log)
		case <-uptimesTicker.C:
			a.refreshUptimesCache(ctx, log)
		}
	}
}

// refreshUptimesCache fetches visor summaries and caches v1 and v2 formats.
func (a *API) refreshUptimesCache(ctx context.Context, logger logrus.FieldLogger) {
	uptimes, err := a.db.GetAllVisorSummaries(ctx, false, false)
	if err != nil {
		logger.WithError(err).Error("failed to refresh uptimes cache")
		return
	}
	uptimesV2, err := a.db.GetAllVisorSummaries(ctx, true, false)
	if err != nil {
		logger.WithError(err).Error("failed to refresh uptimes v2 cache")
		uptimesV2 = uptimes
	}
	a.uptimesMu.Lock()
	a.uptimesCache = uptimes
	a.uptimesV2Cache = uptimesV2
	a.uptimesMu.Unlock()
}

func (a *API) getUptimesFromCache() []store.VisorSummary {
	a.uptimesMu.RLock()
	defer a.uptimesMu.RUnlock()
	return a.uptimesCache
}

func (a *API) getUptimesV2FromCache() []store.VisorSummary {
	a.uptimesMu.RLock()
	defer a.uptimesMu.RUnlock()
	return a.uptimesV2Cache
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

	host := httpauth.GetRemoteAddrLB(r)

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
		// The previous dmsg-port-136 probe gate has been removed: it
		// rejected registrations whenever the noise handshake didn't
		// complete inside the 15s budget, which made every transient
		// flake (SD's own dmsg sessions warming up after restart, a
		// slow delegated server, a visor briefly between sessions)
		// look like an unreachable visor. Combined with the visor's
		// retrier whitelist on ErrVisorUnreachable, that turned a
		// momentary probe failure into "this visor never appears in
		// SD again until it restarts." The IP-public check above
		// already enforces the WAN-reachability invariant SD cares
		// about; route-setup failures are caught by the route
		// finder, where they belong.
	}

	// Combined service update + heartbeat in a single Redis pipeline.
	// Halves Redis round-trips (11 commands in 1 pipeline instead of 2).
	if sErr := a.db.UpdateServiceAndHeartbeat(r.Context(), &se, se.Version); sErr != nil {
		sErr.Log(a.log)
		a.writeError(w, r, sErr.HTTPStatus, sErr.Err)
		return
	}

	// Mirror the visor's FULL service list (not just this new entry) so
	// the DHT target holds the same set as HTTP discovery for this PK.
	a.mirrorVisorServices(r.Context(), se.Addr.PubKey())
	// CXO mirror — best-effort, no-op if the publisher failed to
	// start (no DMSG).
	a.cxoPublisher.PutEntry(&se)

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
	a.mirrorVisorServices(r.Context(), serviceAddr.PubKey())
	a.cxoPublisher.DelEntry(sType, serviceAddr.PubKey())
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
		a.mirrorVisorServices(r.Context(), key)
		a.cxoPublisher.DelEntry(sType, key)
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
	<<< UPTIMES ENDPOINT >>>
*/

// GET /uptimes
// Query params:
//
//	v=v2  — extended format with version and daily uptime percentages
//	v=v3  — v2 + per-day timeline bitmaps (288 chars per day, '.'=up ' '=down)
//	visors — semicolon-separated PK hex list to filter (required for v3, optional for v1/v2)
func (a *API) getUptimes(w http.ResponseWriter, r *http.Request) {
	version := r.URL.Query().Get("v")
	visorsParam := r.URL.Query().Get("visors")

	// v3 is computed on-demand (timeline bitmaps are expensive for all visors).
	if version == "v3" {
		a.getUptimesV3(w, r, visorsParam)
		return
	}

	if version == "v2" {
		uptimes := a.getUptimesV2FromCache()
		if uptimes == nil {
			uptimes = []store.VisorSummary{}
		}
		if visorsParam != "" {
			uptimes = filterByPKs(uptimes, visorsParam)
		}
		httputil.WriteJSON(w, r, http.StatusOK, uptimes)
		return
	}

	uptimes := a.getUptimesFromCache()
	if uptimes == nil {
		uptimes = []store.VisorSummary{}
	}
	if visorsParam != "" {
		uptimes = filterByPKs(uptimes, visorsParam)
	}
	httputil.WriteJSON(w, r, http.StatusOK, uptimes)
}

// getUptimesV3 computes v3 responses on-demand for specific PKs.
func (a *API) getUptimesV3(w http.ResponseWriter, r *http.Request, visorsParam string) {
	all := a.getUptimesV2FromCache()
	if all == nil {
		all = []store.VisorSummary{}
	}

	filtered := all
	if visorsParam != "" {
		filtered = filterByPKs(all, visorsParam)
	}

	now := time.Now().UTC()
	for i := range filtered {
		pkHex := filtered[i].PK.Hex()
		filtered[i].Timeline = a.db.GetDailyTimeline(r.Context(), pkHex, now)
	}

	httputil.WriteJSON(w, r, http.StatusOK, filtered)
}

// bulkUptimesRequest is the JSON body for POST /uptimes. Accepts a
// list of visor PKs and an optional version. Avoids URL-length limits
// when querying hundreds of PKs.
type bulkUptimesRequest struct {
	PKs     []string `json:"pks"`
	Version string   `json:"v,omitempty"`
}

// POST /uptimes — bulk visor uptime query.
// Body: {"pks": ["pk1","pk2",...], "v": "v2"}
func (a *API) postUptimes(w http.ResponseWriter, r *http.Request) {
	var req bulkUptimesRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		httputil.WriteJSON(w, r, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	visorsParam := strings.Join(req.PKs, ";")
	switch req.Version {
	case "v3":
		a.getUptimesV3(w, r, visorsParam)
	default:
		var uptimes []store.VisorSummary
		if req.Version == "v2" {
			uptimes = a.getUptimesV2FromCache()
		} else {
			uptimes = a.getUptimesFromCache()
		}
		if uptimes == nil {
			uptimes = []store.VisorSummary{}
		}
		if visorsParam != "" {
			uptimes = filterByPKs(uptimes, visorsParam)
		}
		httputil.WriteJSON(w, r, http.StatusOK, uptimes)
	}
}

// filterByPKs filters a VisorSummary slice to only include entries whose
// PK hex matches one of the semicolon-separated PKs.
func filterByPKs(summaries []store.VisorSummary, pksParam string) []store.VisorSummary {
	want := make(map[string]struct{})
	for _, pk := range strings.Split(pksParam, ";") {
		pk = strings.TrimSpace(pk)
		if pk != "" {
			want[pk] = struct{}{}
		}
	}
	var result []store.VisorSummary
	for _, s := range summaries {
		if _, ok := want[s.PK.Hex()]; ok {
			result = append(result, s)
		}
	}
	if result == nil {
		result = []store.VisorSummary{}
	}
	return result
}

/*
	<<< HEALTH ENDPOINT >>>
*/

func (a *API) health(w http.ResponseWriter, r *http.Request) {
	info := buildinfo.Get()
	a.writeJSON(w, r, http.StatusOK, HealthCheckResponse{
		ServiceName: "service-discovery",
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
