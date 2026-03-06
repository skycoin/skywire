// Package visor pkg/visor/init.go
package visor

import (
	"bufio"
	"bytes"
	"context"
	crand "crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"math/rand"
	"mime"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/ccding/go-stun/stun"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"github.com/skycoin/dmsg/pkg/direct"
	dmsgdisc "github.com/skycoin/dmsg/pkg/disc"
	"github.com/skycoin/dmsg/pkg/dmsg"
	"github.com/skycoin/dmsg/pkg/dmsgctrl"
	"github.com/skycoin/dmsg/pkg/dmsgcurl"
	"github.com/skycoin/dmsg/pkg/dmsghttp"
	"github.com/skycoin/dmsg/pkg/dmsgpty"
	"github.com/soheilhy/cmux"
	"google.golang.org/grpc"

	"github.com/skycoin/skywire/deployment"
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
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/httputil"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/netutil"
	"github.com/skycoin/skywire/pkg/tpviz"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/transport/network"
	"github.com/skycoin/skywire/pkg/transport/network/addrresolver"
	"github.com/skycoin/skywire/pkg/transport/network/stcp"
	ts "github.com/skycoin/skywire/pkg/transport/setup"
	"github.com/skycoin/skywire/pkg/transport/tpdclient"
	types "github.com/skycoin/skywire/pkg/transport/types"
	"github.com/skycoin/skywire/pkg/utclient"
	"github.com/skycoin/skywire/pkg/util/osutil"
	"github.com/skycoin/skywire/pkg/visor/dmsgtracker"
	"github.com/skycoin/skywire/pkg/visor/logserver"
	"github.com/skycoin/skywire/pkg/visor/rpcgrpc"
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
	// Transportability checker ensures the visor can accept transports by creating a self-transport or exiting after 3 failed attempts to create one
	tc vinit.Module
	// TPD concurrency checker removes transports from tpd that the visor does not have registered locally
	tpdco vinit.Module
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
	// System survey module
	systemSurvey vinit.Module
	// Dmsg http module
	dmsgHTTP vinit.Module
	// Dmsg trackers module
	dmsgTrackers vinit.Module
	// Skywire Forwarding conn module
	skyFwd vinit.Module
	// Ping module (skywire routes)
	pi vinit.Module
	// Latency probe module (transport latency measurement)
	lp vinit.Module
	// Dmsg ping module (dmsg direct connection)
	dmsgPi vinit.Module
	// Dmsg server latency tracking (self-ping via each server)
	dmsgServerLatency vinit.Module
	// Embedded Transport Setup Node (separate dmsg client with TPS identity)
	embTPS vinit.Module
	// Embedded Route Setup Node (separate dmsg client with route setup identity)
	embRouteSetup vinit.Module
	// UI server module (serves tp-viz)
	uiServer vinit.Module
	// Node health tracking for TPS and RSN
	nodeHealth vinit.Module
	// visor that groups all modules together
	vis vinit.Module
	// config initialization
//	visorConfig vinit.Module
)

// register all modules: instantiate modules with correct names and dependencies, wrap init
// functions to have access to visor and runtime errors channel
func registerModules(logger *logging.MasterLogger) {
	// utility module maker, to avoid passing logger and wrapping each init function
	// in withVisorCtx
	maker := func(name string, f initFn, deps ...*vinit.Module) vinit.Module {
		return vinit.MakeModule(name, withInitCtx(f), logger, deps...)
	}
	//	visorConfig = maker("visor_config", initVisorConfig)
	dmsgHTTP = maker("dmsg_http", initDmsgHTTP)
	ebc = maker("event_broadcaster", initEventBroadcaster)
	ar = maker("address_resolver", initAddressResolver, &dmsgC, &sc, &dmsgHTTP)
	disc = maker("discovery", initDiscovery, &dmsgC, &sc, &dmsgHTTP)
	tr = maker("transport", initTransport, &ar, &ebc, &dmsgHTTP)

	sc = maker("stun_client", initStunClient)
	sudphC = maker("sudph", initSudphClient, &sc, &tr)
	stcprC = maker("stcpr", initStcprClient, &tr)
	stcpC = maker("stcp", initStcpClient, &tr)
	dmsgC = maker("dmsg", initDmsg, &ebc, &dmsgHTTP)
	dmsgCtrl = maker("dmsg_ctrl", initDmsgCtrl, &dmsgC, &tr)
	dmsgHTTPLogServer = maker("dmsghttp_logserver", initDmsgHTTPLogServer, &dmsgC, &tr)
	systemSurvey = maker("system_survey", initSystemSurvey, &dmsgHTTPLogServer)
	dmsgTrackers = maker("dmsg_trackers", initDmsgTrackers, &dmsgC)

	pty = maker("dmsg_pty", initDmsgpty, &dmsgC)
	embRouteSetup = maker("embedded_route_setup", initEmbeddedRouteSetup, &dmsgC)
	rt = maker("router", initRouter, &tr, &dmsgC, &dmsgHTTP, &embRouteSetup)
	launch = maker("launcher", initLauncher, &ebc, &disc, &dmsgC, &tr, &rt)
	cli = maker("cli", initCLI)
	hvs = maker("hypervisors", initHypervisors, &dmsgC)
	ut = maker("uptime_tracker", initUptimeTracker, &dmsgHTTP)
	pv = maker("public_autoconnect", initPublicAutoconnect, &tr, &disc)
	trs = maker("transport_setup", initTransportSetup, &dmsgC, &tr)
	tm = vinit.MakeModule("transports", vinit.DoNothing, logger, &sc, &sudphC, &dmsgCtrl, &dmsgHTTPLogServer, &dmsgTrackers, &launch)
	pvs = maker("public_visor", initPublicVisor, &tr, &ar, &disc, &stcprC)
	skyFwd = maker("sky_forward_conn", initSkywireForwardConn, &dmsgC, &dmsgCtrl, &tr, &launch)
	pi = maker("ping", initPing, &dmsgC, &tm)
	lp = maker("latency_probe", initLatencyProbe, &dmsgC, &tm)
	dmsgPi = maker("dmsg_ping", initDmsgPing, &dmsgC)
	dmsgServerLatency = maker("dmsg_server_latency", initDmsgServerLatency, &dmsgPi)
	tc = maker("transportable", initEnsureVisorIsTransportable, &dmsgC, &tm, &stcprC)
	tpdco = maker("tpd_concurrency", initEnsureTPDConcurrency, &dmsgC, &tm)
	embTPS = maker("embedded_tps", initEmbeddedTPS, &dmsgC)
	uiServer = maker("ui_server", initUIServer, &dmsgC, &tr, &embTPS)
	nodeHealth = maker("node_health", initNodeHealth, &dmsgC)
	vis = vinit.MakeModule("visor", vinit.DoNothing, logger, &ebc, &ar, &disc, &pty,
		&tr, &rt, &launch, &cli, &hvs, &ut, &pv, &pvs, &trs, &stcpC, &stcprC, &skyFwd, &pi, &lp, &dmsgPi, &dmsgServerLatency, &systemSurvey, &tc, &tpdco, &embTPS, &embRouteSetup, &uiServer, &nodeHealth)

	hv = maker("hypervisor", initHypervisor, &vis)
}

type initFn func(context.Context, *Visor, *logging.Logger) error

func initDmsgHTTP(ctx context.Context, v *Visor, _ *logging.Logger) error {
	var keys cipher.PubKeys
	servers := shuffleServers(v.conf.Dmsg.Servers)

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

func shuffleServers(in []*dmsgdisc.Entry) []*dmsgdisc.Entry {
	n := len(in)
	for i := n - 1; i > 0; i-- {
		jBig, err := crand.Int(crand.Reader, big.NewInt(int64(i+1)))
		if err != nil {
			panic(err)
		}
		j := int(jBig.Int64())
		in[i], in[j] = in[j], in[i]
	}
	return in
}

/*
func rotateServers(servers []*dmsgdisc.Entry) {
	if len(servers) == 0 {
		return
	}
	first := servers[0]
	copy(servers, servers[1:])
	servers[len(servers)-1] = first
}
*/

func initEventBroadcaster(ctx context.Context, v *Visor, log *logging.Logger) error { //nolint:revive
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

	// Get public IP for address resolver binding (needed for NAT setups)
	// Try multiple methods with fallback chain:
	// 1. dmsg server (may fail if local dmsg server returns private IP)
	// 2. GeoIP service
	// 3. STUN servers
	var pIP string
	var geoData *GeoData

	// Try dmsg LookupIP first (with timeout to avoid blocking init if dmsg isn't ready)
	lookupCtx, lookupCancel := context.WithTimeout(ctx, 10*time.Second)
	ipAddr, err := v.dmsgC.LookupIP(lookupCtx, nil)
	lookupCancel()
	if err != nil {
		log.WithError(err).Debug("Failed to get public IP from dmsg server, trying GeoIP")

		// Fall back to GeoIP - also get geolocation data
		pIP, geoData = GetIPWithGeo(v.conf.GeoIP)
		if pIP == "" {
			log.Debug("Failed to get public IP from GeoIP, trying STUN")

			// Fall back to STUN
			<-v.stunReady
			if v.stunClient.PublicIP != nil {
				pIP = v.stunClient.PublicIP.IP()
				log.WithField("public_ip", pIP).Debug("Got public IP from STUN")
			} else {
				log.Warn("Failed to determine public IP from dmsg, GeoIP, and STUN")
				pIP = ""
			}
		} else {
			log.WithField("public_ip", pIP).Debug("Got public IP from GeoIP")
			if geoData != nil {
				log.WithField("country", geoData.CountryCode).Debug("Got geolocation from GeoIP")
			}
		}
	} else {
		pIP = ipAddr.String()
		log.WithField("public_ip", pIP).Debug("Got public IP from dmsg server")
		// When we get IP from dmsg, still try to get geo data separately
		_, geoData = GetIPWithGeo(v.conf.GeoIP)
		if geoData != nil {
			log.WithField("country", geoData.CountryCode).Debug("Got geolocation from GeoIP")
		}
	}

	// Store geolocation data if we got it
	if geoData != nil {
		v.geoDataMu.Lock()
		v.geoData = geoData
		v.geoDataMu.Unlock()
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

func initDiscovery(ctx context.Context, v *Visor, _ *logging.Logger) error {
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
		factory.HeartbeatInterval = time.Duration(conf.HeartbeatInterval)
		factory.Client = httpC

		// Get public IP for service discovery (needed for NAT setups)
		// Use same fallback chain as address resolver: dmsg -> GeoIP -> STUN
		logger := factory.Log
		var pIP string
		lookupCtx, lookupCancel := context.WithTimeout(ctx, 10*time.Second)
		ipAddr, err := v.dmsgC.LookupIP(lookupCtx, nil)
		lookupCancel()
		if err != nil {
			logger.WithError(err).Debug("Failed to get public IP from dmsg server, trying GeoIP")

			pIP, err = GetIP(v.conf.GeoIP)
			if err != nil {
				logger.WithError(err).Debug("Failed to get public IP from GeoIP, trying STUN")

				<-v.stunReady
				if v.stunClient.PublicIP != nil {
					pIP = v.stunClient.PublicIP.IP()
					logger.WithField("public_ip", pIP).Debug("Got public IP from STUN for service discovery")
				} else {
					logger.Warn("Failed to determine public IP for service discovery from dmsg, GeoIP, and STUN")
					pIP = ""
				}
			} else {
				logger.WithField("public_ip", pIP).Debug("Got public IP from GeoIP for service discovery")
			}
		} else {
			pIP = ipAddr.String()
			logger.WithField("public_ip", pIP).Debug("Got public IP from dmsg server for service discovery")
		}

		factory.ClientPublicIP = pIP
	}

	v.initLock.Lock()
	v.serviceDisc = factory
	v.initLock.Unlock()
	return nil
}

func initStunClient(_ context.Context, v *Visor, log *logging.Logger) error {

	sc := network.GetStunDetails(v.conf.StunServers, log)
	v.initLock.Lock()
	v.stunClient = sc
	v.initLock.Unlock()
	v.stunReadyOnce.Do(func() { close(v.stunReady) })
	return nil
}

func initDmsg(ctx context.Context, v *Visor, _ *logging.Logger) (err error) {
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
	cl, err := dmsgC.Listen(visorconfig.DmsgCtrlPort)
	if err != nil {
		return err
	}
	v.pushCloseStack("dmsgctrl", cl.Close)

	dmsgctrl.ServeListener(cl, 0)
	return nil
}

func initDmsgHTTPLogServer(ctx context.Context, v *Visor, _ *logging.Logger) error {
	dmsgC := v.dmsgC
	if dmsgC == nil {
		return fmt.Errorf("cannot initialize dmsg log server: dmsg not configured")
	}
	logger := v.MasterLogger().PackageLogger("dmsghttp_logserver")

	var printLog bool
	if v.MasterLogger().GetLevel() == logrus.DebugLevel || v.MasterLogger().GetLevel() == logrus.TraceLevel {
		printLog = true
	}

	//whitelist access to the surveys for the hypervisor, dmsggpty whitelist, and for the surveywhitelist of keys which is fetched from the conf service
	var whitelistedPKs []cipher.PubKey
	if v.conf.SurveyWhitelist != nil {
		whitelistedPKs = append(whitelistedPKs, v.conf.SurveyWhitelist...)
	}
	if v.conf.Hypervisors != nil {
		whitelistedPKs = append(whitelistedPKs, v.conf.Hypervisors...)
	}
	if v.conf.Dmsgpty != nil {
		if v.conf.Dmsgpty.Whitelist != nil {
			whitelistedPKs = append(whitelistedPKs, v.conf.Dmsgpty.Whitelist...)
		}
	}

	lsAPI := logserver.New(logger, v.conf.Transport.LogStore.Location, v.conf.LocalPath, v.conf.DmsgHTTPServerPath, whitelistedPKs, &v.survey, printLog)

	// Set visor as health stats provider for /health endpoint
	lsAPI.SetHealthStatsProvider(v)

	// Store the log server API reference for public autocheck to use later
	v.initLock.Lock()
	v.logServerAPI = lsAPI
	v.initLock.Unlock()

	lis, err := dmsgC.Listen(visorconfig.DmsgHTTPPort)
	if err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		if err := lis.Close(); err != nil {
			logger.WithError(err).Error()
		}
	}()

	logger.WithField("dmsg_addr", fmt.Sprintf("dmsg://%v", lis.Addr().String())).
		Debug("Serving...")
	// Increased timeouts for dmsg latency characteristics
	// DMSG has higher latency than direct TCP due to multi-hop routing
	srv := &http.Server{
		ReadTimeout:       visorconfig.HTTPReadTimeout,
		WriteTimeout:      visorconfig.HTTPWriteTimeout,
		IdleTimeout:       visorconfig.HTTPIdleTimeout,
		ReadHeaderTimeout: visorconfig.HTTPReadHeaderTimeout,
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
		if errors.Is(err, http.ErrServerClosed) {
			return
		}
		if err != nil {
			logger.WithError(err).Error("Logserver exited with error.")
		}
	}()
	v.pushCloseStack("dmsghttp.logserver", func() error {
		// Graceful shutdown
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			logger.WithError(err).Warn("HTTP server shutdown error")
		}
		wg.Wait()
		return nil
	})

	// Optionally serve on localhost as well (no whitelist authentication)
	if v.conf.LogServer != nil && v.conf.LogServer.LocalAddr != "" {
		localAddr := v.conf.LogServer.LocalAddr
		logger.WithField("local_addr", localAddr).Info("Starting localhost log server")

		// Create a separate API without whitelist authentication for localhost
		localAPI := logserver.New(logger, v.conf.Transport.LogStore.Location, v.conf.LocalPath, v.conf.DmsgHTTPServerPath, nil, &v.survey, printLog)

		// Set visor as health stats provider for /health endpoint
		localAPI.SetHealthStatsProvider(v)

		// Store the localhost API for potential future use
		v.localLogServerAPI = localAPI

		localLis, err := net.Listen("tcp", localAddr)
		if err != nil {
			logger.WithError(err).WithField("local_addr", localAddr).Warn("Failed to start localhost log server")
		} else {
			localSrv := &http.Server{
				ReadTimeout:       5 * time.Second,
				WriteTimeout:      30 * time.Second,
				IdleTimeout:       60 * time.Second,
				ReadHeaderTimeout: 5 * time.Second,
				Handler:           localAPI,
			}

			localWg := new(sync.WaitGroup)
			localWg.Add(1)

			go func() {
				defer localWg.Done()
				if err := localSrv.Serve(localLis); err != nil && !errors.Is(err, http.ErrServerClosed) {
					logger.WithError(err).Error("Localhost logserver exited with error")
				}
			}()

			v.pushCloseStack("localhost.logserver", func() error {
				shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := localSrv.Shutdown(shutdownCtx); err != nil {
					logger.WithError(err).Warn("Localhost HTTP server shutdown error")
				}
				localWg.Wait()
				return nil
			})

			logger.WithField("local_addr", localAddr).Info("Localhost log server started")
		}
	}

	return nil
}

func initSystemSurvey(_ context.Context, v *Visor, log *logging.Logger) error {
	go GenerateSurvey(v, log, true)
	return nil
}

func initDmsgTrackers(_ context.Context, v *Visor, _ *logging.Logger) error {
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
	var serviceURL dmsgcurl.URL
	_ = serviceURL.Fill(v.conf.Transport.AddressResolver) //nolint:errcheck
	// don't start sudph if we are connection to AR via dmsghttp
	if serviceURL.Scheme == "dmsg" {
		log.Info("SUDPH transport wont be available under dmsghttp")
		return nil
	}
	if v.stunClient != nil {
		switch v.stunClient.NATType {
		case stun.NATSymmetric, stun.NATSymmetricUDPFirewall:
			log.Warnf("SUDPH transport wont be available as visor is under %v", v.stunClient.NATType.String())
		default:
			v.tpM.InitClient(ctx, types.SUDPH, v.conf.Transport.SudphPort)
		}
	}
	return nil
}

func initStcprClient(ctx context.Context, v *Visor, _ *logging.Logger) error {
	v.tpM.InitClient(ctx, types.STCPR, v.conf.Transport.StcprPort)
	return nil
}

func initStcpClient(ctx context.Context, v *Visor, _ *logging.Logger) error {
	if v.conf.STCP != nil {
		v.tpM.InitClient(ctx, types.STCP, 0)
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

	// Initialize latency log store (uses same type as transport log store)
	var latencyLogS transport.LatencyLogStore
	latencyLogDir := filepath.Join(filepath.Dir(v.conf.Transport.LogStore.Location), skyenv.LatencyLogStore)
	if v.conf.Transport.LogStore.Type == visorconfig.MemoryLogStore {
		latencyLogS = transport.InMemoryLatencyLogStore()
	} else if v.conf.Transport.LogStore.Type == visorconfig.FileLogStore {
		latencyLogS, err = transport.FileLatencyLogStore(ctx, latencyLogDir, time.Duration(v.conf.Transport.LogStore.RotationInterval), log)
		if err != nil {
			return err
		}
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
		LatencyLogStore:           latencyLogS,
		PersistentTransportsCache: pTps,
		Version:                   buildinfo.Version(),
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
		// OnExternalSTCPR notifies the public visor updater when an external
		// connection is received, validating that the visor is internet-reachable
		OnExternalSTCPR: func() {
			v.publicVisorUpdaterMu.Lock()
			updater := v.publicVisorUpdater
			v.publicVisorUpdaterMu.Unlock()
			if updater != nil {
				updater.OnExternalSTCPR()
			}
		},
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
		ts, err := ts.NewTransportListener(ctx, v.conf.PK, v.conf.Transport.TransportSetupPKs, v.dmsgC, v.tpM, v.MasterLogger())
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

func initEmbeddedTPS(ctx context.Context, v *Visor, log *logging.Logger) error {
	tpsSK := v.conf.Transport.TPSetupSK
	if tpsSK == nil || *tpsSK == (cipher.SecKey{}) {
		log.Debug("No embedded TPS configured (tps_sk empty), skipping")
		return nil
	}

	tpsPK, err := tpsSK.PubKey()
	if err != nil {
		return fmt.Errorf("invalid tps_sk: %w", err)
	}
	log.WithField("tps_pk", tpsPK).Info("Starting embedded Transport Setup Node")

	// Create a separate dmsg client with the TPS identity.
	// Reuses the visor's dmsg discovery URL but with TPS keys.
	// Default: MinSessions=0 (connect to ALL servers), ServerType="" (all types).
	minSessions := 0
	serverType := ""
	if tpsDmsgConf := v.conf.Transport.TPSDmsg; tpsDmsgConf != nil {
		minSessions = tpsDmsgConf.MinSessions
		serverType = tpsDmsgConf.ServerType
	}
	dmsgConf := &dmsg.Config{
		MinSessions:          minSessions,
		ConnectedServersType: serverType,
		Protocol:             v.conf.Dmsg.Protocol,
	}
	dmsgConf.ClientType = "tps"
	log.WithField("min_sessions", minSessions).WithField("server_type", serverType).Debug("TPS dmsg config")
	httpC := &http.Client{}
	tpsDisc := dmsgdisc.NewHTTP(v.conf.Dmsg.Discovery, httpC, v.MasterLogger().PackageLogger("embedded_tps:disc"))
	tpsDmsgC := dmsg.NewClient(tpsPK, *tpsSK, tpsDisc, dmsgConf)
	tpsDmsgC.SetLogger(v.MasterLogger().PackageLogger("embedded_tps:dmsg"))

	go tpsDmsgC.Serve(ctx)

	select {
	case <-tpsDmsgC.Ready():
		log.Info("Embedded TPS dmsg client connected")
	case <-ctx.Done():
		return fmt.Errorf("context canceled waiting for TPS dmsg client")
	}

	v.initLock.Lock()
	v.embeddedTPS = &embeddedTPS{
		dmsgC: tpsDmsgC,
		pk:    tpsPK,
		log:   log,
	}
	v.initLock.Unlock()

	// Start the embedded TPS server to accept incoming requests from remote visors
	go func() {
		if err := v.embeddedTPS.Serve(ctx); err != nil {
			if ctx.Err() == nil {
				log.WithError(err).Error("Embedded TPS server error")
			}
		}
	}()

	v.pushCloseStack("embedded_tps", func() error {
		return tpsDmsgC.Close()
	})
	return nil
}

func initEmbeddedRouteSetup(ctx context.Context, v *Visor, log *logging.Logger) error {
	routeSetupSK := v.conf.Routing.RouteSetupSK
	if routeSetupSK == nil || *routeSetupSK == (cipher.SecKey{}) {
		log.Debug("No embedded route setup-node configured (route_setup_sk empty), skipping")
		return nil
	}

	routeSetupPK, err := routeSetupSK.PubKey()
	if err != nil {
		return fmt.Errorf("invalid route_setup_sk: %w", err)
	}
	log.WithField("route_setup_pk", routeSetupPK).Info("Starting embedded Route Setup Node")

	// Create a separate dmsg client with the route setup-node identity.
	// Reuses the visor's dmsg discovery URL but with route setup-node keys.
	dmsgConf := &dmsg.Config{
		MinSessions: 0, // Connect to all servers for better connectivity
		Protocol:    v.conf.Dmsg.Protocol,
	}
	dmsgConf.ClientType = "route_setup"
	httpC := &http.Client{}
	routeSetupDisc := dmsgdisc.NewHTTP(v.conf.Dmsg.Discovery, httpC, v.MasterLogger().PackageLogger("embedded_route_setup:disc"))
	routeSetupDmsgC := dmsg.NewClient(routeSetupPK, *routeSetupSK, routeSetupDisc, dmsgConf)
	routeSetupDmsgC.SetLogger(v.MasterLogger().PackageLogger("embedded_route_setup:dmsg"))

	go routeSetupDmsgC.Serve(ctx)

	select {
	case <-routeSetupDmsgC.Ready():
		log.Info("Embedded route setup-node dmsg client connected")
	case <-ctx.Done():
		return fmt.Errorf("context canceled waiting for route setup-node dmsg client")
	}

	v.initLock.Lock()
	v.embeddedRouteSetup = &EmbeddedRouteSetup{
		dmsgC: routeSetupDmsgC,
		pk:    routeSetupPK,
		log:   log,
	}
	v.initLock.Unlock()

	// Start the route setup-node listener to accept incoming requests from other visors
	go func() {
		if err := v.embeddedRouteSetup.Serve(ctx); err != nil {
			log.WithError(err).Error("Embedded route setup-node listener failed")
		}
	}()

	v.pushCloseStack("embedded_route_setup", func() error {
		return routeSetupDmsgC.Close()
	})
	return nil
}

func initSkywireForwardConn(ctx context.Context, v *Visor, log *logging.Logger) error {
	ctx, cancel := context.WithCancel(ctx)
	// waiting for at least one transport to initialize
	<-v.tpM.Ready()
	connApp := appnet.Addr{
		Net:    appnet.TypeSkynet,
		PubKey: v.conf.PK,
		Port:   routing.Port(skyenv.SkyForwardingServerPort),
	}
	l, err := appnet.ListenContext(ctx, connApp)
	if err != nil {
		cancel()
		return err
	}

	v.pushCloseStack("sky_forwarding", func() error {
		cancel()
		if cErr := l.Close(); cErr != nil {
			log.WithError(cErr).Error("Error closing listener.")
		}
		return nil
	})

	go func() {
		for {
			log.Debug("Accepting sky forwarding conn...")
			conn, err := l.Accept()
			if err != nil {
				if !errors.Is(err, appnet.ErrClosedConn) {
					log.WithError(err).Error("Failed to accept conn")
				}
				return
			}
			log.Debug("Accepted sky forwarding conn")

			v.pushCloseStack("sky_forwarding", func() error {
				cancel()
				if cErr := conn.Close(); cErr != nil {
					log.WithError(cErr).Error("Error closing conn.")
				}
				return nil
			})

			log.Debug("Wrapping conn...")
			wrappedConn, err := appnet.WrapConn(conn)
			if err != nil {
				log.WithError(err).Error("Failed to wrap conn")
				return
			}

			rAddr := wrappedConn.RemoteAddr().(appnet.Addr)
			log.Debugf("Accepted sky forwarding conn on %s from %s", wrappedConn.LocalAddr(), rAddr.PubKey)
			go handleServerConn(log, wrappedConn, v)
		}
	}()

	return nil
}

func handleServerConn(log *logging.Logger, remoteConn net.Conn, v *Visor) {
	// Send ready signal to synchronize with client after noise handshake
	// This ensures the noise handshake is complete before data exchange
	if _, err := remoteConn.Write([]byte{0x00}); err != nil {
		log.WithError(err).Error("Failed to send ready signal")
		return
	}
	log.Debug("Sent ready signal to client")

	buf := make([]byte, 32*1024)
	n, err := remoteConn.Read(buf)
	if err != nil {
		log.WithError(err).Error("Failed to read packet")
		return
	}

	var cMsg clientMsg
	err = json.Unmarshal(buf[:n], &cMsg)
	if err != nil {
		log.WithError(err).Error("Failed to marshal json")
		sendError(log, remoteConn, err)
		return
	}
	log.Debugf("Received: %v", cMsg)

	lHost := fmt.Sprintf("localhost:%v", cMsg.Port)
	ok := isPortRegistered(cMsg.Port, v)
	if !ok {
		log.Errorf("Port :%v not registered", cMsg.Port)
		sendError(log, remoteConn, fmt.Errorf("Port :%v not registered", cMsg.Port))
		return
	}

	ok = isPortAvailable(log, cMsg.Port)
	if ok {
		log.Errorf("Failed to dial port %v", cMsg.Port)
		sendError(log, remoteConn, fmt.Errorf("Failed to dial port %v", cMsg.Port))
		return
	}

	log.Debugf("Forwarding %s (raw_tcp=%v)", lHost, cMsg.RawTCP)

	// send nil error to indicate to the remote connection that everything is ok
	sendError(log, remoteConn, nil)

	go forward(log, remoteConn, lHost, cMsg.RawTCP)
}

// forward proxies data between remoteConn (the skywire connection) and a local server.
// When rawTCP is true, it uses bidirectional io.Copy for raw TCP proxying.
// When rawTCP is false, it reads HTTP requests and forwards them to the local server.
func forward(log *logging.Logger, remoteConn net.Conn, lHost string, rawTCP bool) {
	if rawTCP {
		forwardRawTCP(log, remoteConn, lHost)
		return
	}
	forwardHTTP(log, remoteConn, lHost)
}

// forwardRawTCP does bidirectional raw TCP proxying using io.Copy
func forwardRawTCP(log *logging.Logger, remoteConn net.Conn, lHost string) {
	localConn, err := net.Dial("tcp", lHost)
	if err != nil {
		log.WithError(err).Error("Failed to dial local server")
		closeConn(log, remoteConn)
		return
	}

	done := make(chan struct{}, 2)

	// remote -> local
	go func() {
		_, err := io.Copy(localConn, remoteConn)
		if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
			log.WithError(err).Debug("remote->local copy ended")
		}
		done <- struct{}{}
	}()

	// local -> remote
	go func() {
		_, err := io.Copy(remoteConn, localConn)
		if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
			log.WithError(err).Debug("local->remote copy ended")
		}
		done <- struct{}{}
	}()

	// Wait for one direction to finish, then close both
	<-done
	closeConn(log, remoteConn)
	closeConn(log, localConn)
	<-done
}

// forwardHTTP reads HTTP requests from remoteConn and forwards them to the local server
func forwardHTTP(log *logging.Logger, remoteConn net.Conn, lHost string) {
	for {
		buf := make([]byte, 32*1024)
		n, err := remoteConn.Read(buf)
		if err != nil {
			log.WithError(err).Error("Failed to read packet")
			closeConn(log, remoteConn)
			return
		}
		req, err := http.ReadRequest(bufio.NewReader(bytes.NewBuffer(buf[:n])))
		if err != nil {
			log.WithError(err).Error("Failed to ReadRequest")
			closeConn(log, remoteConn)
			return
		}
		req.RequestURI = ""
		req.URL.Scheme = "http"
		req.URL.Host = lHost
		client := http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			log.WithError(err).Error("Failed to Do req")
			closeConn(log, remoteConn)
			return
		}
		err = resp.Write(remoteConn)
		if err != nil {
			log.WithError(err).Error("Failed to Write")
			closeConn(log, remoteConn)
			return
		}
	}
}

func sendError(log *logging.Logger, remoteConn net.Conn, sendErr error) {
	var sReply serverReply
	if sendErr != nil {
		sErr := sendErr.Error()
		sReply = serverReply{
			Error: &sErr,
		}
	}

	srvReply, err := json.Marshal(sReply)
	if err != nil {
		log.WithError(err).Error("Failed to unmarshal json")
	}

	_, err = remoteConn.Write(srvReply)
	if err != nil {
		log.WithError(err).Error("Failed write server msg")
	}

	log.Debugf("Server reply sent %s", srvReply)
	// close conn if we send an error
	if sendErr != nil {
		closeConn(log, remoteConn)
	}
}

func closeConn(log *logging.Logger, conn net.Conn) {
	if err := conn.Close(); err != nil {
		log.WithError(err).Errorf("Error closing client %s connection", conn.RemoteAddr())
	}
}

type clientMsg struct {
	Port   int  `json:"port"`
	RawTCP bool `json:"raw_tcp,omitempty"`
}

type serverReply struct {
	Error *string `json:"error,omitempty"`
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

	v.pushCloseStack("skywire_ping", func() error {
		cancel()
		if cErr := l.Close(); cErr != nil {
			log.WithError(cErr).Error("Error closing listener.")
		}
		return nil
	})

	go func() {
		for {
			log.Debug("Accepting sky ping conn...")
			conn, err := l.Accept()
			if err != nil {
				if !errors.Is(err, appnet.ErrClosedConn) {
					log.WithError(err).Error("Failed to accept ping conn")
				}
				return
			}
			log.Debug("Accepted sky ping conn")
			log.Debug("Wrapping conn...")
			wrappedConn, err := appnet.WrapConn(conn)
			if err != nil {
				log.WithError(err).Error("Failed to wrap conn")
				return
			}

			rAddr := wrappedConn.RemoteAddr().(appnet.Addr)
			log.Debugf("Accepted sky ping conn on %s from %s", wrappedConn.LocalAddr(), rAddr.PubKey)
			go handlePingConn(log, wrappedConn, v)
		}
	}()
	return nil
}

func handlePingConn(log *logging.Logger, remoteConn net.Conn, _ *Visor) {
	defer func() {
		if err := remoteConn.Close(); err != nil {
			log.WithError(err).Debug("Error closing ping conn")
		}
	}()
	for {
		buf := make([]byte, 32*1024)
		n, err := remoteConn.Read(buf)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				log.WithError(err).Error("Failed to read ping packet")
			}
			return
		}
		var size PingSizeMsg
		err = json.Unmarshal(buf[:n], &size)
		if err != nil {
			log.WithError(err).Error("Failed to unmarshal ping size")
			return
		}

		// Ack the size message
		_, err = remoteConn.Write([]byte("ok"))
		if err != nil {
			log.WithError(err).Error("Failed to write ping ack")
			return
		}

		// Read the full ping payload
		var ping []byte
		for len(ping) < size.Size {
			n, err = remoteConn.Read(buf)
			if err != nil {
				if !errors.Is(err, io.EOF) {
					log.WithError(err).Error("Failed to read ping data")
				}
				return
			}
			ping = append(ping, buf[:n]...)
		}

		// Echo back for RTT measurement
		// If EchoFull is set, echo the full payload for bandwidth testing
		if size.EchoFull {
			_, err = remoteConn.Write(ping)
			if err != nil {
				log.WithError(err).Error("Failed to write full ping echo")
				return
			}
			log.Debugf("Echoed full ping response (%d bytes)", len(ping))
		} else {
			_, err = remoteConn.Write([]byte("pong"))
			if err != nil {
				log.WithError(err).Error("Failed to write ping echo")
				return
			}
			log.Debug("Echoed ping response")
		}
	}
}

// initLatencyProbe starts a listener on LatencyProbePort (46) to accept
// transport latency measurement routes. The RouteGroup automatically handles
// ping/pong packets - we just need to keep the connection alive.
func initLatencyProbe(ctx context.Context, v *Visor, log *logging.Logger) error {
	ctx, cancel := context.WithCancel(ctx)
	// Wait for at least one transport to initialize
	<-v.tpM.Ready()

	connApp := appnet.Addr{
		Net:    appnet.TypeSkynet,
		PubKey: v.conf.PK,
		Port:   routing.Port(skyenv.LatencyProbePort),
	}

	l, err := appnet.ListenContext(ctx, connApp)
	if err != nil {
		cancel()
		return err
	}

	v.pushCloseStack("latency_probe", func() error {
		cancel()
		if cErr := l.Close(); cErr != nil {
			log.WithError(cErr).Error("Error closing latency probe listener.")
		}
		return nil
	})

	go func() {
		for {
			log.Debug("Accepting latency probe conn...")
			conn, err := l.Accept()
			if err != nil {
				if !errors.Is(err, appnet.ErrClosedConn) {
					log.WithError(err).Error("Failed to accept latency probe conn")
				}
				return
			}
			log.Debug("Accepted latency probe conn")

			// Handle the connection in a goroutine.
			// The RouteGroup handles ping/pong packets automatically.
			// We just need to keep the connection alive until the initiator closes it.
			go handleLatencyProbeConn(log, conn)
		}
	}()
	return nil
}

// handleLatencyProbeConn keeps a latency probe connection alive.
// The RouteGroup automatically responds to ping packets with pong packets.
// This handler just waits for the connection to close.
func handleLatencyProbeConn(log *logging.Logger, conn net.Conn) {
	defer func() {
		if err := conn.Close(); err != nil {
			log.WithError(err).Debug("Error closing latency probe conn")
		}
	}()

	// Read in a loop to detect when the connection is closed.
	// The RouteGroup handles ping/pong packets at a lower level.
	buf := make([]byte, 1024)
	for {
		_, err := conn.Read(buf)
		if err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
				log.WithError(err).Debug("Latency probe conn closed")
			}
			return
		}
	}
}

func initDmsgPing(ctx context.Context, v *Visor, log *logging.Logger) error {
	dmsgC := v.dmsgC
	if dmsgC == nil {
		return nil
	}

	// Wait for dmsg client to be ready
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-dmsgC.Ready():
	}

	lis, err := dmsgC.Listen(visorconfig.DmsgPingPort)
	if err != nil {
		return err
	}

	v.pushCloseStack("dmsg_ping", lis.Close)

	go func() {
		for {
			conn, err := lis.Accept()
			if err != nil {
				if !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
					log.WithError(err).Error("Failed to accept dmsg ping conn")
				}
				return
			}
			log.Debugf("Accepted dmsg ping conn from %s", conn.RemoteAddr())
			go handleDmsgPingConn(log, conn)
		}
	}()

	log.WithField("port", visorconfig.DmsgPingPort).Info("Dmsg ping listener started")
	return nil
}

func handleDmsgPingConn(log *logging.Logger, conn net.Conn) {
	defer func() {
		if err := conn.Close(); err != nil {
			log.WithError(err).Debug("Error closing dmsg ping conn")
		}
	}()

	for {
		buf := make([]byte, 32*1024)
		n, err := conn.Read(buf)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				log.WithError(err).Error("Failed to read dmsg ping packet")
			}
			return
		}

		var size PingSizeMsg
		err = json.Unmarshal(buf[:n], &size)
		if err != nil {
			log.WithError(err).Error("Failed to unmarshal dmsg ping size")
			return
		}

		// Ack the size message
		_, err = conn.Write([]byte("ok"))
		if err != nil {
			log.WithError(err).Error("Failed to write dmsg ping ack")
			return
		}

		// Read the full ping payload
		var ping []byte
		for len(ping) < size.Size {
			n, err = conn.Read(buf)
			if err != nil {
				if !errors.Is(err, io.EOF) {
					log.WithError(err).Error("Failed to read dmsg ping data")
				}
				return
			}
			ping = append(ping, buf[:n]...)
		}

		// Echo back for RTT measurement
		// If EchoFull is set, echo the full payload for bandwidth testing
		if size.EchoFull {
			_, err = conn.Write(ping)
			if err != nil {
				log.WithError(err).Error("Failed to write full dmsg ping echo")
				return
			}
			log.Debugf("Echoed full dmsg ping response (%d bytes)", len(ping))
		} else {
			_, err = conn.Write([]byte("pong"))
			if err != nil {
				log.WithError(err).Error("Failed to write dmsg ping echo")
				return
			}
			log.Debug("Echoed dmsg ping response")
		}
	}
}

// initDmsgServerLatency initializes DMSG server latency tracking.
// It self-pings via each connected DMSG server on startup and hourly.
func initDmsgServerLatency(ctx context.Context, v *Visor, log *logging.Logger) error {
	dmsgC := v.dmsgC
	if dmsgC == nil {
		return nil
	}

	// Wait for dmsg client to be ready
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-dmsgC.Ready():
	}

	// Helper to measure latency to all connected servers
	measureServerLatencies := func() {
		servers := dmsgC.ConnectedServersPK()
		if len(servers) == 0 {
			log.Debug("No DMSG servers connected, skipping latency measurement")
			return
		}

		log.WithField("servers", len(servers)).Info("Measuring DMSG server latencies via self-ping")

		for _, serverPKStr := range servers {
			var serverPK cipher.PubKey
			if err := serverPK.Set(serverPKStr); err != nil {
				log.WithError(err).WithField("server", serverPKStr).Warn("Invalid server PK")
				continue
			}

			// Self-ping via this server (ping our own PK through the server)
			start := time.Now()
			conf := PingConfig{
				PK:       v.conf.PK,
				Tries:    3,
				PcktSize: 2, // 2KB
			}

			// Use DmsgPingViaServer to ping ourselves through this specific server
			latencies, err := v.DmsgPingViaServer(conf, serverPK)
			if err != nil {
				log.WithError(err).WithField("server", serverPKStr[:16]+"...").Warn("Failed to measure server latency")
				continue
			}

			// Calculate average latency
			var totalLatency time.Duration
			for _, lat := range latencies {
				totalLatency += lat
			}
			avgLatency := totalLatency / time.Duration(len(latencies))

			// Store the latency
			v.dmsgServerLatenciesMu.Lock()
			v.dmsgServerLatencies[serverPK] = avgLatency
			v.dmsgServerLatenciesMu.Unlock()

			log.WithFields(logrus.Fields{
				"server":  serverPKStr[:16] + "...",
				"latency": avgLatency.Round(time.Millisecond),
				"elapsed": time.Since(start).Round(time.Millisecond),
			}).Info("Measured DMSG server latency")
		}
	}

	// Initial measurement
	go func() {
		// Small delay to allow more servers to connect
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
		measureServerLatencies()
	}()

	// Hourly measurement
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				measureServerLatencies()
			}
		}
	}()

	log.Info("DMSG server latency tracking started")
	return nil
}

// initNodeHealth initializes the node health tracker for TPS and RSN nodes.
func initNodeHealth(ctx context.Context, v *Visor, log *logging.Logger) error {
	if v.dmsgC == nil {
		log.Warn("Dmsg client not available, skipping node health tracking")
		return nil
	}

	// Get configured TPS and RSN nodes
	tpsNodes := v.conf.Transport.TransportSetupPKs
	rsnNodes := v.conf.Routing.RouteSetupNodes

	if len(tpsNodes) == 0 && len(rsnNodes) == 0 {
		log.Info("No TPS or RSN nodes configured, skipping node health tracking")
		return nil
	}

	log.WithField("tps_count", len(tpsNodes)).
		WithField("rsn_count", len(rsnNodes)).
		Info("Initializing node health tracker")

	v.nodeHealthTracker = NewNodeHealthTracker(v.dmsgC, log)
	v.nodeHealthTracker.Start(ctx, tpsNodes, rsnNodes)

	return nil
}

// getRouteSetupHooks aka autotransport
// TODO: fix gocyclo error.
//
//gocyclo:ignore
func getRouteSetupHooks(ctx context.Context, v *Visor, log *logging.Logger) []router.RouteSetupHook {
	retrier := netutil.NewRetrier(log, time.Second, time.Second*20, 3, 1.3)
	return []router.RouteSetupHook{
		func(rPK cipher.PubKey, tm *transport.Manager) error {
			establishedTransports, _ := v.Transports([]string{string(types.STCPR), string(types.SUDPH), string(types.DMSG)}, []cipher.PubKey{v.conf.PK}, false) //nolint:errcheck
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
					_, err := tm.SaveTransport(ctx, rPK, types.DMSG, transport.LabelAutomatic)
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
				nType := types.Type(trans)
				if nType == types.STCPR {
					trySTCPR = true
					continue
				}

				// Wait until stun client is ready
				<-v.stunReady

				// skip if SUDPH is under symmetric NAT / under UDP firewall.
				if nType == types.SUDPH && (v.stunClient.NATType == stun.NATSymmetric ||
					v.stunClient.NATType == stun.NATSymmetricUDPFirewall) {
					continue
				}
				trySUDPH = true
			}

			// trying to establish direct connection to rPK using STCPR
			if trySTCPR {
				err := retrier.Do(ctx, func() error {
					_, err := tm.SaveTransport(ctx, rPK, types.STCPR, transport.LabelAutomatic)
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
					_, err := tm.SaveTransport(ctx, rPK, types.SUDPH, transport.LabelAutomatic)
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

	// Use embedded route setup-node if available, otherwise use remote setup-nodes
	var rgDialer router.RouteGroupDialer
	if v.embeddedRouteSetup != nil {
		log.WithField("route_setup_pk", v.embeddedRouteSetup.PK()).Info("Using embedded route setup-node for routing")
		rgDialer = router.NewSetupNodeDialerWithEmbedded(v.embeddedRouteSetup)
	} else {
		rgDialer = router.NewSetupNodeDialer()
	}

	rConf := router.Config{
		Logger:           logger,
		MasterLogger:     v.MasterLogger(),
		PubKey:           v.conf.PK,
		SecKey:           v.conf.SK,
		TransportManager: v.tpM,
		RouteFinder:      rfClient,
		RouteGroupDialer: rgDialer,
		SetupNodes:       conf.RouteSetupNodes,
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

	// Set up transport latency measurement callback
	// When a transport is created, measure its latency via a direct route ping
	v.tpM.SetOnTransportCreated(func(ctx context.Context, remote cipher.PubKey, tpID uuid.UUID) float64 {
		latencyMs, err := r.MeasureTransportLatency(ctx, remote, tpID)
		if err != nil {
			logger.WithError(err).Debugf("Failed to measure latency for transport %s", tpID)
			return 0
		}
		logger.Debugf("Measured latency for transport %s: %.2f ms", tpID, latencyMs)
		return latencyMs
	})

	return nil
}

func initLauncher(_ context.Context, v *Visor, _ *logging.Logger) error {
	conf := v.conf.Launcher

	// Prepare proc manager.
	procM, err := appserver.NewProcManager(v.MasterLogger(), &v.serviceDisc, v.ebc, conf.ServerAddr, v.conf.LocalPath)
	if err != nil {
		err := fmt.Errorf("failed to start proc_manager: %w", err)
		return err
	}

	v.pushCloseStack("launcher.proc_manager", procM.Close)

	// Prepare launcher.
	launchConf := launcher.AppLauncherConfig{
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
		visorconfig.VPNClientName: vpnEnvMaker(v.conf, v.dmsgC, v.dmsgDC, v.tpM.STCPRRemoteAddrs()),
		visorconfig.VPNServerName: vpnEnvMaker(v.conf, v.dmsgC, v.dmsgDC, nil),
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

// cliRPCStats tracks CLI RPC connection statistics for diagnostics
type cliRPCStats struct {
	mu            sync.Mutex
	activeConns   int32
	totalConns    uint64
	totalErrors   uint64
	lastError     string
	lastErrorTime time.Time
	peakConns     int32
	connSemaphore chan struct{}
	maxConns      int
}

func (s *cliRPCStats) acquire() bool {
	select {
	case s.connSemaphore <- struct{}{}:
		s.mu.Lock()
		s.activeConns++
		if s.activeConns > s.peakConns {
			s.peakConns = s.activeConns
		}
		s.totalConns++
		s.mu.Unlock()
		return true
	default:
		return false
	}
}

func (s *cliRPCStats) release() {
	s.mu.Lock()
	s.activeConns--
	s.mu.Unlock()
	<-s.connSemaphore
}

func (s *cliRPCStats) recordError(err string) {
	s.mu.Lock()
	s.totalErrors++
	s.lastError = err
	s.lastErrorTime = time.Now()
	s.mu.Unlock()
}

func (s *cliRPCStats) snapshot() (active int32, total uint64, errors uint64, peak int32, lastErr string, lastErrTime time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.activeConns, s.totalConns, s.totalErrors, s.peakConns, s.lastError, s.lastErrorTime
}

// visorPingAdapter wraps a Visor to implement rpcgrpc.VisorAPI
type visorPingAdapter struct {
	v *Visor
}

func (a *visorPingAdapter) DialPing(conf rpcgrpc.PingConf) error {
	// Convert rpcgrpc.RouteHopInfo to RouteHopInfo
	var forwardHops, reverseHops []RouteHopInfo
	for _, h := range conf.ForwardHops {
		forwardHops = append(forwardHops, RouteHopInfo{
			TpID:   h.TpID,
			From:   h.From,
			To:     h.To,
			TpType: h.TpType,
		})
	}
	for _, h := range conf.ReverseHops {
		reverseHops = append(reverseHops, RouteHopInfo{
			TpID:   h.TpID,
			From:   h.From,
			To:     h.To,
			TpType: h.TpType,
		})
	}
	return a.v.DialPing(PingConfig{
		PK:          conf.PK,
		Tries:       conf.Tries,
		PcktSize:    conf.PcktSize,
		LocalRoute:  conf.LocalRoute,
		TransportID: conf.TransportID,
		ForwardHops: forwardHops,
		ReverseHops: reverseHops,
	})
}

func (a *visorPingAdapter) PingOnce(conf rpcgrpc.PingConf) (time.Duration, error) {
	return a.v.PingOnce(PingConfig{
		PK:         conf.PK,
		Tries:      conf.Tries,
		PcktSize:   conf.PcktSize,
		LocalRoute: conf.LocalRoute,
	})
}

func (a *visorPingAdapter) StopPing(pk cipher.PubKey) error {
	return a.v.StopPing(pk)
}

func (a *visorPingAdapter) GetPingRoute(pk cipher.PubKey) []cipher.PubKey {
	return a.v.GetPingRoute(pk)
}

func (a *visorPingAdapter) GetPingRouteDetails(pk cipher.PubKey) []rpcgrpc.RouteHopInfo {
	details := a.v.GetPingRouteDetails(pk)
	if details == nil {
		return nil
	}
	// Convert router.RouteHopInfo to rpcgrpc.RouteHopInfo
	result := make([]rpcgrpc.RouteHopInfo, len(details))
	for i, d := range details {
		result[i] = rpcgrpc.RouteHopInfo{
			TpID:   d.TpID,
			From:   d.From,
			To:     d.To,
			TpType: d.TpType,
		}
	}
	return result
}

func (a *visorPingAdapter) GetLastRouteCalcTime() time.Duration {
	return a.v.GetLastRouteCalcTime()
}

func (a *visorPingAdapter) DialDmsgPing(pk cipher.PubKey) error {
	return a.v.DialDmsgPing(pk)
}

func (a *visorPingAdapter) DmsgPingOnce(conf rpcgrpc.PingConf) (time.Duration, error) {
	return a.v.DmsgPingOnce(PingConfig{
		PK:       conf.PK,
		Tries:    conf.Tries,
		PcktSize: conf.PcktSize,
	})
}

func (a *visorPingAdapter) PingOnceWithEcho(conf rpcgrpc.PingConf, echoFull bool) (bytesSent, bytesReceived uint64, latency time.Duration, err error) {
	return a.v.PingOnceWithEcho(PingConfig{
		PK:         conf.PK,
		Tries:      conf.Tries,
		PcktSize:   conf.PcktSize,
		LocalRoute: conf.LocalRoute,
	}, echoFull)
}

func (a *visorPingAdapter) DmsgPingOnceWithEcho(conf rpcgrpc.PingConf, echoFull bool) (bytesSent, bytesReceived uint64, latency time.Duration, err error) {
	return a.v.DmsgPingOnceWithEcho(PingConfig{
		PK:       conf.PK,
		Tries:    conf.Tries,
		PcktSize: conf.PcktSize,
	}, echoFull)
}

func (a *visorPingAdapter) StopDmsgPing(pk cipher.PubKey) error {
	return a.v.StopDmsgPing(pk)
}

func (a *visorPingAdapter) DialDmsgPingViaServer(pk cipher.PubKey, serverPK cipher.PubKey) error {
	return a.v.DialDmsgPingViaServer(pk, serverPK)
}

func (a *visorPingAdapter) GetDmsgPingServerPK(pk cipher.PubKey) (cipher.PubKey, error) {
	return a.v.GetDmsgPingServerPK(pk)
}

func (a *visorPingAdapter) GetRemoteDmsgServers(pk cipher.PubKey) ([]cipher.PubKey, error) {
	return a.v.GetRemoteDmsgServers(pk)
}

func (a *visorPingAdapter) DialDmsgRPC(pk cipher.PubKey) (net.Conn, error) {
	return a.v.DialDmsgRPC(pk)
}

func initCLI(_ context.Context, v *Visor, log *logging.Logger) error {
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

	// Create gRPC server for streaming operations
	grpcLog := v.MasterLogger().PackageLogger("cli_grpc")
	grpcServer := grpc.NewServer()
	pingAdapter := &visorPingAdapter{v: v}
	pingServer := rpcgrpc.NewPingServer(pingAdapter, grpcLog)
	rpcgrpc.RegisterPingServiceServer(grpcServer, pingServer)

	v.pushCloseStack("cli.grpc", func() error {
		grpcServer.GracefulStop()
		return nil
	})

	// Use cmux to multiplex gRPC and standard RPC on same port
	mux := cmux.New(cliL)
	grpcL := mux.MatchWithWriters(cmux.HTTP2MatchHeaderFieldSendSettings("content-type", "application/grpc"))
	rpcL := mux.Match(cmux.Any()) // All other connections go to standard RPC

	// Connection limiting and stats for standard RPC
	const maxConcurrentConns = 50
	stats := &cliRPCStats{
		connSemaphore: make(chan struct{}, maxConcurrentConns),
		maxConns:      maxConcurrentConns,
	}

	// Start gRPC server
	go func() {
		grpcLog.Infof("CLI gRPC server listening on %s (multiplexed)", v.conf.CLIAddr)
		if err := grpcServer.Serve(grpcL); err != nil {
			if !strings.Contains(err.Error(), "use of closed network connection") &&
				!strings.Contains(err.Error(), "mux: listener closed") {
				grpcLog.WithError(err).Error("gRPC server error")
			}
		}
	}()

	// Run standard RPC accept loop with panic recovery, connection limiting, and logging
	go func() {
		rpcLog := v.MasterLogger().PackageLogger("cli_rpc")
		rpcLog.Infof("CLI RPC server listening on %s (max %d concurrent connections, multiplexed)", v.conf.CLIAddr, maxConcurrentConns)

		// Periodic stats logging
		go func() {
			ticker := time.NewTicker(30 * time.Second)
			defer ticker.Stop()
			for range ticker.C {
				active, total, errors, peak, lastErr, lastErrTime := stats.snapshot()
				if total > 0 {
					rpcLog.Debugf("CLI RPC stats: active=%d, total=%d, errors=%d, peak=%d", active, total, errors, peak)
					if lastErr != "" && time.Since(lastErrTime) < time.Minute {
						rpcLog.Debugf("CLI RPC last error (%s ago): %s", time.Since(lastErrTime).Round(time.Second), lastErr)
					}
				}
			}
		}()

		var connID uint64
		for {
			conn, err := rpcL.Accept()
			if err != nil {
				// Check if listener was closed (normal shutdown)
				if strings.Contains(err.Error(), "use of closed network connection") ||
					strings.Contains(err.Error(), "mux: listener closed") {
					rpcLog.Debug("CLI RPC listener closed")
					return
				}
				stats.recordError(fmt.Sprintf("accept: %v", err))
				rpcLog.WithError(err).Warn("CLI RPC accept error, continuing...")
				continue
			}

			connID++
			thisConnID := connID

			// Try to acquire connection slot
			if !stats.acquire() {
				stats.recordError("connection limit reached")
				active, _, _, _, _, _ := stats.snapshot()
				rpcLog.Warnf("CLI RPC connection limit reached (%d/%d), rejecting connection", active, maxConcurrentConns)
				conn.Close() //nolint:errcheck,gosec
				continue
			}

			rpcLog.Debugf("CLI RPC conn #%d accepted from %s (active: %d)", thisConnID, conn.RemoteAddr(), stats.activeConns)

			// Handle each connection in a goroutine with panic recovery
			go func(c net.Conn, id uint64) {
				startTime := time.Now()
				defer func() {
					if r := recover(); r != nil {
						stats.recordError(fmt.Sprintf("panic: %v", r))
						rpcLog.Errorf("CLI RPC conn #%d panic recovered: %v", id, r)
					}
					c.Close() //nolint:errcheck,gosec
					stats.release()
					rpcLog.Debugf("CLI RPC conn #%d closed after %s (active: %d)", id, time.Since(startTime).Round(time.Millisecond), stats.activeConns)
				}()

				// Set read/write deadlines to prevent hung connections
				if tc, ok := c.(*net.TCPConn); ok {
					tc.SetKeepAlive(true)                   //nolint:errcheck,gosec
					tc.SetKeepAlivePeriod(30 * time.Second) //nolint:errcheck,gosec
				}

				rpcS.ServeConn(c)
			}(conn, thisConnID)
		}
	}()

	// Start cmux - this must be called after setting up all listeners
	go func() {
		if err := mux.Serve(); err != nil {
			if !strings.Contains(err.Error(), "use of closed network connection") &&
				!strings.Contains(err.Error(), "mux: listener closed") {
				v.log.WithError(err).Error("cmux serve error")
			}
		}
	}()

	return nil
}

func initHypervisors(_ context.Context, v *Visor, _ *logging.Logger) error {

	hvErrs := make(map[cipher.PubKey]chan error, len(v.conf.Hypervisors))
	for _, hv := range v.conf.Hypervisors {
		hvErrs[hv] = make(chan error, 1)
	}

	for hvPK, hvErrs := range hvErrs {
		log := v.MasterLogger().PackageLogger("hypervisor_client").WithField("hypervisor_pk", hvPK)

		addr := dmsg.Addr{PK: hvPK, Port: visorconfig.DmsgHypervisorPort}
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
			//			var autoPeerIP string
			//			if v.autoPeer {
			//				autoPeerIP = v.autoPeerIP
			//			} else {
			//				autoPeerIP = ""
			//			}
			defer delete(v.connectedHypervisors, hvPK)
			v.connectedHypervisors[hvPK] = true
			ServeRPCClient(ctx, log, v.dmsgC, rpcS, addr, hvErrs)
			//			ServeRPCClient(ctx, log, autoPeerIP, v.dmsgC, rpcS, addr, hvErrs)

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
			if err := ut.UpdateVisorUptime(c, v.conf.Version); err != nil {
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

func initEnsureVisorIsTransportable(ctx context.Context, v *Visor, log *logging.Logger) error {
	const tickDuration = 5 * time.Minute
	ticker := time.NewTicker(tickDuration)
	_ = ctx // unused after removing AR check

	// Perform transportability check logic - only DMSG is required
	performCheck := func(tries int) int {
		dmsgOK := tryTransport(v, "dmsg", log)

		if dmsgOK {
			v.isServicesHealthy.set()
			if tries > 0 {
				log.Info("Visor is now transportable (recovered)")
			} else {
				log.Debug("Visor transportability check passed")
			}
			tries = 0
			ticker.Reset(tickDuration)
		} else {
			v.isServicesHealthy.unset()
			tries++
			log.WithField("tries", tries).WithField("dmsg_ok", dmsgOK).
				Warn("Visor transportability check failed")
			ticker.Reset(time.Minute)
		}

		if tries >= 3 {
			if v.conf.DisableShutdownOnNonTransportable {
				log.Error("Visor is not transportable after 3 failed attempts, but shutdown is disabled for troubleshooting")
				// Keep trying but don't accumulate tries forever
				tries = 0
			} else {
				log.Error("Visor is not transportable after 3 failed attempts. Shutting down...")
				if err := v.Shutdown(); err != nil {
					log.WithError(err).Fatal("Failed to shut down gracefully")
				}
				// Signal shutdown to stop the ticker loop
				tries = -1
			}
		}

		return tries
	}

	go func() {
		// Wait 1 minute after startup before first check
		time.Sleep(time.Minute)
		tries := 0

		// Perform first check immediately after initial delay
		tries = performCheck(tries)
		if tries == -1 {
			return // Shutdown triggered
		}

		// Continue periodic checks
		for range ticker.C {
			tries = performCheck(tries)
			if tries == -1 {
				return // Shutdown triggered
			}
		}
	}()
	v.pushCloseStack("transportable", func() error {
		ticker.Stop()
		return nil
	})

	return nil
}

func tryTransport(v *Visor, tpType string, log *logging.Logger) bool {
	tp, err := v.AddTransport(v.conf.PK, tpType, 0, "", false, false)
	if err != nil {
		log.WithError(err).WithField("type", tpType).Warn("Failed to create self-transport")
		return false
	}

	err = v.RemoveTransport(tp.ID)
	if err != nil {
		log.WithError(err).WithField("type", tpType).Warn("Failed to remove self-transport")
	}
	return true
}

// TODO: fix gocyclo error.
//
//gocyclo:ignore
func initEnsureTPDConcurrency(ctx context.Context, v *Visor, log *logging.Logger) error {
	// Run immediate reconciliation on startup (no delay)
	// This ensures stale TPD entries from previous runs are cleaned up quickly
	go func() {
		reconcileTPDWithRetry(ctx, v, log)
	}()

	// Run periodic reconciliation every 5 minutes
	// At scale (3000+ visors), more frequent intervals create too much TPD load
	const tickDuration = 5 * time.Minute
	ticker := time.NewTicker(tickDuration)
	go func() {
		for range ticker.C {
			reconcileTPDWithRetry(ctx, v, log)
		}
	}()

	v.pushCloseStack("tpd_concurrency", func() error {
		ticker.Stop()
		return nil
	})
	return nil
}

// reconcileTPDWithRetry retries TPD reconciliation until success
// This is critical because routing breaks completely if TPD data is stale
func reconcileTPDWithRetry(ctx context.Context, v *Visor, log *logging.Logger) {
	attempt := 0
	maxBackoff := 5 * time.Minute

	for {
		attempt++
		if err := reconcileTPD(ctx, v, log); err != nil {
			// Calculate exponential backoff: 10s, 20s, 40s, 80s, 160s, capped at 5min
			//nolint:gosec
			backoff := time.Duration(10*(1<<uint(attempt-1))) * time.Second
			if backoff > maxBackoff {
				backoff = maxBackoff
			}

			log.WithError(err).Warnf("TPD reconciliation failed (attempt %d), retrying in %v", attempt, backoff)

			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
				continue
			}
		} else {
			if attempt > 1 {
				log.Infof("TPD reconciliation succeeded after %d attempts", attempt)
			}
			return
		}
	}
}

func reconcileTPD(ctx context.Context, v *Visor, log *logging.Logger) error {
	// Query TPD with retry logic
	var entries []*transport.Entry
	var err error
	for retries := 0; retries < 3; retries++ {
		entries, err = v.DiscoverTransportsByPK(v.conf.PK)
		if err == nil {
			break
		}

		// Check if it's an HTTPError with a 4xx status code
		var httpErr *httputil.HTTPError
		if errors.As(err, &httpErr) {
			// Don't retry on 4xx errors (client errors) - these are expected
			// For example, 404 means no transports found, which is normal
			if httpErr.Status >= 400 && httpErr.Status < 500 {
				log.WithError(err).Debug("TPD query returned client error (4xx), not retrying")
				err = nil // Treat 4xx as success (no transports to reconcile)
				break
			}
		}

		// Retry on 5xx errors and connection failures
		if retries < 2 {
			backoff := time.Duration(retries+1) * 2 * time.Second
			log.WithError(err).Warnf("Failed to query TPD (attempt %d/3), retrying in %v", retries+1, backoff)
			time.Sleep(backoff)
		}
	}
	if err != nil {
		v.isServicesHealthy.unset()
		return fmt.Errorf("failed to query TPD after retries: %w", err)
	}

	v.isServicesHealthy.set()

	// Build map of TPD entries (non-loopback only)
	var tpdIDs []uuid.UUID
	for _, e := range entries {
		if e.Edges[0] != e.Edges[1] {
			tpdIDs = append(tpdIDs, e.ID)
		}
	}

	// Get local transports (non-loopback only)
	transports, err := v.Transports(nil, nil, false)
	if err != nil {
		return fmt.Errorf("failed to get local transports: %w", err)
	}

	var localIDs []uuid.UUID
	for _, t := range transports {
		if t.Local != t.Remote {
			localIDs = append(localIDs, t.ID)
		}
	}

	// Find stale TPD entries (in TPD but not local)
	var staleIDs []uuid.UUID
	for _, tpdID := range tpdIDs {
		found := false
		for _, localID := range localIDs {
			if localID == tpdID {
				found = true
				break
			}
		}
		if !found {
			staleIDs = append(staleIDs, tpdID)
		}
	}

	// Remove stale entries from TPD with retry logic
	if len(staleIDs) > 0 {
		log.Infof("Removing %d stale transports from TPD", len(staleIDs))

		var tpdC transport.DiscoveryClient
		for retries := 0; retries < 3; retries++ {
			tpdC, err = connectToTpDisc(ctx, v, log)
			if err == nil {
				break
			}
			if retries < 2 {
				backoff := time.Duration(retries+1) * 2 * time.Second
				log.WithError(err).Warnf("Failed to connect to TPD (attempt %d/3), retrying in %v", retries+1, backoff)
				time.Sleep(backoff)
			}
		}
		if err != nil {
			return fmt.Errorf("failed to connect to TPD after retries: %w", err)
		}

		for _, id := range staleIDs {
			// Retry each deletion
			var deleteErr error
			for retries := 0; retries < 3; retries++ {
				deleteErr = tpdC.DeleteTransport(ctx, id)
				if deleteErr == nil {
					log.Debugf("Removed stale transport %v from TPD", id)
					break
				}
				if retries < 2 {
					backoff := time.Duration(retries+1) * time.Second
					time.Sleep(backoff)
				}
			}
			if deleteErr != nil {
				log.WithError(deleteErr).Warnf("Failed to remove stale transport %v after retries", id)
			}
		}
	}

	return nil
}

// advertise this visor as public in service discovery
// this service is not considered critical and always returns true
func initPublicVisor(_ context.Context, v *Visor, log *logging.Logger) error { //nolint:revive
	// Always attempt to deregister stale entries on startup.
	// This handles the case where visor crashed while public and restarts,
	// ensuring old service discovery entries are cleaned up before re-registering.
	v.serviceDisc.VisorUpdater(0).Stop()

	if !v.conf.IsPublic {
		return nil
	}
	logger := v.MasterLogger().PackageLogger("public_visor")

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

	// Get public visor config for validation settings
	var registrationTimeout time.Duration
	var maxTransports int
	if pvConf := v.conf.PublicVisorConfig; pvConf != nil {
		registrationTimeout = time.Duration(pvConf.RegistrationTimeout)
		maxTransports = pvConf.MaxTransports
	}

	// Create public visor updater with validation logic
	publicUpdater := v.serviceDisc.PublicVisorUpdater(
		port,
		registrationTimeout,
		maxTransports,
		func() int {
			// Return current transport count from transport manager
			return v.tpM.TransportCount()
		},
	)

	if publicUpdater == nil {
		logger.Warn("Failed to create public visor updater")
		return nil
	}

	// Store the updater so the OnExternalSTCPR callback can access it
	v.publicVisorUpdaterMu.Lock()
	v.publicVisorUpdater = publicUpdater
	v.publicVisorUpdaterMu.Unlock()

	publicUpdater.Start()

	v.log.Debugf("Sent request to register visor as public")
	v.pushCloseStack("public visor updater", func() error {
		publicUpdater.Stop()
		return nil
	})
	return nil
}

// TODO: fix gocyclo error.
//
//gocyclo:ignore
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

	// Initialize the dmsgpty whitelist
	if err := wl.Add(v.conf.Dmsgpty.Whitelist...); err != nil {
		return err
	}

	// Ensure hypervisors are added to the whitelist.
	if err := wl.Add(v.conf.Hypervisors...); err != nil {
		return err
	}
	// add the visor's own public key to the whitelist to allow local pty
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
	return v.startPublicAutoconnectInternal(ctx, log)
}

// startPublicAutoconnectInternal starts the public autoconnect goroutine.
// Called both at init time and when starting via API.
func (v *Visor) startPublicAutoconnectInternal(ctx context.Context, log *logging.Logger) error {
	v.autoconnectMu.Lock()
	defer v.autoconnectMu.Unlock()

	if v.autoconnectRunning {
		return nil // already running
	}

	serviceDisc := v.conf.Launcher.ServiceDisc
	if serviceDisc == "" { //it might be intentionally blank ; consider revising.
		serviceDisc = deployment.Prod.ServiceDiscovery
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
	connector := MakeConnector(conf, 3, v.tpM, v.serviceDisc.Client, pIP, log, v.MasterLogger())

	cctx, cancel := context.WithCancel(ctx)
	v.autoconnectCancel = cancel
	v.autoconnectRunning = true

	v.pushCloseStack("public_autoconnect", func() error {
		v.autoconnectMu.Lock()
		defer v.autoconnectMu.Unlock()
		if v.autoconnectCancel != nil {
			v.autoconnectCancel()
			v.autoconnectCancel = nil
		}
		v.autoconnectRunning = false
		return nil
	})

	go connector.Run(cctx, v) //nolint:errcheck

	return nil
}

// StartPublicAutoconnect starts public autoconnect if not already running.
func (v *Visor) StartPublicAutoconnect() error {
	log := v.MasterLogger().PackageLogger("public_autoconnect")
	return v.startPublicAutoconnectInternal(context.Background(), log)
}

// StopPublicAutoconnect stops public autoconnect if running.
func (v *Visor) StopPublicAutoconnect() error {
	v.autoconnectMu.Lock()
	defer v.autoconnectMu.Unlock()

	if !v.autoconnectRunning {
		return nil // not running
	}

	if v.autoconnectCancel != nil {
		v.autoconnectCancel()
		v.autoconnectCancel = nil
	}
	v.autoconnectRunning = false
	return nil
}

// IsPublicAutoconnectRunning returns whether public autoconnect is running.
func (v *Visor) IsPublicAutoconnectRunning() bool {
	v.autoconnectMu.Lock()
	defer v.autoconnectMu.Unlock()
	return v.autoconnectRunning
}

func initHypervisor(_ context.Context, v *Visor, log *logging.Logger) error {
	if v.conf.Hypervisor == nil {
		v.log.Error("hypervisor config = nil")
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())

	conf := *v.conf.Hypervisor
	conf.PK = v.conf.PK
	conf.SK = v.conf.SK
	conf.DmsgDiscovery = v.conf.Dmsg.Discovery

	// Prepare hypervisor.
	hv, err := NewHypervisor(conf, v, v.dmsgC)
	if err != nil {
		v.log.Fatalln("Failed to start hypervisor:", err)
	}

	hv.serveDmsg(ctx, v.log)

	// Serve HTTP(s).

	// Needed to work with modern browsers when serving from windows, which need the correct mime type for javascript.
	if err := mime.AddExtensionType(".js", "application/javascript"); err != nil {
		log.Fatalln("Unable to register js mime type.")
	}

	v.log.WithField("addr", conf.HTTPAddr).
		WithField("tls", conf.EnableTLS).
		Info("Serving hypervisor...")
	tls := ""
	if conf.EnableTLS {
		tls = "s"
	}
	v.log.Info(fmt.Sprintf("Hypervisor UI: http%s://127.0.0.1%s", tls, conf.HTTPAddr))

	handler := hv.HTTPHandler()
	srv := &http.Server{
		Addr:              conf.HTTPAddr,
		Handler:           handler,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      10 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		if conf.EnableTLS {
			err = srv.ListenAndServeTLS(conf.TLSCertFile, conf.TLSKeyFile)
		} else {
			err = srv.ListenAndServe()
		}

		// don't print error if local server is closed
		if !errors.Is(err, http.ErrServerClosed) {
			v.log.WithError(err).Error("Hypervisor exited with error.")
		}
	}()

	v.pushCloseStack("hypervisor", func() error {
		err := srv.Shutdown(ctx)
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

	var serviceURL dmsgcurl.URL
	var delegatedServers []cipher.PubKey
	err := serviceURL.Fill(service)

	if serviceURL.Scheme == "dmsg" {
		if err != nil {
			return nil, fmt.Errorf("provided URL is invalid: %w", err)
		}
		// get delegated servers and add them to the client entry
		servers, err := v.dClient.AvailableServers(ctx)
		if err != nil {
			return nil, fmt.Errorf("error getting AvailableServers: %w", err)
		}
		// randomize dmsg servers list
		rand.Shuffle(len(servers), func(i, j int) {
			servers[i], servers[j] = servers[j], servers[i]
		})
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
			return nil, fmt.Errorf("error saving clientEntry: %w", err)
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
	var serviceURL dmsgcurl.URL
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

	pIP, err = GetIP(v.conf.GeoIP)
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

// GeoData holds geolocation information from the geoIP service.
type GeoData struct {
	CountryCode string  `json:"country_code,omitempty"`
	CountryName string  `json:"country_name,omitempty"`
	RegionCode  string  `json:"region_code,omitempty"`
	RegionName  string  `json:"region_name,omitempty"`
	CityName    string  `json:"city_name,omitempty"`
	Latitude    float64 `json:"latitude,omitempty"`
	Longitude   float64 `json:"longitude,omitempty"`
}

type geoIPResponse struct {
	IP          string   `json:"ip_address"`
	CountryCode string   `json:"country_code"`
	CountryName string   `json:"country_name"`
	RegionCode  string   `json:"region_code"`
	RegionName  string   `json:"region_name"`
	CityName    string   `json:"city_name"`
	Latitude    *float64 `json:"latitude"`
	Longitude   *float64 `json:"longitude"`
}

// GetIP used for getting current IP of visor
func GetIP(geoipURL string) (string, error) {
	ip, _ := GetIPWithGeo(geoipURL)
	return ip, nil
}

// GetIPWithGeo fetches public IP and geolocation data from the geoIP service.
func GetIPWithGeo(geoipURL string) (string, *GeoData) {
	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	resp, err := client.Get(geoipURL)
	if err != nil {
		return "", nil
	}
	defer resp.Body.Close() //nolint:errcheck

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", nil
	}

	var geoResp geoIPResponse
	err = json.Unmarshal(body, &geoResp)
	if err != nil {
		return "", nil
	}

	geo := &GeoData{
		CountryCode: geoResp.CountryCode,
		CountryName: geoResp.CountryName,
		RegionCode:  geoResp.RegionCode,
		RegionName:  geoResp.RegionName,
		CityName:    geoResp.CityName,
	}
	if geoResp.Latitude != nil {
		geo.Latitude = *geoResp.Latitude
	}
	if geoResp.Longitude != nil {
		geo.Longitude = *geoResp.Longitude
	}

	return geoResp.IP, geo
}

// visorAPIAdapter adapts *Visor to the tpviz.VisorAPI interface.
// It converts between visor types and tpviz types.
type visorAPIAdapter struct {
	v *Visor
}

// Overview implements tpviz.VisorAPI.
func (a *visorAPIAdapter) Overview() (*tpviz.VisorOverview, error) {
	ov, err := a.v.Overview()
	if err != nil {
		return nil, err
	}

	// Convert visor.Overview to tpviz.VisorOverview
	result := &tpviz.VisorOverview{
		PubKey:      ov.PubKey,
		RoutesCount: ov.RoutesCount,
	}

	// Convert transports
	for _, tp := range ov.Transports {
		result.Transports = append(result.Transports, &tpviz.TransportSummary{
			ID:      tp.ID,
			Local:   tp.Local,
			Remote:  tp.Remote,
			Type:    tp.Type,
			Log:     tp.Log,
			IsSetup: tp.IsSetup,
			Label:   tp.Label,
		})
	}

	return result, nil
}

// RoutingRules implements tpviz.VisorAPI.
func (a *visorAPIAdapter) RoutingRules() ([]routing.Rule, error) {
	return a.v.RoutingRules()
}

// Close implements tpviz.VisorAPI.
func (a *visorAPIAdapter) Close() error {
	// Don't actually close the visor - the adapter doesn't own it
	return nil
}

// AddTransport implements tpviz.VisorAPI - creates a transport from the local visor to a remote visor.
func (a *visorAPIAdapter) AddTransport(ctx context.Context, remotePK, tpType string) (*tpviz.TransportSummary, error) {
	var remote cipher.PubKey
	if err := remote.UnmarshalText([]byte(remotePK)); err != nil {
		return nil, fmt.Errorf("invalid remote PK: %w", err)
	}

	netType := types.Type(tpType)
	if netType != types.STCPR && netType != types.SUDPH {
		return nil, fmt.Errorf("transport type must be stcpr or sudph")
	}

	tp, err := a.v.tpM.SaveTransport(ctx, remote, netType, transport.LabelSkycoin)
	if err != nil {
		return nil, err
	}

	return &tpviz.TransportSummary{
		ID:      tp.Entry.ID,
		Local:   a.v.conf.PK,
		Remote:  tp.Remote(),
		Type:    tp.Type(),
		IsSetup: true,
		Label:   tp.Entry.Label,
	}, nil
}

// RemoveTransport implements tpviz.VisorAPI - removes a transport from the local visor.
func (a *visorAPIAdapter) RemoveTransport(ctx context.Context, tpID string) error {
	id, err := uuid.Parse(tpID)
	if err != nil {
		return fmt.Errorf("invalid transport ID: %w", err)
	}

	a.v.tpM.DeleteTransport(id)
	return nil
}

// DMSGHealth performs a health check on a remote visor via DMSG.
// It dials the target visor on port 80 and fetches the /health endpoint.
func (a *visorAPIAdapter) DMSGHealth(ctx context.Context, pk string) (*tpviz.DMSGHealthResponse, error) {
	var targetPK cipher.PubKey
	if err := targetPK.UnmarshalText([]byte(pk)); err != nil {
		return nil, fmt.Errorf("invalid PK: %w", err)
	}

	dmsgC := a.v.dmsgC
	if dmsgC == nil {
		return nil, fmt.Errorf("DMSG client not available")
	}

	// Dial the target visor on port 80 (HTTP port)
	conn, err := dmsgC.Dial(ctx, dmsg.Addr{PK: targetPK, Port: 80})
	if err != nil {
		return &tpviz.DMSGHealthResponse{
			Status: "unreachable",
			Error:  fmt.Sprintf("dmsg dial failed: %v", err),
		}, nil
	}
	defer conn.Close() //nolint:errcheck

	// Create HTTP client over the DMSG connection
	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return conn, nil
			},
		},
		Timeout: 10 * time.Second,
	}

	// Make the health check request
	req, err := http.NewRequestWithContext(ctx, "GET", "http://dmsg/health", nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return &tpviz.DMSGHealthResponse{
			Status: "error",
			Error:  fmt.Sprintf("HTTP request failed: %v", err),
		}, nil
	}
	defer resp.Body.Close() //nolint:errcheck

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return &tpviz.DMSGHealthResponse{
			Status: "error",
			Error:  fmt.Sprintf("read response: %v", err),
		}, nil
	}

	// Try to parse as JSON
	var healthResp map[string]interface{}
	if err := json.Unmarshal(body, &healthResp); err == nil {
		status := "healthy"
		if resp.StatusCode != http.StatusOK {
			status = "unhealthy"
		}
		buildInfo := ""
		if bi, ok := healthResp["build_info"].(string); ok {
			buildInfo = bi
		}
		return &tpviz.DMSGHealthResponse{
			Status:    status,
			BuildInfo: buildInfo,
			Message:   string(body),
		}, nil
	}

	// Return raw response
	return &tpviz.DMSGHealthResponse{
		Status:  "healthy",
		Message: string(body),
	}, nil
}

// Ping implements tpviz.VisorAPI - performs a ping to a remote visor via routes or DMSG.
// It handles dial, ping, and cleanup in a single call for UI convenience.
// localRoute: when true and using routes, use cached TPD data instead of querying route finder.
func (a *visorAPIAdapter) Ping(ctx context.Context, pk string, useDMSG, localRoute bool, tries, sizeKB int) (*tpviz.PingResponse, error) {
	var targetPK cipher.PubKey
	if err := targetPK.UnmarshalText([]byte(pk)); err != nil {
		return nil, fmt.Errorf("invalid PK: %w", err)
	}

	mode := "route"
	if useDMSG {
		mode = "dmsg"
	}
	if localRoute && !useDMSG {
		mode = "route (local)"
	}

	conf := PingConfig{
		PK:         targetPK,
		Tries:      tries,
		PcktSize:   sizeKB,
		LocalRoute: localRoute,
	}

	var latencies []time.Duration
	var err error

	if useDMSG {
		// DMSG ping: dial → ping → stop
		if err = a.v.DialDmsgPing(targetPK); err != nil {
			return &tpviz.PingResponse{
				Status: "error",
				Error:  fmt.Sprintf("dmsg dial failed: %v", err),
				Mode:   mode,
			}, nil
		}
		defer func() {
			_ = a.v.StopDmsgPing(targetPK) //nolint:errcheck
		}()

		latencies, err = a.v.DmsgPing(conf)
	} else {
		// Route ping: dial → ping → stop
		if err = a.v.DialPing(conf); err != nil {
			return &tpviz.PingResponse{
				Status: "error",
				Error:  fmt.Sprintf("route dial failed: %v", err),
				Mode:   mode,
			}, nil
		}
		defer func() {
			_ = a.v.StopPing(targetPK) //nolint:errcheck
		}()

		latencies, err = a.v.Ping(conf)
	}

	if err != nil {
		return &tpviz.PingResponse{
			Status: "error",
			Error:  fmt.Sprintf("ping failed: %v", err),
			Mode:   mode,
		}, nil
	}

	// Calculate statistics
	if len(latencies) == 0 {
		return &tpviz.PingResponse{
			Status:     "timeout",
			PacketLoss: 100.0,
			Mode:       mode,
		}, nil
	}

	var sum, minVal, maxVal time.Duration
	latencyMs := make([]int64, 0, len(latencies))
	minVal = latencies[0]
	maxVal = latencies[0]

	for _, lat := range latencies {
		sum += lat
		if lat < minVal {
			minVal = lat
		}
		if lat > maxVal {
			maxVal = lat
		}
		latencyMs = append(latencyMs, lat.Milliseconds())
	}

	avg := sum / time.Duration(len(latencies))
	packetLoss := float64(tries-len(latencies)) / float64(tries) * 100.0

	return &tpviz.PingResponse{
		Status:     "success",
		LatencyMs:  avg.Seconds() * 1000,
		Latencies:  latencyMs,
		MinMs:      minVal.Seconds() * 1000,
		MaxMs:      maxVal.Seconds() * 1000,
		AvgMs:      avg.Seconds() * 1000,
		PacketLoss: packetLoss,
		Mode:       mode,
	}, nil
}

// Apps implements tpviz.VisorAPI - returns the list of all apps and their status.
func (a *visorAPIAdapter) Apps() ([]*tpviz.AppState, error) {
	apps, err := a.v.Apps()
	if err != nil {
		return nil, err
	}

	result := make([]*tpviz.AppState, 0, len(apps))
	for _, app := range apps {
		result = append(result, &tpviz.AppState{
			Name:           app.Name,
			Status:         int(app.Status),
			DetailedStatus: app.DetailedStatus,
			AutoStart:      app.AutoStart,
			Port:           uint16(app.Port),
			Args:           app.Args,
		})
	}
	return result, nil
}

// StartApp implements tpviz.VisorAPI - starts an application.
func (a *visorAPIAdapter) StartApp(appName string) error {
	return a.v.StartApp(appName)
}

// StopApp implements tpviz.VisorAPI - stops an application.
func (a *visorAPIAdapter) StopApp(appName string) error {
	return a.v.StopApp(appName)
}

// SetAutoStart implements tpviz.VisorAPI - sets the auto-start flag for an app.
func (a *visorAPIAdapter) SetAutoStart(appName string, autoStart bool) error {
	return a.v.SetAutoStart(appName, autoStart)
}

// SetAppPK implements tpviz.VisorAPI - sets the server public key for an app.
func (a *visorAPIAdapter) SetAppPK(appName, pk string) error {
	var pubKey cipher.PubKey
	if err := pubKey.UnmarshalText([]byte(pk)); err != nil {
		return fmt.Errorf("invalid public key: %w", err)
	}
	return a.v.SetAppPK(appName, pubKey)
}

// tpsAPIAdapter adapts *Visor's embeddedTPS to the tpviz.TPSAPI interface.
type tpsAPIAdapter struct {
	v *Visor
}

// AddTransport implements tpviz.TPSAPI.
func (a *tpsAPIAdapter) AddTransport(ctx context.Context, targetPK, remotePK, tpType string) (*tpviz.TPSTransportResponse, error) {
	tps := a.v.embeddedTPS
	if tps == nil {
		return nil, fmt.Errorf("embedded TPS not running")
	}

	var target, remote cipher.PubKey
	if err := target.UnmarshalText([]byte(targetPK)); err != nil {
		return nil, fmt.Errorf("invalid target PK: %w", err)
	}
	if err := remote.UnmarshalText([]byte(remotePK)); err != nil {
		return nil, fmt.Errorf("invalid remote PK: %w", err)
	}

	res, err := tps.AddTransport(ctx, target, remote, types.Type(tpType))
	if err != nil {
		return nil, err
	}

	return &tpviz.TPSTransportResponse{
		ID:     res.ID.String(),
		Local:  res.Local.String(),
		Remote: res.Remote.String(),
		Type:   string(res.Type),
	}, nil
}

// RemoveTransport implements tpviz.TPSAPI.
func (a *tpsAPIAdapter) RemoveTransport(ctx context.Context, targetPK, tpID string) error {
	tps := a.v.embeddedTPS
	if tps == nil {
		return fmt.Errorf("embedded TPS not running")
	}

	var target cipher.PubKey
	if err := target.UnmarshalText([]byte(targetPK)); err != nil {
		return fmt.Errorf("invalid target PK: %w", err)
	}

	id, err := uuid.Parse(tpID)
	if err != nil {
		return fmt.Errorf("invalid transport ID: %w", err)
	}

	return tps.RemoveTransport(ctx, target, id)
}

// GetTransports implements tpviz.TPSAPI.
func (a *tpsAPIAdapter) GetTransports(ctx context.Context, targetPK string) ([]tpviz.TPSTransportResponse, error) {
	tps := a.v.embeddedTPS
	if tps == nil {
		return nil, fmt.Errorf("embedded TPS not running")
	}

	var target cipher.PubKey
	if err := target.UnmarshalText([]byte(targetPK)); err != nil {
		return nil, fmt.Errorf("invalid target PK: %w", err)
	}

	entries, err := tps.GetTransports(ctx, target)
	if err != nil {
		return nil, err
	}

	result := make([]tpviz.TPSTransportResponse, len(entries))
	for i, e := range entries {
		result[i] = tpviz.TPSTransportResponse{
			ID:     e.ID.String(),
			Local:  e.Local.String(),
			Remote: e.Remote.String(),
			Type:   string(e.Type),
		}
	}
	return result, nil
}

// PK implements tpviz.TPSAPI.
func (a *tpsAPIAdapter) PK() string {
	tps := a.v.embeddedTPS
	if tps == nil {
		return ""
	}
	return tps.pk.String()
}

func initUIServer(ctx context.Context, v *Visor, log *logging.Logger) error {
	// Check if UI server is configured and enabled
	if v.conf.UIServer == nil || !v.conf.UIServer.Enable {
		log.Debug("UI server not configured or disabled, skipping")
		return nil
	}

	conf := v.conf.UIServer

	// Set default local address if not specified
	localAddr := conf.LocalAddr
	if localAddr == "" {
		localAddr = "localhost:8081"
	}

	// Create tpviz config
	tpvizCfg := tpviz.DefaultConfig()
	// Don't set Addr/Port since we're managing the HTTP server ourselves
	tpvizCfg.SurveyDir = conf.SurveyDir

	// Create tpviz server
	tpvizServer := tpviz.NewServer(tpvizCfg)

	// Set visor API directly (bypasses RPC connection) using an adapter
	adapter := &visorAPIAdapter{v: v}
	tpvizServer.SetVisorAPI(adapter, v.conf.PK.Hex())

	// Set TPS API if embedded TPS is running
	if v.embeddedTPS != nil {
		tpvizServer.SetTPSAPI(&tpsAPIAdapter{v: v})
		log.WithField("tps_pk", v.embeddedTPS.pk.Hex()).Info("TPS API connected to UI server")
	}

	// Start cache refresh (without starting HTTP server)
	tpvizServer.Start()

	// Start local HTTP server
	localListener, err := net.Listen("tcp", localAddr)
	if err != nil {
		return fmt.Errorf("failed to start UI server on %s: %w", localAddr, err)
	}

	srv := &http.Server{
		Handler:           tpvizServer.Handler(),
		ReadTimeout:       visorconfig.HTTPReadTimeout,
		WriteTimeout:      visorconfig.HTTPWriteTimeout,
		IdleTimeout:       visorconfig.HTTPIdleTimeout,
		ReadHeaderTimeout: visorconfig.HTTPReadHeaderTimeout,
	}

	wg := new(sync.WaitGroup)
	wg.Add(1)

	go func() {
		defer wg.Done()
		log.Infof("UI server listening on http://%s", localAddr)
		if err := srv.Serve(localListener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.WithError(err).Error("UI server exited with error")
		}
	}()

	// Start DMSG listener if configured
	var dmsgListener net.Listener
	if conf.DmsgPort > 0 && v.dmsgC != nil {
		dmsgListener, err = v.dmsgC.Listen(conf.DmsgPort)
		if err != nil {
			log.WithError(err).Warnf("Failed to start UI server on DMSG port %d", conf.DmsgPort)
		} else {
			// Create whitelist middleware if whitelist is configured
			handler := tpvizServer.Handler()
			if len(conf.DmsgWhitelist) > 0 {
				whitelistMap := make(map[cipher.PubKey]struct{}, len(conf.DmsgWhitelist))
				for _, pk := range conf.DmsgWhitelist {
					whitelistMap[pk] = struct{}{}
				}
				handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					// Extract remote public key from dmsg connection
					remoteAddr := r.RemoteAddr
					// DMSG addresses are in format "pk:port"
					if idx := strings.Index(remoteAddr, ":"); idx > 0 {
						pkStr := remoteAddr[:idx]
						var remotePK cipher.PubKey
						if err := remotePK.UnmarshalText([]byte(pkStr)); err == nil {
							if _, allowed := whitelistMap[remotePK]; !allowed {
								log.Warnf("UI server DMSG access denied for %s", pkStr)
								http.Error(w, "Forbidden", http.StatusForbidden)
								return
							}
						}
					}
					tpvizServer.Handler().ServeHTTP(w, r)
				})
			}

			dmsgSrv := &http.Server{
				Handler:           handler,
				ReadTimeout:       visorconfig.HTTPReadTimeout,
				WriteTimeout:      visorconfig.HTTPWriteTimeout,
				IdleTimeout:       visorconfig.HTTPIdleTimeout,
				ReadHeaderTimeout: visorconfig.HTTPReadHeaderTimeout,
			}

			wg.Add(1)
			go func() {
				defer wg.Done()
				log.Infof("UI server listening on DMSG port %d", conf.DmsgPort)
				if err := dmsgSrv.Serve(dmsgListener); err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, dmsg.ErrEntityClosed) {
					log.WithError(err).Error("UI server DMSG listener exited with error")
				}
			}()

			v.pushCloseStack("ui_server.dmsg", func() error {
				shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				return dmsgSrv.Shutdown(shutdownCtx)
			})
		}
	}

	v.pushCloseStack("ui_server", func() error {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.WithError(err).Warn("UI server shutdown error")
		}
		tpvizServer.Stop()
		wg.Wait()
		return nil
	})

	return nil
}
