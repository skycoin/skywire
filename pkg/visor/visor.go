// Package visor implements skywire visor.
package visor

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/orandin/lumberjackrus"
	"github.com/sirupsen/logrus"
	"github.com/toqueteos/webbrowser"

	"github.com/skycoin/skywire/pkg/app/appdisc"
	"github.com/skycoin/skywire/pkg/app/appevent"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/app/launcher"
	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cmdutil"
	dmsgcmdutil "github.com/skycoin/skywire/pkg/dmsg/cmdutil"
	dmsgdisc "github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgpty"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/rfclient"
	"github.com/skycoin/skywire/pkg/router"
	"github.com/skycoin/skywire/pkg/serviceuptime"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/transport/network"
	"github.com/skycoin/skywire/pkg/transport/network/addrresolver"
	"github.com/skycoin/skywire/pkg/transport/network/stcp"
	tptypes "github.com/skycoin/skywire/pkg/transport/types"
	"github.com/skycoin/skywire/pkg/utclient"
	"github.com/skycoin/skywire/pkg/visor/dmsgtracker"
	"github.com/skycoin/skywire/pkg/visor/logserver"
	"github.com/skycoin/skywire/pkg/visor/logstore"
	"github.com/skycoin/skywire/pkg/visor/stats"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
	"github.com/skycoin/skywire/pkg/visor/visorinit"
)

var (
	// ErrVisorNotAvailable represents error for unavailable visor
	ErrVisorNotAvailable = errors.New("no visor available")
	// ErrAppProcNotRunning represents lookup error for App related calls.
	ErrAppProcNotRunning = errors.New("no process of given app is running")
	// ErrProcNotAvailable represents error for unavailable process manager
	ErrProcNotAvailable = errors.New("no process manager available")
	// ErrTrpMangerNotAvailable represents error for unavailable transport manager
	ErrTrpMangerNotAvailable = errors.New("no transport manager available")
	// ErrAppLauncherNotAvailable represents error for unavailable app launcher
	ErrAppLauncherNotAvailable = errors.New("no app launcher available")
	// ErrRouterNotReady represents error for router not yet initialized
	ErrRouterNotReady = errors.New("router not ready")
)

const (
	supportedProtocolVersion = "0.1.0"
	shortHashLen             = 6
	// moduleShutdownTimeout is the timeout given to a module to shutdown cleanly.
	// Otherwise the shutdown logic will continue and report a timeout error.
	moduleShutdownTimeout = time.Second * 4
	runtimeLogMaxEntries  = 300
)

var uiAssets = initUI()

var mLog = initLogger()

// Visor provides messaging runtime for Apps by setting up all
// necessary connections and performing messaging gateway functions.
type Visor struct {
	closeStack []closer

	ctx      context.Context // stored so RPC handlers can derive child contexts
	conf     *visorconfig.V1
	log      *logging.Logger
	logstore logstore.Store
	logBcast *logging.Broadcaster // fans entries out to gRPC StreamAppLogs subscribers

	startedAt       time.Time
	startupComplete chan struct{}
	uptimeTracker   utclient.APIClient

	ebc                *appevent.Broadcaster // event broadcaster
	dmsgC              *dmsg.Client
	dmsgDC             *dmsg.Client       // dmsg direct client
	dClient            dmsgdisc.APIClient // dmsg direct api client
	dmsgHTTP           *http.Client       // dmsghttp client
	dmsgHTTPReady      chan struct{}      // closed when dmsgHTTP is set
	awaitSetupListener *dmsg.Listener     // pre-opened DmsgAwaitSetupPort listener; consumed by initRouter

	// dmsgWL is the live, in-memory whitelist shared with the running
	// dmsgpty.Host and (when scp is enabled) dmsgscp.Host. VisorCat's
	// listen-mode auth reads this same reference so runtime mutations
	// made via dmsgpty.WhitelistGateway are honored without rebuilding
	// from v.conf. Mirrors scp's precedence: when Dmsgscp.Whitelist is
	// non-empty, this holds the scp-specific list (so cat behaves like
	// scp); otherwise it holds the dmsgpty list that scp also reuses.
	// nil when initDmsgpty was skipped (Dmsgpty config absent).
	dmsgWL dmsgpty.Whitelist

	// dmsgPty is the live dmsgpty.Host instance, exposed so the visor
	// RPC layer can drive remote pty operations (Exec, list) without
	// going back through the host's CLI control listener (the unix
	// socket / TCP loopback the standalone dmsgpty-cli uses). nil when
	// initDmsgpty was skipped.
	dmsgPty *dmsgpty.Host

	// DMSG tracker state
	dmsgTracker dtmState

	// STUN client state
	stun stunState

	tpM      *transport.Manager
	arClient addrresolver.APIClient
	router   router.Router
	rfClient rfclient.Client

	procM       appserver.ProcManager // proc manager
	appL        *launcher.AppLauncher // app launcher
	serviceDisc appdisc.Factory
	initLock    *sync.RWMutex
	closeMu     *sync.RWMutex
	// when module is failed it pushes its error to this channel
	// used by init and shutdown to show/check for any residual errors
	// produced by concurrent parts of modules
	runtimeErrors chan error

	// Aggregate health flag — set by ALL subsystems; any one unhealthy
	// flips it to connecting. Kept for backward compatibility with the
	// /visors/{pk}/health API response.
	isServicesHealthy *internalHealthInfo
	// Per-subsystem health flags so the UI can show which subsystem is
	// actually unhealthy instead of one generic "services" label.
	isUptimeTrackerHealthy    *internalHealthInfo
	isAutoconnectHealthy      *internalHealthInfo
	isTransportabilityHealthy *internalHealthInfo
	remoteVisors              map[cipher.PubKey]Conn // remote hypervisors the visor is attempting to connect to
	connectedHypervisors      map[cipher.PubKey]bool // remote hypervisors the visor is currently connected to

	// Allowed ports for app connections (legacy — being replaced by forwardedPorts)
	allowed allowedPortsState

	// Rich port forwarding with metadata, whitelist, landing page integration
	forwardedPorts *ForwardedPorts

	// Persisted dmsg-server discovery entries — survives restarts so the
	// bootstrap direct client uses the addresses last learned from
	// dmsg-discovery, not the (potentially stale) addresses in skywire.json.
	dmsgServersCache *DmsgServersCache

	// DMSG listeners for forwarded ports (dmsg=true). Each entry is a
	// cancel function that stops the listener goroutine.
	dmsgFwdMu        sync.Mutex
	dmsgFwdListeners map[int]context.CancelFunc

	// Service handler registry — maps ports to connection handlers
	// so the sky-forwarding server can dispatch without localhost TCP.
	services *ServiceRegistry

	// Skywire ping state
	ping pingState

	// DMSG ping state
	dmsgPing dmsgPingState

	// Survey state
	survey surveyState

	// Cached geolocation data from geoIP service
	geo geoState

	// UI server state (dynamically started/stopped via RPC)
	ui uiState

	// Embedded Transport Setup Node (nil if tps_sk not configured)
	embeddedTPS *embeddedTPS

	// Embedded Route Setup Node (nil if route_setup_sk not configured)
	embeddedRouteSetup *EmbeddedRouteSetup

	// arSelf caches our own AR-side bind state (one entry per transport
	// type, populated by the AR self-refresh loop). Used by ARSelfInfo
	// to answer the CLI without round-tripping AR.
	arSelf arSelfState

	// Visor-local telemetry tracker (nil if Stats.Disabled).
	// Owns the bbolt store at <local_path>/stats.db, samples
	// transport bw/latency and tier/service uptime once a minute,
	// and is read by the /stats/* HTTP handlers and the CXO
	// publisher.
	statsTracker *stats.Tracker

	// Service-self uptime recorder (nil when not wired). Owns the
	// bbolt store at <local_path>/uptime.db, refreshes the current
	// session row on a 60s tick, and surfaces both the visor's
	// running version and its session history via /uptime/* HTTP
	// endpoints. Distinct from statsTracker: this is for
	// "was-the-visor-itself-running" with version provenance, not
	// transport/tier/service uptime.
	uptimeRecorder *serviceuptime.Recorder

	// User-registered CXO TreeStore feeds keyed by name. Each feed
	// is its own dmsg listener on a distinct port; the system feed
	// (telemetry) is published separately by initStats and shows up
	// in ListCXOFeeds via systemCXOFeed below.
	cxoUserFeeds   map[string]*cxoUserFeed
	cxoUserFeedsMu sync.Mutex

	// pairing holds the chat-pair feed manager + bbolt store +
	// inbound message ring. Populated by init_pairing.go after
	// dmsgC is up. nil when pairing is disabled (dmsgC absent or
	// init failed); RPC handlers surface ErrPairingDisabled.
	pairing pairingState

	// grouping holds the chat-group feed manager + bbolt store +
	// inbound message ring. Same shape as pairing, brought up by
	// init_group.go. nil when group chat is disabled.
	grouping groupState

	// Embedded DMSG Web resolver (nil if dmsg_web.enable is false).
	// Provides a localhost SOCKS5 proxy that resolves .dmsg hosts.
	embeddedDmsgWeb *EmbeddedDmsgWeb

	// Embedded Skynet Web resolver (nil if skynet_web.enable is false).
	// Like embeddedDmsgWeb but for .skynet hosts, dialed via the visor's router.
	embeddedSkynetWeb *EmbeddedSkynetWeb
	// Shared VStreamMux for skynet forwarding (route ID 0).
	// Used by both the forwarding server (Accept) and the skynetweb dialer (Dial).
	skynetFwdMux *transport.VStreamMux
	// VStreamMux for direct skywire-network app dials (route ID 0,
	// AppDirectPacket type). Lets skysocks-client / vpn-client and
	// any other skywire-network apps reach their server-side
	// counterparts over an existing direct transport without going
	// through the setup-node-mediated route group machinery. Falls
	// back to router.DialRoutes when no direct transport exists.
	appDirectMux *transport.VStreamMux

	// Hypervisor instance (nil if never initialized; may be enabled/disabled at runtime)
	hvInstance *Hypervisor

	// Public autoconnect runtime control
	autoconnect autoconnectState

	// Public visor registration with validation
	publicVisor publicVisorState

	// DMSG server latency tracking (for preferring low-latency servers)
	dmsgLatency dmsgLatencyState

	// Setup node health tracking (TPS and RSN)
	nodeHealthTracker *NodeHealthTracker

	// Log server references for health stats
	logServer logServerState

	// STCP PK table for runtime address injection (tp add -t stcp --addr)
	stcpTable stcp.PKTable

	// Lazy-initialized CXO subscriber for TPD's network-wide
	// transport-metrics feed. Created on the first hvui-driven
	// FetchTransportMetricsCXO call and kept alive thereafter; the
	// hvui handler reads cached values via Subscriber.Get and falls
	// back to HTTP /metrics when a path hasn't been published yet.
	tpdMetricsSub   *tpdMetricsSubscriber
	tpdMetricsSubMu sync.RWMutex

	// Same lazy-on-demand pattern for TPD's network-wide visor-uptime
	// feed (the /uptimes?v=v3 mirror). Drives the hvui Network Uptime
	// tab. Falls back to DMSG-HTTP / HTTP when the cache misses.
	tpdUptimeSub   *tpdUptimeSubscriber
	tpdUptimeSubMu sync.RWMutex

	// cxoSubMgr is the on-demand CXO subscription manager that owns
	// the network-visualizer / metrics-tab data feeds (SD services,
	// DMSG-D clients-by-server, TPD aggregates). Constructed in
	// initCXOSubscriptionManager when the hypervisor is enabled;
	// nil otherwise. Tabs that source CXO data call AcquireFor on
	// open and ReleaseFor on close.
	cxoSubMgr *CXOSubscriptionManager
	// Records the last unix-nano time a Connect attempt failed so we
	// can throttle re-dials while TPD's publisher is down. Read
	// lock-free on the hot path (atomic), written from inside the
	// connect-fail branch under the outer mutex.
	tpdUptimeLastFail atomic.Int64
}

// pingState manages Skywire transport ping connections.
type pingState struct {
	conns    map[cipher.PubKey]ping
	mu       *sync.Mutex
	pcktSize int
}

// geoState caches geolocation data.
type geoState struct {
	data *GeoData
	mu   sync.RWMutex
}

// uiState manages the dynamically started/stopped UI server.
type uiState struct {
	mu      sync.Mutex
	server  *http.Server
	addr    string
	running bool
}

// autoconnectState manages public autoconnect runtime control.
type autoconnectState struct {
	mu      sync.Mutex
	cancel  context.CancelFunc
	running bool
}

// publicVisorState manages public visor registration.
type publicVisorState struct {
	updater *appdisc.PublicVisorUpdater
	mu      sync.Mutex
}

// dtmState manages the DMSG tracker manager lifecycle.
type dtmState struct {
	manager   *dmsgtracker.Manager
	ready     chan struct{}
	readyOnce sync.Once
}

// stunState manages STUN client state.
type stunState struct {
	client    *network.StunDetails
	ready     chan struct{}
	readyOnce sync.Once
}

// dmsgPingState manages DMSG ping connections.
type dmsgPingState struct {
	conns map[cipher.PubKey]ping
	mu    *sync.Mutex
}

// dmsgLatencyState tracks DMSG server latencies.
type dmsgLatencyState struct {
	servers map[cipher.PubKey]time.Duration
	mu      sync.RWMutex
}

// allowedPortsState manages ports allowed for app connections.
type allowedPortsState struct {
	ports map[int]bool
	mu    *sync.RWMutex
}

// surveyState manages system survey data.
type surveyState struct {
	data visorconfig.Survey
	mu   *sync.RWMutex
}

// logServerState holds log server API references for health stats.
type logServerState struct {
	api      *logserver.API
	localAPI *logserver.API // localhost log server (optional)
}

// Close stack: tracks cleanup functions pushed during initialization.

type closeFn func() error

type closer struct {
	src string
	fn  closeFn
}

func (v *Visor) pushCloseStack(src string, fn closeFn) {
	v.initLock.Lock()
	defer v.initLock.Unlock()
	v.closeStack = append(v.closeStack, closer{src, fn})
}

// MasterLogger returns the underlying master logger (currently contained in visor config).
func (v *Visor) MasterLogger() *logging.MasterLogger {
	return v.conf.MasterLogger()
}

func reload(v *Visor) error {
	if confPath == visorconfig.Stdin {
		v.log.Error("Cannot reload visor ; config was piped via stdin")
		return nil
	}
	if err := v.Close(); err != nil {
		v.log.WithError(err).Error("Visor closed with error.")
		return err
	}
	v = nil
	return run(context.Background(), nil)
}

// Run is the exported entry point used by pkg/services/visor. parentCtx
// becomes the parent of the SignalContext run() builds internally —
// when the multi-service supervisor cancels its ctx, the visor's
// signal-derived ctx cancels too and the visor unwinds cleanly.
//
// Behaves identically to the standalone cobra path when parentCtx is
// context.Background(), which is what runApp / runAppSystray pass.
func Run(parentCtx context.Context, conf *visorconfig.V1) error {
	return run(parentCtx, conf)
}

// run is the implementation for both the standalone cobra entry point
// and the multi-service supervisor. parentCtx is the parent of the
// SignalContext built around it.
func run(parentCtx context.Context, conf *visorconfig.V1) error {
	store, hook := logstore.MakeStore(runtimeLogMaxEntries)
	mLog.AddHook(hook)

	// logBroadcaster fans out logrus entries to gRPC StreamAppLogs
	// subscribers. Attached to both the package-default mLog and the
	// config's MasterLogger so PackageLogger entries from anywhere in
	// the visor end up visible.
	logBroadcaster := logging.NewBroadcaster()
	mLog.AddHook(logBroadcaster)

	stopPProf := dmsgcmdutil.InitPProf(mLog.PackageLogger("pprof"), pprofMode, pprofAddr)
	defer stopPProf()

	if conf == nil {
		conf = initConfig()
	}

	conf.MasterLogger().AddHook(hook)
	conf.MasterLogger().AddHook(logBroadcaster)

	if disableHypervisorPKs {
		conf.Hypervisors = []cipher.PubKey{}
	}

	pubkey := cipher.PubKey{}
	if remoteHypervisorPKs != "" {
		hypervisorPKsSlice := strings.Split(remoteHypervisorPKs, ",")
		for _, pubkeyString := range hypervisorPKsSlice {
			if err := pubkey.Set(pubkeyString); err != nil {
				mLog.Warnf("Cannot add %s PK as remote hypervisor PK due to: %s", pubkeyString, err)
				continue
			}
			mLog.Infof("%s PK added as remote hypervisor PK", pubkeyString)
			conf.Hypervisors = append(conf.Hypervisors, pubkey)
		}
	}

	if logLvl != "" {
		//validate & set log level
		_, err := logging.LevelFromString(logLvl)
		if err != nil {
			mLog.WithError(err).Error("Invalid log level specified: ", logLvl)
		} else {
			conf.LogLevel = logLvl
			mLog.Info("setting log level to: ", logLvl)
		}
	}

	if conf.Hypervisor != nil {
		conf.Hypervisor.UIAssets = *uiAssets
	}

	// Open the service-self uptime recorder before NewVisor so a panic
	// during subsystem init still leaves a session row with the
	// running binary's version. Failure to open is non-fatal — the
	// visor brings up without it; only the /uptime/* surface is missing.
	uptimeRec, uptimeErr := serviceuptime.New(
		filepath.Join(conf.LocalPath, "uptime.db"),
		serviceuptime.Config{
			Service: "skywire-visor",
			Version: buildinfo.Version(),
			Commit:  buildinfo.Commit(),
		},
	)
	if uptimeErr != nil {
		mLog.WithError(uptimeErr).Warn("Service-self uptime recorder unavailable")
	} else {
		defer func() { _ = uptimeRec.Close() }() //nolint:errcheck
	}

	ctx, cancel := cmdutil.SignalContext(parentCtx, mLog)
	vis, ok := NewVisor(ctx, conf)
	if !ok {
		select {
		case <-ctx.Done():
			mLog.Info("Visor closed early.")
		default:
			return fmt.Errorf("failed to start visor")
		}
		return nil
	}

	if uptimeRec != nil {
		vis.SetUptimeRecorder(uptimeRec)
		uptimeRec.Start()
	}

	stopVisorFn = func() {
		if err := vis.Close(); err != nil {
			mLog.WithError(err).Error("Visor closed with error.")
		}
		cancel()
	}
	vis.SetLogstore(store)
	vis.SetLogBroadcaster(logBroadcaster)
	//	vis.uiAssets = uiAssets
	if launchBrowser {
		if conf.Hypervisor == nil {
			mLog.Errorln("Hypervisor not started - hypervisor UI unavailable")
		}
		runBrowser(conf.Hypervisor.HTTPAddr, conf.Hypervisor.EnableTLS)
		launchBrowser = false
	}
	// Wait.
	<-ctx.Done()
	stopVisorFn()
	return nil
}

// NewVisor constructs new Visor.
func NewVisor(ctx context.Context, conf *visorconfig.V1) (*Visor, bool) {
	if conf == nil {
		conf = initConfig()
	}

	if isForceColor {
		setForceColor(conf)
	}

	v := &Visor{
		log:                       conf.MasterLogger().PackageLogger("visor"),
		conf:                      conf,
		dmsgHTTPReady:             make(chan struct{}),
		initLock:                  new(sync.RWMutex),
		closeMu:                   new(sync.RWMutex),
		isServicesHealthy:         newInternalHealthInfo(),
		isUptimeTrackerHealthy:    newInternalHealthInfo(),
		isAutoconnectHealthy:      newInternalHealthInfo(),
		isTransportabilityHealthy: newInternalHealthInfo(),
		connectedHypervisors:      make(map[cipher.PubKey]bool),
		allowed: allowedPortsState{
			ports: make(map[int]bool),
			mu:    new(sync.RWMutex),
		},
		forwardedPorts:   NewForwardedPorts(filepath.Join(conf.LocalPath, "forwarded_ports.json")),
		dmsgServersCache: NewDmsgServersCache(filepath.Join(conf.LocalPath, "dmsg_servers.json")),
		dmsgFwdListeners: make(map[int]context.CancelFunc),
		dmsgTracker: dtmState{
			ready: make(chan struct{}),
		},
		stun: stunState{
			ready: make(chan struct{}),
		},
		arSelf: newARSelfState(),
		ping: pingState{
			conns: make(map[cipher.PubKey]ping),
			mu:    new(sync.Mutex),
		},
		dmsgPing: dmsgPingState{
			conns: make(map[cipher.PubKey]ping),
			mu:    new(sync.Mutex),
		},
		survey: surveyState{
			mu: new(sync.RWMutex),
		},
		dmsgLatency: dmsgLatencyState{
			servers: make(map[cipher.PubKey]time.Duration),
		},
	}
	v.isServicesHealthy.init()
	v.isUptimeTrackerHealthy.init()
	v.isAutoconnectHealthy.init()
	v.isTransportabilityHealthy.init()

	logLevel := conf.LogLevel
	if logLevel == "" {
		logLevel = "debug"
	}
	if logLvl, err := logging.LevelFromString(logLevel); err != nil {
		v.log.WithError(err).Warn("Failed to read log level from config.")
	} else {
		v.conf.MasterLogger().SetLevel(logLvl)
	}

	v.ctx = ctx
	v.services = NewServiceRegistry()
	v.startedAt = time.Now()
	v.startupComplete = make(chan struct{})

	// Set Go memory limit based on config
	applyMemoryLimit(v.log, conf.MemoryLimit)
	if isStoreLog {
		storeLog(conf)
	}
	log := v.MasterLogger().PackageLogger("visor:startup")
	log.WithField("public_key", conf.PK).
		Info("Begin startup.")
	ctx = context.WithValue(ctx, visorKey, v)
	v.runtimeErrors = make(chan error)
	ctx = context.WithValue(ctx, runtimeErrsKey, v.runtimeErrors)
	if dmsgServer != "" {
		type dmsgServerKey struct{}
		ctx = context.WithValue(ctx, dmsgServerKey{}, dmsgServer)
	}
	registerModules(v.MasterLogger())
	var mainModule visorinit.Module
	if v.conf.Hypervisor == nil {
		mainModule = vis
	} else {
		log.Info("main module set to hypervisor")

		mainModule = hv
	}
	// run Transport module in a non blocking mode
	go tm.InitConcurrent(ctx)
	mainModule.InitConcurrent(ctx)
	if err := mainModule.Wait(ctx); err != nil {
		select {
		case <-ctx.Done():
			if err := v.Close(); err != nil {
				log.WithError(err).Error("Visor closed with error.")
			}
		default:
			log.Error(err)
		}
		return nil, false
	}
	if err := tm.Wait(ctx); err != nil {
		select {
		case <-ctx.Done():
			if err := v.Close(); err != nil {
				log.WithError(err).Error("Visor closed with error.")
			}
		default:
			log.Error(err)
		}
		return nil, false
	}
	// todo: rewrite to be infinite concurrent loop that will watch for
	// module runtime errors and act on it (by stopping visor for example)
	if !v.processRuntimeErrs() {
		return nil, false
	}
	log.Info("Startup complete.")
	close(v.startupComplete)
	return v, true
}

func (v *Visor) processRuntimeErrs() bool {
	ok := true
	for {
		select {
		case err := <-v.runtimeErrors:
			v.log.Error(err)
			ok = false
		default:
			return ok
		}
	}
}

func (v *Visor) isStunReady() bool {
	select {
	case <-v.stun.ready:
		return true
	default:
		return false
	}
}

func initLogger() *logging.MasterLogger {
	mLog := logging.NewMasterLogger()
	return mLog
}

// runBrowser opens the hypervisor interface in the browser
func runBrowser(httpAddr string, enableTLS bool) {
	log := mLog.PackageLogger("visor:launch-browser")

	addr := httpAddr
	if addr[0] == ':' {
		addr = "localhost" + addr
	}
	if addr[:4] != "http" {
		if enableTLS {
			addr = "https://" + addr
		} else {
			addr = "http://" + addr
		}
	}
	go func() {
		if !isHvRunning(addr, 5) {
			log.Error("Cannot open hypervisor in browser: status check failed")
			return
		}
		if err := webbrowser.Open(addr); err != nil {
			log.WithError(err).Error("webbrowser.Open failed")
		}
	}()
}

func isHvRunning(addr string, retries int) bool {
	url := addr + "/api/ping"
	for i := 0; i < retries; i++ {
		time.Sleep(500 * time.Millisecond)
		resp, err := http.Get(url) // nolint: gosec
		if err != nil {
			continue
		}
		err = resp.Body.Close()
		if err != nil {
			continue
		}
		if resp.StatusCode < 400 {
			return true
		}
	}
	return false
}

// Close safely stops spawned Apps and Visor.
func (v *Visor) Close() error {
	if v == nil {
		return nil
	}
	// todo: with timout: wait for the module to initialize,
	// then try to stop it
	// don't need waitgroups this way because modules are concurrent anyway
	// start what you need in a module's goroutine?
	//

	log := v.MasterLogger().PackageLogger("visor:shutdown")
	log.Info("Begin shutdown.")

	// Tear down lazy CXO subscribers (TPD metrics + uptime) before
	// the closeStack runs, since they hold dmsg conns that closeStack
	// also touches via the dmsg client shutdown.
	v.closeTPDMetricsSubscriber()
	v.closeTPDUptimeSubscriber()
	if v.cxoSubMgr != nil {
		v.cxoSubMgr.Close()
	}

	// Cleanly close ongoing raw TCP forward conns
	for _, forwardConn := range appnet.GetAllRawTCPForwardConns() {
		err := forwardConn.Close()
		if err != nil {
			log.WithError(err).Warn("Forward conn stopped with unexpected result.")
			continue
		}
	}

	for i := len(v.closeStack) - 1; i >= 0; i-- {
		cl := v.closeStack[i]

		start := time.Now()
		errCh := make(chan error, 1)
		t := time.NewTimer(moduleShutdownTimeout)

		log := v.MasterLogger().PackageLogger(fmt.Sprintf("visor:shutdown:%s", cl.src)).
			WithField("func", fmt.Sprintf("[%d/%d]", i+1, len(v.closeStack)))
		log.Debug("Shutting down module...")

		go func(cl closer) {
			errCh <- cl.fn()
			close(errCh)
		}(cl)

		select {
		case err := <-errCh:
			t.Stop()
			if err != nil {
				log.WithError(err).WithField("elapsed", time.Since(start)).Warn("Module stopped with unexpected result.")
				continue
			}
			log.WithField("elapsed", time.Since(start)).Debug("Module stopped cleanly.")

		case <-t.C:
			log.WithField("elapsed", time.Since(start)).Error("Module timed out.")
		}
	}
	v.processRuntimeErrs()
	log.Info("Shutdown complete. Goodbye!")
	v = nil
	return nil
}

func (v *Visor) isDTMReady() bool {
	select {
	case <-v.dmsgTracker.ready:
		return true
	default:
		return false
	}
}

// SetLogstore sets visor runtime logstore
func (v *Visor) SetLogstore(store logstore.Store) {
	v.logstore = store
}

// SetLogBroadcaster wires the visor with the master logger's
// Broadcaster hook. Stored on the Visor so the gRPC adapter can
// forward Subscribe calls through to it.
func (v *Visor) SetLogBroadcaster(b *logging.Broadcaster) {
	v.logBcast = b
}

// SubscribeLogs subscribes to log entries matching the filter.
// Wraps the broadcaster so callers don't need a direct handle.
// Returns nil channel + nil cancel when the broadcaster is not
// installed (e.g. tests).
func (v *Visor) SubscribeLogs(f logging.Filter, capacity int) (<-chan *logrus.Entry, func() uint64) {
	if v.logBcast == nil {
		return nil, func() uint64 { return 0 }
	}
	return v.logBcast.Subscribe(f, capacity)
}

// SubscribeGroupMessages registers a live subscriber on the visor's
// group inbox. Every message delivered to the inbox from this point
// forward is fanned out to the returned channel (bounded; bursts past
// capacity are dropped and counted via the cancel-returned uint64).
//
// Powers the gRPC StreamGroupMessages handler — the long-lived
// streaming replacement for the net/rpc GroupPoll loop. Backlog
// (messages already buffered in the ring at subscribe time) is NOT
// replayed; callers that want that should call GroupPoll with a
// since-timestamp before subscribing.
//
// Returns nil channel + a no-op cancel when grouping is disabled on
// this visor (e.g. early init or tests).
func (v *Visor) SubscribeGroupMessages(capacity int) (<-chan GroupMessage, func() uint64) {
	v.initLock.RLock()
	inbox := v.grouping.inbox
	v.initLock.RUnlock()
	if inbox == nil {
		return nil, func() uint64 { return 0 }
	}
	sub := inbox.subscribe(capacity)
	return sub.ch, func() uint64 {
		inbox.unsubscribe(sub)
		return sub.dropCount.Load()
	}
}

// tpDiscClient is a convenience function to obtain transport discovery client.
func (v *Visor) tpDiscClient() transport.DiscoveryClient {
	return v.tpM.Conf.DiscoveryClient
}

// LocalPK returns this visor's public key. Used by gRPC handlers that need
// to default the source PK on calc-route style requests.
func (v *Visor) LocalPK() cipher.PubKey {
	return v.conf.PK
}

// FetchAllTransportEntries pulls every transport from the configured TPD
// client AND merges in the local transport manager's view. The TPD bulk
// endpoint can lag behind reality (its cache is async-refreshed), so a
// transport this visor has actively negotiated may not be present yet.
// The local transport manager is authoritative for "what this visor can
// actually dial right now"; merging avoids missed 1-hop routes in calc
// responses while the BFS depth limit + streaming output bound memory.
//
// Dedup is by transport ID — TPD's view is preferred when it has a
// matching entry (it carries label/QoS metadata the local view lacks).
func (v *Visor) FetchAllTransportEntries(ctx context.Context) ([]*transport.Entry, error) {
	tpD := v.tpDiscClient()
	if tpD == nil {
		return nil, errors.New("transport discovery client not available")
	}
	entries, err := tpD.GetAllTransports(ctx)
	if err != nil {
		return nil, err
	}

	if v.tpM == nil {
		return entries, nil
	}

	seen := make(map[uuid.UUID]struct{}, len(entries))
	for _, e := range entries {
		if e != nil {
			seen[e.ID] = struct{}{}
		}
	}
	v.tpM.WalkTransports(func(tp *transport.ManagedTransport) bool {
		if tp == nil {
			return true
		}
		if _, dup := seen[tp.Entry.ID]; dup {
			return true
		}
		seen[tp.Entry.ID] = struct{}{}
		entry := tp.Entry
		entries = append(entries, &entry)
		return true
	})
	return entries, nil
}

//go:embed static
var ui embed.FS

func initUI() *fs.FS {
	//initialize the ui
	uiFS, err := fs.Sub(ui, "static")
	if err != nil {
		mLog.WithError(err).Error("frontend not found")
		//		return err
	}
	return &uiFS

}

func storeLog(conf *visorconfig.V1) {
	logPath := conf.LocalPath + "/log"
	logFile := logPath + "/skywire.log"

	// Create log directory with readable permissions for log access
	if err := os.MkdirAll(logPath, 0750); err != nil { //nolint:gosec
		mLog.WithError(err).Warn("Failed to create log directory")
	}

	// Pre-create or fix permissions on the log file to make it readable
	// This is important when running as root in a directory not owned by root
	if _, err := os.Stat(logFile); os.IsNotExist(err) {
		// Create new file with readable permissions
		f, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY, 0644) //nolint:gosec
		if err == nil {
			if err := f.Close(); err != nil {
				mLog.WithError(err).Warn("Failed to close log file")
			}
		}
	} else if err == nil {
		// Fix permissions on existing file
		_ = os.Chmod(logFile, 0644) //nolint:gosec,errcheck
	}

	hook, _ := lumberjackrus.NewHook( //nolint:errcheck
		&lumberjackrus.LogFile{
			Filename:   conf.LocalPath + "/log/skywire.log",
			MaxSize:    1,
			MaxBackups: 1,
			MaxAge:     1,
			Compress:   false,
			LocalTime:  false,
		},
		logrus.TraceLevel,
		&logging.TextFormatter{
			DisableColors:   true,
			FullTimestamp:   true,
			ForceFormatting: true,
		},
		&lumberjackrus.LogFileOpts{
			logrus.InfoLevel: &lumberjackrus.LogFile{
				Filename:   conf.LocalPath + "/log/skywire.log",
				MaxSize:    1,
				MaxBackups: 1,
				MaxAge:     1,
				Compress:   false,
				LocalTime:  false,
			},
			logrus.WarnLevel: &lumberjackrus.LogFile{
				Filename:   conf.LocalPath + "/log/skywire.log",
				MaxSize:    1,
				MaxBackups: 1,
				MaxAge:     1,
				Compress:   false,
				LocalTime:  false,
			},
			logrus.TraceLevel: &lumberjackrus.LogFile{
				Filename:   conf.LocalPath + "/log/skywire.log",
				MaxSize:    1,
				MaxBackups: 1,
				MaxAge:     1,
				Compress:   false,
				LocalTime:  false,
			},
			logrus.ErrorLevel: &lumberjackrus.LogFile{
				Filename:   conf.LocalPath + "/log/skywire.log",
				MaxSize:    1,
				MaxBackups: 1,
				MaxAge:     1,
				Compress:   false,
				LocalTime:  false,
			},
			logrus.DebugLevel: &lumberjackrus.LogFile{
				Filename:   conf.LocalPath + "/log/skywire.log",
				MaxSize:    1,
				MaxBackups: 1,
				MaxAge:     1,
				Compress:   false,
				LocalTime:  false,
			},
			logrus.FatalLevel: &lumberjackrus.LogFile{
				Filename:   conf.LocalPath + "/log/skywire.log",
				MaxSize:    1,
				MaxBackups: 1,
				MaxAge:     1,
				Compress:   false,
				LocalTime:  false,
			},
		},
	)
	mLog.Hooks.Add(hook)
	conf.MasterLogger().Hooks.Add(hook)
}

func setForceColor(conf *visorconfig.V1) {
	conf.MasterLogger().SetFormatter(&logging.TextFormatter{
		FullTimestamp:      true,
		AlwaysQuoteStrings: true,
		QuoteEmptyFields:   true,
		ForceFormatting:    true,
		DisableColors:      false,
		ForceColors:        true,
	})

	mLog.SetFormatter(&logging.TextFormatter{
		FullTimestamp:      true,
		AlwaysQuoteStrings: true,
		QuoteEmptyFields:   true,
		ForceFormatting:    true,
		DisableColors:      false,
		ForceColors:        true,
	})
}

// HealthStatsProvider implementation for logserver

// GetTransportCounts returns the count of STCPR and SUDPH transports (excluding "user" labeled).
func (v *Visor) GetTransportCounts() (stcpr, sudph int) {
	if v.tpM == nil {
		return 0, 0
	}

	// Get transports that are not user-created (skycoin + automatic labels)
	tps := v.tpM.GetTransportsByLabels(transport.LabelSkycoin, transport.LabelAutomatic)
	for _, tp := range tps {
		switch tp.Type() {
		case tptypes.STCPR:
			stcpr++
		case tptypes.SUDPH:
			sudph++
		}
	}
	return stcpr, sudph
}

// GetNetworkTypes returns the network types used by the visor.
func (v *Visor) GetNetworkTypes() []string {
	types, err := v.TransportTypes()
	if err != nil {
		return nil
	}
	return types
}
