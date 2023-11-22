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
	"github.com/skycoin/dmsg/pkg/dmsg"
	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/httpauth"
	"github.com/skycoin/skywire-utilities/pkg/httputil"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire-utilities/pkg/metricsutil"
	"github.com/skycoin/skywire-utilities/pkg/netutil"
	"github.com/skycoin/skywire-utilities/pkg/networkmonitor"
	"github.com/skycoin/skywire/pkg/transport/network"
	"github.com/skycoin/skywire/pkg/transport/network/addrresolver"
	"github.com/skycoin/skywire/pkg/transport/network/handshake"

	"github.com/skycoin/skywire-services/internal/armetrics"
	"github.com/skycoin/skywire-services/pkg/address-resolver/store"
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

	dmsgAddr string
}

// HealthCheckResponse is struct of /health endpoint
type HealthCheckResponse struct {
	BuildInfo *buildinfo.Info `json:"build_info,omitempty"`
	StartedAt time.Time       `json:"started_at"`
	DmsgAddr  string          `json:"dmsg_address,omitempty"`
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

// New creates a new api.
func New(log *logging.Logger, s store.Store, nonceStore httpauth.NonceStore,
	enableMetrics bool, m armetrics.Metrics, dmsgAddr string) *API {
	api := &API{
		log:                         log,
		store:                       s,
		metrics:                     m,
		reqsInFlightCountMiddleware: metricsutil.NewRequestsInFlightCountMiddleware(),
		udpConns:                    make(map[cipher.PubKey]net.Conn),
		startedAt:                   time.Now(),
		closeC:                      make(chan struct{}),
		dmsgAddr:                    dmsgAddr,
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
		// the ip of the visor is either private(because of proxy/loadbalancer) or ipv6
		err := fmt.Sprintf("Cannot bind %v to %v (STCPR). Invalid IP address in request: %v", pk, remoteAddr, localAddresses)
		a.logger(r).Errorf(err)
		a.writeJSON(w, r, http.StatusBadRequest, &Error{
			Error: err,
		})
		return
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

	visorData := addrresolver.VisorData{
		RemoteAddr:     remoteAddr,
		LocalAddresses: localAddresses,
	}

	if err := a.store.Bind(ctx, network.STCPR, pk, visorData); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		a.logger(r).Errorf("Failed to bind PK (STCPR): %v", err)

		return
	}

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

	if err := a.store.DelBind(ctx, network.STCPR, pk); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		a.logger(r).Errorf("Failed to delete bind PK (STCPR): %v", err)

		return
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

	receiverVisorData, err := a.store.Resolve(ctx, network.Type(tpType), receiverPK)
	if errors.Is(err, store.ErrNoEntry) {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	if err != nil {
		a.logger(r).Errorf("Failed to resolve PK:%v (%v): %v", receiverPK, tpType, err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	if err != nil {
		a.logger(r).Errorf("Failed to resolve PK:%v (%v): %v", senderPK, tpType, err)
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

	if network.Type(tpType) == network.SUDPH {

		senderVisorData, err := a.store.Resolve(ctx, network.Type(tpType), senderPK)
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
		BuildInfo: info,
		StartedAt: a.startedAt,
		DmsgAddr:  a.dmsgAddr,
	})
}

func (a *API) transports(w http.ResponseWriter, r *http.Request) {

	info := &ArData{
		Sudph: a.getTransports(r, network.SUDPH),
		Stcpr: a.getTransports(r, network.STCPR),
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

	var netType network.Type
	switch chi.URLParam(r, "network") {
	case "sudph":
		netType = network.SUDPH
	case "stcpr":
		netType = network.STCPR
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
	}

	a.log.WithFields(logrus.Fields{"Number of Keys": len(keys), "Keys": keys, "Network Type": netType}).Info("Deregistration process completed.")
	a.writeJSON(w, r, http.StatusOK, nil)
}

func (a *API) getTransports(r *http.Request, netType network.Type) []string {
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

// ListenUDP listens for UDP connections for SUDPH.
func (a *API) ListenUDP(listener net.Listener) {
	a.log.Infof("Listening UDP on %v", listener.Addr())

	for {
		conn, err := listener.Accept()
		if err != nil {
			a.log.Fatal(err)
		}

		a.log.Infof("Accepted new UDP connection from %q", conn.RemoteAddr())

		go a.sudphConnHandshake(conn)
	}
}

func (a *API) sudphConnHandshake(conn net.Conn) {
	remoteAddr := conn.RemoteAddr().String()

	hs := handshake.ResponderHandshake(func(f2 handshake.Frame2) error { return nil })

	wrapped, err := network.DoHandshake(conn, hs, network.SUDPH, a.log)
	if err != nil {
		return
	}

	remote := wrapped.RemoteAddr().(dmsg.Addr)

	a.log.Infof("Performed handshake via UDP with %v@%v", remote, conn.RemoteAddr())

	a.bindSUDPH(wrapped, remoteAddr, remote.PK.String())
}

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

		if err := oldConn.Close(); err != nil {
			a.log.WithError(err).Warnf("Failed to close old connection")
		}
	}

	a.setUDPConn(pk, conn)

	buf := make([]byte, 4096)

	n, err := conn.Read(buf)
	if err != nil {
		a.log.WithError(err).Warnf("Failed to read from connection")
		return
	}

	var localAddresses addrresolver.LocalAddresses
	if err := json.Unmarshal(buf[:n], &localAddresses); err != nil {
		a.log.WithError(err).Warnf("Failed to unmarshal data: %v", string(buf[:n]))
		return
	}

	visorData := addrresolver.VisorData{
		RemoteAddr:     remoteAddr,
		LocalAddresses: localAddresses,
	}

	if err := a.store.Bind(context.TODO(), network.SUDPH, pk, visorData); err != nil {
		a.log.WithError(err).Errorf("Failed to bind (SUDPH) pk %q to addr %q", strPK, remoteAddr)
		return
	}

	a.log.Infof("Bound %v to %v (SUDPH)", pk, remoteAddr)

	go func(pk cipher.PubKey, fromAddr string, conn net.Conn) {
		for {
			buf := make([]byte, 4096)

			n, err := conn.Read(buf)
			if err != nil {
				a.log.Warnf("Failed to read packet from %v: %v", pk, err)
				return
			}

			data := buf[:n]
			a.log.Debugf("(SUDPH) New packet from %v@%v: %v", pk, fromAddr, string(data))
			if string(data) == addrresolver.UDPDelBindMessage {
				err = a.store.DelBind(context.Background(), network.SUDPH, pk)
				if err != nil {
					a.log.Warnf("Failed to delete bind (SUDPH) in redis %v: %v", pk, err)
					return
				}
				a.log.Debugf("Deleted bind %v from %v (SUDPH)", pk, remoteAddr)
				return
			}
		}
	}(pk, remoteAddr, conn)
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
