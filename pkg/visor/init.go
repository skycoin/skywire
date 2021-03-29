package visor

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/skycoin/dmsg"
	dmsgnetutil "github.com/skycoin/dmsg/netutil"

	"github.com/sirupsen/logrus"
	"github.com/skycoin/dmsg/cipher"
	"github.com/skycoin/dmsg/dmsgctrl"
	"github.com/skycoin/skycoin/src/util/logging"

	"github.com/skycoin/skywire/internal/utclient"
	"github.com/skycoin/skywire/internal/vpn"
	"github.com/skycoin/skywire/pkg/app/appdisc"
	"github.com/skycoin/skywire/pkg/app/appevent"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/app/launcher"
	"github.com/skycoin/skywire/pkg/routefinder/rfclient"
	"github.com/skycoin/skywire/pkg/router"
	"github.com/skycoin/skywire/pkg/setup/setupclient"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/snet"
	"github.com/skycoin/skywire/pkg/snet/arclient"
	"github.com/skycoin/skywire/pkg/snet/directtp/tptypes"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/transport/tpdclient"
	"github.com/skycoin/skywire/pkg/util/updater"
	"github.com/skycoin/skywire/pkg/visor/hypervisorconfig"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
	vinit "github.com/skycoin/skywire/pkg/visor/visorinit"
)

type visorCtxKey int

const visorKey visorCtxKey = iota

var up, ebc, ar, disc, sn, pty, tr, rt, launch, cli, hvs, ut, trv, hv, dmsgCtrl vinit.Module

// visor module to group all visor dependencies
var vis vinit.Module

// todo: return error from init functions with meaningful values instead of bool
// todo: consider refactoring reporting system

func registerModules(logger *logging.MasterLogger) {
	// procedure:
	// look at init function for module x. if init function makes use of
	// field v.y for some module y of visor v, then y depends on x.
	// if init function for x passes v itself, it's fucked up. All deps should be added, but
	// better yet this should be refactored to pass the actual dep
	up = vinit.MakeModule("updater", initUpdater, logger)
	ebc = vinit.MakeModule("event_broadcaster", initEventBroadcaster, logger)
	ar = vinit.MakeModule("address_resolver", initAddressResolver, logger)
	disc = vinit.MakeModule("discovery", initDiscovery, logger)
	sn = vinit.MakeModule("snet", initSNet, logger, &ar, &disc, &ebc)
	dmsgCtrl = vinit.MakeModule("dmsg_ctrl", initDmsgCtrl, logger, &sn)
	pty = vinit.MakeModule("dmsg_pty", initDmsgpty, logger, &sn)
	tr = vinit.MakeModule("transport", initTransport, logger, &sn, &ebc)
	rt = vinit.MakeModule("router", initRouter, logger, &tr, &sn)
	launch = vinit.MakeModule("launcher", initLauncher, logger, &ebc, &disc, &sn, &tr, &rt)
	cli = vinit.MakeModule("cli", initCLI, logger)
	hvs = vinit.MakeModule("hypervisors", initHypervisors, logger, &sn)
	ut = vinit.MakeModule("uptime_tracker", initUptimeTracker, logger)
	trv = vinit.MakeModule("trusted_visors", initTrustedVisors, logger, &tr)
	vis = vinit.MakeModule("visor", vinit.DoNothing, logger, &up, &ebc, &ar, &disc, &sn, &pty,
		&tr, &rt, &launch, &cli, &hvs, &ut, &trv, &dmsgCtrl)
	hv = vinit.MakeModule("hypervisor", initHypervisor, logger, &vis)
}

// -------------------------------------------------------------------

func initUpdater(ctx context.Context, log *logging.Logger) error {
	v, err := getVisor(ctx)
	if err != nil {
		return err
	}
	updater := updater.New(v.log, v.restartCtx, v.conf.Launcher.BinPath)

	v.initLock.Lock()
	defer v.initLock.Unlock()
	v.restartCtx.SetCheckDelay(time.Duration(v.conf.RestartCheckDelay))
	v.restartCtx.RegisterLogger(v.log)
	v.updater = updater
	return nil
}

func initEventBroadcaster(ctx context.Context, log *logging.Logger) error {
	v, err := getVisor(ctx)
	if err != nil {
		return err
	}
	report := v.makeReporter("event_broadcaster")

	const ebcTimeout = time.Second
	ebc := appevent.NewBroadcaster(log, ebcTimeout)
	v.pushCloseStack("event_broadcaster", func() bool {
		return report(ebc.Close())
	})

	v.initLock.Lock()
	v.ebc = ebc
	v.initLock.Unlock()
	return nil
}

func initAddressResolver(ctx context.Context, log *logging.Logger) error {
	v, err := getVisor(ctx)
	if err != nil {
		return err
	}
	report := v.makeReporter("address-resolver")
	conf := v.conf.Transport

	arClient, err := arclient.NewHTTP(conf.AddressResolver, v.conf.PK, v.conf.SK)
	if err != nil {
		err := fmt.Errorf("failed to create address resolver client: %w", err)
		report(err)
		return err
	}

	v.initLock.Lock()
	v.arClient = arClient
	v.initLock.Unlock()
	return nil
}

func initDiscovery(ctx context.Context, log *logging.Logger) error {
	v, err := getVisor(ctx)
	if err != nil {
		return err
	}

	// Prepare app discovery factory.
	factory := appdisc.Factory{
		Log: v.MasterLogger().PackageLogger("app_discovery"),
	}

	conf := v.conf.Launcher

	if conf.Discovery != nil {
		factory.PK = v.conf.PK
		factory.SK = v.conf.SK
		factory.UpdateInterval = time.Duration(conf.Discovery.UpdateInterval)
		factory.ProxyDisc = conf.Discovery.ServiceDisc
	}
	v.initLock.Lock()
	v.serviceDisc = factory
	v.initLock.Unlock()
	return nil
}

func initSNet(ctx context.Context, log *logging.Logger) error {
	v, err := getVisor(ctx)
	if err != nil {
		return err
	}
	report := v.makeReporter("snet")

	nc := snet.NetworkConfigs{
		Dmsg: v.conf.Dmsg,
		STCP: v.conf.STCP,
	}

	conf := snet.Config{
		PubKey:         v.conf.PK,
		SecKey:         v.conf.SK,
		ARClient:       v.arClient,
		NetworkConfigs: nc,
		ServiceDisc:    v.serviceDisc,
		PublicTrusted:  v.conf.PublicTrustedVisor,
	}

	n, err := snet.New(conf, v.ebc)
	if err != nil {
		report(err)
		return err
	}

	if err := n.Init(); err != nil {
		report(err)
		return err
	}
	v.pushCloseStack("snet", func() bool {
		return report(n.Close())
	})

	v.initLock.Lock()
	v.net = n
	v.initLock.Unlock()
	return nil
}

func initDmsgCtrl(ctx context.Context, _ *logging.Logger) error {
	v, err := getVisor(ctx)
	if err != nil {
		return err
	}
	report := v.makeReporter("snet.dmsg.ctrl")
	dmsgC := v.net.Dmsg()
	if dmsgC == nil {
		return nil
	}
	const dmsgTimeout = time.Second * 20
	logger := dmsgC.Logger().WithField("timeout", dmsgTimeout)
	logger.Info("Connecting to the dmsg network...")
	go func() {
		select {
		case <-time.After(dmsgTimeout):
			logger.Warn("Failed to connect to the dmsg network, will try again later.")
		case <-v.net.Dmsg().Ready():
			logger.Info("Connected to the dmsg network.")
		}
	}()

	// dmsgctrl setup
	cl, err := dmsgC.Listen(skyenv.DmsgCtrlPort)
	if err != nil {
		report(err)
		return err
	}
	v.pushCloseStack("snet.dmsgctrl", func() bool {
		return report(cl.Close())
	})

	dmsgctrl.ServeListener(cl, 0)
	return nil
}

func initTransport(ctx context.Context, log *logging.Logger) error {
	v, err := getVisor(ctx)
	if err != nil {
		return err
	}
	report := v.makeReporter("transport")
	conf := v.conf.Transport

	tpdC, err := connectToTpDisc(v)
	if err != nil {
		err := fmt.Errorf("failed to create transport discovery client: %w", err)
		report(err)
		return err
	}

	var logS transport.LogStore
	switch conf.LogStore.Type {
	case visorconfig.FileLogStore:
		logS, err = transport.FileTransportLogStore(conf.LogStore.Location)
		if err != nil {
			err := fmt.Errorf("failed to create %s log store: %w", visorconfig.FileLogStore, err)
			report(err)
			return err
		}
	case visorconfig.MemoryLogStore:
		logS = transport.InMemoryTransportLogStore()
	default:
		err := fmt.Errorf("invalid log store type: %s", conf.LogStore.Type)
		report(err)
		return err
	}

	tpMConf := transport.ManagerConfig{
		PubKey:          v.conf.PK,
		SecKey:          v.conf.SK,
		DefaultVisors:   conf.TrustedVisors,
		DiscoveryClient: tpdC,
		LogStore:        logS,
	}
	managerLogger := v.MasterLogger().PackageLogger("transport_manager")
	tpM, err := transport.NewManager(managerLogger, v.net, &tpMConf)
	if err != nil {
		err := fmt.Errorf("failed to start transport manager: %w", err)
		report(err)
		return err
	}

	tpM.OnAfterTPClosed(func(network, addr string) {
		if network == tptypes.STCPR && addr != "" {
			data := appevent.TCPCloseData{RemoteNet: network, RemoteAddr: addr}
			event := appevent.NewEvent(appevent.TCPClose, data)
			if err := v.ebc.Broadcast(context.Background(), event); err != nil {
				v.log.WithError(err).Errorln("Failed to broadcast TCPClose event")
			}
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	wg := new(sync.WaitGroup)
	wg.Add(1)

	go func() {
		defer wg.Done()
		tpM.Serve(ctx)
	}()

	v.pushCloseStack("transport.manager", func() bool {
		cancel()
		ok := report(tpM.Close())
		wg.Wait()
		return ok
	})

	v.initLock.Lock()
	v.tpM = tpM
	v.initLock.Unlock()
	return nil
}

func initRouter(ctx context.Context, log *logging.Logger) error {
	v, err := getVisor(ctx)
	if err != nil {
		return err
	}
	report := v.makeReporter("router")
	conf := v.conf.Routing
	rfClient := rfclient.NewHTTP(conf.RouteFinder, time.Duration(conf.RouteFinderTimeout))

	rConf := router.Config{
		Logger:           v.MasterLogger().PackageLogger("router"),
		PubKey:           v.conf.PK,
		SecKey:           v.conf.SK,
		TransportManager: v.tpM,
		RouteFinder:      rfClient,
		RouteGroupDialer: setupclient.NewSetupNodeDialer(),
		SetupNodes:       conf.SetupNodes,
		RulesGCInterval:  0, // TODO
		MinHops:          v.conf.Routing.MinHops,
	}

	r, err := router.New(v.net, &rConf)
	if err != nil {
		err := fmt.Errorf("failed to create router: %w", err)
		report(err)
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	wg := new(sync.WaitGroup)
	wg.Add(1)

	go func() {
		defer wg.Done()
		if err := r.Serve(ctx); err != nil {
			err := fmt.Errorf("serve router stopped: %w", err)
			report(err)
		}
	}()

	v.pushCloseStack("router.serve", func() bool {
		cancel()
		ok := report(r.Close())
		wg.Wait()
		return ok
	})

	v.initLock.Lock()
	v.rfClient = rfClient
	v.router = r
	v.initLock.Unlock()

	return nil
}

func initLauncher(ctx context.Context, log *logging.Logger) error {
	v, err := getVisor(ctx)
	if err != nil {
		return err
	}
	report := v.makeReporter("launcher")
	conf := v.conf.Launcher

	// Prepare proc manager.
	procM, err := appserver.NewProcManager(v.MasterLogger(), &v.serviceDisc, v.ebc, conf.ServerAddr)
	if err != nil {
		err := fmt.Errorf("failed to start proc_manager: %w", err)
		report(err)
		return err
	}

	v.pushCloseStack("launcher.proc_manager", func() bool {
		return report(procM.Close())
	})

	// Prepare launcher.
	launchConf := launcher.Config{
		VisorPK:    v.conf.PK,
		Apps:       conf.Apps,
		ServerAddr: conf.ServerAddr,
		BinPath:    conf.BinPath,
		LocalPath:  conf.LocalPath,
	}

	launchLog := v.MasterLogger().PackageLogger("launcher")

	launch, err := launcher.NewLauncher(launchLog, launchConf, v.net.Dmsg(), v.router, procM)
	if err != nil {
		err := fmt.Errorf("failed to start launcher: %w", err)
		report(err)
		return err
	}

	err = launch.AutoStart(map[string]func() ([]string, error){
		skyenv.VPNClientName: func() ([]string, error) { return makeVPNEnvs(v.conf, v.net, v.tpM.STCPRRemoteAddrs()) },
		skyenv.VPNServerName: func() ([]string, error) { return makeVPNEnvs(v.conf, v.net, nil) },
	})

	if err != nil {
		err := fmt.Errorf("failed to autostart apps: %w", err)
		report(err)
		return err
	}

	v.initLock.Lock()
	v.procM = procM
	v.appL = launch
	v.initLock.Unlock()

	return nil
}

func makeVPNEnvs(conf *visorconfig.V1, n *snet.Network, tpRemoteAddrs []string) ([]string, error) {
	var envCfg vpn.DirectRoutesEnvConfig

	if conf.Dmsg != nil {
		envCfg.DmsgDiscovery = conf.Dmsg.Discovery

		r := dmsgnetutil.NewRetrier(logrus.New(), 1*time.Second, 10*time.Second, 0, 1)
		err := r.Do(context.Background(), func() error {
			for _, ses := range n.Dmsg().AllSessions() {
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

func initCLI(ctx context.Context, log *logging.Logger) error {
	v, err := getVisor(ctx)
	if err != nil {
		return err
	}
	report := v.makeReporter("cli")

	if v.conf.CLIAddr == "" {
		v.log.Info("'cli_addr' is not configured, skipping.")
		return nil
	}

	cliL, err := net.Listen("tcp", v.conf.CLIAddr)
	if err != nil {
		report(err)
		return err
	}

	v.pushCloseStack("cli.listener", func() bool {
		return report(cliL.Close())
	})

	rpcS, err := newRPCServer(v, "CLI")
	if err != nil {
		err := fmt.Errorf("failed to start rpc server for cli: %w", err)
		report(err)
		return err
	}
	go rpcS.Accept(cliL) // We do not use sync.WaitGroup here as it will never return anyway.

	return nil
}

func initHypervisors(ctx context.Context, log *logging.Logger) error {
	v, err := getVisor(ctx)
	if err != nil {
		return err
	}
	report := v.makeReporter("hypervisors")

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
			report(err)
			return err
		}

		ctx, cancel := context.WithCancel(context.Background())
		wg := new(sync.WaitGroup)
		wg.Add(1)

		go func(hvErrs chan error) {
			defer wg.Done()
			ServeRPCClient(ctx, log, v.net, rpcS, addr, hvErrs)
		}(hvErrs)

		v.pushCloseStack("hypervisor."+hvPK.String()[:shortHashLen], func() bool {
			cancel()
			wg.Wait()
			return true
		})
	}

	return nil
}

func initUptimeTracker(ctx context.Context, log *logging.Logger) error {
	v, err := getVisor(ctx)
	if err != nil {
		return err
	}
	const tickDuration = 1 * time.Minute

	report := v.makeReporter("uptime_tracker")
	conf := v.conf.UptimeTracker

	if conf == nil {
		v.log.Info("'uptime_tracker' is not configured, skipping.")
		return nil
	}

	ut, err := utclient.NewHTTP(conf.Addr, v.conf.PK, v.conf.SK)
	if err != nil {
		// TODO(evanlinjin): We should design utclient to retry automatically instead of returning error.
		// return report(err)
		v.log.WithError(err).Warn("Failed to connect to uptime tracker.")
		return nil
	}

	ticker := time.NewTicker(tickDuration)

	go func() {
		for range ticker.C {
			ctx := context.Background()
			if err := ut.UpdateVisorUptime(ctx); err != nil {
				log.WithError(err).Warn("Failed to update visor uptime.")
			}
		}
	}()

	v.pushCloseStack("uptime_tracker", func() bool {
		ticker.Stop()
		return report(nil)
	})

	v.initLock.Lock()
	v.uptimeTracker = ut
	v.initLock.Unlock()

	return nil
}

func initTrustedVisors(ctx context.Context, log *logging.Logger) error {
	v, err := getVisor(ctx)
	if err != nil {
		return err
	}
	const trustedVisorsTransportType = tptypes.STCPR

	go func() {
		time.Sleep(transport.TrustedVisorsDelay)
		for _, pk := range v.tpM.Conf.DefaultVisors {
			v.log.WithField("pk", pk).Infof("Adding trusted visor")

			if _, err := v.tpM.SaveTransport(context.Background(), pk, trustedVisorsTransportType); err != nil {
				v.log.
					WithError(err).
					WithField("pk", pk).
					WithField("type", trustedVisorsTransportType).
					Warnf("Failed to add transport to trusted visor via")
			} else {
				v.log.
					WithField("pk", pk).
					WithField("type", trustedVisorsTransportType).
					Infof("Added transport to trusted visor")
			}
		}
	}()

	return nil
}

func initHypervisor(ctx context.Context, log *logging.Logger) error {
	v, err := getVisor(ctx)
	if err != nil {
		return err
	}
	if v.conf.Hypervisor == nil {
		return nil
	}

	v.log.Infof("Initializing hypervisor")

	ctx, cancel := context.WithCancel(context.Background())

	conf := *v.conf.Hypervisor
	conf.PK = v.conf.PK
	conf.SK = v.conf.SK
	conf.DmsgDiscovery = v.conf.Dmsg.Discovery

	// Prepare hypervisor.
	hv, err := New(conf, v, v.net.Dmsg())
	if err != nil {
		v.log.Fatalln("Failed to start hypervisor:", err)
	}

	serveDmsg(ctx, v.log, hv, conf)

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

// todo: consider moving to td package
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

// todo: move to hypervisor file, make a method and remove conf argument
func serveDmsg(ctx context.Context, log *logging.Logger, hv *Hypervisor, conf hypervisorconfig.Config) {
	go func() {
		if err := hv.ServeRPC(ctx, conf.DmsgPort); err != nil {
			log.WithError(err).Fatal("Failed to serve RPC client over dmsg.")
		}
	}()
	log.WithField("addr", dmsg.Addr{PK: conf.PK, Port: conf.DmsgPort}).
		Info("Serving RPC client over dmsg.")
}

// ErrNoVisorInCtx is returned when visor is not set in module initialization context
var ErrNoVisorInCtx = errors.New("visor not set in module initialization context")

func getVisor(ctx context.Context) (*Visor, error) {
	val := ctx.Value(visorKey)
	v, ok := val.(*Visor)
	if !ok || v == nil {
		return nil, ErrNoVisorInCtx
	}
	return v, nil
}
