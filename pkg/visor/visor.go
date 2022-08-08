// Package visor implements skywire visor.
package visor

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	dmsgdisc "github.com/skycoin/dmsg/pkg/disc"
	"github.com/skycoin/dmsg/pkg/dmsg"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/app/appdisc"
	"github.com/skycoin/skywire/pkg/app/appevent"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/app/launcher"
	"github.com/skycoin/skywire/pkg/restart"
	"github.com/skycoin/skywire/pkg/routefinder/rfclient"
	"github.com/skycoin/skywire/pkg/router"
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
)

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
	appL        *launcher.Launcher    // app launcher
	serviceDisc appdisc.Factory
	initLock    *sync.RWMutex
	// when module is failed it pushes its error to this channel
	// used by init and shutdown to show/check for any residual errors
	// produced by concurrent parts of modules
	runtimeErrors chan error

	isServicesHealthy    *internalHealthInfo
	autoPeer             bool                   // autoPeer=true tells the visor to query the http endpoint of the hypervisor on the local network for the hypervisor's public key when connectio to the hypervisor is lost
	autoPeerIP           string                 // autoPeerCmd is the command string used to return the public key of the hypervisor
	remoteVisors         map[cipher.PubKey]Conn // remote hypervisors the visor is attempting to connect to
	connectedHypervisors map[cipher.PubKey]bool // remote hypervisors the visor is currently connected to
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

// NewVisor constructs new Visor.
func NewVisor(ctx context.Context, conf *visorconfig.V1, restartCtx *restart.Context, autoPeer bool, autoPeerIP string) (*Visor, bool) {

	v := &Visor{
		log:                  conf.MasterLogger().PackageLogger("visor"),
		conf:                 conf,
		restartCtx:           restartCtx,
		initLock:             new(sync.RWMutex),
		isServicesHealthy:    newInternalHealthInfo(),
		dtmReady:             make(chan struct{}),
		stunReady:            make(chan struct{}),
		connectedHypervisors: make(map[cipher.PubKey]bool),
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
	registerModules(v.MasterLogger())
	var mainModule visorinit.Module
	if v.conf.Hypervisor == nil {
		mainModule = vis
	} else {
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
	if autoPeer {
		v.autoPeer = true
		v.autoPeerIP = autoPeerIP
	}
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
