// Package api pkg/address-resolver/api.go
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/sirupsen/logrus"

	armetrics "github.com/skycoin/skywire/pkg/address-resolver/metrics"
	"github.com/skycoin/skywire/pkg/address-resolver/store"
	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/httpauth"
	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/metricsutil"
	"github.com/skycoin/skywire/pkg/netutil"
	"github.com/skycoin/skywire/pkg/networkmonitor"
	"github.com/skycoin/skywire/pkg/transport/network"
	"github.com/skycoin/skywire/pkg/transport/network/addrresolver"
	"github.com/skycoin/skywire/pkg/transport/network/handshake"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

// ErrNotConnected is returned when requested peer is not connected.
var ErrNotConnected = errors.New("peer is not connected")

// ErrMissingNetworkType is returned when there is no type in request.
var ErrMissingNetworkType = errors.New("missing network type in request")

// ErrUnauthorizedNetworkMonitor occurs in case of invalid network monitor key
var ErrUnauthorizedNetworkMonitor = errors.New("invalid network monitor key")

// ErrBadInput occurs in case of bad input
var ErrBadInput = errors.New("error bad input")

// WhitelistPKs store whitelisted pks of network monitor
var WhitelistPKs = networkmonitor.GetWhitelistPKs()

// API represents the api of the address-resolver service.
type API struct {
	http.Handler

	log   *logging.Logger
	store store.Store

	metrics                     armetrics.Metrics
	reqsInFlightCountMiddleware *metricsutil.RequestsInFlightCountMiddleware

	udpConnsMu sync.RWMutex
	udpConns   map[cipher.PubKey]net.Conn
	startedAt  time.Time

	closeOnce sync.Once
	closeC    chan struct{}

	dmsgAddr    string
	DmsgServers []string

	// publicUDPAddr is the externally-reachable host:port of the AR's
	// SUDPH UDP listener. Empty when the operator hasn't configured one.
	// Visors that reach this AR over dmsghttp use the value advertised in
	// /health to dial SUDPH; without it those visors cannot register
	// SUDPH at all (no host part to UDP-resolve from a dmsg:// PK URL).
	publicUDPAddr string

	// dhtMirrorStcpr / dhtMirrorSudph mirror AR bind state to the DHT
	// under salts "addr:stcpr" / "addr:sudph". Each writes the same
	// addrresolver.VisorData payload that AR holds for that visor;
	// readers (visors / DHT full nodes) get the same view they would
	// from /resolve. Both are nil when the operator hasn't configured
	// a Redis mirror, in which case the bind handlers behave exactly
	// as before. Two mirrors instead of one keyed by (pk, transport)
	// avoids a merge-races on every Bind/DelBind: each transport type
	// owns its own DHT target.
	dhtMirrorStcpr dhtMirror
	dhtMirrorSudph dhtMirror
}

// dhtMirror is the subset of dht.RedisMirror used by AR. Anything that
// can write a per-PK signed entry under a salt and tombstone it on
// deletion fits.
type dhtMirror interface {
	Mirror(subjectPK cipher.PubKey, entry interface{}, seq uint64)
	Delete(subjectPK cipher.PubKey)
}

// HealthCheckResponse is struct of /health endpoint
type HealthCheckResponse struct {
	ServiceName string          `json:"service_name,omitempty"`
	BuildInfo   *buildinfo.Info `json:"build_info,omitempty"`
	StartedAt   time.Time       `json:"started_at"`
	DmsgAddr    string          `json:"dmsg_address,omitempty"`
	DmsgServers []string        `json:"dmsg_servers,omitempty"`
	// UDPAddr is the externally-reachable host:port of this AR's SUDPH
	// listener. Visors that connect over dmsghttp use this value to
	// register SUDPH — without it the dmsg-only URL has no IP to dial.
	// Omitted when the operator did not configure --public-udp-address.
	UDPAddr string `json:"udp_address,omitempty"`
}

// ArData has all the visors that have registered with sudph or stcpr transport
type ArData struct {
	Sudph []string `json:"sudph,omitempty"`
	Stcpr []string `json:"stcpr,omitempty"`
}

// Error is the object returned to the client when there's an error.
type Error struct {
	Error string `json:"error"`
}

// SetDHTMirrors wires per-transport DHT mirrors. Stcpr and sudph are
// independent — passing nil for either skips mirroring on that path.
// Should be called once at startup before the listeners are accepting
// traffic; concurrent calls are not supported.
func (a *API) SetDHTMirrors(stcpr, sudph dhtMirror) {
	a.dhtMirrorStcpr = stcpr
	a.dhtMirrorSudph = sudph
}

func (a *API) mirrorSTCPR(pk cipher.PubKey, data *addrresolver.VisorData) {
	if a.dhtMirrorStcpr == nil {
		return
	}
	a.dhtMirrorStcpr.Mirror(pk, data, uint64(time.Now().UnixNano())) //nolint:gosec
}

func (a *API) mirrorSUDPH(pk cipher.PubKey, data *addrresolver.VisorData) {
	if a.dhtMirrorSudph == nil {
		return
	}
	a.dhtMirrorSudph.Mirror(pk, data, uint64(time.Now().UnixNano())) //nolint:gosec
}

// BackfillDHTMirror walks AR's existing STCPR and SUDPH bindings and
// republishes each one through the configured mirrors so the DHT view
// converges on whatever AR's redis store already holds, rather than
// only ever reflecting binds that arrive after startup.
//
// Best-effort: a single Resolve failure logs and skips that PK; a
// GetAll failure logs and skips that whole transport type. We do not
// abort startup — AR continues serving HTTP either way, and live
// Bind/DelBind traffic will gradually rebuild the mirror anyway.
func (a *API) BackfillDHTMirror(ctx context.Context, log logrus.FieldLogger) {
	if a.dhtMirrorStcpr == nil && a.dhtMirrorSudph == nil {
		return
	}
	totals := map[types.Type]int{}
	for _, netType := range []types.Type{types.STCPR, types.SUDPH} {
		var mirror dhtMirror
		switch netType {
		case types.STCPR:
			mirror = a.dhtMirrorStcpr
		case types.SUDPH:
			mirror = a.dhtMirrorSudph
		}
		if mirror == nil {
			continue
		}
		pkStrs, err := a.store.GetAll(ctx, netType)
		if err != nil {
			log.WithError(err).WithField("type", netType).Warn("DHT backfill: GetAll failed")
			continue
		}
		for _, pkStr := range pkStrs {
			if ctx.Err() != nil {
				return
			}
			var pk cipher.PubKey
			if err := pk.Set(pkStr); err != nil {
				continue
			}
			data, err := a.store.Resolve(ctx, netType, pk)
			if err != nil {
				continue
			}
			mirror.Mirror(pk, &data, uint64(time.Now().UnixNano())) //nolint:gosec
			totals[netType]++
		}
	}
	log.WithField("stcpr", totals[types.STCPR]).
		WithField("sudph", totals[types.SUDPH]).
		Info("DHT backfill complete")
}

// New creates a new api. publicUDPAddr is the externally-reachable
// host:port of the AR's SUDPH UDP listener (e.g. "ar.example.com:30178");
// pass empty when not configured — clients that reach the AR over
// dmsghttp will then have no way to register SUDPH.
func New(log *logging.Logger, s store.Store, nonceStore httpauth.NonceStore,
	enableMetrics bool, m armetrics.Metrics, dmsgAddr, publicUDPAddr string) *API {
	api := &API{
		log:                         log,
		store:                       s,
		metrics:                     m,
		reqsInFlightCountMiddleware: metricsutil.NewRequestsInFlightCountMiddleware(),
		udpConns:                    make(map[cipher.PubKey]net.Conn),
		startedAt:                   time.Now(),
		closeC:                      make(chan struct{}),
		dmsgAddr:                    dmsgAddr,
		DmsgServers:                 []string{},
		publicUDPAddr:               publicUDPAddr,
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
	r.Use(httputil.SetLoggerMiddleware(log))

	r.Group(func(r chi.Router) {
		r.Use(httpauth.MakeMiddleware(nonceStore))

		r.Post("/bind/stcpr", api.bind)
		r.Delete("/bind/stcpr", api.delBind)
		r.Get("/resolve/{type}/{pk}", api.resolve)
	})

	r.Get("/health", api.health)
	r.Get("/transports", api.transports)
	r.Delete("/deregister/{network}", api.deregister)

	nonceHandler := &httpauth.NonceHandler{Store: nonceStore}
	r.Get("/security/nonces/{pk}", nonceHandler.ServeHTTP)

	api.Handler = r

	go api.updateMetricsLoop()

	return api
}

// Close stops API.
func (a *API) Close() {
	a.closeOnce.Do(func() {
		close(a.closeC)
	})
}

func (a *API) updateMetricsLoop() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	a.updateMetrics()
	for {
		select {
		case <-a.closeC:
			return
		case <-ticker.C:
			a.updateMetrics()
		}
	}
}

func (a *API) updateMetrics() {
	a.udpConnsMu.RLock()
	clientsCount := len(a.udpConns)
	a.udpConnsMu.RUnlock()

	a.metrics.SetClientsCount(int64(clientsCount))
}

func (a *API) logger(r *http.Request) logrus.FieldLogger {
	return httputil.GetLogger(r)
}

// splitFamilyAddr returns the IPv4 and IPv6 RemoteAddr values for an
// observed bind source address. Exactly one of the return values is
// non-empty; family is inferred by parsing the host portion of addr.
// addr may be either a bare host ("1.2.3.4" / "::1" — STCPR style)
// or host:port ("1.2.3.4:5060" / "[::1]:5060" — SUDPH style); both
// are accepted. An unparseable input (host that isn't a valid IP)
// falls through to v4 to match historical behavior where any
// non-empty observed RemoteAddr went into the single RemoteAddr
// field unchanged.
//
// Paired with the per-family merge in store.Bind: a dual-stack
// visor's two binds (one over IPv4 HTTP, one over IPv6 HTTP) each
// populate one of v4/v6 here, and the store merges them into a
// single record. See pkg/transport/network/addrresolver.VisorData
// and #1525 for the full design.
func splitFamilyAddr(addr string) (v4, v6 string) {
	if addr == "" {
		return "", ""
	}
	host := addr
	if h, _, err := net.SplitHostPort(addr); err == nil {
		host = h
	}
	ip := net.ParseIP(host)
	if ip == nil || ip.To4() != nil {
		return addr, ""
	}
	return "", addr
}

func (a *API) bind(w http.ResponseWriter, r *http.Request) {
	remoteAddr := httpauth.GetRemoteAddr(r)
	a.logger(r).Infof("New POST /bind/stcpr request from %v", remoteAddr)

	ctx := r.Context()

	pk, ok := r.Context().Value(httpauth.ContextAuthKey).(cipher.PubKey)
	if !ok {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	rawBody, err := io.ReadAll(r.Body)
	if err != nil {
		a.logger(r).WithError(err).Errorf("Failed to read /bind/stcpr request body")
	}

	var localAddresses addrresolver.LocalAddresses
	if err := json.Unmarshal(rawBody, &localAddresses); err != nil {
		a.logger(r).WithError(err).Errorf("Failed to unmarshal data: %v", string(rawBody))
		return
	}

	if !netutil.IsPublicIP(net.ParseIP(remoteAddr)) {
		// Observed source IP is non-public — typically because the visor
		// shares a host with AR and reaches it via hairpin SNAT through
		// a Docker bridge (e.g., 172.x.y.z) rather than its real public
		// IP. If the visor declared a known-public PublicIP in the bind
		// payload, trust that instead; rejecting the bind would lose all
		// AR coverage for that visor's STCPR. Visors that don't declare
		// (older clients, or genuinely-misconfigured proxy/IPv6 traffic)
		// still hit the original reject below.
		if localAddresses.PublicIP != "" && netutil.IsPublicIP(net.ParseIP(localAddresses.PublicIP)) {
			a.logger(r).Infof("STCPR: observed %v non-public; using declared PublicIP %v for %v",
				remoteAddr, localAddresses.PublicIP, pk)
			remoteAddr = localAddresses.PublicIP
		} else {
			err := fmt.Sprintf("Cannot bind %v to %v (STCPR). Invalid IP address in request: %v", pk, remoteAddr, localAddresses)
			a.logger(r).Errorf(err)
			a.writeJSON(w, r, http.StatusBadRequest, &Error{
				Error: err,
			})
			return
		}
	}

	if !a.hasAddress(remoteAddr, localAddresses) {
		// visor didn't provide the IP it's trying to bind from
		// probably is behind NAT and shouldn't bind
		err := fmt.Sprintf("Cannot bind %v to %v (STCPR). Remote address not present in request: %v", pk, remoteAddr, localAddresses)
		a.logger(r).Errorf(err)

		a.writeJSON(w, r, http.StatusBadRequest, &Error{
			Error: err,
		})
		return
	}

	v4Addr, v6Addr := splitFamilyAddr(remoteAddr)
	visorData := addrresolver.VisorData{
		RemoteAddr:     v4Addr,
		RemoteAddrV6:   v6Addr,
		LocalAddresses: localAddresses,
	}

	if err := a.store.Bind(ctx, types.STCPR, pk, visorData); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		a.logger(r).Errorf("Failed to bind PK (STCPR): %v", err)

		return
	}

	a.mirrorSTCPR(pk, &visorData)

	w.WriteHeader(http.StatusOK)
	a.logger(r).Debugf("Bound %v to %v (STCPR)", pk, remoteAddr)
}

func (a *API) delBind(w http.ResponseWriter, r *http.Request) {
	remoteAddr := httpauth.GetRemoteAddr(r)

	a.logger(r).Infof("New DELETE /bind/stcpr request from %v", remoteAddr)

	ctx := r.Context()

	pk, ok := r.Context().Value(httpauth.ContextAuthKey).(cipher.PubKey)
	if !ok {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	if err := a.store.DelBind(ctx, types.STCPR, pk); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		a.logger(r).Errorf("Failed to delete bind PK (STCPR): %v", err)

		return
	}

	if a.dhtMirrorStcpr != nil {
		a.dhtMirrorStcpr.Delete(pk)
	}

	w.WriteHeader(http.StatusOK)
	a.logger(r).Debugf("Deleted bind %v from %v (STCPR)", pk, remoteAddr)
}

// Check if localAddresses contains given address
// Address provided must be in host:port form while addresses
// in the local are plain IP addresses
func (a *API) hasAddress(ip string, local addrresolver.LocalAddresses) bool {
	for _, localIP := range local.Addresses {
		if ip == localIP {
			return true
		}
	}
	return false
}

func (a *API) resolve(w http.ResponseWriter, r *http.Request) {

	remoteAddr := httpauth.GetRemoteAddr(r)

	a.logger(r).Infof("New /resolve request from %v", remoteAddr)

	tpType := chi.URLParam(r, "type")
	rawReceiverPK := chi.URLParam(r, "pk")
	a.logger(r).Infof("New /resolve request of type %v from %v", tpType, remoteAddr)

	ctx := r.Context()

	senderPK, ok := ctx.Value(httpauth.ContextAuthKey).(cipher.PubKey)
	if !ok {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	receiverPK := cipher.PubKey{}
	if err := receiverPK.UnmarshalText([]byte(rawReceiverPK)); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	receiverVisorData, err := a.store.Resolve(ctx, types.Type(tpType), receiverPK)
	if errors.Is(err, store.ErrNoEntry) {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	if err != nil {
		a.logger(r).Errorf("Failed to resolve PK:%v (%v): %v", receiverPK, tpType, err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	if sameIP(receiverVisorData.RemoteAddr, remoteAddr) {
		a.logger(r).Infof("Visors have the same remote address: %v, %v", receiverVisorData.RemoteAddr, remoteAddr)
		receiverVisorData.IsLocal = true
	} else {
		a.logger(r).Infof("Visors have different remote addresses: %v, %v", receiverVisorData.RemoteAddr, remoteAddr)
	}

	// Sender gets the receiver's data and dails to it.
	a.writeJSON(w, r, http.StatusOK, receiverVisorData)
	a.logger(r).Infof("Resolved %v to %v (%v)", receiverPK, receiverVisorData, tpType)

	if types.Type(tpType) == types.SUDPH {

		senderVisorData, err := a.store.Resolve(ctx, types.Type(tpType), senderPK)
		if errors.Is(err, store.ErrNoEntry) {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		// The receiver is also asked to dail to the sender in (SUDPH).
		if err := a.askToDialUDP(receiverPK, senderPK, r, senderVisorData); err != nil {
			a.logger(r).Warnf("Failed to ask %v to dial %v@%v: %v", receiverPK, senderPK, remoteAddr, err)
			return
		}

		a.logger(r).Infof("Asked %v to dial %v (%v)", receiverPK, senderPK, tpType)
	}
}

func (a *API) health(w http.ResponseWriter, r *http.Request) {
	info := buildinfo.Get()
	a.writeJSON(w, r, http.StatusOK, HealthCheckResponse{
		ServiceName: "address-resolver",
		BuildInfo:   info,
		StartedAt:   a.startedAt,
		DmsgAddr:    a.dmsgAddr,
		DmsgServers: a.DmsgServers,
		UDPAddr:     a.publicUDPAddr,
	})
}

func (a *API) transports(w http.ResponseWriter, r *http.Request) {

	info := &ArData{
		Sudph: a.getTransports(r, types.SUDPH),
		Stcpr: a.getTransports(r, types.STCPR),
	}

	a.writeJSON(w, r, http.StatusOK, info)
}

func (a *API) deregister(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	a.log.Info("Deregistration process started.")

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

	var netType types.Type
	switch chi.URLParam(r, "network") {
	case "sudph":
		netType = types.SUDPH
	case "stcpr":
		netType = types.STCPR
	default:
		a.log.WithError(ErrMissingNetworkType).WithField("Step", "Checking Network Type").Error("Deregistration process interrupt.")
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	pks := []cipher.PubKey{}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		a.log.WithError(ErrBadInput).WithField("Step", "Reading keys").Error("Deregistration process interrupt.")
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	var keys []string
	if err := json.Unmarshal(body, &keys); err != nil {
		a.log.WithError(ErrBadInput).WithField("Step", "Slicing keys").Error("Deregistration process interrupt.")
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	for _, key := range keys {
		pk := cipher.PubKey{}
		if err := pk.UnmarshalText([]byte(key)); err != nil {
			a.log.WithError(ErrBadInput).WithField("Step", "Checking keys").Error("Deregistration process interrupt.")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		pks = append(pks, pk)
	}

	for _, pk := range pks {
		err := a.store.DelBind(ctx, netType, pk)
		if err != nil {
			a.log.WithFields(logrus.Fields{"PK": pk.Hex(), "Step": "Delete Bind"}).Error("Deregistration process interrupt.")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		switch netType {
		case types.STCPR:
			if a.dhtMirrorStcpr != nil {
				a.dhtMirrorStcpr.Delete(pk)
			}
		case types.SUDPH:
			if a.dhtMirrorSudph != nil {
				a.dhtMirrorSudph.Delete(pk)
			}
		}
	}

	a.log.WithFields(logrus.Fields{"Number of Keys": len(keys), "Keys": keys, "Network Type": netType}).Info("Deregistration process completed.")
	a.writeJSON(w, r, http.StatusOK, nil)
}

func (a *API) getTransports(r *http.Request, netType types.Type) []string {
	ctx := r.Context()
	pks, err := a.store.GetAll(ctx, netType)
	if err != nil {
		a.logger(r).Warnf("Failed to get all (%v) transports", netType)
		return nil
	}
	return pks
}

func (a *API) askToDialUDP(dialerPK, dialeePK cipher.PubKey, r *http.Request, dialeeVisorData addrresolver.VisorData) error {
	conn, ok := a.udpConn(dialerPK)
	if !ok {
		return ErrNotConnected
	}

	a.logger(r).Infof("Sending %v@%v to %v", dialeePK, dialeeVisorData.RemoteAddr, dialerPK)

	remote := addrresolver.RemoteVisor{
		PK:   dialeePK,
		Addr: dialeeVisorData.RemoteAddr,
	}

	data, err := json.Marshal(remote)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	if _, err := conn.Write(data); err != nil {
		return err
	}

	return nil
}

func (a *API) udpConn(pk cipher.PubKey) (net.Conn, bool) {
	a.udpConnsMu.RLock()
	defer a.udpConnsMu.RUnlock()

	conn, ok := a.udpConns[pk]

	return conn, ok
}

func (a *API) setUDPConn(pk cipher.PubKey, conn net.Conn) {
	a.udpConnsMu.Lock()
	defer a.udpConnsMu.Unlock()

	a.udpConns[pk] = conn
}

func (a *API) deleteUDPConn(pk cipher.PubKey) {
	a.udpConnsMu.Lock()
	defer a.udpConnsMu.Unlock()

	delete(a.udpConns, pk)
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

// sudphHandshakeTimeout is the per-connection deadline for completing the
// SUDPH responder handshake. Legitimate clients on a healthy network
// complete the 3-frame exchange in well under a second, but real visors
// on slow / lossy links (high RTT + KCP retransmission) can take longer.
// The default handshake.Timeout of 10s was needlessly generous for a
// public-facing UDP listener and let scanner / broken-client connections
// accumulate state for the full 10s; 3s was tight enough that legitimate
// slow clients were timing out and never appearing in the AR. 5s is the
// compromise: still shaves ~50% off the worst-case scanner waste vs. the
// 10s default, while leaving enough budget for high-RTT real handshakes.
const sudphHandshakeTimeout = 5 * time.Second

// sudphMaxInFlightHandshakes caps the number of concurrent in-flight
// SUDPH handshakes. Any new UDP connection arriving while this many
// handshakes are already in progress is immediately closed rather than
// queued, providing hard backpressure against scanner floods. In
// steady state a healthy AR sees ~10 concurrent handshakes, so 256 is
// ~25× headroom for legitimate bursts while still bounding resource
// usage.
const sudphMaxInFlightHandshakes = 256

// ListenUDP listens for UDP connections for SUDPH.
func (a *API) ListenUDP(listener net.Listener) {
	a.log.Infof("Listening UDP on %v", listener.Addr())

	// Semaphore bounding in-flight handshake goroutines.
	sem := make(chan struct{}, sudphMaxInFlightHandshakes)

	for {
		conn, err := listener.Accept()
		if err != nil {
			// Accept errors on a KCP listener can occur under load
			// (EMFILE, invalid peer packets). Previously this was a
			// Fatal that killed the whole AR process; continue the
			// loop instead so transient errors don't take the
			// service down.
			a.log.WithError(err).Warn("SUDPH listener Accept error, continuing")
			select {
			case <-a.closeC:
				return
			default:
			}
			continue
		}

		// Non-blocking acquire: if the semaphore is full, drop
		// the connection immediately rather than queueing.
		select {
		case sem <- struct{}{}:
		default:
			a.log.Warnf("SUDPH handshake queue full (%d in flight), dropping %s",
				sudphMaxInFlightHandshakes, conn.RemoteAddr())
			if closeErr := conn.Close(); closeErr != nil {
				a.log.WithError(closeErr).Debug("Failed to close dropped SUDPH connection")
			}
			continue
		}

		a.log.Debugf("Accepted new UDP connection from %q", conn.RemoteAddr())

		go func(c net.Conn) {
			defer func() { <-sem }()
			a.sudphConnHandshake(c)
		}(conn)
	}
}

func (a *API) sudphConnHandshake(conn net.Conn) {
	remoteAddr := conn.RemoteAddr().String()

	hs := handshake.ResponderHandshake(func(_ handshake.Frame2) error { return nil })

	wrapped, err := network.DoHandshakeWithTimeout(conn, hs, types.SUDPH, sudphHandshakeTimeout, a.log)
	if err != nil {
		// Close the connection on handshake failure to prevent KCP session goroutine leak.
		if closeErr := conn.Close(); closeErr != nil {
			a.log.WithError(closeErr).Debug("Failed to close connection after handshake failure")
		}
		return
	}

	remote := wrapped.RemoteAddr().(dmsg.Addr)

	a.log.Infof("Performed handshake via UDP with %v@%v", remote, conn.RemoteAddr())

	a.bindSUDPH(wrapped, remoteAddr, remote.PK.String())
}

// sudphReadTimeout is the maximum time to wait for data from a SUDPH connection.
// Clients send heartbeats every 10s and re-register every 90s, so 3 minutes
// is generous enough to detect dead connections without false positives.
const sudphReadTimeout = 3 * time.Minute

// sudphInitialReadTimeout is the maximum time to wait for the initial
// LocalAddresses payload after a successful handshake.
const sudphInitialReadTimeout = 30 * time.Second

func (a *API) bindSUDPH(conn net.Conn, remoteAddr, strPK string) {
	a.log.Infof("Binding %v to %v (SUDPH)", strPK, remoteAddr)

	var pk cipher.PubKey
	if err := pk.Set(strPK); err != nil {
		a.log.WithError(err).Errorf("Failed to parse (SUDPH) PK %q", strPK)
		return
	}

	oldConn, ok := a.udpConn(pk)
	if ok {
		a.log.Infof("New connection from %v, closing old one", pk)
		// Remove the old connection from the map BEFORE closing it.
		// This prevents the old goroutine's deferred cleanupUDPConn from
		// deleting the NEW connection that we're about to store.
		a.deleteUDPConn(pk)
		if err := oldConn.Close(); err != nil {
			a.log.WithError(err).Warnf("Failed to close old connection")
		}
	}

	a.setUDPConn(pk, conn)

	buf := make([]byte, 4096)

	if err := conn.SetReadDeadline(time.Now().Add(sudphInitialReadTimeout)); err != nil {
		a.log.WithError(err).Warnf("Failed to set read deadline")
		a.cleanupUDPConn(pk, conn)
		return
	}

	n, err := conn.Read(buf)
	if err != nil {
		a.log.WithError(err).Warnf("Failed to read from connection")
		a.cleanupUDPConn(pk, conn)
		return
	}

	var localAddresses addrresolver.LocalAddresses
	if err := json.Unmarshal(buf[:n], &localAddresses); err != nil {
		a.log.WithError(err).Warnf("Failed to unmarshal data: %v", string(buf[:n]))
		a.cleanupUDPConn(pk, conn)
		return
	}

	// Override the observed UDP source when it's non-public AND the visor
	// declared a known-public PublicIP. The hairpin-SNAT case (visor and
	// AR on the same Docker host) rewrites BOTH the source IP (to the
	// docker bridge gateway) and the source port (to a kernel-chosen
	// ephemeral) — neither is reachable from external peers. Use the
	// visor's listen port (LocalAddresses.Port), which IS the externally-
	// reachable UDP port. Visors with public observed sources (the
	// common case, including legitimate NAT'd visors that need
	// hole-punching to retain the NAT-mapped source port) keep the
	// observed value unchanged.
	effectiveRemoteAddr := remoteAddr
	if host, _, err := net.SplitHostPort(remoteAddr); err == nil && !netutil.IsPublicIP(net.ParseIP(host)) {
		if localAddresses.PublicIP != "" && localAddresses.Port != "" &&
			netutil.IsPublicIP(net.ParseIP(localAddresses.PublicIP)) {
			effectiveRemoteAddr = net.JoinHostPort(localAddresses.PublicIP, localAddresses.Port)
			a.log.Infof("SUDPH: observed %v non-public; using declared %v for %v",
				remoteAddr, effectiveRemoteAddr, pk)
		}
	}

	v4SUDPH, v6SUDPH := splitFamilyAddr(effectiveRemoteAddr)
	visorData := addrresolver.VisorData{
		RemoteAddr:     v4SUDPH,
		RemoteAddrV6:   v6SUDPH,
		LocalAddresses: localAddresses,
	}

	bindCtx, bindCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer bindCancel()
	if err := a.store.Bind(bindCtx, types.SUDPH, pk, visorData); err != nil {
		a.log.WithError(err).Errorf("Failed to bind (SUDPH) pk %q to addr %q", strPK, remoteAddr)
		a.cleanupUDPConn(pk, conn)
		return
	}

	a.mirrorSUDPH(pk, &visorData)

	a.log.Infof("Bound %v to %v (SUDPH)", pk, remoteAddr)

	go func(pk cipher.PubKey, fromAddr string, conn net.Conn) {
		defer a.cleanupUDPConn(pk, conn)

		for {
			if err := conn.SetReadDeadline(time.Now().Add(sudphReadTimeout)); err != nil {
				a.log.Warnf("Failed to set read deadline for %v: %v", pk, err)
				return
			}

			buf := make([]byte, 4096)

			n, err := conn.Read(buf)
			if err != nil {
				a.log.Warnf("Failed to read packet from %v: %v", pk, err)
				return
			}

			data := buf[:n]
			a.log.Debugf("(SUDPH) New packet from %v@%v: %v", pk, fromAddr, string(data))
			if string(data) == addrresolver.UDPDelBindMessage {
				err = a.store.DelBind(context.Background(), types.SUDPH, pk)
				if err != nil {
					a.log.Warnf("Failed to delete bind (SUDPH) in redis %v: %v", pk, err)
					return
				}
				if a.dhtMirrorSudph != nil {
					a.dhtMirrorSudph.Delete(pk)
				}
				a.log.Debugf("Deleted bind %v from %v (SUDPH)", pk, remoteAddr)
				return
			}
			// Handle re-registration: try to unmarshal as LocalAddresses
			var localAddresses addrresolver.LocalAddresses
			if err := json.Unmarshal(data, &localAddresses); err == nil && localAddresses.Port != "" {
				v4Re, v6Re := splitFamilyAddr(fromAddr)
				newVisorData := addrresolver.VisorData{
					RemoteAddr:     v4Re,
					RemoteAddrV6:   v6Re,
					LocalAddresses: localAddresses,
				}
				if err := a.store.Bind(context.Background(), types.SUDPH, pk, newVisorData); err != nil {
					a.log.Warnf("Failed to re-bind (SUDPH) for %v: %v", pk, err)
				} else {
					a.mirrorSUDPH(pk, &newVisorData)
					a.log.Debugf("Re-bound %v to %v (SUDPH)", pk, fromAddr)
				}
			}
		}
	}(pk, remoteAddr, conn)
}

// cleanupUDPConn removes the connection from the map (if it's still the same one)
// and closes it. The identity check prevents a replaced connection's goroutine
// from accidentally removing the new connection from the map.
func (a *API) cleanupUDPConn(pk cipher.PubKey, conn net.Conn) {
	a.udpConnsMu.Lock()
	if current, ok := a.udpConns[pk]; ok && current == conn {
		delete(a.udpConns, pk)
	}
	a.udpConnsMu.Unlock()

	if err := conn.Close(); err != nil {
		a.log.WithError(err).Debugf("Failed to close SUDPH connection for %v", pk)
	}
}

func sameIP(addr1, addr2 string) bool {
	host1, _, err := net.SplitHostPort(addr1)
	if err != nil {
		return false
	}

	host2, _, err := net.SplitHostPort(addr2)
	if err != nil {
		return false
	}

	return host1 == host2
}
