// Package visor pkg/visor/hypervisor.go
package visor

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgpty"
	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/tpviz"
	"github.com/skycoin/skywire/pkg/visor/usermanager"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

const (
	httpTimeout = 30 * time.Second
)
const (
	statusStop = iota
	statusStart
)

type Conn struct {
	Addr  dmsg.Addr
	SrvPK cipher.PubKey
	API   API
	PtyUI *dmsgPtyUI
}
type Hypervisor struct {
	c            visorconfig.HypervisorConfig
	visor        *Visor
	remoteVisors map[cipher.PubKey]Conn // connected remote visors to hypervisor
	dmsgC        *dmsg.Client
	users        *usermanager.UserManager
	mu           *sync.RWMutex
	vsMu         *sync.RWMutex
	selfConn     Conn
	logger       *logging.Logger
	tpvizServer  *tpviz.Server
	lanDmsg      *LANDmsgServer // embedded LAN DMSG server (nil if disabled)

	// summaryCache holds the most recent successful Summary per
	// remote visor PK so the UI can keep showing version / IP / etc
	// when the visor is briefly unreachable, instead of replacing
	// every field with "-". Cache hits are returned with Online=false
	// and OfflineSince/LastSeenAt set so the UI can dim them and
	// show a "last seen" timestamp. In-memory only — repopulates
	// from the next successful summary after a hypervisor restart.
	summaryCache   map[cipher.PubKey]cachedSummary
	summaryCacheMx sync.RWMutex

	// Runtime state for enable/disable toggle
	httpSrv   *http.Server // nil when disabled
	srvCancel context.CancelFunc
	enabled   bool
	enableMu  sync.Mutex
}

// cachedSummary is one entry in Hypervisor.summaryCache.
type cachedSummary struct {
	sum    *Summary
	seenAt time.Time
}

func NewHypervisor(config visorconfig.HypervisorConfig, visor *Visor, dmsgC *dmsg.Client) (*Hypervisor, error) {
	config.Cookies.TLS = config.EnableTLS

	boltUserDB, err := usermanager.NewBoltUserStore(config.DBPath)
	if err != nil {
		return nil, err
	}

	singleUserDB := usermanager.NewSingleUserStore("admin", boltUserDB)

	selfConn := Conn{
		Addr:  dmsg.Addr{PK: config.PK, Port: config.DmsgPort},
		API:   visor,
		PtyUI: nil,
	}
	mLogger := logging.NewMasterLogger()
	if visor != nil {
		mLogger = visor.MasterLogger()
		visor.remoteVisors = make(map[cipher.PubKey]Conn)
	}

	hv := &Hypervisor{
		c:            config,
		visor:        visor,
		remoteVisors: make(map[cipher.PubKey]Conn),
		dmsgC:        dmsgC,
		users:        usermanager.NewUserManager(mLogger, singleUserDB, config.Cookies),
		mu:           new(sync.RWMutex),
		vsMu:         new(sync.RWMutex),
		selfConn:     selfConn,
		logger:       mLogger.PackageLogger("hypervisor"),
		summaryCache: make(map[cipher.PubKey]cachedSummary),
	}

	if config.TPViz.Enable && visor != nil {
		tpvizCfg := tpviz.DefaultConfig()
		if config.TPViz.SurveyDir != "" {
			tpvizCfg.SurveyDir = config.TPViz.SurveyDir
		}
		if config.TPViz.CacheMaxAge > 0 {
			tpvizCfg.CacheMaxAge = config.TPViz.CacheMaxAge
		}
		hv.tpvizServer = tpviz.NewServer(tpvizCfg)
		adapter := &visorAPIAdapter{v: visor}
		hv.tpvizServer.SetVisorAPI(adapter, visor.conf.PK.Hex())
		hv.tpvizServer.Start()
	}

	return hv, nil
}
func (hv *Hypervisor) Enable(ctx context.Context) error {
	hv.enableMu.Lock()
	defer hv.enableMu.Unlock()

	if hv.enabled {
		return nil
	}

	srvCtx, cancel := context.WithCancel(ctx)
	hv.srvCancel = cancel

	// Start DMSG RPC listener for remote visors (waits for DMSG to be ready)
	if hv.dmsgC != nil {
		go func() {
			<-hv.dmsgC.Ready()
			hv.logger.WithField("addr", fmt.Sprintf("%s:%d", hv.c.PK, hv.c.DmsgPort)).
				Info("Serving hypervisor RPC over DMSG")
			if err := hv.ServeRPC(srvCtx, hv.c.DmsgPort); err != nil && !errors.Is(err, dmsg.ErrEntityClosed) {
				hv.logger.WithError(err).Error("Hypervisor DMSG RPC stopped")
			}
		}()
	}

	// Start HTTP server
	handler := hv.HTTPHandler()
	hv.httpSrv = &http.Server{
		Handler:           handler,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      10 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
	}

	lis, err := net.Listen("tcp", hv.c.HTTPAddr)
	if err != nil {
		cancel()
		return fmt.Errorf("hypervisor HTTP listen %s: %w", hv.c.HTTPAddr, err)
	}

	go func() {
		hv.logger.Infof("Hypervisor HTTP serving on %s", hv.c.HTTPAddr)
		var srvErr error
		if hv.c.EnableTLS {
			srvErr = hv.httpSrv.ServeTLS(lis, hv.c.TLSCertFile, hv.c.TLSKeyFile)
		} else {
			srvErr = hv.httpSrv.Serve(lis)
		}
		if srvErr != nil && !errors.Is(srvErr, http.ErrServerClosed) {
			hv.logger.WithError(srvErr).Error("Hypervisor HTTP server error")
		}
	}()

	// Start tpviz if configured
	if hv.tpvizServer != nil {
		hv.tpvizServer.Start()
	}

	hv.enabled = true
	hv.logger.Info("Hypervisor enabled")
	return nil
}
func (hv *Hypervisor) Disable() error {
	hv.enableMu.Lock()
	defer hv.enableMu.Unlock()

	if !hv.enabled {
		return nil
	}

	// Stop HTTP server
	if hv.httpSrv != nil {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		if err := hv.httpSrv.Shutdown(shutdownCtx); err != nil {
			hv.logger.WithError(err).Warn("Hypervisor HTTP shutdown error")
		}
		hv.httpSrv = nil
	}

	// Cancel DMSG RPC context (stops listener)
	if hv.srvCancel != nil {
		hv.srvCancel()
		hv.srvCancel = nil
	}

	// Disconnect all remote visors
	hv.vsMu.Lock()
	for pk, conn := range hv.remoteVisors {
		if conn.API != nil {
			hv.logger.Infof("Disconnecting remote visor %s", pk)
		}
		delete(hv.remoteVisors, pk)
	}
	hv.vsMu.Unlock()

	// Stop tpviz
	if hv.tpvizServer != nil {
		hv.tpvizServer.Stop()
	}

	hv.enabled = false
	hv.logger.Info("Hypervisor disabled")
	return nil
}
func (hv *Hypervisor) IsEnabled() bool {
	hv.enableMu.Lock()
	defer hv.enableMu.Unlock()
	return hv.enabled
}
func (hv *Hypervisor) ServeRPC(ctx context.Context, dmsgPort uint16) error {
	lis, err := hv.dmsgC.Listen(dmsgPort)
	if err != nil {
		return err
	}

	if hv.visor.isDTMReady() {
		// Track hypervisor node.
		if _, err := hv.visor.dmsgTracker.manager.ShouldGet(ctx, hv.visor.conf.PK); err != nil {
			hv.logger.WithField("addr", hv.c.DmsgDiscovery).WithError(err).Warn("Failed to dial tracker stream.")
		}
	}

	// setup local PTY using direct connection (bypasses DMSG for local visor)
	hv.mu.Lock()
	if hv.visor != nil && hv.visor.conf.Dmsgpty != nil && hv.visor.conf.Dmsgpty.CLINet != "" {
		hv.selfConn.PtyUI = setupLocalPtyUI(hv.visor.conf.Dmsgpty.CLINet, hv.visor.conf.Dmsgpty.CLIAddr)
	} else {
		// Fallback to DMSG if local CLI config not available
		hv.selfConn.PtyUI = setupDmsgPtyUI(hv.dmsgC, hv.c.PK)
	}
	hv.mu.Unlock()

	for {
		conn, err := lis.AcceptStream()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, dmsg.ErrEntityClosed) {
				return nil
			}
			hv.visor.MasterLogger().PackageLogger("hypervisor").WithError(err).Warn("Failed to accept dmsg stream, continuing...")
			continue
		}

		addr := conn.RawRemoteAddr()
		log := hv.visor.MasterLogger().PackageLogger(fmt.Sprintf("rpc_client:%s", addr.PK))

		visorConn := &Conn{
			Addr:  addr,
			SrvPK: conn.ServerPK(),
			API:   NewRPCClient(log, conn, RPCPrefix, skyenv.RPCTimeout),
			PtyUI: setupDmsgPtyUI(hv.dmsgC, addr.PK),
		}
		if hv.visor.isDTMReady() {
			if _, err := hv.visor.dmsgTracker.manager.ShouldGet(ctx, addr.PK); err != nil {
				log.WithField("addr", hv.c.DmsgDiscovery).WithError(err).Warn("Failed to dial tracker stream.")
			}
		}

		log.Debug("Accepted.")

		hv.mu.Lock()
		hv.visor.remoteVisors[addr.PK] = *visorConn
		hv.remoteVisors[addr.PK] = *visorConn
		hv.mu.Unlock()

		// Push LAN DMSG server info to the connected visor
		if hv.lanDmsg != nil {
			go func() {
				if err := visorConn.API.SetLANDmsgServer(LANDmsgServerInfo{
					Enabled: true,
					PK:      hv.lanDmsg.PK,
					Address: hv.lanDmsg.Address,
				}); err != nil {
					hv.logger.WithError(err).Debug("Failed to push LAN DMSG server info to visor")
				} else {
					hv.logger.WithField("visor", addr.PK.String()).
						Info("Pushed LAN DMSG server info to visor")
				}
			}()
		}
	}
}

type MockConfig struct {
	Visors            int
	MaxTpsPerVisor    int
	MaxRoutesPerVisor int
	EnableAuth        bool
}
type elementResponse struct {
	Success bool   `json:"success"`
	Error   string `json:"error"`
}

func (hv *Hypervisor) AddMockData(config MockConfig) error {
	r := rand.New(rand.NewSource(time.Now().UnixNano())) // nolint:gosec

	for i := 0; i < config.Visors; i++ {
		pk, client, err := NewMockRPCClient(r, config.MaxTpsPerVisor, config.MaxRoutesPerVisor)
		if err != nil {
			return err
		}

		hv.mu.Lock()
		hv.remoteVisors[pk] = Conn{
			Addr: dmsg.Addr{
				PK:   pk,
				Port: uint16(i), //nolint: gosec
			},
			API: client,
		}
		hv.mu.Unlock()
	}

	hv.c.EnableAuth = config.EnableAuth

	return nil
}
func (hv *Hypervisor) HTTPHandler() http.Handler {
	return hv.makeMux()
}

type logrusLogFormatter struct {
	logger *logging.Logger
}
type logrusLogEntry struct {
	logger *logging.Logger
	method string
	path   string
}

func (f *logrusLogFormatter) NewLogEntry(r *http.Request) middleware.LogEntry {
	return &logrusLogEntry{
		logger: f.logger,
		method: r.Method,
		path:   r.URL.Path,
	}
}
func (e *logrusLogEntry) Write(status, bytes int, header http.Header, elapsed time.Duration, extra interface{}) {
	e.logger.WithFields(logrus.Fields{
		"method":  e.method,
		"path":    e.path,
		"status":  status,
		"bytes":   bytes,
		"elapsed": elapsed.Round(time.Microsecond),
	}).Debug("HTTP request")
}
func (e *logrusLogEntry) Panic(v interface{}, stack []byte) {
	e.logger.WithField("stack", string(stack)).Errorf("HTTP panic: %v", v)
}
func (hv *Hypervisor) makeMux() chi.Router {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)

	if hv.visor != nil {
		if hv.visor.MasterLogger().GetLevel() == logrus.DebugLevel || hv.visor.MasterLogger().GetLevel() == logrus.TraceLevel {
			r.Use(middleware.RequestLogger(&logrusLogFormatter{logger: hv.logger}))
			r.Use(middleware.Recoverer)
		}
	}

	r.Use(httputil.SetLoggerMiddleware(hv.logger))

	r.Route("/", func(r chi.Router) {
		r.Route("/api", func(r chi.Router) {
			r.Use(middleware.Timeout(httpTimeout))

			r.Get("/ping", hv.getPong())

			r.Get("/csrf", hv.getCsrf())

			r.Get("/user-exists", hv.users.UserExists())

			if hv.c.EnableAuth {
				r.Group(func(r chi.Router) {
					r.Post("/create-account", hv.users.CreateAccount())
					r.Post("/login", hv.users.Login())
					r.Post("/logout", hv.users.Logout())
				})
			}

			r.Group(func(r chi.Router) {
				if hv.c.EnableAuth {
					r.Use(hv.users.Authorize)
				}

				r.Get("/user", hv.users.UserInfo())
				r.Post("/change-password", hv.users.ChangePassword())
				r.Get("/about", hv.getAbout())
				r.Get("/dmsg", hv.getDmsg())
				r.Get("/service-health", hv.getServiceHealth())
				r.Get("/route-setup-nodes/stats", hv.getRSNRemoteStats())
				r.Get("/network/transports", hv.getNetworkTransports())
				r.Get("/network/visor-uptime", hv.getNetworkVisorUptime())
				r.Get("/dmsg/sessions", hv.getDmsgSessions())
				r.Post("/dmsg/connect-all", hv.postDmsgConnectAll())
				r.Put("/dmsg/sessions-count", hv.putDmsgSessionsCount())

				r.Get("/lan-dmsg-server", hv.getLANDmsgServer())
				r.Get("/visors", hv.getVisors())
				r.Get("/visors-summary", hv.getAllVisorsSummary())
				r.Get("/network-view", hv.getNetworkView())
				r.Get("/reward-rules", hv.getRewardRules())
				r.Get("/visors/{pk}", hv.getVisor())
				r.Get("/visors/{pk}/summary", hv.getVisorSummary())
				r.Get("/visors/{pk}/health", hv.getHealth())
				r.Get("/visors/{pk}/uptime", hv.getUptime())
				r.Get("/visors/{pk}/apps", hv.getApps())
				r.Post("/visors/{pk}/apps", hv.postApp())
				r.Get("/visors/{pk}/apps/{app}", hv.getApp())
				r.Put("/visors/{pk}/apps/{app}", hv.putApp())
				r.Delete("/visors/{pk}/apps/{app}", hv.deleteApp())
				r.Get("/visors/{pk}/apps/{app}/help", hv.appHelp())
				r.Get("/visors/{pk}/router-settings", hv.getRouterSettings())
				r.Put("/visors/{pk}/router-settings", hv.putRouterSettings())
				r.Get("/visors/{pk}/apps/{app}/logs", hv.appLogsSince())
				r.Get("/visors/{pk}/apps/{app}/stats", hv.getAppStats())
				r.Get("/visors/{pk}/apps/{app}/connections", hv.appConnections())
				r.Get("/visors/{pk}/transport-types", hv.getTransportTypes())
				r.Get("/visors/{pk}/transports", hv.getTransports())
				r.Post("/visors/{pk}/transports", hv.postTransport())
				r.Get("/visors/{pk}/transports/{tid}", hv.getTransport())
				r.Delete("/visors/{pk}/transports/{tid}", hv.deleteTransport())
				r.Delete("/visors/{pk}/transports/", hv.deleteTransports())
				r.Put("/visors/{pk}/public-autoconnect", hv.putPublicAutoconnect())
				r.Get("/visors/{pk}/routes", hv.getRoutes())
				r.Post("/visors/{pk}/routes", hv.postRoute())
				r.Get("/visors/{pk}/routes/{rid}", hv.getRoute())
				r.Put("/visors/{pk}/routes/{rid}", hv.putRoute())
				r.Delete("/visors/{pk}/routes/{rid}", hv.deleteRoute())
				r.Delete("/visors/{pk}/routes/", hv.deleteRoutes())
				r.Get("/visors/{pk}/routegroups", hv.getRouteGroups())
				r.Post("/visors/{pk}/shutdown", hv.shutdown())
				r.Get("/visors/{pk}/runtime-logs", hv.getRuntimeLogs())
				r.Get("/visors/{pk}/runtime-stats", hv.getRuntimeStats())
				r.Get("/visors/{pk}/host-stats", hv.getHostStats())
				r.Post("/visors/{pk}/min-hops", hv.postMinHops())
				r.Get("/visors/{pk}/persistent-transports", hv.getPersistentTransports())
				r.Put("/visors/{pk}/persistent-transports", hv.putPersistentTransports())
				r.Get("/visors/{pk}/log/rotation", hv.getLogRotationInterval())
				r.Put("/visors/{pk}/log/rotation", hv.putLogRotationInterval())
				r.Get("/visors/{pk}/reward", hv.getRewardAddress())
				r.Put("/visors/{pk}/reward", hv.putRewardAddress())
				r.Delete("/visors/{pk}/reward", hv.deleteRewardAddress())
				r.Put("/visors/{pk}/public", hv.putIsPublic())
				r.Get("/visors/{pk}/public", hv.getIsPublic())
				r.Get("/visors/{pk}/runtime-config", hv.getRuntimeConfig())
				r.Put("/visors/{pk}/runtime-config", hv.putRuntimeConfig())
				r.Get("/visors/{pk}/local-transport-stats", hv.getLocalTransportStats())
				r.Get("/visors/{pk}/local-uptime-stats", hv.getLocalUptimeStats())
				r.Get("/visors/{pk}/ports", hv.getPorts())

				// Resolving proxy controls
				r.Get("/visors/{pk}/proxies", hv.getProxies())
				r.Post("/visors/{pk}/proxies/set", hv.postProxyEnabled())
				r.Post("/visors/{pk}/proxies/upstream", hv.postProxyUpstream())

				// Skynet port forwarding (legacy simple)
				r.Get("/visors/{pk}/skynet-ports", hv.getSkynetPorts())
				r.Post("/visors/{pk}/skynet-ports/register", hv.postRegisterSkynetPort())
				r.Post("/visors/{pk}/skynet-ports/deregister", hv.postDeregisterSkynetPort())

				// Rich port forwarding with metadata
				r.Get("/visors/{pk}/forwarded-ports", hv.getForwardedPorts())
				r.Post("/visors/{pk}/forwarded-ports/register", hv.postRegisterForwardedPort())
				r.Post("/visors/{pk}/forwarded-ports/update", hv.postUpdateForwardedPort())

				// Skynet reverse proxy (connect remote port to local)
				r.Get("/visors/{pk}/skynet-forwards", hv.getSkynetForwards())
				r.Post("/visors/{pk}/skynet-forwards/connect", hv.postSkynetConnect())
				r.Post("/visors/{pk}/skynet-forwards/disconnect", hv.postSkynetDisconnect())

				// Per-visor DMSG settings (used by the per-visor DMSG
				// tab in the hvui — supersedes the home-level
				// /api/dmsg/* endpoints which were local-visor-only).
				r.Get("/visors/{pk}/dmsg/sessions", hv.getVisorDmsgSessions())
				r.Post("/visors/{pk}/dmsg/connect-all", hv.postVisorDmsgConnectAll())
				r.Put("/visors/{pk}/dmsg/sessions-count", hv.putVisorDmsgSessionsCount())

				// Skychat password management.
				r.Get("/visors/{pk}/skychat/password", hv.getSkychatPassword())
				r.Put("/visors/{pk}/skychat/password", hv.putSkychatPassword())
				r.Delete("/visors/{pk}/skychat/password", hv.deleteSkychatPassword())
				// Skychat reverse-proxy: forward all calls under
				// /skychat/proxy/* to the local skychat HTTP server.
				r.HandleFunc("/visors/{pk}/skychat/proxy/*", hv.skychatProxyHandler())
			})
		})

		// Reward system proxy — fetches from the reward system via the visor's
		// DMSG client (or HTTP fallback), avoiding CORS issues and ensuring
		// DMSG-first access pattern.
		r.Get("/api/rewards/*", hv.proxyRewardSystem())

		// we don't enable `dmsgpty` endpoints for Windows
		r.Route("/pty", func(r chi.Router) {
			if hv.c.EnableAuth {
				r.Use(hv.users.Authorize)
			}

			r.Get("/{pk}", hv.getPty())
		})

		// Mount tp-viz UI if enabled
		if hv.tpvizServer != nil {
			r.Mount("/tp-viz", http.StripPrefix("/tp-viz", hv.tpvizServer.Handler()))
			// tp-viz bundle.js uses absolute paths like /api/health, /api/transports etc.
			// Mount the tp-viz handler at root to serve those paths too.
			tpvHandler := hv.tpvizServer.Handler()
			r.Get("/api/health", tpvHandler.ServeHTTP)
			r.Get("/api/transports", tpvHandler.ServeHTTP)
			r.Get("/api/uptimes", tpvHandler.ServeHTTP)
			r.Get("/api/services", tpvHandler.ServeHTTP)
			r.Get("/api/ip-groups", tpvHandler.ServeHTTP)
			r.Get("/api/local-visor", tpvHandler.ServeHTTP)
			r.Get("/api/tps/status", tpvHandler.ServeHTTP)
			r.Post("/api/tps/add-transport", tpvHandler.ServeHTTP)
			r.Post("/api/tps/remove-transport", tpvHandler.ServeHTTP)
			r.Get("/api/tps/refresh-transports", tpvHandler.ServeHTTP)
			r.Post("/api/local/add-transport", tpvHandler.ServeHTTP)
			r.Get("/api/dmsg/servers", tpvHandler.ServeHTTP)
			r.Get("/api/dmsg/entries", tpvHandler.ServeHTTP)
			r.Get("/api/dmsg/health", tpvHandler.ServeHTTP)
		}

		r.Handle("/*", http.FileServer(http.FS(hv.c.UIAssets)))
	})

	return r
}
func (hv *Hypervisor) log(r *http.Request) logrus.FieldLogger {
	return httputil.GetLogger(r)
}

type Csrf struct {
	Token string `json:"csrf_token"`
}
type About struct {
	PubKey        cipher.PubKey   `json:"public_key"` // The hypervisor's public key.
	Build         *buildinfo.Info `json:"build"`
	DmsgConnected bool            `json:"dmsg_connected"` // Whether the DMSG client is connected to servers.
	DmsgSessions  int             `json:"dmsg_sessions"`  // Number of active DMSG server sessions.
}
type LANDmsgServerInfo struct {
	Enabled bool          `json:"enabled"`
	PK      cipher.PubKey `json:"pk,omitempty"`
	Address string        `json:"address,omitempty"`
}
type dmsgSessionsCountRequest struct {
	Count int `json:"count"`
}
type Health struct {
	Status int `json:"status"`
	*HealthInfo
}

func makeSummaryResp(online, hyper bool, sum *Summary) Summary {
	sum.Online = online
	sum.IsHypervisor = hyper
	return *sum
}

type LogsRes struct {
	LastLogTimestamp string   `json:"last_log_timestamp"`
	Logs             []string `json:"logs"`
}
type publicAutoconnectReq struct {
	PublicAutoconnect bool `json:"public_autoconnect"`
}
type routingRuleResp struct {
	Key     routing.RouteID      `json:"key"`
	Rule    string               `json:"rule"`
	Summary *routing.RuleSummary `json:"rule_summary,omitempty"`
}

func makeRoutingRuleResp(key routing.RouteID, rule routing.Rule, summary bool) routingRuleResp {
	resp := routingRuleResp{
		Key:  key,
		Rule: hex.EncodeToString(rule),
	}

	if summary {
		resp.Summary = rule.Summary()
	}

	return resp
}

type routeGroupResp struct {
	ConsumeRuleID routing.RouteID               `json:"consume_rule_id"`
	FwdRuleID     routing.RouteID               `json:"fwd_rule_id"`
	Desc          routing.RouteDescriptorFields `json:"desc"`
	FwdNextTpID   string                        `json:"fwd_next_tp_id,omitempty"`
	Initiator     bool                          `json:"initiator"`
	Hops          []RouteHopInfo                `json:"hops,omitempty"`
}

func makeRouteGroupResp(info RouteGroupInfo) routeGroupResp {
	return routeGroupResp(info)
}

type isPublicResp struct {
	IsPublic bool `json:"is_public"`
}
type httpCtx struct {
	// Hypervisor
	Conn

	// isRemote is true when the request targets a remote visor (not the local hypervisor).
	isRemote bool

	// App
	App *appserver.AppState

	// Transport
	Tp *TransportSummary

	// Route
	RtKey routing.RouteID
}
type (
	valuesFunc  func(w http.ResponseWriter, r *http.Request) (*httpCtx, bool)
	handlerFunc func(w http.ResponseWriter, r *http.Request, ctx *httpCtx)
)

const remoteVisorTimeout = 15 * time.Second

type timeoutResponseWriter struct {
	header     http.Header
	body       []byte
	statusCode int
	written    bool
}

func newTimeoutResponseWriter() *timeoutResponseWriter {
	return &timeoutResponseWriter{header: make(http.Header), statusCode: http.StatusOK}
}
func (tw *timeoutResponseWriter) Header() http.Header  { return tw.header }
func (tw *timeoutResponseWriter) WriteHeader(code int) { tw.statusCode = code; tw.written = true }
func (tw *timeoutResponseWriter) Write(b []byte) (int, error) {
	tw.body = append(tw.body, b...)
	tw.written = true
	return len(b), nil
}
func (tw *timeoutResponseWriter) copyTo(w http.ResponseWriter) {
	for k, v := range tw.header {
		w.Header()[k] = v
	}
	w.WriteHeader(tw.statusCode)
	w.Write(tw.body) //nolint:errcheck,gosec
}
func pkFromParam(r *http.Request, key string) (cipher.PubKey, error) {
	pk := cipher.PubKey{}
	err := pk.UnmarshalText([]byte(chi.URLParam(r, key)))

	return pk, err
}
func uuidFromParam(r *http.Request, key string) (uuid.UUID, error) {
	return uuid.Parse(chi.URLParam(r, key))
}
func ridFromParam(r *http.Request, key string) (routing.RouteID, error) {
	rid, err := strconv.ParseUint(chi.URLParam(r, key), 10, 32)
	if err != nil {
		return 0, errors.New("invalid route ID provided")
	}

	return routing.RouteID(rid), nil
}
func strSliceFromQuery(r *http.Request, key string, defaultVal []string) []string {
	slice, ok := r.URL.Query()[key]
	if !ok {
		return defaultVal
	}

	return slice
}
func pkSliceFromQuery(r *http.Request, key string, defaultVal []cipher.PubKey) ([]cipher.PubKey, error) {
	qPKs, ok := r.URL.Query()[key]
	if !ok {
		return defaultVal, nil
	}

	pks := make([]cipher.PubKey, len(qPKs))

	for i, qPK := range qPKs {
		pk := cipher.PubKey{}
		if err := pk.UnmarshalText([]byte(qPK)); err != nil {
			return nil, err
		}

		pks[i] = pk
	}

	return pks, nil
}

type dmsgPtyUI struct {
	PtyUI *dmsgpty.UI
}

func setupDmsgPtyUI(dmsgC *dmsg.Client, visorPK cipher.PubKey) *dmsgPtyUI {
	ptyDialer := dmsgpty.DmsgUIDialer(dmsgC, dmsg.Addr{PK: visorPK, Port: skyenv.DmsgPtyPort})
	return &dmsgPtyUI{
		PtyUI: dmsgpty.NewUI(ptyDialer, dmsgpty.DefaultUIConfig()),
	}
}
func setupLocalPtyUI(cliNet, cliAddr string) *dmsgPtyUI {
	ptyDialer := dmsgpty.NetUIDialer(cliNet, cliAddr)
	return &dmsgPtyUI{
		PtyUI: dmsgpty.NewUI(ptyDialer, dmsgpty.DefaultUIConfig()),
	}
}
