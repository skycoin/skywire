// Package dmsgserver pkg/dmsgserver/api.go
package dmsgserver

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/pires/go-proxyproto"
	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/cipher"
	dmsg "github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg/metrics"
	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/logging"
)

// ServerAPI main object of the server
type ServerAPI struct {
	metrics         metrics.Metrics
	startedAt       time.Time
	dmsgServer      *dmsg.Server
	sMu             sync.Mutex
	minuteDecValues map[*dmsg.SessionCommon]uint64
	minuteEncValues map[*dmsg.SessionCommon]uint64
	secondDecValues map[*dmsg.SessionCommon]uint64
	secondEncValues map[*dmsg.SessionCommon]uint64
	router          *chi.Mux
}

// NewServerAPI returns a new ServerAPI object, which can be started as a server
func NewServerAPI(r *chi.Mux, log *logging.Logger, m metrics.Metrics) *ServerAPI {
	api := &ServerAPI{
		metrics:         m,
		startedAt:       time.Now(),
		minuteDecValues: make(map[*dmsg.SessionCommon]uint64),
		minuteEncValues: make(map[*dmsg.SessionCommon]uint64),
		secondDecValues: make(map[*dmsg.SessionCommon]uint64),
		secondEncValues: make(map[*dmsg.SessionCommon]uint64),
		router:          r,
	}
	r.Use(httputil.SetLoggerMiddleware(log))
	r.Get("/health", api.health)
	return api
}

// RunBackgroundTasks is function which runs periodic tasks of dmsg-server.
func (a *ServerAPI) RunBackgroundTasks(ctx context.Context) {
	ticker := time.NewTicker(time.Second * 10)
	//tickerEverySecond := time.NewTicker(time.Second * 1)
	tickerEveryMinute := time.NewTicker(time.Second * 60)
	defer ticker.Stop()
	//defer tickerEverySecond.Stop()
	defer tickerEveryMinute.Stop()
	a.updateInternalState()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.updateInternalState()
		case <-tickerEveryMinute.C:
			a.updateAverageNumberOfPacketsPerMinute()
			/*case <-tickerEverySecond.C:
			a.updateAverageNumberOfPacketsPerSecond()*/
		}
	}
}

// SetDmsgServer saves srv in the ServerAPI
func (a *ServerAPI) SetDmsgServer(srv *dmsg.Server) {
	a.sMu.Lock()
	a.dmsgServer = srv
	a.sMu.Unlock()
}

// ListenAndServe runs dmsg Serve function alongside health endpoint. When wsURL
// is non-empty the main TCP port ALSO serves dmsg-over-WebSocket (demuxed by
// first bytes — raw Noise vs HTTP/1 upgrade), so one advertised ip:port carries
// both protocols (see dmsg.Server.ServeWithWS and
// docs/design/dmsg-server-protocol-unification.md). Empty wsURL = raw-dmsg only.
func (a *ServerAPI) ListenAndServe(lAddr, pAddr, httpAddr, wsURL string) error {
	errCh := make(chan error, 3)

	dmsgLn, err := net.Listen("tcp", lAddr)
	if err != nil {
		return err
	}
	dmsgLis := &proxyproto.Listener{Listener: dmsgLn, ConnPolicy: optionalProxyHeader}
	go func(l net.Listener, address, ws string) {
		var serr error
		if ws != "" {
			// raw-dmsg + WebSocket on the one port
			serr = a.dmsgServer.ServeWithWS(l, address, ws)
		} else {
			serr = a.dmsgServer.Serve(l, address)
		}
		l.Close() //nolint:errcheck,gosec
		// Label so a dead DATA PLANE is identifiable, and use %v so a clean
		// (nil) stop still yields a non-nil signal — ANY stop of the dmsg
		// protocol Serve must surface, not be silently absorbed.
		errCh <- fmt.Errorf("dmsg data-plane server stopped: %v", serr)
	}(dmsgLis, pAddr, wsURL)

	// dmsg-over-QUIC (#2607): also listen on UDP (same port number as TCP) so
	// QUIC-capable clients get a session with native QUIC stream multiplexing +
	// an unreliable datagram channel. Best-effort + additive — a QUIC bind
	// failure leaves the TCP listener serving normally, and non-QUIC clients
	// keep using the advertised TCP Address. QUIC clients discover this via the
	// Server.AddressUDP + Protocol="quic" the entry now advertises.
	if udpConn, uerr := net.ListenPacket("udp", lAddr); uerr != nil {
		logging.MustGetLogger("dmsg_server").WithError(uerr).
			Warn("dmsg-over-QUIC: failed to bind UDP listener; serving TCP only")
	} else {
		go func(udp net.PacketConn, advertised string) {
			serr := a.dmsgServer.ServeQUIC(udp, advertised)
			udp.Close() //nolint:errcheck,gosec
			errCh <- fmt.Errorf("dmsg QUIC server stopped: %v", serr)
		}(udpConn, pAddr)
	}

	ln, err := net.Listen("tcp", httpAddr)
	if err != nil {
		dmsgLis.Close() //nolint:errcheck,gosec
		return err
	}
	lis := &proxyproto.Listener{Listener: ln, ConnPolicy: optionalProxyHeader}
	srv := &http.Server{
		ReadTimeout:       3 * time.Second,
		WriteTimeout:      3 * time.Second,
		IdleTimeout:       30 * time.Second,
		ReadHeaderTimeout: 3 * time.Second,
		//Addr:              lis,
		Handler: a.router,
	}
	// Run the HTTP health server in the BACKGROUND too. Previously srv.Serve
	// blocked here in the foreground, so if the dmsg data-plane Serve died
	// first its error sat unread in errCh while this kept serving — the HTTP
	// /health endpoint reported healthy while the protocol listener was dead
	// (the "looks healthy but can't relay" mask). Now whichever Serve dies
	// FIRST returns, so the caller (service Run) can restart/exit.
	go func() {
		serr := srv.Serve(lis)
		lis.Close() //nolint:errcheck,gosec
		errCh <- fmt.Errorf("dmsg http health server stopped: %v", serr)
	}()

	err = <-errCh
	// One half died — tear the other down so we don't leak a half-dead server.
	dmsgLis.Close() //nolint:errcheck,gosec
	lis.Close()     //nolint:errcheck,gosec
	return err
}

// Close closes connection to both http server and dmsg server
func (a *ServerAPI) Close() error {
	return a.dmsgServer.Close()
}

// Health serves health page
func (a *ServerAPI) health(w http.ResponseWriter, r *http.Request) {
	info := buildinfo.Get()
	a.writeJSON(w, r, http.StatusOK, httputil.HealthCheckResponse{
		BuildInfo: info,
		StartedAt: a.startedAt,
	})
}

// writeJSON writes a json object on a http.ResponseWriter with the given code.
func (a *ServerAPI) writeJSON(w http.ResponseWriter, r *http.Request, code int, object interface{}) {
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

func (a *ServerAPI) log(r *http.Request) logrus.FieldLogger {
	return httputil.GetLogger(r)
}

// UpdateInternalState is background function which updates numbers of clients.
func (a *ServerAPI) updateInternalState() {
	if a.dmsgServer != nil {
		a.metrics.SetClientsCount(int64(len(a.dmsgServer.GetSessions())))
	}
}

// UpdateAverageNumberOfPacketsPerMinute is function which needs to called every minute.
func (a *ServerAPI) updateAverageNumberOfPacketsPerMinute() {
	if a.dmsgServer != nil {
		a.sMu.Lock()
		defer a.sMu.Unlock()

		newDecValues, newEncValues, average := calculateThroughput(
			a.dmsgServer.GetSessions(),
			a.minuteDecValues,
			a.minuteEncValues,
		)

		a.metrics.SetPacketsPerMinute(average)

		a.minuteDecValues = newDecValues
		a.minuteEncValues = newEncValues
	}
}

// TODO (darkrengarius): reimplement efficiently
/*// UpdateAverageNumberOfPacketsPerSecond is function which needs to called every second.
func (a *ServerAPI) updateAverageNumberOfPacketsPerSecond() {
	if a.dmsgServer != nil {
		newDecValues, newEncValues, average := calculateThroughput(
			a.dmsgServer.GetSessions(),
			a.secondDecValues,
			a.secondEncValues,
		)

		a.metrics.SetPacketsPerSecond(average)

		a.sMu.Lock()
		defer a.sMu.Unlock()
		a.secondDecValues = newDecValues
		a.secondEncValues = newEncValues
	}
}*/

func calculateThroughput(
	sessions map[cipher.PubKey]*dmsg.SessionCommon,
	previousDecValues map[*dmsg.SessionCommon]uint64,
	previousEncValues map[*dmsg.SessionCommon]uint64,
) (
	map[*dmsg.SessionCommon]uint64,
	map[*dmsg.SessionCommon]uint64,
	uint64,
) {

	var average uint64 = math.MaxUint64
	total := big.NewInt(0)
	count := big.NewInt(0)
	bigOne := big.NewInt(1)
	newDecValues := make(map[*dmsg.SessionCommon]uint64)
	newEncValues := make(map[*dmsg.SessionCommon]uint64)
	for _, session := range sessions {
		currentDecValue := session.GetDecNonce()
		currentEncValue := session.GetEncNonce()

		newDecValues[session] = currentDecValue
		newEncValues[session] = currentEncValue

		previousDecValue := previousDecValues[session]
		previousEncValue := previousEncValues[session]
		if currentDecValue != previousDecValue {
			if currentDecValue < previousDecValue {
				// overflow happened
				tmp := new(big.Int).SetUint64(currentDecValue)
				total = total.Add(total, tmp)
				tmp = new(big.Int).SetUint64(math.MaxUint64 - previousDecValue)
				total = total.Add(total, tmp)
			} else {
				tmp := new(big.Int).SetUint64(currentDecValue - previousDecValue)
				total = total.Add(total, tmp)
			}
			count = count.Add(count, bigOne)
		}
		if currentEncValue != previousEncValue {
			if currentEncValue < previousEncValue {
				// overflow happened
				tmp := new(big.Int).SetUint64(currentEncValue)
				total = total.Add(total, tmp)
				tmp = new(big.Int).SetUint64(math.MaxUint64 - previousEncValue)
				total = total.Add(total, tmp)
			} else {
				tmp := new(big.Int).SetUint64(currentEncValue - previousEncValue)
				total = total.Add(total, tmp)
			}
			count = count.Add(count, bigOne)
		}
	}
	if count.Uint64() == uint64(0) {
		average = 0
	} else {
		res := total.Div(total, count)
		if res.IsUint64() {
			average = res.Uint64()
		}
	}
	return newDecValues, newEncValues, average
}

// optionalProxyHeader restores go-proxyproto's pre-v0.15 lenient default (USE):
// honor a PROXY header when an upstream proxy sends one, but ALSO accept
// connections that arrive without one (direct dmsg clients doing a Noise
// handshake, health checks). v0.15 changed the zero-value default to REQUIRE,
// which rejects every header-less connection with ErrNoProxyProtocol — that
// silently broke the client-facing dmsg listener fleet-wide.
func optionalProxyHeader(proxyproto.ConnPolicyOptions) (proxyproto.Policy, error) {
	return proxyproto.USE, nil
}
