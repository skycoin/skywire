// Package api pkg/discovery/api/api.go
package api

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	jsoniter "github.com/json-iterator/go"
	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/disc/metrics"
	"github.com/skycoin/skywire/pkg/dmsg/discovery/store"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/metricsutil"
	"github.com/skycoin/skywire/pkg/networkmonitor"
)

var log = logging.MustGetLogger("dmsg-discovery")

var json = jsoniter.ConfigFastest

// WhitelistPKs store whitelisted pks of network monitor
var WhitelistPKs = networkmonitor.GetWhitelistPKs()

const maxGetAvailableServersResult = 512

// API represents the api of the dmsg-discovery service`
type API struct {
	http.Handler
	metrics                     metrics.Metrics
	db                          store.Storer
	reqsInFlightCountMiddleware *metricsutil.RequestsInFlightCountMiddleware
	testMode                    bool
	startedAt                   time.Time
	enableLoadTesting           bool
	dmsgAddr                    string
	DmsgServers                 []string
	authPassphrase              string
	OfficialServers             map[string]bool

	// dhtMirror mirrors entries to the DHT under the visor's PK-derived
	// target. The mirror signs with its own key but stores under the
	// visor's target so DHT lookups by visor PK find the entry. Delete
	// propagates DelEntry removals into the DHT so stale targets don't
	// accumulate past their HTTP-discovery lifetime.
	dhtMirror interface {
		Mirror(subjectPK cipher.PubKey, entry interface{}, seq uint64)
		Delete(subjectPK cipher.PubKey)
	}

	// clientEntryTTL is the Redis expiration applied to client entries
	// on POST /dmsg-discovery/entry. Clients refresh their entry every
	// DefaultUpdateInterval*5 (5 minutes), so a TTL of 60 minutes
	// (12× the refresh interval) gives ~2-3 missed refreshes of slack
	// for brief network hiccups before Redis prunes the entry. This
	// fixes the "stale delegated_servers" problem where crashed or
	// network-dropped visors lingered in discovery forever, driving
	// route setup failures on visors that tried to dial them. 0 means
	// no expiration (the prior behavior).
	clientEntryTTL time.Duration

	// allClientsCache serves the `/dmsg-discovery/servers/clients` and
	// `/dmsg-discovery/server/{pk}/clients` endpoints from a short-lived
	// in-memory cache. Each call would otherwise do a full Redis
	// SMEMBERS + MGET of every client entry on the network, which for a
	// large deployment is O(N) work per request and crushes the
	// discovery service under any meaningful polling load. We only do
	// the expensive lookup at most once per allClientsCacheTTL.
	allClientsMu     sync.Mutex
	allClientsCache  []*disc.Entry
	allClientsAt     time.Time
	allClientsErr    error
	allClientsSingle singleflight // dedupes concurrent refreshes

	// cxoPublisher mirrors set/del entry events into a CXO TreeStore
	// feed under clients-by-server/<server>/<client>/{entry,tombstone}.
	// Started automatically whenever DMSG-D runs with a configured
	// SecKey + dmsg-capable mode. Nil if the publisher failed to bind
	// or DMSG isn't enabled; calling sites null-check via pub == nil
	// so the HTTP path always works regardless.
	cxoPublisher *ClientsByServerCXOPublisher
}

// singleflight is a tiny in-place dedupe primitive. When a refresh is
// already running, concurrent callers wait on the same channel instead
// of each hitting Redis.
type singleflight struct {
	mu   sync.Mutex
	done chan struct{}
}

// allClientsCacheTTL is the freshness window for the cached response
// of the servers/clients and server/{pk}/clients endpoints. 15s is
// chosen to be shorter than the minimum client update interval
// (DefaultUpdateInterval = 1 minute) so callers still see new clients
// reasonably quickly, while keeping Redis load proportional to the
// poll rate of the cache, not the request rate of the API.
const allClientsCacheTTL = 15 * time.Second

// New returns a new API object, which can be started as a server.
// clientEntryTTL is the Redis expiration applied to client entries;
// 0 disables expiration (legacy behavior).
func New(log logrus.FieldLogger, db store.Storer, m metrics.Metrics, testMode, enableLoadTesting, enableMetrics bool, dmsgAddr, authPassphrase string, clientEntryTTL time.Duration) *API {
	if log == nil {
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
		clientEntryTTL:              clientEntryTTL,
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
	r.Post("/dmsg-discovery/entries/batch", api.batchEntries())
	r.Get("/dmsg-discovery/visorEntries", api.allVisorEntries())
	r.Delete("/dmsg-discovery/deregister", api.deregisterEntry())
	r.Get("/dmsg-discovery/available_servers", api.getAvailableServers())
	r.Get("/dmsg-discovery/all_servers", api.getAllServers())
	r.Get("/dmsg-discovery/servers/clients", api.allClientsByServer())
	r.Get("/dmsg-discovery/server/{pk}/clients", api.clientsByServer())
	r.Get("/uptimes", api.getUptimes)
	r.Post("/uptimes", api.postUptimes)
	r.Get("/health", api.serviceHealth)

	return api
}

// SetDHTMirror sets a mirror that publishes entries to the DHT on every
// successful SetEntry and removes them from the DHT on DelEntry.
func (a *API) SetDHTMirror(m interface {
	Mirror(subjectPK cipher.PubKey, entry interface{}, seq uint64)
	Delete(subjectPK cipher.PubKey)
}) {
	a.dhtMirror = m
}

// SetClientsByServerCXOPublisher installs the CXO publisher that
// mirrors set/del entry events into a TreeStore feed at
// clients-by-server/<server>/<client>/{entry,tombstone}. Pass nil to
// disable. Wired by cmd/dmsg/dmsg-discovery after the publisher is
// built; no-op until then.
func (a *API) SetClientsByServerCXOPublisher(p *ClientsByServerCXOPublisher) {
	a.cxoPublisher = p
}

// BackfillDHTMirror iterates all existing entries in the store and
// mirrors them to the DHT. This ensures the DHT has the full dataset
// on startup, not just entries updated since the last restart.
func (a *API) BackfillDHTMirror(ctx context.Context, log logrus.FieldLogger) {
	if a.dhtMirror == nil {
		return
	}
	pks, err := a.db.AllEntries(ctx)
	if err != nil {
		log.WithError(err).Warn("DHT backfill: failed to list entries")
		return
	}
	mirrored := 0
	for _, pkHex := range pks {
		if ctx.Err() != nil {
			break
		}
		var pk cipher.PubKey
		if err := pk.Set(pkHex); err != nil {
			continue
		}
		entry, err := a.db.Entry(ctx, pk)
		if err != nil || entry == nil {
			continue
		}
		a.dhtMirror.Mirror(entry.Static, entry, entry.Sequence)
		mirrored++
	}
	log.WithField("count", mirrored).Info("DHT backfill complete")
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

// batchEntries returns entries for a list of specific PKs.
// URI: /dmsg-discovery/entries/batch
// Method: POST
// Body: JSON array of PK hex strings
func (a *API) batchEntries() func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		var pks []string
		body, readErr := io.ReadAll(r.Body)
		if readErr != nil {
			a.handleError(w, r, disc.ErrBadInput)
			return
		}
		if err := json.Unmarshal(body, &pks); err != nil {
			a.handleError(w, r, disc.ErrBadInput)
			return
		}

		result := make(map[string]*disc.Entry)
		for _, pkStr := range pks {
			pk := cipher.PubKey{}
			if err := pk.UnmarshalText([]byte(strings.TrimSpace(pkStr))); err != nil {
				continue
			}
			entry, err := a.db.Entry(r.Context(), pk)
			if err != nil {
				continue
			}
			result[pkStr] = entry
		}

		a.writeJSON(w, r, http.StatusOK, result)
	}
}

// allVisorEntries returns all visor client entries connected to dmsg
// URI: /dmsg-discovery/visorEntries
// Method: GET
func (a *API) allVisorEntries() func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		entries, err := a.db.AllVisorEntries(r.Context())
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

		// Default client entry TTL comes from the API config. This
		// prunes stale entries from visors that crashed or lost
		// network without cleanly updating their delegated_servers.
		// 0 means no expiration.
		entryTimeout := a.clientEntryTTL

		// Legacy override: older (pre-v0.5.0) visors sent ?timeout=true
		// and expected a short TTL. Preserve that behavior for them.
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

			// Mirror to DHT under the visor's PK.
			if a.dhtMirror != nil {
				a.dhtMirror.Mirror(entry.Static, entry, entry.Sequence)
			}

			// CXO mirror — best-effort, no-op if the publisher
			// failed to start (no DMSG). Server entries are ignored
			// at this layer (the publisher only emits
			// clients-by-server leaves).
			a.cxoPublisher.PublishSetEntry(nil, entry)

			// Record heartbeat for uptime tracking on new client entries.
			if entry.Client != nil {
				_ = a.db.RecordHeartbeat(r.Context(), entry.Static, entry.ClientType) //nolint:errcheck
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

		// Mirror to DHT under the visor's PK.
		if a.dhtMirror != nil {
			a.dhtMirror.Mirror(entry.Static, entry, entry.Sequence)
		}

		// CXO mirror — diff old vs new DelegatedServers so subscribers
		// see clean adds/removes for the (server, client) pairs.
		a.cxoPublisher.PublishSetEntry(oldEntry, entry)

		// Record heartbeat for uptime tracking on client entry updates.
		if entry.Client != nil {
			_ = a.db.RecordHeartbeat(r.Context(), entry.Static, entry.ClientType) //nolint:errcheck
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
		defer r.Body.Close() //nolint:errcheck
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

		// Capture the existing entry BEFORE deleting it so the CXO
		// publisher knows which (server, client) pairs to tombstone.
		// Best-effort: a fetch failure here just means we publish no
		// CXO tombstones; the HTTP delete still proceeds.
		var preDelEntry *disc.Entry
		if a.cxoPublisher != nil {
			if existing, ferr := a.db.Entry(r.Context(), entry.Static); ferr == nil {
				preDelEntry = existing
			}
		}

		err := a.db.DelEntry(r.Context(), entry.Static)

		// If we make sure that every error is handled then we can
		// remove the if and make the entry return the switch default
		if err != nil {
			a.handleError(w, r, err)
			return
		}

		if a.dhtMirror != nil {
			a.dhtMirror.Delete(entry.Static)
		}

		// CXO mirror: tombstone every (server, client) pair the
		// deleted client entry was previously delegated to.
		a.cxoPublisher.PublishDelEntry(preDelEntry)

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

// getCachedAllClientEntries returns the cached result of
// store.AllClientEntries, refreshing at most once per allClientsCacheTTL.
// Concurrent callers that arrive while a refresh is in flight wait on
// the same inflight fetch instead of each dispatching their own Redis
// MGET — this is the primary defense against the thundering herd that
// pegs the discovery service at 100%+ CPU in production.
func (a *API) getCachedAllClientEntries(ctx context.Context) ([]*disc.Entry, error) {
	a.allClientsMu.Lock()
	if time.Since(a.allClientsAt) < allClientsCacheTTL && a.allClientsCache != nil {
		entries := a.allClientsCache
		err := a.allClientsErr
		a.allClientsMu.Unlock()
		return entries, err
	}

	// Stale or empty. See if a refresh is already in flight.
	a.allClientsSingle.mu.Lock()
	inflight := a.allClientsSingle.done
	if inflight == nil {
		inflight = make(chan struct{})
		a.allClientsSingle.done = inflight
		a.allClientsSingle.mu.Unlock()
		a.allClientsMu.Unlock()

		// We own the refresh. Run it, then publish and broadcast.
		entries, err := a.db.AllClientEntries(ctx)

		a.allClientsMu.Lock()
		a.allClientsCache = entries
		a.allClientsErr = err
		a.allClientsAt = time.Now()
		a.allClientsMu.Unlock()

		a.allClientsSingle.mu.Lock()
		a.allClientsSingle.done = nil
		close(inflight)
		a.allClientsSingle.mu.Unlock()

		return entries, err
	}
	a.allClientsSingle.mu.Unlock()
	a.allClientsMu.Unlock()

	// Wait for the existing refresh. Honor caller cancellation so a
	// dying request doesn't linger here.
	select {
	case <-inflight:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	a.allClientsMu.Lock()
	entries := a.allClientsCache
	err := a.allClientsErr
	a.allClientsMu.Unlock()
	return entries, err
}

// allClientsByServer returns all client PKs grouped by the server they are delegated to
// URI: /dmsg-discovery/servers/clients
// Method: GET
// Response: { "server_pk": ["client_pk1", "client_pk2", ...], ... }
func (a *API) allClientsByServer() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		entries, err := a.getCachedAllClientEntries(r.Context())
		if err != nil {
			a.handleError(w, r, err)
			return
		}

		result := make(map[string][]string)
		for _, entry := range entries {
			if entry.Client == nil {
				continue
			}
			clientPK := entry.Static.Hex()
			for _, serverPK := range entry.Client.DelegatedServers {
				// Hex once per (entry, server) pair — previously
				// called twice per inner iter (map key + value
				// lookup), doubling the hex-encode load on every
				// invocation of this endpoint.
				sHex := serverPK.Hex()
				result[sHex] = append(result[sHex], clientPK)
			}
		}

		a.writeJSON(w, r, http.StatusOK, result)
	}
}

// clientsByServer returns all client PKs delegated to a specific server
// URI: /dmsg-discovery/server/{pk}/clients
// Method: GET
// Response: ["client_pk1", "client_pk2", ...]
func (a *API) clientsByServer() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		serverPK := cipher.PubKey{}
		if err := serverPK.UnmarshalText([]byte(chi.URLParam(r, "pk"))); err != nil {
			a.handleError(w, r, disc.ErrBadInput)
			return
		}

		entries, err := a.getCachedAllClientEntries(r.Context())
		if err != nil {
			a.handleError(w, r, err)
			return
		}

		var result []string
		for _, entry := range entries {
			if entry.Client == nil {
				continue
			}
			for _, delegatedPK := range entry.Client.DelegatedServers {
				if delegatedPK == serverPK {
					result = append(result, entry.Static.Hex())
					break
				}
			}
		}

		a.writeJSON(w, r, http.StatusOK, result)
	}
}

// GET /uptimes — visor uptime summaries.
// Query params: v=v2 (daily percentages), v=v3 (+ timeline), visors=pk1;pk2 (filter).
func (a *API) getUptimes(w http.ResponseWriter, r *http.Request) {
	version := r.URL.Query().Get("v")
	visorsParam := r.URL.Query().Get("visors")
	v2 := version == "v2" || version == "v3"
	timeline := version == "v3"

	summaries, err := a.db.GetAllVisorSummaries(r.Context(), v2, timeline)
	if err != nil {
		a.handleError(w, r, err)
		return
	}

	if visorsParam != "" {
		want := make(map[string]struct{})
		for _, pk := range strings.Split(visorsParam, ";") {
			pk = strings.TrimSpace(pk)
			if pk != "" {
				want[pk] = struct{}{}
			}
		}
		filtered := make([]store.VisorSummary, 0)
		for _, s := range summaries {
			if _, ok := want[s.PK.Hex()]; ok {
				filtered = append(filtered, s)
			}
		}
		summaries = filtered
	}

	if summaries == nil {
		summaries = []store.VisorSummary{}
	}
	a.writeJSON(w, r, http.StatusOK, summaries)
}

// bulkUptimesRequest is the JSON body for POST /uptimes.
type bulkUptimesRequest struct {
	PKs     []string `json:"pks"`
	Version string   `json:"v,omitempty"`
}

// POST /uptimes — bulk visor uptime query. Body: {"pks": [...], "v": "v2"}
func (a *API) postUptimes(w http.ResponseWriter, r *http.Request) {
	var req bulkUptimesRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		a.writeJSON(w, r, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	v2 := req.Version == "v2" || req.Version == "v3"
	timeline := req.Version == "v3"

	summaries, err := a.db.GetAllVisorSummaries(r.Context(), v2, timeline)
	if err != nil {
		a.handleError(w, r, err)
		return
	}

	if len(req.PKs) > 0 {
		want := make(map[string]struct{}, len(req.PKs))
		for _, pk := range req.PKs {
			pk = strings.TrimSpace(pk)
			if pk != "" {
				want[pk] = struct{}{}
			}
		}
		filtered := make([]store.VisorSummary, 0)
		for _, s := range summaries {
			if _, ok := want[s.PK.Hex()]; ok {
				filtered = append(filtered, s)
			}
		}
		summaries = filtered
	}

	if summaries == nil {
		summaries = []store.VisorSummary{}
	}
	a.writeJSON(w, r, http.StatusOK, summaries)
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

	ip := net.ParseIP(host)
	if ip == nil {
		return false, nil
	}
	return ip.IsLoopback(), nil
}

// writeJSON writes a json object on a http.ResponseWriter with the given code.
func (a *API) writeJSON(w http.ResponseWriter, r *http.Request, code int, object interface{}) {
	jsonObject, err := json.Marshal(object)
	if err != nil {
		a.log(r).Warnf("Failed to encode json response: %s", err)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)

	_, err = w.Write(jsonObject) //nolint:gosec // G705: jsonObject is marshaled, not raw user input
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

	// Clean up stale client entries: remove "clients" set members whose
	// Redis keys have expired, and apply the configured TTL to legacy
	// entries that were written before the TTL feature was deployed.
	if a.clientEntryTTL > 0 {
		removed, ttlSet, err := a.db.RemoveStaleClientEntries(ctx, a.clientEntryTTL)
		if err != nil {
			logger.WithError(err).Warn("failed to clean stale client entries")
		} else if removed > 0 || ttlSet > 0 {
			logger.Infof("Client entry cleanup: removed %d stale set members, applied TTL to %d legacy entries", removed, ttlSet)
		}
	}

	serversCount, clientsCount, err := a.db.CountEntries(ctx)
	if err != nil {
		logger.WithError(err).Errorf("failed to get clients and servers count")
		return
	}

	a.metrics.SetClientsCount(clientsCount)
	a.metrics.SetServersCount(serversCount)
}
