// Package visor implements skywire visor.
package visor

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"strings"
	"sync"
	"time"

	dmsgdisc "github.com/skycoin/dmsg/pkg/disc"
	"github.com/skycoin/dmsg/pkg/dmsg"
	"github.com/toqueteos/webbrowser"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/cmdutil"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/app/appdisc"
	"github.com/skycoin/skywire/pkg/app/appevent"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/app/launcher"
	"github.com/skycoin/skywire/pkg/restart"
	"github.com/skycoin/skywire/pkg/routefinder/rfclient"
	"github.com/skycoin/skywire/pkg/router"
	"github.com/skycoin/skywire/pkg/syslog"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/transport/network"
	"github.com/skycoin/skywire/pkg/transport/network/addrresolver"
	"github.com/skycoin/skywire/pkg/utclient"
	"github.com/skycoin/skywire/pkg/visor/dmsgtracker"
	"github.com/skycoin/skywire/pkg/visor/logstore"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
	"github.com/skycoin/skywire/pkg/visor/visorinit"
)

var (
	// ErrAppProcNotRunning represents lookup error for App related calls.
	ErrAppProcNotRunning = errors.New("no process of given app is running")
	// ErrProcNotAvailable represents error for unavailable process manager
	ErrProcNotAvailable = errors.New("no process manager available")
	// ErrTrpMangerNotAvailable represents error for unavailable transport manager
	ErrTrpMangerNotAvailable = errors.New("no transport manager available")
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

	conf     *visorconfig.V1
	log      *logging.Logger
	logstore logstore.Store

	startedAt     time.Time
	restartCtx    *restart.Context
	uptimeTracker utclient.APIClient

	ebc          *appevent.Broadcaster // event broadcaster
	dmsgC        *dmsg.Client
	dmsgDC       *dmsg.Client       // dmsg direct client
	dClient      dmsgdisc.APIClient // dmsg direct api client
	dmsgHTTP     *http.Client       // dmsghttp client
	dtm          *dmsgtracker.Manager
	dtmReady     chan struct{}
	dtmReadyOnce sync.Once

	stunClient    *network.StunDetails
	stunReady     chan struct{}
	stunReadyOnce sync.Once

	tpM      *transport.Manager
	arClient addrresolver.APIClient
	router   router.Router
	rfClient rfclient.Client

	procM       appserver.ProcManager // proc manager
	appL        *launcher.AppLauncher // app launcher
	serviceDisc appdisc.Factory
	initLock    *sync.RWMutex
	// when module is failed it pushes its error to this channel
	// used by init and shutdown to show/check for any residual errors
	// produced by concurrent parts of modules
	runtimeErrors chan error

	isServicesHealthy *internalHealthInfo
	//	autoPeer             bool                   // autoPeer=true tells the visor to query the http endpoint of the hypervisor on the local network for the hypervisor's public key when connectio to the hypervisor is lost
	//	autoPeerIP           string                 // autoPeerCmd is the command string used to return the public key of the hypervisor
	remoteVisors         map[cipher.PubKey]Conn // remote hypervisors the visor is attempting to connect to
	connectedHypervisors map[cipher.PubKey]bool // remote hypervisors the visor is currently connected to
	allowedPorts         map[int]bool
	allowedMX            *sync.RWMutex

	pingConns    map[cipher.PubKey]ping
	pingConnMx   *sync.Mutex
	pingPcktSize int
}

// todo: consider moving module closing to the module system

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
	return run(nil)
}

// RunVisor runs the visor
func run(conf *visorconfig.V1) error {
	store, hook := logstore.MakeStore(runtimeLogMaxEntries)
	mLog.AddHook(hook)

//commented out pending fixes for go 1.19
	//stopPProf := initPProf(mLog, pprofMode, pprofAddr)
	//defer stopPProf()

	if conf == nil {
		conf = initConfig()
	}

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

	//	if isAutoPeer {
	//		conf = initAutopeer(conf)
	//	}

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

	ctx, cancel := cmdutil.SignalContext(context.Background(), mLog)
	vis, ok := NewVisor(ctx, conf)
	if !ok {
		select {
		case <-ctx.Done():
			mLog.Info("Visor closed early.")
		default:
			return fmt.Errorf("Failed to start visor.") //nolint
		}
		return nil
	}

	stopVisorFn = func() {
		if err := vis.Close(); err != nil {
			mLog.WithError(err).Error("Visor closed with error.") //nolint
		}
		cancel()
	}
	vis.SetLogstore(store)
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
	v := &Visor{
		log:                  conf.MasterLogger().PackageLogger("visor"),
		conf:                 conf,
		restartCtx:           restartCtx,
		initLock:             new(sync.RWMutex),
		allowedMX:            new(sync.RWMutex),
		isServicesHealthy:    newInternalHealthInfo(),
		dtmReady:             make(chan struct{}),
		stunReady:            make(chan struct{}),
		connectedHypervisors: make(map[cipher.PubKey]bool),
		pingConns:            make(map[cipher.PubKey]ping),
		pingConnMx:           new(sync.Mutex),
		allowedPorts:         make(map[int]bool),
	}
	v.isServicesHealthy.init()

	if logLvl, err := logging.LevelFromString(conf.LogLevel); err != nil {
		v.log.WithError(err).Warn("Failed to read log level from config.")
	} else {
		v.conf.MasterLogger().SetLevel(logLvl)
	}

	log := v.MasterLogger().PackageLogger("visor:startup")
	log.WithField("public_key", conf.PK).
		Info("Begin startup.")
	v.startedAt = time.Now()
	ctx = context.WithValue(ctx, visorKey, v)
	v.runtimeErrors = make(chan error)
	ctx = context.WithValue(ctx, runtimeErrsKey, v.runtimeErrors)
	if dmsgServer != "" {
		ctx = context.WithValue(ctx, "dmsgServer", dmsgServer) //nolint
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
	//	if isAutoPeer {
	//		v.autoPeer = true
	//		v.autoPeerIP = autoPeerIP
	//	}
	log.Info("Startup complete.")
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
	case <-v.stunReady:
		return true
	default:
		return false
	}
}

/*
func initAutopeer(conf *visorconfig.V1) *visorconfig.V1 {
	log := mLog.PackageLogger("visor:autopeer")

	if !isAutoPeer {
		log.WithError(fmt.Errorf("erroneous initialization")).Error("error autopeering")
		return conf
	}
	//autopeering should only happen when there is no local or remote hypervisor set in the config.
	//and hence can be disabled by setting these. the visor may still be invoked with autopeering flag.
	if conf.Hypervisor != nil {
		isAutoPeer = false
		log.Info("Local hypervisor running, disabling autopeer")
		return conf
	}

	if len(conf.Hypervisors) > 0 {
		isAutoPeer = false
		log.Info("%d Remote hypervisor(s) set in config; disabling autopeer", len(conf.Hypervisors))
		log.Info(conf.Hypervisors)
		return conf
	}

	log.Info("Autopeer: ", isAutoPeer)
	hvkey, err := FetchHvPk(autoPeerIP)
	if err != nil {
		log.WithError(err).Error("error autopeering")
		return conf
	}

	pubkey := cipher.PubKey{}
	hvkey = strings.TrimSpace(hvkey)
	hypervisorPKsSlice := strings.Split(hvkey, ",")
	for _, pubkeyString := range hypervisorPKsSlice {
		if err := pubkey.Set(pubkeyString); err != nil {
			log.Warnf("Cannot add %s PK as remote hypervisor PK due to: %s", pubkeyString, err)
			continue
		}
		log.Infof("%s PK added as remote hypervisor PK", pubkeyString)
		conf.Hypervisors = append(conf.Hypervisors, pubkey)
	}

	return conf
}
*/

func initLogger() *logging.MasterLogger {
	mLog := logging.NewMasterLogger()
	if syslogAddr != "" {
		hook, err := syslog.SetupHook(syslogAddr, logTag)
		if err != nil {
			mLog.WithError(err).Error("Failed to connect to the syslog daemon.")
		} else {
			mLog.AddHook(hook)
			mLog.Out = io.Discard
		}
	}
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

	// Cleanly close ongoing forward conns
	for _, forwardConn := range appnet.GetAllForwardConns() {
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
	case <-v.dtmReady:
		return true
	default:
		return false
	}
}

// SetLogstore sets visor runtime logstore
func (v *Visor) SetLogstore(store logstore.Store) {
	v.logstore = store
}

// tpDiscClient is a convenience function to obtain transport discovery client.
func (v *Visor) tpDiscClient() transport.DiscoveryClient {
	return v.tpM.Conf.DiscoveryClient
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
