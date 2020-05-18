package visor

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/SkycoinProject/dmsg/netutil"
	"github.com/sirupsen/logrus"

	"github.com/SkycoinProject/dmsg"
	"github.com/SkycoinProject/dmsg/cipher"

	"github.com/SkycoinProject/skywire-mainnet/internal/utclient"
	"github.com/SkycoinProject/skywire-mainnet/internal/vpn"
	"github.com/SkycoinProject/skywire-mainnet/pkg/app/appdisc"
	"github.com/SkycoinProject/skywire-mainnet/pkg/app/appserver"
	"github.com/SkycoinProject/skywire-mainnet/pkg/app/launcher"
	"github.com/SkycoinProject/skywire-mainnet/pkg/routefinder/rfclient"
	"github.com/SkycoinProject/skywire-mainnet/pkg/router"
	"github.com/SkycoinProject/skywire-mainnet/pkg/setup/setupclient"
	"github.com/SkycoinProject/skywire-mainnet/pkg/skyenv"
	"github.com/SkycoinProject/skywire-mainnet/pkg/snet"
	"github.com/SkycoinProject/skywire-mainnet/pkg/transport"
	"github.com/SkycoinProject/skywire-mainnet/pkg/transport/tpdclient"
	"github.com/SkycoinProject/skywire-mainnet/pkg/util/updater"
)

type initFunc func(v *Visor) bool

func initUpdater(v *Visor) bool {
	report := v.makeReporter("updater")

	restartCheckDelay, err := time.ParseDuration(v.conf.RestartCheckDelay)
	if err != nil {
		return report(err)
	}

	v.restartCtx.SetCheckDelay(restartCheckDelay)
	v.restartCtx.RegisterLogger(v.log)
	v.updater = updater.New(v.log, v.restartCtx, v.conf.Launcher.BinPath)
	return report(nil)
}

func initSNet(v *Visor) bool {
	report := v.makeReporter("snet")

	n := snet.New(snet.Config{
		PubKey: v.conf.KeyPair.PubKey,
		SecKey: v.conf.KeyPair.SecKey,
		Dmsg:   v.conf.Dmsg,
		STCP:   v.conf.STCP,
	})
	if err := n.Init(); err != nil {
		return report(err)
	}
	v.pushCloseStack("snet", func() bool {
		return report(n.Close())
	})

	v.net = n
	return report(nil)
}

func initTransport(v *Visor) bool {
	report := v.makeReporter("transport")
	conf := v.conf.Transport

	tpdC, err := tpdclient.NewHTTP(conf.Discovery, v.conf.KeyPair.PubKey, v.conf.KeyPair.SecKey)
	if err != nil {
		return report(fmt.Errorf("failed to create transport discovery client: %w", err))
	}

	var logS transport.LogStore
	switch conf.LogStore.Type {
	case LogStoreFile:
		logS, err = transport.FileTransportLogStore(conf.LogStore.Location)
		if err != nil {
			return report(fmt.Errorf("failed to create %s log store: %w", LogStoreFile, err))
		}
	case LogStoreMemory:
		logS = transport.InMemoryTransportLogStore()
	default:
		return report(fmt.Errorf("invalid log store type: %s", conf.LogStore.Type))
	}

	tpMConf := transport.ManagerConfig{
		PubKey:          v.conf.KeyPair.PubKey,
		SecKey:          v.conf.KeyPair.SecKey,
		DefaultVisors:   conf.TrustedVisors,
		DiscoveryClient: tpdC,
		LogStore:        logS,
	}

	tpM, err := transport.NewManager(v.net, &tpMConf)
	if err != nil {
		return report(fmt.Errorf("failed to start transport manager: %w", err))
	}

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
}

func initRouter(v *Visor) bool {
	report := v.makeReporter("router")
	conf := v.conf.Routing

	rConf := router.Config{
		Logger:           v.MasterLogger().PackageLogger("router"),
		PubKey:           v.conf.KeyPair.PubKey,
		SecKey:           v.conf.KeyPair.SecKey,
		TransportManager: v.tpM,
		RouteFinder:      rfclient.NewHTTP(conf.RouteFinder, time.Duration(conf.RouteFinderTimeout)),
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

	v.router = r
	return report(nil)
}

func initLauncher(v *Visor) bool {
	report := v.makeReporter("launcher")
	conf := v.conf.Launcher

	// Prepare app discovery factory.
	factory := appdisc.Factory{Log: v.MasterLogger().PackageLogger("app_disc")}
	if conf.Discovery != nil {
		factory.PK = v.conf.KeyPair.PubKey
		factory.SK = v.conf.KeyPair.SecKey
		factory.UpdateInterval = time.Duration(conf.Discovery.UpdateInterval)
		factory.ProxyDisc = conf.Discovery.ProxyDisc
	}

	// Prepare proc manager.
	procMLog := v.MasterLogger().PackageLogger("proc_manager")
	procM, err := appserver.NewProcManager(procMLog, &factory, conf.ServerAddr)
	if err != nil {
		return report(fmt.Errorf("failed to start proc_manager: %w", err))
	}

	v.pushCloseStack("launcher.proc_manager", func() bool {
		return report(procM.Close())
	})

	// Prepare launcher.
	launchConf := launcher.Config{
		VisorPK:    v.conf.KeyPair.PubKey,
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
	launch.AutoStart(map[string]func() []string{
		skyenv.VPNClientName: func() []string { return makeVPNEnvs(v.conf, v.net) },
		skyenv.VPNServerName: func() []string { return makeVPNEnvs(v.conf, v.net) },
	})

	v.procM = procM
	v.appL = launch
	return report(nil)
}

func makeVPNEnvs(conf *Config, n *snet.Network) []string {
	var envCfg vpn.DirectRoutesEnvConfig

	if conf.Dmsg != nil {
		envCfg.DmsgDiscovery = conf.Dmsg.Discovery
		r := netutil.NewRetrier(logrus.New(), 1*time.Second, 10*time.Second, 0, 1)
		err := r.Do(context.Background(), func() error {
			envCfg.DmsgServers = n.Dmsg().ConnectedServers()
			if len(envCfg.DmsgServers) == 0 {
				return errors.New("no Dmsg servers found")
			}

			return nil
		})
		if err != nil {
			// TODO: remove panic
			panic(fmt.Errorf("error getting Dmsg servers: %w", err))
		}
	}
	if conf.Transport != nil {
		envCfg.TPDiscovery = conf.Transport.Discovery
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

	envMap := vpn.AppEnvArgs(envCfg)

	envs := make([]string, 0, len(envMap))
	for k, v := range vpn.AppEnvArgs(envCfg) {
		envs = append(envs, fmt.Sprintf("%s=%s", k, v))
	}
	return envs
}

func initCLI(v *Visor) bool {
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
}

func initHypervisors(v *Visor) bool {
	report := v.makeReporter("hypervisors")

	hvErrs := make(map[cipher.PubKey]chan error, len(v.conf.Hypervisors))
	for _, hv := range v.conf.Hypervisors {
		hvErrs[hv.PubKey] = make(chan error, 1)
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
}

func initUptimeTracker(v *Visor) bool {
	const tickDuration = time.Second

	report := v.makeReporter("uptime_tracker")
	conf := v.conf.UptimeTracker

	if conf == nil {
		v.log.Info("'uptime_tracker' is not configured, skipping.")
		return true
	}

	ut, err := utclient.NewHTTP(conf.Addr, v.conf.KeyPair.PubKey, v.conf.KeyPair.SecKey)
	if err != nil {
		// TODO(evanlinjin): We should design utclient to retry automatically instead of returning error.
		//return report(err)
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

	return true
}
