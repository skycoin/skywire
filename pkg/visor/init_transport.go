// init_transport.go contains transport initialization logic.
package visor

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/ccding/go-stun/stun"
	"github.com/google/uuid"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/app/appdisc"
	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/cipher"
	dmsgdisc "github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/netutil"
	"github.com/skycoin/skywire/pkg/servicedisc"
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

	// Try DMSG-HTTP first for address resolver if configured
	arURL := conf.AddressResolver
	if conf.AddressResolverDmsg != "" && v.dmsgC != nil {
		if dmsgC, err := getHTTPClient(ctx, v, conf.AddressResolverDmsg); err == nil {
			arURL = conf.AddressResolverDmsg
			_ = dmsgC // URL is enough, getHTTPClient sets up DMSG routing
			log.Info("Using DMSG-HTTP for address resolver")
		} else if arURL != "" {
			log.WithError(err).Warn("DMSG-HTTP address resolver failed, using plain HTTP")
		} else {
			return fmt.Errorf("address resolver: DMSG-only but unreachable: %w", err)
		}
	} else if arURL == "" && conf.AddressResolverDmsg != "" {
		arURL = conf.AddressResolverDmsg
	}

	httpC, err := getHTTPClient(ctx, v, arURL)
	if err != nil {
		return err
	}

	// Get public IP (and ideally geolocation) from a connected
	// dmsg-server. LookupIPGeo asks the server to fold in the geoip
	// data using its own embedded MaxMind DB so the visor doesn't
	// need an HTTP round-trip to the geoip service. Older
	// dmsg-servers without the geo lookup hook return GeoCountry=""
	// — we fall back to the visor's own embedded LookupGeo in that
	// case. STUN is the last-resort path when dmsg can't answer.
	var pIP string
	var serverGeo *GeoData

	lookupCtx, lookupCancel := context.WithTimeout(ctx, 10*time.Second)
	resp, err := v.dmsgC.LookupIPGeo(lookupCtx, nil)
	lookupCancel()
	if err != nil {
		log.WithError(err).Debug("Failed to get public IP+geo from dmsg server, trying STUN")
		<-v.stun.ready
		if v.stun.client.PublicIP != nil {
			pIP = v.stun.client.PublicIP.IP()
			log.WithField("public_ip", pIP).Debug("Got public IP from STUN")
		} else {
			log.Warn("Failed to determine public IP from dmsg and STUN")
		}
	} else {
		pIP = resp.IP.String()
		log.WithField("public_ip", pIP).Debug("Got public IP from dmsg server")
		if resp.GeoCountry != "" {
			serverGeo = &GeoData{
				CountryCode: resp.GeoCountry,
				RegionCode:  resp.GeoRegion,
				Latitude:    resp.GeoLat,
				Longitude:   resp.GeoLon,
			}
			log.WithField("country", serverGeo.CountryCode).Debug("Got geolocation from dmsg server")
		}
	}

	geoData := serverGeo
	if geoData == nil {
		if local := LookupGeo(pIP); local != nil {
			log.WithField("country", local.CountryCode).Debug("Got geolocation from embedded GeoIP database (fallback)")
			geoData = local
		}
	}
	if geoData != nil {
		v.geo.mu.Lock()
		v.geo.data = geoData
		v.geo.mu.Unlock()
	}

	// Check AR transport limit — if negative, never register with AR.
	if v.conf.Transport != nil && v.conf.Transport.ARTransportLimit < 0 {
		log.Info("AR registration disabled (ar_transport_limit < 0)")
		if v.conf.Transport.PublicAutoconnect {
			log.Warn("ar_transport_limit < 0 conflicts with public_autoconnect — public visors must be discoverable")
		}
	}

	arClient, err := addrresolver.NewHTTP(arURL, v.conf.PK, v.conf.SK, httpC, pIP, log, v.MasterLogger())
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
		factory.Geo = v.serviceGeo()

		// Get public IP for service discovery (needed for NAT setups).
		// Try dmsg first; fall back to STUN. No HTTP geoip query.
		logger := factory.Log
		var pIP string
		lookupCtx, lookupCancel := context.WithTimeout(ctx, 10*time.Second)
		ipAddr, err := v.dmsgC.LookupIP(lookupCtx, nil)
		lookupCancel()
		if err != nil {
			logger.WithError(err).Debug("Failed to get public IP from dmsg server, trying STUN")
			<-v.stun.ready
			if v.stun.client.PublicIP != nil {
				pIP = v.stun.client.PublicIP.IP()
				logger.WithField("public_ip", pIP).Debug("Got public IP from STUN for service discovery")
			} else {
				logger.Warn("Failed to determine public IP for service discovery from dmsg and STUN")
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

// stunRetryInterval is how often to retry STUN detection after the initial
// probe failed with a transient error (NATError / NATUnknown / NATBlocked).
// These results usually mean STUN servers were unreachable at the moment of
// the probe — DNS hiccup, transient UDP egress drop, packet loss spike, or
// the configured STUN servers being briefly down. A periodic retry recovers
// SUDPH for visors whose network became healthy after boot, instead of
// requiring a full visor restart.
const stunRetryInterval = 5 * time.Minute

// stunIsTransientFail reports whether a NAT type indicates a probe-side
// failure (worth retrying) rather than a genuine NAT topology that blocks
// SUDPH (NATSymmetric, NATSymmetricUDPFirewall — those don't retry).
func stunIsTransientFail(t stun.NATType) bool {
	switch t {
	case stun.NATError, stun.NATUnknown, stun.NATBlocked:
		return true
	default:
		return false
	}
}

func initStunClient(_ context.Context, v *Visor, log *logging.Logger) error {

	sc := network.GetStunDetails(v.conf.StunServers, log)
	v.initLock.Lock()
	v.stun.client = sc
	v.initLock.Unlock()
	v.stun.readyOnce.Do(func() { close(v.stun.ready) })

	// If the initial probe failed transiently, schedule a background retry.
	// On first successful probe the goroutine updates v.stun.client and
	// lazily starts the SUDPH client (which initSudphClient skipped at boot).
	if sc != nil && stunIsTransientFail(sc.NATType) {
		go v.retryStunOnTransientFail(log)
	}

	return nil
}

// retryStunOnTransientFail periodically re-runs STUN detection until either
// the visor shuts down or a probe returns a usable NAT type. On success it
// installs the new StunDetails and starts the SUDPH transport client. Idle
// when the initial probe was already successful.
func (v *Visor) retryStunOnTransientFail(log *logging.Logger) {
	ctx := v.ctx
	if ctx == nil {
		return
	}

	ticker := time.NewTicker(stunRetryInterval)
	defer ticker.Stop()

	log.Infof("STUN: scheduling background retry every %v after transient failure", stunRetryInterval)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		sc := network.GetStunDetails(v.conf.StunServers, log)
		if sc == nil || stunIsTransientFail(sc.NATType) {
			continue
		}

		// Install the new result regardless of whether SUDPH ends up usable
		// (NATSymmetric still gets recorded for telemetry / diagnostics).
		v.initLock.Lock()
		v.stun.client = sc
		v.initLock.Unlock()
		log.Infof("STUN: detection succeeded on retry, NAT type: %v, public IP: %v", sc.NATType.String(), sc.PublicIP.String())

		// Symmetric NAT means STUN worked but SUDPH won't — stop retrying.
		if sc.NATType == stun.NATSymmetric || sc.NATType == stun.NATSymmetricUDPFirewall {
			log.Warnf("SUDPH transport wont be available as visor is under %v", sc.NATType.String())
			return
		}

		// Lazy-start SUDPH now that STUN has classified a usable NAT.
		// Mirrors the default branch of initSudphClient. tpM.InitClient is
		// safe to call post-init: it locks, registers the client, runs it.
		log.Info("STUN: lazy-starting SUDPH transport client now that STUN succeeded")
		v.tpM.InitClient(ctx, types.SUDPH, v.conf.Transport.SudphPort)
		return
	}
}

func initSudphClient(ctx context.Context, v *Visor, log *logging.Logger) error {
	// Previously this short-circuited when AR was reached via dmsg
	// because the dmsg URL host is a PK with no IP for SUDPH. The AR
	// client now learns AR's public UDP address from /health
	// (udp_address); when that field is missing, BindSUDPH returns a
	// clear error and SUDPH simply stays unavailable for this AR. No
	// reason to hard-skip up front.
	if v.stun.client != nil {
		switch v.stun.client.NATType {
		case stun.NATSymmetric, stun.NATSymmetricUDPFirewall:
			log.Warnf("SUDPH transport wont be available as visor is under %v", v.stun.client.NATType.String())
		case stun.NATError, stun.NATUnknown, stun.NATBlocked:
			// Initial STUN probe failed transiently — initStunClient has
			// scheduled a background retry that will lazy-start SUDPH if
			// detection later succeeds.
			log.Warnf("SUDPH transport wont be available yet: STUN detection failed (%v); will retry in background", v.stun.client.NATType.String())
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

	// The on-disk CSV log store has been retired — historical
	// bandwidth/latency now lives in the bbolt-backed stats store
	// (pkg/visor/stats), reachable via /stats/transports/history and
	// the GetTransportLogs RPC. This in-memory store only exists so
	// a transport that closes and re-opens within the same visor
	// session preserves its cumulative byte counters across the gap.
	// LogStore.Type / LogStore.Location in config are no-ops; warn
	// if a config still requests file-mode so operators notice.
	if v.conf.Transport.LogStore.Type == visorconfig.FileLogStore {
		log.Warn("transport.log_store.type=\"file\" is deprecated; the on-disk CSV log store has been retired. " +
			"Historical bandwidth is now served from the bbolt stats store; this setting is now treated as \"memory\".")
	}
	logS := transport.InMemoryTransportLogStore()

	pTps, err := v.conf.GetPersistentTransports()
	if err != nil {
		err := fmt.Errorf("failed to get persistent transports: %w", err)
		return err
	}

	var arLimit int
	if v.conf.Transport != nil {
		arLimit = v.conf.Transport.ARTransportLimit
	}
	tpMConf := transport.ManagerConfig{
		PubKey:                    v.conf.PK,
		SecKey:                    v.conf.SK,
		DiscoveryClient:           tpdC,
		LogStore:                  logS,
		PersistentTransportsCache: pTps,
		Version:                   buildinfo.Version(),
		ARTransportLimit:          arLimit,
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
			v.publicVisor.mu.Lock()
			updater := v.publicVisor.updater
			v.publicVisor.mu.Unlock()
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
	ctx, cancel := context.WithCancel(context.Background()) //nolint:gosec // cancel is called in pushCloseStack
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

	// AR self-refresh + addr:* publisher. Polls AR for our own STCPR /
	// SUDPH bind state, caches it (so ARSelfInfo answers the CLI from
	// memory), and dual-publishes to the DHT under addr:stcpr / addr:sudph
	// alongside AR's own RedisMirror. Started here rather than from
	// initDHT because the cache is useful even when DHT is disabled —
	// CLI display works in both configurations, the DHT publish branch
	// in the loop simply no-ops when v.dhtNode is nil.
	go v.arSelfRefreshLoop(ctx, log)

	return nil
}

func initTransportSetup(ctx context.Context, v *Visor, log *logging.Logger) error {
	ctx, cancel := context.WithCancel(ctx) //nolint:gosec // cancel is called in pushCloseStack
	// To remove the block set by NewTransportListener if dmsg is not initialized
	go func() {
		ts, err := ts.NewTransportListener(ctx, v.conf.PK, v.conf.EffectiveTransportSetupPKs(), v.dmsgC, v.tpM, v.MasterLogger())
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

	// Resolve discovery URL: prefer HTTP, fall back to dmsghttp.
	tpsDiscURL := v.conf.Dmsg.Discovery
	if tpsDiscURL == "" && v.conf.Dmsg.DiscoveryDmsg != "" {
		tpsDiscURL = v.conf.Dmsg.DiscoveryDmsg
	}
	tpsHTTP, err := getHTTPClient(ctx, v, tpsDiscURL)
	if err != nil {
		return fmt.Errorf("embedded TPS: %w", err)
	}
	tpsDisc := dmsgdisc.NewHTTP(tpsDiscURL, tpsHTTP, v.MasterLogger().PackageLogger("embedded_tps:disc"))
	tpsDmsgC := dmsg.NewClient(tpsPK, *tpsSK, tpsDisc, dmsgConf)
	tpsDmsgC.SetLogger(v.MasterLogger().PackageLogger("embedded_tps:dmsg"))

	go tpsDmsgC.Serve(ctx)

	select {
	case <-tpsDmsgC.Ready():
		log.Info("Embedded TPS dmsg client connected")
	case <-ctx.Done():
		return fmt.Errorf("context canceled waiting for TPS dmsg client")
	}
	// Seed the deployment service PKs into tpsDmsgC's entry cache
	// BEFORE swapping in dmsgfirst — same reason as the main dmsgC
	// path in initDmsg. Without the seed, dmsgfirst's primary
	// DialStream(dmsgdiscPK) cache-misses, recurses through the
	// disc client, and overflows the goroutine stack.
	v.seedDmsgServiceEntries(tpsDmsgC, log)
	// Same dmsgfirst upgrade as the main dmsgC: avoids pinning the
	// embedded TPS's discovery refresh to plain HTTP for the process
	// lifetime when initDmsgHTTP's dmsgDC isn't ready at construction.
	upgradeDmsgDiscToDmsgfirst(tpsDmsgC, v.conf.Dmsg, log)

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
	v.autoconnect.mu.Lock()
	defer v.autoconnect.mu.Unlock()

	if v.autoconnect.running {
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
		Geo:           v.serviceGeo(),
	}
	// only needed for dmsghttp
	pIP, err := getPublicIP(v, serviceDisc)
	if err != nil {
		return err
	}
	connector := MakeConnector(conf, 3, v.tpM, v.dmsgC, v.dhtNode, v.serviceDisc.Client, pIP, log, v.MasterLogger())

	cctx, cancel := context.WithCancel(ctx) //nolint:gosec // cancel stored in v.autoconnect.cancel, called in pushCloseStack
	v.autoconnect.cancel = cancel
	v.autoconnect.running = true

	v.pushCloseStack("public_autoconnect", func() error {
		v.autoconnect.mu.Lock()
		defer v.autoconnect.mu.Unlock()
		if v.autoconnect.cancel != nil {
			v.autoconnect.cancel()
			v.autoconnect.cancel = nil
		}
		v.autoconnect.running = false
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
	v.autoconnect.mu.Lock()
	defer v.autoconnect.mu.Unlock()

	if !v.autoconnect.running {
		return nil // not running
	}

	if v.autoconnect.cancel != nil {
		v.autoconnect.cancel()
		v.autoconnect.cancel = nil
	}
	v.autoconnect.running = false
	return nil
}

// IsPublicAutoconnectRunning returns whether public autoconnect is running.
func (v *Visor) IsPublicAutoconnectRunning() bool {
	v.autoconnect.mu.Lock()
	defer v.autoconnect.mu.Unlock()
	return v.autoconnect.running
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
	v.publicVisor.mu.Lock()
	v.publicVisor.updater = publicUpdater
	v.publicVisor.mu.Unlock()

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
			v.isTransportabilityHealthy.set()
			if tries > 0 {
				log.Info("Visor is now transportable (recovered)")
			} else {
				log.Debug("Visor transportability check passed")
			}
			tries = 0
			ticker.Reset(tickDuration)
		} else {
			v.isServicesHealthy.unset()
			v.isTransportabilityHealthy.unset()
			tries++
			log.WithField("tries", tries).WithField("dmsg_ok", dmsgOK).
				Warn("Visor transportability check failed")
			ticker.Reset(time.Minute)
		}

		if tries >= 3 {
			log.Warn("Visor is not transportable after 3 failed attempts — will keep retrying")
			// Reset counter and keep trying. The visor should not shut down
			// because of transient DMSG issues — many have been fixed and the
			// self-transport test is not a reliable indicator of visor health.
			tries = 0
			ticker.Reset(5 * time.Minute) // Back off to 5 minutes
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

// nolint: gocyclo
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

func connectToTpDisc(ctx context.Context, v *Visor, log *logging.Logger) (transport.DiscoveryClient, error) {
	const (
		initBO = 1 * time.Second
		maxBO  = 10 * time.Second
		tries  = 0
		factor = 1
	)

	conf := v.conf.Transport

	pIP, err := getPublicIP(v, conf.AddressResolver)
	if err != nil {
		return nil, err
	}

	// Try DMSG-HTTP first if configured, fall back to plain HTTP
	if conf.DiscoveryDmsg != "" && v.dmsgC != nil {
		dmsgHTTPC, err := getHTTPClient(ctx, v, conf.DiscoveryDmsg)
		if err != nil {
			log.WithError(err).Warn("Failed to get DMSG-HTTP client for TPD, trying plain HTTP")
		} else {
			tpdC, err := tpdclient.NewHTTP(conf.DiscoveryDmsg, v.conf.PK, v.conf.SK, dmsgHTTPC, pIP, v.MasterLogger())
			if err == nil {
				log.Info("Connected to transport discovery via DMSG-HTTP")
				return tpdC, nil
			}
			log.WithError(err).Warn("DMSG-HTTP transport discovery failed, falling back to plain HTTP")
		}
	}

	// Plain HTTP (primary if no DMSG config, fallback if DMSG failed)
	tpdURL := conf.Discovery
	if tpdURL == "" && conf.DiscoveryDmsg != "" {
		tpdURL = conf.DiscoveryDmsg // DMSG-only deployment
	}
	httpC, err := getHTTPClient(ctx, v, tpdURL)
	if err != nil {
		return nil, err
	}

	tpdCRetrier := netutil.NewRetrier(log, initBO, maxBO, tries, factor)

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
