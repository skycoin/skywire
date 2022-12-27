// Package visor pkg/visor/init.go
package visor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/ccding/go-stun/stun"
	"github.com/sirupsen/logrus"
	"github.com/skycoin/dmsg/pkg/direct"
	dmsgdisc "github.com/skycoin/dmsg/pkg/disc"
	"github.com/skycoin/dmsg/pkg/dmsg"
	"github.com/skycoin/dmsg/pkg/dmsgctrl"
	"github.com/skycoin/dmsg/pkg/dmsgget"
	"github.com/skycoin/dmsg/pkg/dmsghttp"
	"github.com/skycoin/dmsg/pkg/dmsgpty"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire-utilities/pkg/netutil"
	utilenv "github.com/skycoin/skywire-utilities/pkg/skyenv"
	"github.com/skycoin/skywire/internal/vpn"
	"github.com/skycoin/skywire/pkg/app/appdisc"
	"github.com/skycoin/skywire/pkg/app/appevent"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/app/launcher"
	"github.com/skycoin/skywire/pkg/dmsgc"
	"github.com/skycoin/skywire/pkg/routefinder/rfclient"
	"github.com/skycoin/skywire/pkg/router"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/servicedisc"
	"github.com/skycoin/skywire/pkg/setup/setupclient"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/transport/network"
	"github.com/skycoin/skywire/pkg/transport/network/addrresolver"
	"github.com/skycoin/skywire/pkg/transport/network/stcp"
	ts "github.com/skycoin/skywire/pkg/transport/setup"
	"github.com/skycoin/skywire/pkg/transport/tpdclient"
	"github.com/skycoin/skywire/pkg/utclient"
	"github.com/skycoin/skywire/pkg/util/osutil"
	"github.com/skycoin/skywire/pkg/visor/dmsgtracker"
	"github.com/skycoin/skywire/pkg/visor/logserver"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
	vinit "github.com/skycoin/skywire/pkg/visor/visorinit"
)

type visorCtxKey int

const visorKey visorCtxKey = iota

type runtimeErrsCtxKey int

const runtimeErrsKey runtimeErrsCtxKey = iota

const ownerRWX = 0700

// Visor initialization is split into modules, that can be initialized independently
// Modules are declared here as package-level variables, but also need to be registered
// in the modules system: they need init function and dependencies and their name to be set
// To add new piece of functionality to visor, you need to create a new module variable
// and register it properly in registerModules function
var (
	// Event broadcasting system
	ebc vinit.Module
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
	// Application launcher
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
	// Dmsg ctrl module
	dmsgCtrl vinit.Module
	// Dmsg http log server module
	dmsgHTTPLogServer vinit.Module
	// Dmsg http module
	dmsgHTTP vinit.Module
	// Dmsg trackers module
	dmsgTrackers vinit.Module
	// Ping module
	pi vinit.Module
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
	dmsgHTTP = maker("dmsg_http", initDmsgHTTP)
	ebc = maker("event_broadcaster", initEventBroadcaster)
	ar = maker("address_resolver", initAddressResolver, &dmsgHTTP)
	disc = maker("discovery", initDiscovery, &dmsgHTTP)
	tr = maker("transport", initTransport, &ar, &ebc, &dmsgHTTP)

	sc = maker("stun_client", initStunClient)
	sudphC = maker("sudph", initSudphClient, &sc, &tr)
	stcprC = maker("stcpr", initStcprClient, &tr)
	stcpC = maker("stcp", initStcpClient, &tr)
	dmsgC = maker("dmsg", initDmsg, &ebc, &dmsgHTTP)
	dmsgCtrl = maker("dmsg_ctrl", initDmsgCtrl, &dmsgC, &tr)
	dmsgHTTPLogServer = maker("dmsghttp_logserver", initDmsgHTTPLogServer, &dmsgC, &tr)
	dmsgTrackers = maker("dmsg_trackers", initDmsgTrackers, &dmsgC)

	pty = maker("dmsg_pty", initDmsgpty, &dmsgC)
	rt = maker("router", initRouter, &tr, &dmsgC, &dmsgHTTP)
	launch = maker("launcher", initLauncher, &ebc, &disc, &dmsgC, &tr, &rt)
	cli = maker("cli", initCLI)
	hvs = maker("hypervisors", initHypervisors, &dmsgC)
	ut = maker("uptime_tracker", initUptimeTracker, &dmsgHTTP)
	pv = maker("public_autoconnect", initPublicAutoconnect, &tr, &disc)
	trs = maker("transport_setup", initTransportSetup, &dmsgC, &tr)
	tm = vinit.MakeModule("transports", vinit.DoNothing, logger, &sc, &sudphC, &dmsgCtrl, &dmsgHTTPLogServer, &dmsgTrackers, &launch)
	pvs = maker("public_visor", initPublicVisor, &tr, &ar, &disc, &stcprC)
	pi = maker("ping", initPing, &dmsgC, &tm)
	vis = vinit.MakeModule("visor", vinit.DoNothing, logger, &ebc, &ar, &disc, &pty,
		&tr, &rt, &launch, &cli, &hvs, &ut, &pv, &pvs, &trs, &stcpC, &stcprC, &pi)

	hv = maker("hypervisor", initHypervisor, &vis)
}

type initFn func(context.Context, *Visor, *logging.Logger) error

func initDmsgHTTP(ctx context.Context, v *Visor, log *logging.Logger) error {
	var keys cipher.PubKeys
	servers := v.conf.Dmsg.Servers

	if len(servers) == 0 {
		return nil
	}

	keys = append(keys, v.conf.PK)
	entries := direct.GetAllEntries(keys, servers)
	dClient := direct.NewClient(entries, v.MasterLogger().PackageLogger("dmsg_http:direct_client"))

	dmsgDC, closeDmsgDC, err := direct.StartDmsg(ctx, v.MasterLogger().PackageLogger("dmsg_http:dmsgDC"),
		v.conf.PK, v.conf.SK, dClient, dmsg.DefaultConfig())
	if err != nil {
		return fmt.Errorf("failed to start dmsg: %w", err)
	}

	dmsgHTTP := http.Client{Transport: dmsghttp.MakeHTTPTransport(ctx, dmsgDC)}

	v.pushCloseStack("dmsg_http", func() error {
		closeDmsgDC()
		return nil
	})

	v.initLock.Lock()
	v.dClient = dClient
	v.dmsgHTTP = &dmsgHTTP
	v.dmsgDC = dmsgDC
	v.initLock.Unlock()
	time.Sleep(time.Duration(len(entries)) * time.Second)
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

	httpC, err := getHTTPClient(ctx, v, conf.AddressResolver)
	if err != nil {
		return err
	}

	// only needed for dmsghttp
	pIP, err := getPublicIP(v, conf.AddressResolver)
	if err != nil {
		return err
	}

	arClient, err := addrresolver.NewHTTP(conf.AddressResolver, v.conf.PK, v.conf.SK, httpC, pIP, log, v.MasterLogger())
	if err != nil {
		err = fmt.Errorf("failed to create address resolver client: %w", err)
		return err
	}

	v.initLock.Lock()
	v.arClient = arClient
	v.initLock.Unlock()

	doneCh := make(chan struct{}, 1)
	v.pushCloseStack("address_resolver", func() error {
		doneCh <- struct{}{}
		return nil
	})

	return nil
}

func initDiscovery(ctx context.Context, v *Visor, log *logging.Logger) error {
	// Prepare app discovery factory.
	factory := appdisc.Factory{
		Log:  v.MasterLogger().PackageLogger("app_discovery"),
		MLog: v.MasterLogger(),
	}

	conf := v.conf.Launcher

	httpC, err := getHTTPClient(ctx, v, conf.ServiceDisc)
	if err != nil {
		return err
	}

	if conf.ServiceDisc != "" {
		factory.PK = v.conf.PK
		factory.SK = v.conf.SK
		factory.ServiceDisc = conf.ServiceDisc
		factory.DisplayNodeIP = conf.DisplayNodeIP
		factory.Client = httpC
		// only needed for dmsghttp
		pIP, err := getPublicIP(v, conf.ServiceDisc)
		if err != nil {
			return err
		}
		factory.ClientPublicIP = pIP
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
	v.stunReadyOnce.Do(func() { close(v.stunReady) })
	return nil
}

func initDmsg(ctx context.Context, v *Visor, log *logging.Logger) (err error) {
	if v.conf.Dmsg == nil {
		return fmt.Errorf("cannot initialize dmsg: empty configuration")
	}

	httpC, err := getHTTPClient(ctx, v, v.conf.Dmsg.Discovery)
	if err != nil {
		return err
	}
	dmsgC := dmsgc.New(v.conf.PK, v.conf.SK, v.ebc, v.conf.Dmsg, httpC, v.MasterLogger())
	wg := new(sync.WaitGroup)
	wg.Add(1)
	go func() {
		defer wg.Done()
		dmsgC.Serve(ctx)
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
	logger.Debug("Connecting to the dmsg network...")
	select {
	case <-time.After(dmsgTimeout):
		logger.Warn("Failed to connect to the dmsg network, will try again later.")
		go func() {
			<-v.dmsgC.Ready()
			logger.Debug("Connected to the dmsg network.")
			v.tpM.InitDmsgClient(ctx, dmsgC)
		}()
	case <-v.dmsgC.Ready():
		logger.Debug("Connected to the dmsg network.")
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

func initDmsgHTTPLogServer(ctx context.Context, v *Visor, log *logging.Logger) error {
	dmsgC := v.dmsgC
	if dmsgC == nil {
		return fmt.Errorf("cannot initialize dmsg log server: dmsg not configured")
	}
	logger := v.MasterLogger().PackageLogger("dmsghttp_logserver")

	var printLog bool
	if v.MasterLogger().GetLevel() == logrus.DebugLevel || v.MasterLogger().GetLevel() == logrus.TraceLevel {
		printLog = true
	}

	lsAPI := logserver.New(logger, v.conf.Transport.LogStore.Location, v.conf.LocalPath, v.conf.CustomDmsgHTTPPath, printLog)

	lis, err := dmsgC.Listen(skyenv.DmsgHTTPPort)
	if err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		if err := lis.Close(); err != nil {
			logger.WithError(err).Error()
		}
	}()

	log.WithField("dmsg_addr", fmt.Sprintf("dmsg://%v", lis.Addr().String())).
		Debug("Serving...")
	srv := &http.Server{
		ReadHeaderTimeout: 2 * time.Second,
		IdleTimeout:       30 * time.Second,
		Handler:           lsAPI,
	}

	wg := new(sync.WaitGroup)
	wg.Add(1)

	go func() {
		defer wg.Done()
		err = srv.Serve(lis)
		if errors.Is(err, dmsg.ErrEntityClosed) {
			return
		}
		if err != nil {
			logger.WithError(err).Error("Logserver exited with error.")
		}
	}()

	v.pushCloseStack("dmsghttp.logserver", func() error {
		if err := srv.Close(); err != nil {
			return err
		}
		wg.Wait()
		return nil
	})

	return nil
}

func initDmsgTrackers(ctx context.Context, v *Visor, _ *logging.Logger) error {
	dmsgC := v.dmsgC

	dtm := dmsgtracker.NewDmsgTrackerManager(v.MasterLogger(), dmsgC, 0, 0)
	v.pushCloseStack("dmsg_tracker_manager", func() error {
		return dtm.Close()
	})
	v.initLock.Lock()
	v.dtm = dtm
	v.initLock.Unlock()
	v.dtmReadyOnce.Do(func() { close(v.dtmReady) })
	return nil
}

func initSudphClient(ctx context.Context, v *Visor, log *logging.Logger) error {

	var serviceURL dmsgget.URL
	_ = serviceURL.Fill(v.conf.Transport.AddressResolver) //nolint:errcheck
	// don't start sudph if we are connection to AR via dmsghttp
	if serviceURL.Scheme == "dmsg" {
		log.Info("SUDPH transport wont be available under dmsghttp")
		return nil
	}

	switch v.stunClient.NATType {
	case stun.NATSymmetric, stun.NATSymmetricUDPFirewall:
		log.Warnf("SUDPH transport wont be available as visor is under %v", v.stunClient.NATType.String())
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

	managerLogger := v.MasterLogger().PackageLogger("transport_manager")

	tpdC, err := connectToTpDisc(ctx, v, managerLogger)
	if err != nil {
		err := fmt.Errorf("failed to create transport discovery client: %w", err)
		return err
	}

	var logS transport.LogStore
	if v.conf.Transport.LogStore.Type == visorconfig.MemoryLogStore {
		logS = transport.InMemoryTransportLogStore()
	} else if v.conf.Transport.LogStore.Type == visorconfig.FileLogStore {
		logS, err = transport.FileTransportLogStore(ctx, v.conf.Transport.LogStore.Location, time.Duration(v.conf.Transport.LogStore.RotationInterval), log)
		if err != nil {
			return err
		}
	} else {
		return fmt.Errorf("invalid store type: %v", v.conf.Transport.LogStore.Type)
	}

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
		MLogger:    v.MasterLogger(),
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
	// To remove the block set by NewTransportListener if dmsg is not initialized
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

	// waiting for at least one transport to initialize
	<-v.tpM.Ready()

	v.pushCloseStack("transport_setup.rpc", func() error {
		cancel()
		return nil
	})
	return nil
}

func initPing(ctx context.Context, v *Visor, log *logging.Logger) error {
	ctx, cancel := context.WithCancel(ctx)
	// waiting for at least one transport to initialize
	<-v.tpM.Ready()

	connApp := appnet.Addr{
		Net:    appnet.TypeSkynet,
		PubKey: v.conf.PK,
		Port:   routing.Port(skyenv.SkyPingPort),
	}

	l, err := appnet.ListenContext(ctx, connApp)
	if err != nil {
		cancel()
		return err
	}

	v.pushCloseStack("skywire_proxy", func() error {
		cancel()
		if cErr := l.Close(); cErr != nil {
			log.WithError(cErr).Error("Error closing listener.")
		}
		return nil
	})

	go func() {
		for {
			log.Debug("Accepting sky proxy conn...")
			conn, err := l.Accept()
			if err != nil {
				if !errors.Is(err, appnet.ErrConnClosed) {
					log.WithError(err).Error("Failed to accept ping conn")
				}
				return
			}
			log.Debug("Accepted sky proxy conn")
			log.Debug("Wrapping conn...")
			wrappedConn, err := appnet.WrapConn(conn)
			if err != nil {
				log.WithError(err).Error("Failed to wrap conn")
				return
			}

			rAddr := wrappedConn.RemoteAddr().(appnet.Addr)
			log.Debugf("Accepted sky proxy conn on %s from %s", wrappedConn.LocalAddr(), rAddr.PubKey)
			go handlePingConn(log, wrappedConn, v)
		}
	}()
	return nil
}

func handlePingConn(log *logging.Logger, remoteConn net.Conn, v *Visor) {
	for {
		buf := make([]byte, (32+v.pingPcktSize)*1024)
		n, err := remoteConn.Read(buf)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				log.WithError(err).Error("Failed to read packet")
			}
			return
		}
		var size PingSizeMsg
		err = json.Unmarshal(buf[:n], &size)
		if err != nil {
			log.WithError(err).Error("Failed to unmarshal json")
			return
		}

		_, err = remoteConn.Write([]byte("ok"))
		if err != nil {
			log.WithError(err).Error("Failed to write message")
			return
		}
		var ping []byte
		for len(ping) != size.Size {
			n, err = remoteConn.Read(buf)
			if err != nil {
				if !errors.Is(err, io.EOF) {
					log.WithError(err).Error("Failed to read packet")
				}
				return
			}
			ping = append(ping, buf[:n]...)
		}
		var msg PingMsg
		err = json.Unmarshal(ping, &msg)
		if err != nil {
			log.WithError(err).Error("Failed to unmarshal json")
			return
		}
		now := time.Now()
		diff := now.Sub(msg.Timestamp)
		v.pingConns[msg.PingPk].latency <- diff

		log.Debugf("Received: %s", buf[:n])
	}
}

// getRouteSetupHooks aka autotransport
func getRouteSetupHooks(ctx context.Context, v *Visor, log *logging.Logger) []router.RouteSetupHook {
	retrier := netutil.NewRetrier(log, time.Second, time.Second*20, 3, 1.3)
	return []router.RouteSetupHook{
		func(rPK cipher.PubKey, tm *transport.Manager) error {
			establishedTransports, _ := v.Transports([]string{string(network.STCPR), string(network.SUDPH), string(network.DMSG)}, []cipher.PubKey{v.conf.PK}, false) //nolint
			for _, transportSum := range establishedTransports {
				if transportSum.Remote.Hex() == rPK.Hex() {
					log.Debugf("Established transport exist. Type: %s", transportSum.Type)
					return nil
				}
			}

			allTransports, err := v.arClient.Transports(ctx)
			if err != nil {
				log.WithError(err).Warn("failed to fetch AR transport")
			}

			dmsgFallback := func() error {
				return retrier.Do(ctx, func() error {
					_, err := tm.SaveTransport(ctx, rPK, network.DMSG, transport.LabelAutomatic)
					if err != nil {
						log.Debugf("Establishing automatic DMSG transport failed.")
					}
					return err
				})
			}
			// check visor's AR transport
			if allTransports == nil && !v.conf.Transport.PublicAutoconnect {
				// skips if there's no AR transports
				log.Warn("empty AR transports")
				return dmsgFallback()
			}
			transports, ok := allTransports[rPK]
			if !ok {
				log.WithField("pk", rPK.String()).Warn("pk not found in the transports")
				// check if automatic transport is available, if it does,
				// continue with route creation
				return dmsgFallback()
			}
			// try to establish direct connection to rPK (single hop) using SUDPH or STCPR
			trySTCPR := false
			trySUDPH := false

			for _, trans := range transports {
				nType := network.Type(trans)
				if nType == network.STCPR {
					trySTCPR = true
					continue
				}

				// Wait until stun client is ready
				<-v.stunReady

				// skip if SUDPH is under symmetric NAT / under UDP firewall.
				if nType == network.SUDPH && (v.stunClient.NATType == stun.NATSymmetric ||
					v.stunClient.NATType == stun.NATSymmetricUDPFirewall) {
					continue
				}
				trySUDPH = true
			}

			// trying to establish direct connection to rPK using STCPR
			if trySTCPR {
				err := retrier.Do(ctx, func() error {
					_, err := tm.SaveTransport(ctx, rPK, network.STCPR, transport.LabelAutomatic)
					return err
				})
				if err == nil {
					return nil
				}
				log.Debugf("Establishing automatic STCPR transport failed.")
			}
			// trying to establish direct connection to rPK using SUDPH
			if trySUDPH {
				err := retrier.Do(ctx, func() error {
					_, err := tm.SaveTransport(ctx, rPK, network.SUDPH, transport.LabelAutomatic)
					return err
				})
				if err == nil {
					return nil
				}
				log.Debugf("Establishing automatic SUDPH transport failed.")
			}

			return dmsgFallback()
		},
	}
}

func initRouter(ctx context.Context, v *Visor, log *logging.Logger) error {
	conf := v.conf.Routing

	httpC, err := getHTTPClient(ctx, v, conf.RouteFinder)
	if err != nil {
		return err
	}

	rfClient := rfclient.NewHTTP(conf.RouteFinder, time.Duration(conf.RouteFinderTimeout), httpC, v.MasterLogger())
	logger := v.MasterLogger().PackageLogger("router")
	rConf := router.Config{
		Logger:           logger,
		MasterLogger:     v.MasterLogger(),
		PubKey:           v.conf.PK,
		SecKey:           v.conf.SK,
		TransportManager: v.tpM,
		RouteFinder:      rfClient,
		RouteGroupDialer: setupclient.NewSetupNodeDialer(),
		SetupNodes:       conf.SetupNodes,
		RulesGCInterval:  0, // TODO
		MinHops:          v.conf.Routing.MinHops,
	}

	routeSetupHooks := getRouteSetupHooks(ctx, v, log)

	r, err := router.New(v.dmsgC, &rConf, routeSetupHooks)
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
		VisorPK:       v.conf.PK,
		Apps:          conf.Apps,
		ServerAddr:    conf.ServerAddr,
		BinPath:       conf.BinPath,
		LocalPath:     v.conf.LocalPath,
		DisplayNodeIP: conf.DisplayNodeIP,
	}

	launchLog := v.MasterLogger().PackageLogger("launcher")

	launch, err := launcher.NewLauncher(launchLog, launchConf, v.dmsgC, v.router, procM)
	if err != nil {
		err := fmt.Errorf("failed to start launcher: %w", err)
		return err
	}

	err = launch.AutoStart(launcher.EnvMap{
		skyenv.VPNClientName: vpnEnvMaker(v.conf, v.dmsgC, v.dmsgDC, v.tpM.STCPRRemoteAddrs()),
		skyenv.VPNServerName: vpnEnvMaker(v.conf, v.dmsgC, v.dmsgDC, nil),
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
func vpnEnvMaker(conf *visorconfig.V1, dmsgC, dmsgDC *dmsg.Client, tpRemoteAddrs []string) launcher.EnvMaker {
	return func() ([]string, error) {
		var envCfg vpn.DirectRoutesEnvConfig

		if conf.Dmsg != nil {
			envCfg.DmsgDiscovery = conf.Dmsg.Discovery

			log := conf.MasterLogger().PackageLogger("vpn_env_maker")
			r := netutil.NewRetrier(log, 1*time.Second, 10*time.Second, 0, 1)
			err := r.Do(context.Background(), func() error {
				for _, ses := range dmsgC.AllSessions() {
					envCfg.DmsgServers = append(envCfg.DmsgServers, ses.RemoteTCPAddr().String())
				}

				if len(envCfg.DmsgServers) == 0 {
					return errors.New("no dmsg servers found")
				}

				if dmsgDC != nil {
					for _, ses := range dmsgDC.AllSessions() {
						envCfg.DmsgServers = append(envCfg.DmsgServers, ses.RemoteTCPAddr().String())
					}
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
		v.log.Debug("'cli_addr' is not configured, skipping.")
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
			var autoPeerIP string
			if v.autoPeer {
				autoPeerIP = v.autoPeerIP
			} else {
				autoPeerIP = ""
			}
			defer delete(v.connectedHypervisors, hvPK)
			v.connectedHypervisors[hvPK] = true
			ServeRPCClient(ctx, log, autoPeerIP, v.dmsgC, rpcS, addr, hvErrs)

		}(hvErrs)

		v.pushCloseStack("hypervisor."+hvPK.String()[:shortHashLen], func() error {
			cancel()
			wg.Wait()
			return nil
		})
	}

	return nil
}

func initUptimeTracker(ctx context.Context, v *Visor, log *logging.Logger) error {
	const tickDuration = 5 * time.Minute

	conf := v.conf.UptimeTracker

	if conf == nil {
		v.log.Debug("'uptime_tracker' is not configured, skipping.")
		return nil
	}

	httpC, err := getHTTPClient(ctx, v, conf.Addr)
	if err != nil {
		return err
	}

	pIP, err := getPublicIP(v, conf.Addr)
	if err != nil {
		return err
	}

	ut, err := utclient.NewHTTP(conf.Addr, v.conf.PK, v.conf.SK, httpC, pIP, v.MasterLogger())
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
		// call Stop() method to clean service discovery for the situation that
		// visor was public, then stop (not normal shutdown), then start as non-public
		v.serviceDisc.VisorUpdater(0).Stop()
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
	visorUpdater := v.serviceDisc.VisorUpdater(port)
	visorUpdater.Start()

	v.log.Debugf("Sent request to register visor as public")
	v.pushCloseStack("public visor updater", func() error {
		visorUpdater.Stop()
		return nil
	})
	return nil
}

func initDmsgpty(ctx context.Context, v *Visor, log *logging.Logger) error {
	conf := v.conf.Dmsgpty

	if conf == nil {
		log.Debug("'dmsgpty' is not configured, skipping.")
		return nil
	}

	// Unlink dmsg socket files (just in case).
	if conf.CLINet == "unix" {
		if runtime.GOOS == "windows" {
			conf.CLIAddr = dmsgpty.ParseWindowsEnv(conf.CLIAddr)
		}

		if err := osutil.UnlinkSocketFiles(v.conf.Dmsgpty.CLIAddr); err != nil {
			log.Error("insufficient permissions")
			return err
		}
	}

	wl := dmsgpty.NewMemoryWhitelist()

	// Ensure hypervisors are added to the whitelist.
	if err := wl.Add(v.conf.Hypervisors...); err != nil {
		return err
	}
	// add itself to the whitelist to allow local pty
	if err := wl.Add(v.conf.PK); err != nil {
		v.log.Errorf("Cannot add itself to the pty whitelist: %s", err)
	}

	dmsgC := v.dmsgC
	if dmsgC == nil {
		err := errors.New("cannot create dmsgpty with nil dmsg client")
		return err
	}

	pty := dmsgpty.NewHost(dmsgC, wl)

	if ptyPort := conf.DmsgPort; ptyPort != 0 {
		serveCtx, cancel := context.WithCancel(context.Background())
		wg := new(sync.WaitGroup)
		wg.Add(1)

		go func() {
			defer wg.Done()
			runtimeErrors := getErrors(ctx)
			if err := pty.ListenAndServe(serveCtx, ptyPort); err != nil {
				runtimeErrors <- fmt.Errorf("listen and serve stopped: %w", err)
			}
		}()

		v.pushCloseStack("router.serve", func() error {
			cancel()
			wg.Wait()
			return nil
		})

	}

	if conf.CLINet != "" {

		if conf.CLINet == "unix" {
			if err := os.MkdirAll(filepath.Dir(conf.CLIAddr), ownerRWX); err != nil {
				err := fmt.Errorf("failed to prepare unix file for dmsgpty cli listener: %w", err)
				return err
			}
		}

		cliL, err := net.Listen(conf.CLINet, conf.CLIAddr)
		if err != nil {
			err := fmt.Errorf("failed to start dmsgpty cli listener: %w", err)
			return err
		}

		serveCtx, cancel := context.WithCancel(context.Background())
		wg := new(sync.WaitGroup)
		wg.Add(1)

		go func() {
			defer wg.Done()
			runtimeErrors := getErrors(ctx)
			if err := pty.ServeCLI(serveCtx, cliL); err != nil {
				runtimeErrors <- fmt.Errorf("serve cli stopped: %w", err)
			}
		}()

		v.pushCloseStack("router.serve", func() error {
			cancel()
			err := cliL.Close()
			wg.Wait()
			return err
		})
	}

	return nil
}

func initPublicAutoconnect(ctx context.Context, v *Visor, log *logging.Logger) error {
	if !v.conf.Transport.PublicAutoconnect {
		return nil
	}
	serviceDisc := v.conf.Launcher.ServiceDisc
	if serviceDisc == "" {
		serviceDisc = utilenv.ServiceDiscAddr
	}

	// todo: refactor updatedisc: split connecting to services in updatedisc and
	// advertising oneself as a service. Currently, config is tailored to
	// advertising oneself and requires things like port that are not used
	// in connecting to services
	conf := servicedisc.Config{
		Type:          servicedisc.ServiceTypeVisor,
		PK:            v.conf.PK,
		SK:            v.conf.SK,
		Port:          uint16(0),
		DiscAddr:      serviceDisc,
		DisplayNodeIP: v.conf.Launcher.DisplayNodeIP,
	}
	// only needed for dmsghttp
	pIP, err := getPublicIP(v, serviceDisc)
	if err != nil {
		return err
	}
	connector := servicedisc.MakeConnector(conf, 3, v.tpM, v.serviceDisc.Client, pIP, log, v.MasterLogger())

	cctx, cancel := context.WithCancel(ctx)
	v.pushCloseStack("public_autoconnect", func() error {
		cancel()
		return err
	})

	go connector.Run(cctx) //nolint:errcheck

	return nil
}

func initHypervisor(_ context.Context, v *Visor, log *logging.Logger) error {

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
		handler := hv.HTTPHandler()
		srv := &http.Server{ //nolint gosec
			Addr:         conf.HTTPAddr,
			Handler:      handler,
			ReadTimeout:  5 * time.Second,
			WriteTimeout: 10 * time.Second,
		}
		if conf.EnableTLS {
			err = srv.ListenAndServeTLS(conf.TLSCertFile, conf.TLSKeyFile)
		} else {
			err = srv.ListenAndServe()
		}

		if err != nil {
			v.log.WithError(err).Fatal("Hypervisor exited with error.")
		}

		cancel()
	}()

	v.pushCloseStack("hypervisor", func() error {
		cancel()
		return err
	})

	return nil
}

func connectToTpDisc(ctx context.Context, v *Visor, log *logging.Logger) (transport.DiscoveryClient, error) {
	const (
		initBO = 1 * time.Second
		maxBO  = 10 * time.Second
		// trying till success
		tries  = 0
		factor = 1
	)

	conf := v.conf.Transport

	httpC, err := getHTTPClient(ctx, v, conf.Discovery)
	if err != nil {
		return nil, err
	}

	// only needed for dmsghttp
	pIP, err := getPublicIP(v, conf.AddressResolver)
	if err != nil {
		return nil, err
	}

	tpdCRetrier := netutil.NewRetrier(log,
		initBO, maxBO, tries, factor)

	var tpdC transport.DiscoveryClient
	retryFunc := func() error {
		var err error
		tpdC, err = tpdclient.NewHTTP(conf.Discovery, v.conf.PK, v.conf.SK, httpC, pIP, v.MasterLogger())
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

func getHTTPClient(ctx context.Context, v *Visor, service string) (*http.Client, error) {

	var serviceURL dmsgget.URL
	var delegatedServers []cipher.PubKey
	err := serviceURL.Fill(service)

	if serviceURL.Scheme == "dmsg" {
		if err != nil {
			return nil, fmt.Errorf("provided URL is invalid: %w", err)
		}
		// get delegated servers and add them to the client entry
		servers, err := v.dClient.AvailableServers(ctx)
		if err != nil {
			return nil, fmt.Errorf("Error getting AvailableServers: %w", err)
		}

		for _, server := range servers {
			delegatedServers = append(delegatedServers, server.Static)
		}

		clientEntry := &dmsgdisc.Entry{
			Client: &dmsgdisc.Client{
				DelegatedServers: delegatedServers,
			},
			Static: serviceURL.Addr.PK,
		}

		err = v.dClient.PostEntry(ctx, clientEntry)
		if err != nil {
			return nil, fmt.Errorf("Error saving clientEntry: %w", err)
		}
		return v.dmsgHTTP, nil
	}
	return &http.Client{
		Transport: &http.Transport{
			DisableKeepAlives: true,
			IdleConnTimeout:   time.Second * 5,
		},
	}, nil
}

func getPublicIP(v *Visor, service string) (string, error) {
	var serviceURL dmsgget.URL
	var pIP string
	err := serviceURL.Fill(service)
	// only get the IP if the url is of dmsg
	// else just send empty string as ip
	if serviceURL.Scheme != "dmsg" {
		return pIP, nil
	}
	if err != nil {
		return pIP, fmt.Errorf("provided URL is invalid: %w", err)
	}

	pIP, err = GetIP()
	if err != nil {
		<-v.stunReady
		if v.stunClient.PublicIP != nil {
			pIP = v.stunClient.PublicIP.IP()
			return pIP, nil
		}
		err = fmt.Errorf("cannot fetch public ip")
	}
	if err != nil {
		return pIP, err
	}

	return pIP, nil
}

type ipAPI struct {
	PublicIP string `json:"ip_address"`
}

// GetIP used for getting current IP of visor
func GetIP() (string, error) {
	req, err := http.Get("http://ip.skycoin.com")
	if err != nil {
		return "", err
	}
	defer req.Body.Close() // nolint

	body, err := io.ReadAll(req.Body)
	if err != nil {
		return "", err
	}

	var ip ipAPI
	err = json.Unmarshal(body, &ip)
	if err != nil {
		return "", err
	}

	return ip.PublicIP, nil
}
