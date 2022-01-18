// Package visor implements skywire visor.
package visor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os/exec"
	"sync"
	"time"

	"github.com/jaypipes/ghw"
	"github.com/jaypipes/ghw/pkg/baseboard"
	"github.com/jaypipes/ghw/pkg/product"
	"github.com/skycoin/dmsg"
	"github.com/skycoin/dmsg/cipher"
	dmsgdisc "github.com/skycoin/dmsg/disc"
	"github.com/skycoin/skycoin/src/util/logging"

	"github.com/skycoin/skywire/internal/httpauth"
	"github.com/skycoin/skywire/internal/utclient"
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
	"github.com/skycoin/skywire/pkg/util/updater"
	"github.com/skycoin/skywire/pkg/visor/dmsgtracker"
	"github.com/skycoin/skywire/pkg/visor/logstore"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
	"github.com/skycoin/skywire/pkg/visor/visorinit"
)

var (
	// ErrAppProcNotRunning represents lookup error for App related calls.
	ErrAppProcNotRunning = errors.New("no process of given app is running")
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
	updater       *updater.Updater
	uptimeTracker utclient.APIClient

	ebc      *appevent.Broadcaster // event broadcaster
	dmsgC    *dmsg.Client
	dmsgDC   *dmsg.Client       // dmsg direct client
	dClient  dmsgdisc.APIClient // dmsg direct api client
	dmsgHTTP *http.Client       // dmsghttp client
	trackers *dmsgtracker.Manager

	stunClient   *network.StunDetails
	wgStunClient *sync.WaitGroup
	tpM          *transport.Manager
	arClient     addrresolver.APIClient
	router       router.Router
	rfClient     rfclient.Client

	procM       appserver.ProcManager // proc manager
	appL        *launcher.Launcher    // app launcher
	serviceDisc appdisc.Factory
	initLock    *sync.Mutex
	wgTrackers  *sync.WaitGroup
	// when module is failed it pushes its error to this channel
	// used by init and shutdown to show/check for any residual errors
	// produced by concurrent parts of modules
	runtimeErrors chan error

	isServicesHealthy *internalHealthInfo
	transportCacheMu  *sync.Mutex
	transportsCache   map[cipher.PubKey][]string
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
func NewVisor(conf *visorconfig.V1, restartCtx *restart.Context) (*Visor, bool) {
	v := &Visor{
		log:               conf.MasterLogger().PackageLogger("visor"),
		conf:              conf,
		restartCtx:        restartCtx,
		initLock:          new(sync.Mutex),
		isServicesHealthy: newInternalHealthInfo(),
		wgTrackers:        new(sync.WaitGroup),
		wgStunClient:      new(sync.WaitGroup),
		transportCacheMu:  new(sync.Mutex),
	}
	v.wgStunClient.Add(1)
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
	ctx := context.Background()
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
	if err := tm.Wait(ctx); err != nil {
		log.Error(err)
		return nil, false
	}
	// todo: rewrite to be infinite concurrent loop that will watch for
	// module runtime errors and act on it (by stopping visor for example)
	if !v.processRuntimeErrs() {
		return nil, false
	}
	v.wgTrackers.Add(1)
	defer v.wgTrackers.Done()
	v.trackers = dmsgtracker.NewDmsgTrackerManager(v.MasterLogger(), v.dmsgC, 0, 0)
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
		log.Info("Shutting down module...")

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
			log.WithField("elapsed", time.Since(start)).Info("Module stopped cleanly.")

		case <-t.C:
			log.WithField("elapsed", time.Since(start)).Error("Module timed out.")
		}
	}
	v.processRuntimeErrs()
	log.Info("Shutdown complete. Goodbye!")
	return nil
}

// SetLogstore sets visor runtime logstore
func (v *Visor) SetLogstore(store logstore.Store) {
	v.logstore = store
}

// tpDiscClient is a convenience function to obtain transport discovery client.
func (v *Visor) tpDiscClient() transport.DiscoveryClient {
	return v.tpM.Conf.DiscoveryClient
}

// HostKeeper save host info of running visor.
func (v *Visor) HostKeeper(skybianBuildVersion string) {
	var model, serialNumber string
	logger := v.MasterLogger().PackageLogger("host-keeper")

	if skybianBuildVersion != "" {
		modelByte, err := exec.Command("cat", "/proc/device-tree/model").Output()
		if err != nil {
			logger.Errorf("Error during get model of board due to %v", err)
			return
		}
		model = string(modelByte)

		serialNumberByte, err := exec.Command("cat", "/proc/device-tree/serial-number").Output()
		if err != nil {
			logger.Errorf("Error during get serial number of board due to %v", err)
			return
		}
		serialNumber = string(serialNumberByte)
	} else {
		baseboardInfo, err := baseboard.New(ghw.WithDisableWarnings())
		if err != nil {
			logger.Errorf("Error during get information of host due to %v", err)
			return
		}
		model = baseboardInfo.Vendor
		serialNumber = baseboardInfo.SerialNumber
		if model == "unknown" || serialNumber == "unknown" {
			productInfo, err := product.New(ghw.WithDisableWarnings())
			if err != nil {
				logger.Errorf("Error during get information of host due to %v", err)
				return
			}
			model = productInfo.Vendor
			serialNumber = productInfo.UUID
		}
	}

	var keeperInfo HostKeeperData
	keeperInfo.Model = model
	keeperInfo.SerialNumber = serialNumber

	logger.WithField("Info", keeperInfo).Info("Host information achieved.")

	client, err := httpauth.NewClient(context.Background(), v.conf.HostKeeper, v.conf.PK, v.conf.SK, &http.Client{}, "", v.MasterLogger())
	if err != nil {
		logger.Errorf("Host Keeper httpauth: %v", err)
		return
	}

	keeperInfoByte, err := json.Marshal(keeperInfo)
	if err != nil {
		logger.Errorf("Error during marshal host info due to %v", err)
		return
	}

	req, err := http.NewRequest(http.MethodPost, v.conf.HostKeeper+"/update", bytes.NewReader(keeperInfoByte))
	if err != nil {
		logger.Errorf("Error during make request of host info due to %v", err)
		return
	}

	_, err = client.Do(req)
	if err != nil {
		logger.Errorf("Error during send host info to host-keeper service due to %v", err)
		return
	}

	logger.Info("Host info successfully updated.")
}

// HostKeeperData the struct of host keeper data
type HostKeeperData struct {
	Model        string
	SerialNumber string
}

// MigrateLocalVisor trying to make migration from direct connection to dmsghttp for local visors
func (v *Visor) MigrateLocalVisor() bool {
	if v.conf.ConnectionType == "dmsghttp" {
		v.log.Info("The connection type is dmsghttp. No migration process need.")
		return false
	}

	if !v.isLocalVisor() {
		v.log.Info("The visor is not local. No migration process need.")
		return false
	}

	if !v.setDMSGHTTP() {
		v.log.Warn("Setting dmsghttp config not complete.")
		return false
	}

	err := v.conf.Flush()
	if err != nil {
		v.log.Warn("Saving dmsghttp config not complete.")
		return false
	}

	return true
}

func (v *Visor) isLocalVisor() bool {
	resp, err := http.Get("https://ipinfo.io/country")
	if err != nil {
		v.log.WithError(err).Warn("Failed to fetch data from server. Migration process not complete.")
		return false
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		v.log.WithError(err).Warn("Failed to read fetched data. Migration process not complete.")
		return false
	}

	return string(body) == "CN\n"
}

func (v *Visor) setDMSGHTTP() bool {
	var dmsgHTTPServersList visorconfig.DmsgHTTPServers
	serversListJSON, err := ioutil.ReadFile("dmsghttp-config.json")
	if err != nil {
		v.log.WithError(err).Warn("Failed to read servers.json file. Migration process not complete.")
		return false
	}
	err = json.Unmarshal(serversListJSON, &dmsgHTTPServersList)
	if err != nil {
		v.log.WithError(err).Warn("Error during parsing servers list. Migration process not complete.")
		return false
	}
	v.conf.Dmsg.Servers = dmsgHTTPServersList.Prod.DMSGServers
	v.conf.Dmsg.Discovery = dmsgHTTPServersList.Prod.DMSGDiscovery
	v.conf.Transport.AddressResolver = dmsgHTTPServersList.Prod.AddressResolver
	v.conf.Transport.Discovery = dmsgHTTPServersList.Prod.TransportDiscovery
	v.conf.UptimeTracker.Addr = dmsgHTTPServersList.Prod.UptimeTracker
	v.conf.Routing.RouteFinder = dmsgHTTPServersList.Prod.RouteFinder
	v.conf.Launcher.ServiceDisc = dmsgHTTPServersList.Prod.ServiceDiscovery
	v.conf.ConnectionType = "dmsghttp"

	return true
}
