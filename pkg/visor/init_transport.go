// init_transport.go contains transport initialization logic.
package visor

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/ccding/go-stun/stun"
	"github.com/google/uuid"
	dmsgdisc "github.com/skycoin/dmsg/pkg/disc"
	"github.com/skycoin/dmsg/pkg/dmsg"
	"github.com/skycoin/dmsg/pkg/dmsgcurl"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/app/appdisc"
	"github.com/skycoin/skywire/pkg/cxo/subscriber"
	"github.com/skycoin/skywire/pkg/servicedisc"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/httputil"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/netutil"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/transport/network"
	"github.com/skycoin/skywire/pkg/transport/network/addrresolver"
	"github.com/skycoin/skywire/pkg/transport/network/stcp"
	ts "github.com/skycoin/skywire/pkg/transport/setup"
	"github.com/skycoin/skywire/pkg/transport/tpdclient"
	types "github.com/skycoin/skywire/pkg/transport/types"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

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
		case stun.NATError, stun.NATUnknown, stun.NATBlocked:
			log.Warnf("SUDPH transport wont be available: STUN detection failed (%v)", v.stunClient.NATType.String())
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

	// Wrap TPD client with CXO subscriber if DMSG is available and a CXO feed PK is configured
	if v.conf.Transport.CXOFeedPK != "" && v.dmsgC != nil {
		tpdC = wrapTPDWithCXO(ctx, v, tpdC, log)
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
	v.stcpTable = table
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

	// Remove stale entries from TPD using batch delete
	if len(staleIDs) > 0 {
		log.Infof("Removing %d stale transports from TPD (batch)", len(staleIDs))

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

		var deleted int
		var deleteErr error
		for retries := 0; retries < 3; retries++ {
			deleted, deleteErr = tpdC.DeleteTransports(ctx, staleIDs)
			if deleteErr == nil {
				log.Infof("Removed %d/%d stale transports from TPD", deleted, len(staleIDs))
				break
			}
			if retries < 2 {
				backoff := time.Duration(retries+1) * 2 * time.Second
				log.WithError(deleteErr).Warnf("Batch delete failed (attempt %d/3), retrying in %v", retries+1, backoff)
				time.Sleep(backoff)
			}
		}
		if deleteErr != nil {
			log.WithError(deleteErr).Warnf("Failed to remove stale transports after retries")
		}
	}

	return nil
}

// wrapTPDWithCXO creates a CXO subscriber for the TPD feed and wraps the
// HTTP client with a CXO-aware client that checks the local cache first.
func wrapTPDWithCXO(ctx context.Context, v *Visor, httpClient transport.DiscoveryClient, log *logging.Logger) transport.DiscoveryClient {
	var feedPK cipher.PubKey
	if err := feedPK.Set(v.conf.Transport.CXOFeedPK); err != nil {
		log.WithError(err).Warn("Invalid CXO feed PK, continuing without CXO")
		return httpClient
	}

	subConf := subscriber.DefaultConfig()
	subConf.Logger = v.MasterLogger().PackageLogger("cxo-tpd-sub")

	sub, err := subscriber.New(v.dmsgC, feedPK, subConf)
	if err != nil {
		log.WithError(err).Warn("Failed to create CXO subscriber, continuing without CXO")
		return httpClient
	}

	// Connect to the TPD's CXO feed over DMSG
	if err := sub.Connect(feedPK); err != nil {
		log.WithError(err).Warn("Failed to connect to TPD CXO feed, continuing without CXO")
		sub.Close() //nolint:errcheck
		return httpClient
	}

	log.Infof("Connected to TPD CXO feed: %s", feedPK)

	// Close subscriber when context is done
	go func() {
		<-ctx.Done()
		sub.Close() //nolint:errcheck
	}()

	return tpdclient.NewCXOClient(httpClient, sub, v.MasterLogger().PackageLogger("tpd-cxo"))
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
