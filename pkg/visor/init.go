package visor

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/ccding/go-stun/stun"
	"github.com/sirupsen/logrus"
	"github.com/skycoin/dmsg"
	"github.com/skycoin/dmsg/cipher"
	"github.com/skycoin/dmsg/dmsgctrl"
	dmsgnetutil "github.com/skycoin/dmsg/netutil"
	"github.com/skycoin/skycoin/src/util/logging"

	"github.com/skycoin/skywire/internal/utclient"
	"github.com/skycoin/skywire/internal/vpn"
	"github.com/skycoin/skywire/pkg/app/appdisc"
	"github.com/skycoin/skywire/pkg/app/appevent"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/app/launcher"
	"github.com/skycoin/skywire/pkg/dmsgc"
	"github.com/skycoin/skywire/pkg/routefinder/rfclient"
	"github.com/skycoin/skywire/pkg/router"
	"github.com/skycoin/skywire/pkg/servicedisc"
	"github.com/skycoin/skywire/pkg/setup/setupclient"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/transport/network"
	"github.com/skycoin/skywire/pkg/transport/network/addrresolver"
	"github.com/skycoin/skywire/pkg/transport/network/stcp"
	ts "github.com/skycoin/skywire/pkg/transport/setup"
	"github.com/skycoin/skywire/pkg/transport/tpdclient"
	"github.com/skycoin/skywire/pkg/util/netutil"
	"github.com/skycoin/skywire/pkg/util/updater"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
	vinit "github.com/skycoin/skywire/pkg/visor/visorinit"
)

type visorCtxKey int

const visorKey visorCtxKey = iota

type runtimeErrsCtxKey int

const runtimeErrsKey runtimeErrsCtxKey = iota

// Visor initialization is split into modules, that can be initialized independently
// Modules are declared here as package-level variables, but also need to be registered
// in the modules system: they need init function and dependencies and their name to be set
// To add new piece of functionality to visor, you need to create a new module variable
// and register it properly in registerModules function
var (
	// Event broadcasting system
	ebc vinit.Module
	// visor updater
	up vinit.Module
	// Address resolver
	ar vinit.Module
	// App discovery
	disc vinit.Module
	// Stun module
	sc vinit.Module
	// SUDPH module
	sudphC vinit.Module
	// STCPR module
	stcprC vinit.Module
	// STCP module
	stcpC vinit.Module
	// dmsg pty: a remote terminal to the visor working over dmsg protocol
	pty vinit.Module
	// Dmsg module
	dmsgC vinit.Module
	// Transport manager
	tr vinit.Module
	// Transport setup
	trs vinit.Module
	// Routing system
	rt vinit.Module
	// Application launcer
	launch vinit.Module
	// CLI
	cli vinit.Module
	// hypervisors to control this visor
	hvs vinit.Module
	// Uptime tracker
	ut vinit.Module
	// Public visors: automatically establish connections to public visors
	pvs vinit.Module
	// Public visor: advertise current visor as public
	pv vinit.Module
	// Transport module (this is not a functional module but a grouping of all heavy transport types initializations)
	tm vinit.Module
	// hypervisor module
	hv vinit.Module
	// dmsg ctrl
	dmsgCtrl vinit.Module
	// visor that groups all modules together
	vis vinit.Module
)

// register all modules: instantiate modules with correct names and dependencies, wrap init
// functions to have access to visor and runtime errors channel
func registerModules(logger *logging.MasterLogger) {
	// utility module maker, to avoid passing logger and wrapping each init function
	// in withVisorCtx
	maker := func(name string, f initFn, deps ...*vinit.Module) vinit.Module {
		return vinit.MakeModule(name, withInitCtx(f), logger, deps...)
	}
	up = maker("updater", initUpdater)
	ebc = maker("event_broadcaster", initEventBroadcaster)
	ar = maker("address_resolver", initAddressResolver)
	disc = maker("discovery", initDiscovery)
	tr = maker("transport", initTransport, &ar, &ebc)

	sc = maker("stun_client", initStunClient)
	sudphC = maker("sudph", initSudphClient, &sc, &tr)
	stcprC = maker("stcpr", initStcprClient, &tr)
	stcpC = maker("stcp", initStcpClient, &tr)
	dmsgC = maker("dmsg", initDmsg, &ebc)
	dmsgCtrl = maker("dmsg_ctrl", initDmsgCtrl, &dmsgC, &tr)

	pty = maker("dmsg_pty", initDmsgpty, &dmsgC)
	rt = maker("router", initRouter, &tr, &dmsgC)
	launch = maker("launcher", initLauncher, &ebc, &disc, &dmsgC, &tr, &rt)
	cli = maker("cli", initCLI)
	hvs = maker("hypervisors", initHypervisors, &dmsgC)
	ut = maker("uptime_tracker", initUptimeTracker)
	pv = maker("public_visors", initPublicVisors, &tr)
	trs = maker("transport_setup", initTransportSetup, &dmsgC, &tr)
	tm = vinit.MakeModule("transports", vinit.DoNothing, logger, &sc, &sudphC, &dmsgCtrl)
	pvs = maker("public_visor", initPublicVisor, &tr, &ar, &disc)
	vis = vinit.MakeModule("visor", vinit.DoNothing, logger, &up, &ebc, &ar, &disc, &pty,
		&tr, &rt, &launch, &cli, &hvs, &ut, &pv, &pvs, &trs, &stcpC, &stcprC)

	hv = maker("hypervisor", initHypervisor, &vis)
}

type initFn func(context.Context, *Visor, *logging.Logger) error

func initUpdater(ctx context.Context, v *Visor, log *logging.Logger) error {
	updater := updater.New(v.log, v.restartCtx, v.conf.Launcher.BinPath)

	v.initLock.Lock()
	defer v.initLock.Unlock()
	v.restartCtx.SetCheckDelay(time.Duration(v.conf.RestartCheckDelay))
	v.restartCtx.RegisterLogger(v.log)
	v.updater = updater
	return nil
}

func initEventBroadcaster(ctx context.Context, v *Visor, log *logging.Logger) error {
	const ebcTimeout = time.Second
	ebc := appevent.NewBroadcaster(log, ebcTimeout)
	v.pushCloseStack("event_broadcaster", ebc.Close)

	v.initLock.Lock()
	v.ebc = ebc
	v.initLock.Unlock()
	return nil
}

func initAddressResolver(ctx context.Context, v *Visor, log *logging.Logger) error {
	conf := v.conf.Transport

	arClient, err := addrresolver.NewHTTP(conf.AddressResolver, v.conf.PK, v.conf.SK, log)
	if err != nil {
		err := fmt.Errorf("failed to create address resolver client: %w", err)
		return err
	}
	v.initLock.Lock()
	v.arClient = arClient
	v.initLock.Unlock()
	return nil
}

func initDiscovery(ctx context.Context, v *Visor, log *logging.Logger) error {
	// Prepare app discovery factory.
	factory := appdisc.Factory{
		Log: v.MasterLogger().PackageLogger("app_discovery"),
	}

	conf := v.conf.Launcher

	if conf.ServiceDisc != "" {
		factory.PK = v.conf.PK
		factory.SK = v.conf.SK
		factory.ServiceDisc = conf.ServiceDisc
	}
	v.initLock.Lock()
	v.serviceDisc = factory
	v.initLock.Unlock()
	return nil
}

func initStunClient(ctx context.Context, v *Visor, log *logging.Logger) error {
	sc := network.GetStunDetails(v.conf.StunServers, log)
	v.initLock.Lock()
	v.stunClient = sc
	v.initLock.Unlock()
	return nil
}

func initDmsg(ctx context.Context, v *Visor, log *logging.Logger) error {
	if v.conf.Dmsg == nil {
		return fmt.Errorf("cannot initialize dmsg: empty configuration")
	}
	dmsgC := dmsgc.New(v.conf.PK, v.conf.SK, v.ebc, v.conf.Dmsg)

	wg := new(sync.WaitGroup)
	wg.Add(1)
	go func() {
		defer wg.Done()
		dmsgC.Serve(context.Background())
	}()

	v.pushCloseStack("dmsg", func() error {
		if err := dmsgC.Close(); err != nil {
			return err
		}
		wg.Wait()
		return nil
	})

	v.initLock.Lock()
	v.dmsgC = dmsgC
	v.initLock.Unlock()
	return nil
}

func initDmsgCtrl(ctx context.Context, v *Visor, _ *logging.Logger) error {
	dmsgC := v.dmsgC
	if dmsgC == nil {
		return nil
	}

	const dmsgTimeout = time.Second * 20
	logger := dmsgC.Logger().WithField("timeout", dmsgTimeout)
	logger.Info("Connecting to the dmsg network...")
	select {
	case <-time.After(dmsgTimeout):
		logger.Warn("Failed to connect to the dmsg network, will try again later.")
		go func() {
			<-v.dmsgC.Ready()
			logger.Info("Connected to the dmsg network.")
			v.tpM.InitDmsgClient(ctx, dmsgC)
		}()
	case <-v.dmsgC.Ready():
		logger.Info("Connected to the dmsg network.")
		v.tpM.InitDmsgClient(ctx, dmsgC)
	}
	// dmsgctrl setup
	cl, err := dmsgC.Listen(skyenv.DmsgCtrlPort)
	if err != nil {
		return err
	}
	v.pushCloseStack("dmsgctrl", cl.Close)

	dmsgctrl.ServeListener(cl, 0)
	return nil
}

func initSudphClient(ctx context.Context, v *Visor, log *logging.Logger) error {
	switch v.stunClient.NATType {
	case stun.NATSymmetric, stun.NATSymmetricUDPFirewall:
		log.Infof("SUDPH transport wont be available as visor is under %v", v.stunClient.NATType.String())
	default:
		v.tpM.InitClient(ctx, network.SUDPH)
	}
	return nil
}

func initStcprClient(ctx context.Context, v *Visor, log *logging.Logger) error {
	v.tpM.InitClient(ctx, network.STCPR)
	return nil
}

func initStcpClient(ctx context.Context, v *Visor, log *logging.Logger) error {
	if v.conf.STCP != nil {
		v.tpM.InitClient(ctx, network.STCP)
	}
	return nil
}

func initTransport(ctx context.Context, v *Visor, log *logging.Logger) error {

	tpdC, err := connectToTpDisc(v)
	if err != nil {
		err := fmt.Errorf("failed to create transport discovery client: %w", err)
		return err
	}

	logS := transport.InMemoryTransportLogStore()

	pTps, err := v.conf.GetPersistentTransports()
	if err != nil {
		err := fmt.Errorf("failed to get persistent transports: %w", err)
		return err
	}

	tpMConf := transport.ManagerConfig{
		PubKey:                    v.conf.PK,
		SecKey:                    v.conf.SK,
		DiscoveryClient:           tpdC,
		LogStore:                  logS,
		PersistentTransportsCache: pTps,
	}
	managerLogger := v.MasterLogger().PackageLogger("transport_manager")

	// todo: pass down configuration?
	var table stcp.PKTable
	var listenAddr string
	if v.conf.STCP != nil {
		table = stcp.NewTable(v.conf.STCP.PKTable)
		listenAddr = v.conf.STCP.ListeningAddress
	}
	factory := network.ClientFactory{
		PK:         v.conf.PK,
		SK:         v.conf.SK,
		ListenAddr: listenAddr,
		PKTable:    table,
		ARClient:   v.arClient,
		EB:         v.ebc,
	}
	tpM, err := transport.NewManager(managerLogger, v.arClient, v.ebc, &tpMConf, factory)
	if err != nil {
		err := fmt.Errorf("failed to start transport manager: %w", err)
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	wg := new(sync.WaitGroup)
	wg.Add(1)

	go func() {
		defer wg.Done()
		tpM.Serve(ctx)
	}()

	v.pushCloseStack("transport.manager", func() error {
		cancel()
		tpM.Close()
		wg.Wait()
		return nil
	})

	v.initLock.Lock()
	v.tpM = tpM
	v.initLock.Unlock()
	return nil
}

func initTransportSetup(ctx context.Context, v *Visor, log *logging.Logger) error {
	ctx, cancel := context.WithCancel(ctx)
	// To remove the block set by NewTransportListener if dmsg is not initilized
	go func() {
		ts, err := ts.NewTransportListener(ctx, v.conf, v.dmsgC, v.tpM, v.MasterLogger())
		if err != nil {
			log.Warn(err)
			cancel()
		}
		select {
		case <-ctx.Done():
		default:
			go ts.Serve(ctx)
		}
	}()

	// waiting for atleast one transport to initilize
	<-v.tpM.Ready()

	v.pushCloseStack("transport_setup.rpc", func() error {
		cancel()
		return nil
	})
	return nil
}

func initRouter(ctx context.Context, v *Visor, log *logging.Logger) error {
	conf := v.conf.Routing
	rfClient := rfclient.NewHTTP(conf.RouteFinder, time.Duration(conf.RouteFinderTimeout))
	logger := v.MasterLogger().PackageLogger("router")
	rConf := router.Config{
		Logger:           logger,
		PubKey:           v.conf.PK,
		SecKey:           v.conf.SK,
		TransportManager: v.tpM,
		RouteFinder:      rfClient,
		RouteGroupDialer: setupclient.NewSetupNodeDialer(),
		SetupNodes:       conf.SetupNodes,
		RulesGCInterval:  0, // TODO
		MinHops:          v.conf.Routing.MinHops,
	}

	r, err := router.New(v.dmsgC, &rConf)
	if err != nil {
		err := fmt.Errorf("failed to create router: %w", err)
		return err
	}

	serveCtx, cancel := context.WithCancel(context.Background())
	if err := r.Serve(serveCtx); err != nil {
		cancel()
		return err
	}

	v.pushCloseStack("router.serve", func() error {
		cancel()
		return r.Close()
	})

	v.initLock.Lock()
	v.rfClient = rfClient
	v.router = r
	v.initLock.Unlock()

	return nil
}

func initLauncher(ctx context.Context, v *Visor, log *logging.Logger) error {
	conf := v.conf.Launcher

	// Prepare proc manager.
	procM, err := appserver.NewProcManager(v.MasterLogger(), &v.serviceDisc, v.ebc, conf.ServerAddr)
	if err != nil {
		err := fmt.Errorf("failed to start proc_manager: %w", err)
		return err
	}

	v.pushCloseStack("launcher.proc_manager", procM.Close)

	// Prepare launcher.
	launchConf := launcher.Config{
		VisorPK:    v.conf.PK,
		Apps:       conf.Apps,
		ServerAddr: conf.ServerAddr,
		BinPath:    conf.BinPath,
		LocalPath:  v.conf.LocalPath,
	}

	launchLog := v.MasterLogger().PackageLogger("launcher")

	launch, err := launcher.NewLauncher(launchLog, launchConf, v.dmsgC, v.router, procM)
	if err != nil {
		err := fmt.Errorf("failed to start launcher: %w", err)
		return err
	}

	err = launch.AutoStart(launcher.EnvMap{
		skyenv.VPNClientName: vpnEnvMaker(v.conf, v.dmsgC, v.tpM.STCPRRemoteAddrs()),
		skyenv.VPNServerName: vpnEnvMaker(v.conf, v.dmsgC, nil),
	})

	if err != nil {
		err := fmt.Errorf("failed to autostart apps: %w", err)
		return err
	}

	v.initLock.Lock()
	v.procM = procM
	v.appL = launch
	v.initLock.Unlock()

	return nil
}

// Make an env maker function for vpn application
func vpnEnvMaker(conf *visorconfig.V1, dmsgC *dmsg.Client, tpRemoteAddrs []string) launcher.EnvMaker {
	return func() ([]string, error) {
		var envCfg vpn.DirectRoutesEnvConfig

		if conf.Dmsg != nil {
			envCfg.DmsgDiscovery = conf.Dmsg.Discovery

			r := dmsgnetutil.NewRetrier(logrus.New(), 1*time.Second, 10*time.Second, 0, 1)
			err := r.Do(context.Background(), func() error {
				for _, ses := range dmsgC.AllSessions() {
					envCfg.DmsgServers = append(envCfg.DmsgServers, ses.RemoteTCPAddr().String())
				}

				if len(envCfg.DmsgServers) == 0 {
					return errors.New("no dmsg servers found")
				}

				return nil
			})

			if err != nil {
				return nil, fmt.Errorf("error getting Dmsg servers: %w", err)
			}
		}

		if conf.Transport != nil {
			envCfg.TPDiscovery = conf.Transport.Discovery
			envCfg.AddressResolver = conf.Transport.AddressResolver
		}

		if conf.Routing != nil {
			envCfg.RF = conf.Routing.RouteFinder
		}

		if conf.UptimeTracker != nil {
			envCfg.UptimeTracker = conf.UptimeTracker.Addr
		}

		if conf.STCP != nil && len(conf.STCP.PKTable) != 0 {
			envCfg.STCPTable = conf.STCP.PKTable
		}

		envCfg.TPRemoteIPs = tpRemoteAddrs

		envMap := vpn.AppEnvArgs(envCfg)

		envs := make([]string, 0, len(envMap))
		for k, v := range envMap {
			envs = append(envs, fmt.Sprintf("%s=%s", k, v))
		}

		return envs, nil
	}
}

func initCLI(ctx context.Context, v *Visor, log *logging.Logger) error {
	if v.conf.CLIAddr == "" {
		v.log.Info("'cli_addr' is not configured, skipping.")
		return nil
	}

	cliL, err := net.Listen("tcp", v.conf.CLIAddr)
	if err != nil {
		return err
	}

	v.pushCloseStack("cli.listener", cliL.Close)

	rpcS, err := newRPCServer(v, "CLI")
	if err != nil {
		err := fmt.Errorf("failed to start rpc server for cli: %w", err)
		return err
	}
	go rpcS.Accept(cliL) // We do not use sync.WaitGroup here as it will never return anyway.

	return nil
}

func initHypervisors(ctx context.Context, v *Visor, log *logging.Logger) error {
	hvErrs := make(map[cipher.PubKey]chan error, len(v.conf.Hypervisors))
	for _, hv := range v.conf.Hypervisors {
		hvErrs[hv] = make(chan error, 1)
	}

	for hvPK, hvErrs := range hvErrs {
		log := v.MasterLogger().PackageLogger("hypervisor_client").WithField("hypervisor_pk", hvPK)

		addr := dmsg.Addr{PK: hvPK, Port: skyenv.DmsgHypervisorPort}
		rpcS, err := newRPCServer(v, addr.PK.String()[:shortHashLen])
		if err != nil {
			err := fmt.Errorf("failed to start RPC server for hypervisor %s: %w", hvPK, err)
			return err
		}

		ctx, cancel := context.WithCancel(context.Background())
		wg := new(sync.WaitGroup)
		wg.Add(1)

		go func(hvErrs chan error) {
			defer wg.Done()
			ServeRPCClient(ctx, log, v.dmsgC, rpcS, addr, hvErrs)
		}(hvErrs)

		v.pushCloseStack("hypervisor."+hvPK.String()[:shortHashLen], func() error {
			cancel()
			wg.Wait()
			return nil
		})
	}

	return nil
}

func initUptimeTracker(_ context.Context, v *Visor, log *logging.Logger) error {
	const tickDuration = 1 * time.Minute

	conf := v.conf.UptimeTracker

	if conf == nil {
		v.log.Info("'uptime_tracker' is not configured, skipping.")
		return nil
	}

	ut, err := utclient.NewHTTP(conf.Addr, v.conf.PK, v.conf.SK)
	if err != nil {
		v.log.WithError(err).Warn("Failed to connect to uptime tracker.")
		return nil
	}

	ticker := time.NewTicker(tickDuration)

	go func() {
		for range ticker.C {
			c := context.Background()
			if err := ut.UpdateVisorUptime(c); err != nil {
				v.isServicesHealthy.unset()
				log.WithError(err).Warn("Failed to update visor uptime.")
			} else {
				v.isServicesHealthy.set()
			}
		}
	}()

	v.pushCloseStack("uptime_tracker", func() error {
		ticker.Stop()
		return nil
	})

	v.initLock.Lock()
	v.uptimeTracker = ut
	v.initLock.Unlock()

	return nil
}

// advertise this visor as public in service discovery
// this service is not considered critical and always returns true
func initPublicVisor(_ context.Context, v *Visor, log *logging.Logger) error {
	if !v.conf.IsPublic {
		return nil
	}
	logger := v.MasterLogger().PackageLogger("public_visor")
	hasPublic, err := netutil.HasPublicIP()
	if err != nil {
		logger.WithError(err).Warn("Failed to check for existing public IP address")
		return nil
	}
	if !hasPublic {
		logger.Warn("No public IP address found, stopping")
		return nil
	}

	stcpr, ok := v.tpM.Stcpr()
	if !ok {
		logger.Warn("No stcpr client found, stopping")
		return nil
	}
	addr, err := stcpr.LocalAddr()
	if err != nil {
		logger.Warn("Failed to get STCPR local addr")
		return nil
	}
	port, err := netutil.ExtractPort(addr)
	if err != nil {
		logger.Warn("Failed to get STCPR port")
		return nil
	}
	visorUpdater := v.serviceDisc.VisorUpdater(uint16(port))
	visorUpdater.Start()

	v.log.Infof("Sent request to register visor as public")
	v.pushCloseStack("public visor updater", func() error {
		visorUpdater.Stop()
		return nil
	})
	return nil
}

func initPublicVisors(ctx context.Context, v *Visor, log *logging.Logger) error {
	if !v.conf.Transport.PublicAutoconnect {
		return nil
	}
	serviceDisc := v.conf.Launcher.ServiceDisc
	if serviceDisc == "" {
		serviceDisc = skyenv.DefaultServiceDiscAddr
	}

	// todo: refactor updatedisc: split connecting to services in updatedisc and
	// advertising oneself as a service. Currently, config is tailored to
	// advertising oneself and requires things like port that are not used
	// in connecting to services
	conf := servicedisc.Config{
		Type:     servicedisc.ServiceTypeVisor,
		PK:       v.conf.PK,
		SK:       v.conf.SK,
		Port:     uint16(0),
		DiscAddr: serviceDisc,
	}
	connector := servicedisc.MakeConnector(conf, 5, v.tpM, log)
	go connector.Run(ctx) //nolint:errcheck

	return nil
}

func initHypervisor(_ context.Context, v *Visor, log *logging.Logger) error {
	v.log.Infof("Initializing hypervisor")

	ctx, cancel := context.WithCancel(context.Background())

	conf := *v.conf.Hypervisor
	conf.PK = v.conf.PK
	conf.SK = v.conf.SK
	conf.DmsgDiscovery = v.conf.Dmsg.Discovery

	// Prepare hypervisor.
	hv, err := New(conf, v, v.dmsgC)
	if err != nil {
		v.log.Fatalln("Failed to start hypervisor:", err)
	}

	hv.serveDmsg(ctx, v.log)

	// Serve HTTP(s).
	v.log.WithField("addr", conf.HTTPAddr).
		WithField("tls", conf.EnableTLS).
		Info("Serving hypervisor...")

	go func() {
		if handler := hv.HTTPHandler(); conf.EnableTLS {
			err = http.ListenAndServeTLS(conf.HTTPAddr, conf.TLSCertFile, conf.TLSKeyFile, handler)
		} else {
			err = http.ListenAndServe(conf.HTTPAddr, handler)
		}

		if err != nil {
			v.log.WithError(err).Fatal("Hypervisor exited with error.")
		}

		cancel()
	}()

	v.log.Infof("Hypervisor initialized")

	return nil
}

func connectToTpDisc(v *Visor) (transport.DiscoveryClient, error) {
	const (
		initBO = 1 * time.Second
		maxBO  = 10 * time.Second
		// trying till success
		tries  = 0
		factor = 1
	)

	conf := v.conf.Transport

	log := v.MasterLogger().PackageLogger("tp_disc_retrier")
	tpdCRetrier := dmsgnetutil.NewRetrier(log,
		initBO, maxBO, tries, factor)

	var tpdC transport.DiscoveryClient
	retryFunc := func() error {
		var err error
		tpdC, err = tpdclient.NewHTTP(conf.Discovery, v.conf.PK, v.conf.SK)
		if err != nil {
			log.WithError(err).Error("Failed to connect to transport discovery, retrying...")
			return err
		}

		return nil
	}

	if err := tpdCRetrier.Do(context.Background(), retryFunc); err != nil {
		return nil, err
	}

	return tpdC, nil
}

// ErrNoVisorInCtx is returned when visor is not set in module initialization context
var ErrNoVisorInCtx = errors.New("visor not set in module initialization context")

// ErrNoErrorsCtx is returned when errors channel is not set in module initialization context
var ErrNoErrorsCtx = errors.New("errors not set in module initialization context")

// withInitCtx wraps init function and returns a hook that can be used in
// the module system
// Passed context should have visor value under visorKey key, this visor will be used
// in the passed function
// Passed context should have errors channel for module runtime errors. It can be accessed
// through a function call
func withInitCtx(f initFn) vinit.Hook {
	return func(ctx context.Context, log *logging.Logger) error {
		val := ctx.Value(visorKey)
		v, ok := val.(*Visor)
		if !ok && v == nil {
			return ErrNoVisorInCtx
		}
		val = ctx.Value(runtimeErrsKey)
		errs, ok := val.(chan error)
		if !ok && errs == nil {
			return ErrNoErrorsCtx
		}
		return f(ctx, v, log)
	}
}

func getErrors(ctx context.Context) chan error {
	val := ctx.Value(runtimeErrsKey)
	errs, ok := val.(chan error)
	if !ok && errs == nil {
		// ok to panic because with check for this value in withInitCtx
		// probably will never be reached, but better than generic NPE just in case
		panic("runtime errors channel is not set in context")
	}
	return errs
}
