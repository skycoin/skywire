// Package api internal/dmsg-discovery/api/api.go
package api

import (
	"context"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	jsoniter "github.com/json-iterator/go"
	"github.com/sirupsen/logrus"
	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/httputil"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire-utilities/pkg/metricsutil"
	"github.com/skycoin/skywire-utilities/pkg/networkmonitor"

	"github.com/skycoin/dmsg/internal/discmetrics"
	"github.com/skycoin/dmsg/internal/dmsg-discovery/store"
	"github.com/skycoin/dmsg/pkg/disc"
	"github.com/skycoin/dmsg/pkg/dmsg"
)

var log = logging.MustGetLogger("dmsg-discovery")

var json = jsoniter.ConfigFastest

// WhitelistPKs store whitelisted pks of network monitor
var WhitelistPKs = networkmonitor.GetWhitelistPKs()

const maxGetAvailableServersResult = 512

// API represents the api of the dmsg-discovery service`
type API struct {
	http.Handler
	metrics                     discmetrics.Metrics
	db                          store.Storer
	reqsInFlightCountMiddleware *metricsutil.RequestsInFlightCountMiddleware
	testMode                    bool
	startedAt                   time.Time
	enableLoadTesting           bool
	dmsgAddr                    string
	DmsgServers                 []string
	authPassphrase              string
	OfficialServers             map[string]bool
}

// New returns a new API object, which can be started as a server
func New(log logrus.FieldLogger, db store.Storer, m discmetrics.Metrics, testMode, enableLoadTesting, enableMetrics bool, dmsgAddr, authPassphrase string) *API {
	if log != nil {
		log = logging.MustGetLogger("dmsg_disc")
	}

	if db == nil {
		panic("cannot create new api without a store.Storer")
	}

	r := chi.NewRouter()
	api := &API{
		Handler:                     r,
		metrics:                     m,
		db:                          db,
		testMode:                    testMode,
		startedAt:                   time.Now(),
		enableLoadTesting:           enableLoadTesting,
		reqsInFlightCountMiddleware: metricsutil.NewRequestsInFlightCountMiddleware(),
		dmsgAddr:                    dmsgAddr,
		DmsgServers:                 []string{},
		authPassphrase:              authPassphrase,
		OfficialServers:             make(map[string]bool),
	}

	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	if enableMetrics {
		r.Use(api.reqsInFlightCountMiddleware.Handle)
		r.Use(metricsutil.RequestDurationMiddleware)
	}
	r.Use(httputil.SetLoggerMiddleware(log))

	r.Get("/dmsg-discovery/entry/{pk}", api.getEntry())
	r.Post("/dmsg-discovery/entry/", api.setEntry())
	r.Post("/dmsg-discovery/entry/{pk}", api.setEntry())
	r.Delete("/dmsg-discovery/entry", api.delEntry())
	r.Get("/dmsg-discovery/entries", api.allEntries())
	r.Delete("/dmsg-discovery/deregister", api.deregisterEntry())
	r.Get("/dmsg-discovery/available_servers", api.getAvailableServers())
	r.Get("/dmsg-discovery/all_servers", api.getAllServers())
	r.Get("/health", api.serviceHealth)

	return api
}

func (a *API) log(r *http.Request) logrus.FieldLogger {
	return httputil.GetLogger(r)
}

// RunBackgroundTasks is goroutine which runs in background periodic tasks of dmsg-discovery.
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

// AllServers is used to get all the available servers registered to the dmsg-discovery.
func (a *API) AllServers(ctx context.Context, _ logrus.FieldLogger) (entries []*disc.Entry, err error) {
	entries, err = a.db.AllServers(ctx)
	if err != nil {
		return entries, err
	}
	return entries, err
}

// getEntry returns the entry associated with the given public key
// URI: /dmsg-discovery/entry/:pk
// Method: GET
func (a *API) getEntry() func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		staticPK := cipher.PubKey{}
		if err := staticPK.UnmarshalText([]byte(chi.URLParam(r, "pk"))); err != nil {
			a.handleError(w, r, disc.ErrBadInput)
			return
		}

		entry, err := a.db.Entry(r.Context(), staticPK)

		// If we make sure that every error is handled then we can
		// remove the if and make the entry return the switch default
		if err != nil {
			a.handleError(w, r, err)
			return
		}

		a.writeJSON(w, r, http.StatusOK, entry)
	}
}

// allEntries returns all client entries connected to dmsg
// URI: /dmsg-discovery/entries
// Method: GET
func (a *API) allEntries() func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		entries, err := a.db.AllEntries(r.Context())
		if err != nil {
			a.handleError(w, r, err)
			return
		}
		a.writeJSON(w, r, http.StatusOK, entries)
	}
}

// deregisterEntry deletes the client entry associated with the PK requested by the network monitor
// URI: /dmsg-discovery/deregister/:pk
// Method: DELETE
func (a *API) deregisterEntry() func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		log.Info("Deregistration process started.")

		nmPkString := r.Header.Get("NM-PK")
		if ok := WhitelistPKs.Get(nmPkString); !ok {
			log.WithError(disc.ErrUnauthorizedNetworkMonitor).WithField("Step", "Checking NMs PK").Error("Deregistration process interrupt.")
			w.WriteHeader(http.StatusForbidden)
			return
		}

		nmPk := cipher.PubKey{}
		if err := nmPk.UnmarshalText([]byte(nmPkString)); err != nil {
			log.WithError(disc.ErrBadInput).WithField("Step", "Reading NMs PK").Error("Deregistration process interrupt.")
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		nmSign := cipher.Sig{}
		if err := nmSign.UnmarshalText([]byte(r.Header.Get("NM-Sign"))); err != nil {
			log.WithError(disc.ErrBadInput).WithField("Step", "Checking sign").Error("Deregistration process interrupt.")
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		if err := cipher.VerifyPubKeySignedPayload(nmPk, nmSign, []byte(nmPk.Hex())); err != nil {
			log.WithError(disc.ErrUnauthorizedNetworkMonitor).WithField("Step", "Veryfing request").Error("Deregistration process interrupt.")
			w.WriteHeader(http.StatusForbidden)
			return
		}

		keys := []cipher.PubKey{}
		keysBody, err := io.ReadAll(r.Body)
		if err != nil {
			log.WithError(disc.ErrBadInput).WithField("Step", "Reading keys").Error("Deregistration process interrupt.")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var keysSlice []string
		if err := json.Unmarshal(keysBody, &keysSlice); err != nil {
			log.WithError(disc.ErrBadInput).WithField("Step", "Slicing keys").Error("Deregistration process interrupt.")
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		for _, key := range keysSlice {
			tempKey := cipher.PubKey{}
			if err := tempKey.UnmarshalText([]byte(key)); err != nil {
				log.WithError(disc.ErrBadInput).WithField("Step", "Checking keys").Error("Deregistration process interrupt.")
				a.handleError(w, r, disc.ErrBadInput)
				return
			}
			keys = append(keys, tempKey)
		}

		for _, key := range keys {
			err := a.db.DelEntry(r.Context(), key)
			if err != nil {
				log.WithFields(logrus.Fields{"PK": key.Hex(), "Step": "Delete Entry"}).Error("Deregistration process interrupt.")
				a.handleError(w, r, err)
				return
			}
		}
		log.WithFields(logrus.Fields{"Number of Keys": len(keys), "Keys": keys}).Info("Deregistration process completed.")
		a.writeJSON(w, r, http.StatusOK, nil)
	}
}

// setEntry adds a new entry associated with the given public key
// or updates a previous one if signed by the same instance that
// created the previous one
// URI: /dmsg-discovery/entry/[?timeout={true|false}]
// Method: POST
// Args:
//
//	json serialized entry object
func (a *API) setEntry() func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := r.Body.Close(); err != nil {
				log.WithError(err).Warn("Failed to decode HTTP response body")
			}
		}()

		entryTimeout := time.Duration(0) // no timeout

		// Since v0.5.0 visors do not send ?timeout=true anymore so this is for older visors.
		if timeout := r.URL.Query().Get("timeout"); timeout == "true" {
			entryTimeout = store.DefaultTimeout
		}
		entry := new(disc.Entry)
		if err := json.NewDecoder(r.Body).Decode(entry); err != nil {
			a.handleError(w, r, disc.ErrUnexpected)
			return
		}

		if entry.Server != nil && !a.testMode {
			if ok, err := isLoopbackAddr(entry.Server.Address); ok {
				if err != nil {
					a.log(r).Warningf("failed to parse hostname and port: %s", err)
				}

				a.handleError(w, r, disc.ErrValidationServerAddress)
				return
			}
		}

		validateTimestamp := !a.enableLoadTesting
		// we donot validate timestamp when entry is a client as the client no longer updates itself
		// periodically and so the timestamp is never updated
		if entry.Client != nil {
			validateTimestamp = false
		}
		if err := entry.Validate(validateTimestamp); err != nil {
			a.handleError(w, r, err)
			return
		}

		if !a.enableLoadTesting {
			if err := entry.VerifySignature(); err != nil {
				a.handleError(w, r, disc.ErrUnauthorized)
				return
			}
		}

		if entry.Server != nil {
			if entry.Server.ServerType == a.authPassphrase || a.OfficialServers[entry.Static.Hex()] {
				entry.Server.ServerType = dmsg.DefaultOfficialDmsgServerType
			} else {
				entry.Server.ServerType = dmsg.DefaultCommunityDmsgServerType
			}
		}

		// Recover previous entry. If key not found we insert with sequence 0
		// If there was a previous entry we check the new one is a valid iteration
		oldEntry, err := a.db.Entry(r.Context(), entry.Static)
		if err == disc.ErrKeyNotFound {
			setErr := a.db.SetEntry(r.Context(), entry, entryTimeout)
			if setErr != nil {
				a.handleError(w, r, setErr)
				return
			}

			a.writeJSON(w, r, http.StatusOK, disc.MsgEntrySet)

			return
		} else if err != nil {
			a.handleError(w, r, err)
			return
		}

		if !a.enableLoadTesting {
			if err := oldEntry.ValidateIteration(entry); err != nil {
				a.handleError(w, r, err)
				return
			}
		}

		if err := a.db.SetEntry(r.Context(), entry, entryTimeout); err != nil {
			a.handleError(w, r, err)
			return
		}

		a.writeJSON(w, r, http.StatusOK, disc.MsgEntryUpdated)
	}
}

// delEntry deletes the entry associated with the given public key
// URI: /dmsg-discovery/entry
// Method: DELETE
// Args:
//
//	json serialized entry object
func (a *API) delEntry() func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		entry := new(disc.Entry)
		if err := json.NewDecoder(r.Body).Decode(entry); err != nil {
			a.handleError(w, r, disc.ErrUnexpected)
			return
		}

		validateTimestamp := !a.enableLoadTesting
		// we donot validate timestamp when entry is a client as the client no longer updates itself
		// periodically and so the timestamp is never updated
		if entry.Client != nil {
			validateTimestamp = false
		}
		if err := entry.Validate(validateTimestamp); err != nil {
			a.handleError(w, r, err)
			return
		}

		if !a.enableLoadTesting {
			if err := entry.VerifySignature(); err != nil {
				a.handleError(w, r, disc.ErrUnauthorized)
				return
			}
		}

		err := a.db.DelEntry(r.Context(), entry.Static)

		// If we make sure that every error is handled then we can
		// remove the if and make the entry return the switch default
		if err != nil {
			a.handleError(w, r, err)
			return
		}

		a.writeJSON(w, r, http.StatusOK, disc.MsgEntryDeleted)
	}
}

// getAvailableServers returns all available server entries as an array of json codified entry objects
// URI: /dmsg-discovery/available_servers
// Method: GET
func (a *API) getAvailableServers() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		entries, err := a.db.AvailableServers(r.Context(), maxGetAvailableServersResult)
		if err != nil {
			a.handleError(w, r, err)
			return
		}

		if len(entries) == 0 {
			a.writeJSON(w, r, http.StatusNotFound, disc.HTTPMessage{
				Code:    http.StatusNotFound,
				Message: disc.ErrNoAvailableServers.Error(),
			})

			return
		}

		a.writeJSON(w, r, http.StatusOK, entries)
	}
}

// getAllServers returns all servers entries as an array of json codified entry objects
// URI: /dmsg-discovery/all_servers
// Method: GET
func (a *API) getAllServers() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		entries, err := a.db.AllServers(r.Context())
		if err != nil {
			a.handleError(w, r, err)
			return
		}

		if len(entries) == 0 {
			a.writeJSON(w, r, http.StatusNotFound, disc.HTTPMessage{
				Code:    http.StatusNotFound,
				Message: disc.ErrNoAvailableServers.Error(),
			})

			return
		}

		a.writeJSON(w, r, http.StatusOK, entries)
	}
}

func (a *API) serviceHealth(w http.ResponseWriter, r *http.Request) {
	info := buildinfo.Get()
	a.writeJSON(w, r, http.StatusOK, httputil.HealthCheckResponse{
		BuildInfo:   info,
		StartedAt:   a.startedAt,
		DmsgAddr:    a.dmsgAddr,
		DmsgServers: a.DmsgServers,
	})
}

// isLoopbackAddr checks if string is loopback interface
func isLoopbackAddr(addr string) (bool, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false, err
	}

	if host == "" {
		return true, nil
	}

	return net.ParseIP(host).IsLoopback(), nil
}

// writeJSON writes a json object on a http.ResponseWriter with the given code.
func (a *API) writeJSON(w http.ResponseWriter, r *http.Request, code int, object interface{}) {
	jsonObject, err := json.Marshal(object)
	if err != nil {
		a.log(r).Warnf("Failed to encode json response: %s", err)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)

	_, err = w.Write(jsonObject)
	if err != nil {
		a.log(r).Warnf("Failed to write response: %s", err)
	}
}

func (a *API) updateInternalState(ctx context.Context, logger logrus.FieldLogger) {
	err := a.db.RemoveOldServerEntries(ctx)
	if err != nil {
		logger.WithError(err).Errorf("failed to check and remove servers entries")
		return
	}
	serversCount, clientsCount, err := a.db.CountEntries(ctx)
	if err != nil {
		logger.WithError(err).Errorf("failed to get clients and servers count")
		return
	}

	a.metrics.SetClientsCount(clientsCount)
	a.metrics.SetServersCount(serversCount)
}
