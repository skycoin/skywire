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

	"github.com/rakyll/statik/fs"
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

var up, ebc, ar, disc, sn, pty, tr, rt, launch, cli, hvs, ut, trv, hv vinit.Module

// visor module to group all visor dependencies
var vis vinit.Module

// todo: add visor transaction that allows updating visor fields atomically

func registerModules() {
	// procedure:
	// look at init function for module x. if init function makes use of
	// field v.y for some module y of visor v, then y depends on x.
	// if init function for x passes v itself, it's fucked up. All deps should be added, but
	// better yet this should be refactored to pass the actual dep
	up = vinit.MakeModule("updater", initUpdater)
	ebc = vinit.MakeModule("event broadcaster", initEventBroadcaster)
	ar = vinit.MakeModule("address resolver", initAddressResolver)
	disc = vinit.MakeModule("discovery", initDiscovery)
	sn = vinit.MakeModule("snet", initSNet)
	pty = vinit.MakeModule("dmsgpty", initDmsgpty)
	tr = vinit.MakeModule("transport", initTransport)
	rt = vinit.MakeModule("router", initRouter)
	launch = vinit.MakeModule("launcher", initLauncher)
	cli = vinit.MakeModule("cli", initCLI)
	hvs = vinit.MakeModule("hypervisors", initHypervisors)
	ut = vinit.MakeModule("uptime tracker", initUptimeTracker)
	trv = vinit.MakeModule("trusted visors", initTrustedVisors)
	vis = vinit.MakeModule("visor", vinit.DoNothing)
	hv = vinit.MakeModule("hypervisor", initHypervisor, &vis)
}

// -------------------------------------------------------------------

func initUpdater(ctx context.Context) error {
	return withVisorCtx(ctx, func(v *Visor) bool {
		report := v.makeReporter("updater")

		v.restartCtx.SetCheckDelay(time.Duration(v.conf.RestartCheckDelay))
		v.restartCtx.RegisterLogger(v.log)
		v.updater = updater.New(v.log, v.restartCtx, v.conf.Launcher.BinPath)
		return report(nil)
	})(ctx)
}

func initEventBroadcaster(ctx context.Context) error {
	return withVisorCtx(ctx, func(v *Visor) bool {
		report := v.makeReporter("event_broadcaster")

		log := v.MasterLogger().PackageLogger("event_broadcaster")
		const ebcTimeout = time.Second
		ebc := appevent.NewBroadcaster(log, ebcTimeout)

		v.pushCloseStack("event_broadcaster", func() bool {
			return report(ebc.Close())
		})

		v.ebc = ebc
		return report(nil)
	})(ctx)
}

func initAddressResolver(ctx context.Context) error {
	return withVisorCtx(ctx, func(v *Visor) bool {
		report := v.makeReporter("address-resolver")
		conf := v.conf.Transport

		arClient, err := arclient.NewHTTP(conf.AddressResolver, v.conf.PK, v.conf.SK)
		if err != nil {
			return report(fmt.Errorf("failed to create address resolver client: %w", err))
		}

		v.arClient = arClient

		return report(nil)
	})(ctx)

}

func initDiscovery(ctx context.Context) error {
	return withVisorCtx(ctx, func(v *Visor) bool {
		report := v.makeReporter("discovery")

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

		v.serviceDisc = factory

		return report(nil)
	})(ctx)
}

func initSNet(ctx context.Context) error {
	return withVisorCtx(ctx, func(v *Visor) bool {
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
			return report(err)
		}

		if err := n.Init(); err != nil {
			return report(err)
		}

		v.pushCloseStack("snet", func() bool {
			return report(n.Close())
		})

		if dmsgC := n.Dmsg(); dmsgC != nil {
			const dmsgTimeout = time.Second * 20
			log := dmsgC.Logger().WithField("timeout", dmsgTimeout)
			log.Info("Connecting to the dmsg network...")
			select {
			case <-time.After(dmsgTimeout):
				log.Warn("Failed to connect to the dmsg network, will try again later.")
			case <-n.Dmsg().Ready():
				log.Info("Connected to the dmsg network.")
			}

			// dmsgctrl setup
			cl, err := dmsgC.Listen(skyenv.DmsgCtrlPort)
			if err != nil {
				return report(err)
			}
			v.pushCloseStack("snet.dmsgctrl", func() bool {
				return report(cl.Close())
			})

			dmsgctrl.ServeListener(cl, 0)
		}

		v.net = n
		return report(nil)
	})(ctx)
}

func initDmsgpty(ctx context.Context) error {
	return withVisorCtx(ctx, func(v *Visor) bool {
		return initDmsgptyVisor(v)
	})(ctx)
}

func initTransport(ctx context.Context) error {
	return withVisorCtx(ctx, func(v *Visor) bool {
		report := v.makeReporter("transport")
		conf := v.conf.Transport

		tpdC, err := connectToTpDisc(v)
		if err != nil {
			return report(fmt.Errorf("failed to create transport discovery client: %w", err))
		}

		var logS transport.LogStore
		switch conf.LogStore.Type {
		case visorconfig.FileLogStore:
			logS, err = transport.FileTransportLogStore(conf.LogStore.Location)
			if err != nil {
				return report(fmt.Errorf("failed to create %s log store: %w", visorconfig.FileLogStore, err))
			}
		case visorconfig.MemoryLogStore:
			logS = transport.InMemoryTransportLogStore()
		default:
			return report(fmt.Errorf("invalid log store type: %s", conf.LogStore.Type))
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
			return report(fmt.Errorf("failed to start transport manager: %w", err))
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

		v.tpM = tpM

		return report(nil)
	})(ctx)
}

func initRouter(ctx context.Context) error {
	return withVisorCtx(ctx, func(v *Visor) bool {
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
		}

		r, err := router.New(v.net, &rConf)
		if err != nil {
			return report(fmt.Errorf("failed to create router: %w", err))
		}

		ctx, cancel := context.WithCancel(context.Background())
		wg := new(sync.WaitGroup)
		wg.Add(1)

		go func() {
			defer wg.Done()
			if err := r.Serve(ctx); err != nil {
				report(fmt.Errorf("serve router stopped: %w", err))
			}
		}()

		v.pushCloseStack("router.serve", func() bool {
			cancel()
			ok := report(r.Close())
			wg.Wait()
			return ok
		})

		v.rfClient = rfClient
		v.router = r

		return report(nil)
	})(ctx)
}

func initLauncher(ctx context.Context) error {
	return withVisorCtx(ctx, func(v *Visor) bool {
		report := v.makeReporter("launcher")
		conf := v.conf.Launcher

		// Prepare proc manager.
		procM, err := appserver.NewProcManager(v.MasterLogger(), &v.serviceDisc, v.ebc, conf.ServerAddr)
		if err != nil {
			return report(fmt.Errorf("failed to start proc_manager: %w", err))
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
			return report(fmt.Errorf("failed to start launcher: %w", err))
		}

		err = launch.AutoStart(map[string]func() ([]string, error){
			skyenv.VPNClientName: func() ([]string, error) { return makeVPNEnvs(v.conf, v.net, v.tpM.STCPRRemoteAddrs()) },
			skyenv.VPNServerName: func() ([]string, error) { return makeVPNEnvs(v.conf, v.net, nil) },
		})

		if err != nil {
			return report(fmt.Errorf("failed to autostart apps: %w", err))
		}

		v.procM = procM
		v.appL = launch

		return report(nil)
	})(ctx)
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

func initCLI(ctx context.Context) error {
	return withVisorCtx(ctx, func(v *Visor) bool {
		report := v.makeReporter("cli")

		if v.conf.CLIAddr == "" {
			v.log.Info("'cli_addr' is not configured, skipping.")
			return report(nil)
		}

		cliL, err := net.Listen("tcp", v.conf.CLIAddr)
		if err != nil {
			return report(err)
		}

		v.pushCloseStack("cli.listener", func() bool {
			return report(cliL.Close())
		})

		rpcS, err := newRPCServer(v, "CLI")
		if err != nil {
			return report(fmt.Errorf("failed to start rpc server for cli: %w", err))
		}
		go rpcS.Accept(cliL) // We do not use sync.WaitGroup here as it will never return anyway.

		return report(nil)
	})(ctx)
}

func initHypervisors(ctx context.Context) error {
	return withVisorCtx(ctx, func(v *Visor) bool {
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
				return report(fmt.Errorf("failed to start RPC server for hypervisor %s: %w", hvPK, err))
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

		return report(nil)
	})(ctx)
}

func initUptimeTracker(ctx context.Context) error {
	return withVisorCtx(ctx, func(v *Visor) bool {
		const tickDuration = 1 * time.Minute

		report := v.makeReporter("uptime_tracker")
		conf := v.conf.UptimeTracker

		if conf == nil {
			v.log.Info("'uptime_tracker' is not configured, skipping.")
			return true
		}

		ut, err := utclient.NewHTTP(conf.Addr, v.conf.PK, v.conf.SK)
		if err != nil {
			// TODO(evanlinjin): We should design utclient to retry automatically instead of returning error.
			// return report(err)
			v.log.WithError(err).Warn("Failed to connect to uptime tracker.")
			return true
		}

		log := v.MasterLogger().PackageLogger("uptime_tracker")
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

		v.uptimeTracker = ut

		return true
	})(ctx)
}

func initTrustedVisors(ctx context.Context) error {
	return withVisorCtx(ctx, func(v *Visor) bool {
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

		return true
	})(ctx)
}

func initHypervisor(ctx context.Context) error {
	return withVisorCtx(ctx, func(v *Visor) bool {
		if v.conf.Hypervisor == nil {
			return true
		}

		v.log.Infof("Initializing hypervisor")

		ctx, cancel := context.WithCancel(context.Background())

		assets, err := fs.New()
		if err != nil {
			v.log.Fatalf("Failed to obtain embedded static files: %v", err)
		}

		conf := *v.conf.Hypervisor
		conf.PK = v.conf.PK
		conf.SK = v.conf.SK
		conf.DmsgDiscovery = v.conf.Dmsg.Discovery

		// Prepare hypervisor.
		hv, err := New(conf, assets, v, v.net.Dmsg())
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

		return true
	})(ctx)

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

// withVisorCtx wraps old-style init function and returns a hook that can be used in
// the module system
// Passed context should have visor value under visorKey key, this visor will be used
// in the passed function
// The point of this function is to avoid circular dependency of module system (visorinit) and visor, and
// to make use of existing init functions that take visor as argument
func withVisorCtx(ctx context.Context, f func(v *Visor) bool) vinit.Hook {
	return func(_ context.Context) error {
		val := ctx.Value(visorKey)
		v, ok := val.(*Visor)
		if !ok && v == nil {
			return fmt.Errorf("visor not set in module initialization context")
		}
		res := f(v)
		if !res {
			return fmt.Errorf("init failed")
		}
		return nil
	}
}
