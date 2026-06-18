// Package dmsgsrv pkg/services/dmsgsrv/dmsgsrv.go
//
// dmsg-server as a pkg/services.Service. Wraps the existing
// pkg/dmsg/dmsgserver primitives (Config, ServerAPI) so the
// multi-service supervisor (`skywire svc run`) can host a dmsg-server
// alongside dmsg-discovery and the rest of the deployment services
// in one process.
//
// The standalone `skywire dmsg server start` cobra command keeps
// working — it parses flags + JSON config into a dmsgsrv.Config and
// hands off to Run. The low-level dmsg-server primitives stay in
// pkg/dmsg/dmsgserver; this package is purely the run-loop wrapper.
package dmsgsrv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/rpc"
	"net/url"
	"os"
	"strconv"
	"time"

	chi "github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/direct"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	dmsg "github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg/metrics"
	"github.com/skycoin/skywire/pkg/dmsg/dmsghttp"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgserver"
	"github.com/skycoin/skywire/pkg/geoip"
	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/metricsutil"
	"github.com/skycoin/skywire/pkg/router"
	"github.com/skycoin/skywire/pkg/router/setupmetrics"
	"github.com/skycoin/skywire/pkg/services"
	"github.com/skycoin/skywire/pkg/skyenv"
)

// Type is the registry key used in services.json blocks.
const Type = "dmsg-server"

func init() {
	services.Register(Type, factory)
}

// Config wraps the existing dmsgserver.Config and adds the runtime
// knobs that previously came from cobra flags rather than the JSON
// file (auth passphrase, pprof, metrics). When loaded from a JSON
// services-block, all fields parse from the same flat object.
type Config struct {
	dmsgserver.Config

	// ConfigPath, when non-empty, is the path to an existing
	// dmsg-server JSON config file. Read at Run() time; values
	// from the file overwrite the inline dmsgserver.Config.
	// Lets services.json blocks reuse the same operator-shipped
	// config the standalone `skywire dmsg server start <path>`
	// already uses.
	ConfigPath string `json:"config_path,omitempty"`

	// AuthPassphrase, when non-empty, is the shared secret a
	// dmsg-server presents to the discovery for "official" server
	// registration. Previously a CLI-only flag.
	AuthPassphrase string `json:"auth_passphrase,omitempty"`
	// PProfMode / PProfAddr — pass-through to dmsgcmdutil.InitPProf.
	PProfMode string `json:"pprof_mode,omitempty"`
	PProfAddr string `json:"pprof_addr,omitempty"`
	// MetricsAddr is the address to expose Prometheus metrics on
	// (empty disables metrics).
	MetricsAddr string `json:"metrics_addr,omitempty"`
}

// LoadFile reads a standalone dmsg-server JSON config file (the
// existing `skywire dmsg server start /path/to/file.json` shape).
// Returned for callers (the multi-service factory) that want to
// merge file-based config with their inline block fields.
func LoadFile(path string) (*dmsgserver.Config, error) {
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("dmsg-server: read config %q: %w", path, err)
	}
	var c dmsgserver.Config
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("dmsg-server: parse config %q: %w", path, err)
	}
	return &c, nil
}

// factory parses a services.json block into a Config and returns a
// Service.
func factory(raw json.RawMessage, log *logging.Logger) (services.Service, error) {
	cfg, err := ParseBlock(raw)
	if err != nil {
		return nil, err
	}
	return New(cfg, log), nil
}

// ParseBlock decodes a services.json block into a Config. Tolerant
// of unknown fields (the supervisor's framing keys "type" and "name"
// arrive in the same object).
func ParseBlock(raw []byte) (*Config, error) {
	var c Config
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("dmsg-server: parse block: %w", err)
	}
	return &c, nil
}

// New builds a Service from an already-parsed Config. The cobra
// command in cmd/dmsg/dmsg-server/commands/start uses this path
// after mapping flags + --config file into a Config.
func New(cfg *Config, log *logging.Logger) services.Service {
	return &service{cfg: cfg, log: log}
}

type service struct {
	cfg *Config
	log *logging.Logger
}

// Run is the long-lived run loop. Mirrors the previous
// `skywire dmsg server start` cobra Run callback verbatim — same
// HTTP API listener, same dmsg client + dmsghttp + pprof + optional
// route setup-node — but takes its config from the struct rather
// than package-level variables.
func (s *service) Run(ctx context.Context) error {
	// Resolve the underlying dmsgserver.Config: file path wins
	// over inline embedded config when ConfigPath is set, so the
	// services.json block can either embed the full config inline
	// or point at the existing operator-shipped JSON file.
	if s.cfg.ConfigPath != "" {
		fileConf, err := LoadFile(s.cfg.ConfigPath)
		if err != nil {
			return err
		}
		s.cfg.Config = *fileConf
	}
	cfg := &s.cfg.Config // alias for the resolved dmsgserver.Config
	log := s.log

	if cfg.MaxSessions <= 0 {
		cfg.MaxSessions = dmsg.DefaultMaxSessions
	}
	if cfg.HTTPAddress == "" {
		u, err := url.Parse(cfg.LocalAddress)
		if err != nil {
			return fmt.Errorf("dmsg-server: parse local_address %q: %w", cfg.LocalAddress, err)
		}
		hp, err := strconv.Atoi(u.Port())
		if err != nil {
			return fmt.Errorf("dmsg-server: parse local_address port %q: %w", cfg.LocalAddress, err)
		}
		cfg.HTTPAddress = ":" + strconv.Itoa(hp+1)
	}

	var m metrics.Metrics
	if s.cfg.MetricsAddr == "" {
		m = metrics.NewEmpty()
	} else {
		m = metrics.NewVictoriaMetrics()
	}
	metricsutil.ServeHTTPMetrics(log, s.cfg.MetricsAddr)

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP) //nolint:staticcheck
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	srvAPI := dmsgserver.NewServerAPI(r, log, m)

	// Convert peer config to dmsg.PeerEntry.
	var peers []dmsg.PeerEntry
	for _, p := range cfg.Peers {
		peers = append(peers, dmsg.PeerEntry{PK: p.PubKey, Addr: p.Address})
	}

	srvConf := dmsg.ServerConfig{
		MaxSessions:             cfg.MaxSessions,
		UpdateInterval:          cfg.UpdateInterval,
		AuthPassphrase:          s.cfg.AuthPassphrase,
		Peers:                   peers,
		AnnounceAsPeer:          cfg.AnnounceAsPeer,
		AcceptPeerAnnouncements: cfg.AcceptPeerAnnouncements,
		AcceptedPeerPKs:         cfg.AcceptedPeerPKs,
	}

	deployments := cfg.NormalizedDeployments()
	if len(deployments) == 0 {
		return errors.New("dmsg-server: no dmsg-discoveries configured; set 'discovery' or 'dmsg' in config")
	}

	// Strict dmsg-only: every deployment must carry a discovery_dmsg PK so the
	// server registers + resolves over dmsg with no plain-HTTP egress.
	discPKs := make([]cipher.PubKey, len(deployments))
	for i, d := range deployments {
		pk := dmsgserver.PKFromDmsgURL(d.DiscoveryDmsg)
		if pk == (cipher.PubKey{}) {
			return fmt.Errorf("dmsg-server: deployment %q has no discovery_dmsg; "+
				"strict dmsg-only registration requires it", d.Discovery)
		}
		discPKs[i] = pk
	}

	// Build the transit dmsg client BEFORE the server so even the first
	// (seq-0) registration goes over dmsg, never plain HTTP.
	dmsgC, dClient, closeDmsg, err := s.buildTransitDmsg(ctx, deployments, discPKs)
	if err != nil {
		return fmt.Errorf("dmsg-server: build transit dmsg client: %w", err)
	}
	defer closeDmsg()

	srv := dmsg.NewServer(cfg.PubKey, cfg.SecKey, newDmsgOnly(dmsgC, discPKs[0], log), &srvConf, m)
	srv.SetLogger(log)

	if geoDB, err := geoip.OpenEmbedded(); err != nil {
		log.WithError(err).Warn("failed to open embedded geoip DB; LookupIPGeo will return empty geo fields")
	} else {
		srv.SetGeoLookup(func(ip net.IP) (country, region string, lat, lon float64) {
			res, err := geoip.Lookup(geoDB, ip.String())
			if err != nil || res == nil {
				return "", "", 0, 0
			}
			if res.Latitude != nil {
				lat = *res.Latitude
			}
			if res.Longitude != nil {
				lon = *res.Longitude
			}
			return res.CountryCode, res.RegionCode, lat, lon
		})
	}

	for i := 1; i < len(deployments); i++ {
		extra := deployments[i]
		srv.AddDiscoveryDualStack(
			newDmsgOnly(dmsgC, discPKs[i], log),
			extra.AdvertisedAddress,
			extra.AdvertisedAddressV6,
			discPKs[i],
		)
	}

	// Primary deployment's v6 endpoint goes through SetAdvertisedAddrV6
	// since the existing ListenAndServe contract carries only the v4
	// pAddr. Empty primaryAdvertisedV6 keeps the pre-#1525 behavior
	// (server registers AddressV6="" → discovery omits the field).
	primaryAdvertisedV6 := deployments[0].AdvertisedAddressV6
	if primaryAdvertisedV6 == "" {
		primaryAdvertisedV6 = cfg.PublicAddressV6
	}
	srv.SetAdvertisedAddrV6(primaryAdvertisedV6)

	srvAPI.SetDmsgServer(srv)

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer func() {
		if err := srvAPI.Close(); err != nil {
			log.WithError(err).Info("Closed server.")
		} else {
			log.Info("Closed server.")
		}
	}()

	primaryAdvertised := deployments[0].AdvertisedAddress
	if primaryAdvertised == "" {
		primaryAdvertised = cfg.PublicAddress
	}

	go srvAPI.RunBackgroundTasks(runCtx)
	log.WithField("addr", cfg.HTTPAddress).Info("Serving server API...")
	go func() {
		if err := srvAPI.ListenAndServe(cfg.LocalAddress, primaryAdvertised, cfg.HTTPAddress); err != nil {
			log.Errorf("Serve: %v", err)
			cancel()
		}
	}()

	go s.serveDmsgSurfaces(runCtx, cancel, dmsgC, dClient)

	<-runCtx.Done()
	return nil
}

// serveDmsgSurfaces stands up the DMSG-side health/pprof endpoints on the
// already-built transit dmsg client and conditionally hosts the integrated
// route setup-node. The discovery clients are already dmsg-only (built in Run
// before the server starts), so there is no discovery upgrade here.
func (s *service) serveDmsgSurfaces(
	ctx context.Context,
	cancel context.CancelFunc,
	dmsgC *dmsg.Client,
	dClient disc.APIClient,
) {
	cfg := &s.cfg.Config
	log := s.log

	startedAt := time.Now()
	dmsgAddr := fmt.Sprintf("%s:%d", cfg.PubKey.Hex(), dmsg.DefaultDmsgHTTPPort)
	healthMux := http.NewServeMux()
	healthMux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var peerPKs []string
		for _, p := range cfg.Peers {
			peerPKs = append(peerPKs, p.PubKey.Hex())
		}
		resp := httputil.HealthCheckResponse{
			ServiceName:   "dmsg-server",
			BuildInfo:     buildinfo.Get(),
			StartedAt:     startedAt,
			DmsgAddr:      dmsgAddr,
			DmsgDiscovery: cfg.Discovery,
			PeerServers:   peerPKs,
		}
		json.NewEncoder(w).Encode(resp) //nolint:errcheck,gosec
	})

	// Fold pprof + /debug/log onto the main dmsg :80 (survey-gated) instead of a
	// separate :81 listener. The ring buffer captures recent global-logger output.
	rb := logging.NewRingBuffer(0)
	logging.AddHook(logging.NewWriteHook(rb))
	handler := dmsghttp.WithDebug(healthMux, deployment.Prod.SurveyWhitelist, rb.Bytes)
	go func() {
		if err := dmsghttp.ListenAndServe(ctx, cfg.SecKey, handler, dClient,
			dmsg.DefaultDmsgHTTPPort, dmsgC, log); err != nil {
			log.WithError(err).Error("DMSG HTTP server stopped")
		}
	}()
	log.Infof("DMSG HTTP (health/debug) available at %s", dmsgAddr)

	if cfg.EnableRouteSetup {
		go s.serveRouteSetup(ctx, dmsgC)
	} else {
		log.Info("Route setup-node disabled (enable with enable_route_setup in config)")
	}

	<-ctx.Done()
	_ = cancel
}

// serveRouteSetup hosts the integrated route setup-node on the
// dmsg setup port. Each accepted stream gets its own RPC server
// dispatched to the router.SetupRPCGateway.
func (s *service) serveRouteSetup(ctx context.Context, dmsgC *dmsg.Client) {
	snLog := logging.MustGetLogger("dmsg-server:setup-node")
	snLog.Info("Starting integrated route setup-node")

	lis, lisErr := dmsgC.Listen(skyenv.DmsgSetupPort)
	if lisErr != nil {
		snLog.WithError(lisErr).Error("Failed to listen on setup port")
		return
	}
	defer lis.Close() //nolint:errcheck

	snLog.WithField("dmsg_port", skyenv.DmsgSetupPort).Info("Route setup-node listening")

	const maxConcurrent = 20
	sem := make(chan struct{}, maxConcurrent)
	setupMetrics := setupmetrics.NewEmpty()

	for {
		conn, accErr := lis.AcceptStream()
		if accErr != nil {
			if ctx.Err() != nil || errors.Is(accErr, dmsg.ErrEntityClosed) {
				snLog.Debug("Route setup-node listener stopped")
				return
			}
			snLog.WithError(accErr).Warn("Failed to accept setup stream")
			continue
		}

		reqPK := conn.RemoteAddr().(dmsg.Addr).PK
		snLog.WithField("remote_pk", reqPK).Debug("Accepted route setup request")

		gw := &router.SetupRPCGateway{
			Metrics: setupMetrics,
			Ctx:     ctx,
			Conn:    conn,
			ReqPK:   reqPK,
			Dialer:  router.WrapDmsgClient(dmsgC),
			Timeout: 2 * time.Minute,
		}

		rpcS := rpc.NewServer()
		if regErr := rpcS.Register(gw); regErr != nil {
			snLog.WithError(regErr).Error("Failed to register setup RPC")
			conn.Close() //nolint:errcheck,gosec
			continue
		}

		go func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			rpcS.ServeConn(conn)
		}()
	}
}

// newDmsgOnly returns a disc.APIClient that registers + resolves the discovery
// STRICTLY over dmsg, with NO plain-HTTP fallback. dmsgC must be able to
// resolve discPK over dmsg (a direct.Client synthetic entry delegating to the
// transit servers). Mirrors dmsgfirst's primary leg without the fallback
// wrapper, so there is no plain-HTTP egress at all.
func newDmsgOnly(dmsgC *dmsg.Client, discPK cipher.PubKey, log *logging.Logger) disc.APIClient {
	dmsgURL := fmt.Sprintf("http://%s:%d", discPK.Hex(), dmsg.DefaultDmsgHTTPPort)
	tr := dmsghttp.MakeHTTPTransport(context.Background(), dmsgC)
	return disc.NewHTTP(dmsgURL, &http.Client{Transport: tr}, log)
}

// buildTransitDmsg stands up the server's outbound/transit dmsg client before
// the server itself, so the very first (seq-0) registration can ride dmsg. The
// direct client resolves the transit servers (self + per-deployment +
// embedded) AND every discovery PK locally — the discovery PKs as synthetic
// client entries delegating to all transit servers — so the client connects to
// the seed servers directly (no discovery lookup) and can dial each discovery
// through a peer relay. No discovery HTTP is contacted. Returns the dmsg
// client, the direct disc client (reused by the DMSG health/pprof listener),
// and a close func.
func (s *service) buildTransitDmsg(ctx context.Context, deployments []dmsgserver.Deployment, discPKs []cipher.PubKey) (*dmsg.Client, disc.APIClient, func(), error) {
	cfg := &s.cfg.Config
	log := s.log

	serverEntry := &disc.Entry{
		Version: "0.0.1",
		Static:  cfg.PubKey,
		Server: &disc.Server{
			Address:           cfg.PublicAddress,
			AvailableSessions: cfg.MaxSessions,
		},
	}
	servers := []*disc.Entry{serverEntry}
	seenPK := map[cipher.PubKey]struct{}{cfg.PubKey: {}}
	addServer := func(e *disc.Entry) {
		if e == nil {
			return
		}
		if _, ok := seenPK[e.Static]; ok {
			return
		}
		seenPK[e.Static] = struct{}{}
		servers = append(servers, e)
	}
	for _, d := range deployments {
		for _, e := range d.Servers {
			addServer(e)
		}
	}
	for i := range dmsg.Prod.DmsgServers {
		addServer(&dmsg.Prod.DmsgServers[i])
	}

	// Resolve self (transit identity) + every discovery PK locally. Giving
	// GetAllEntries the discovery PKs creates a synthetic client entry per
	// discovery delegating to all transit servers, so DialStream(discPK)
	// resolves without a discovery lookup and rides a peer relay.
	keys := append(cipher.PubKeys{cfg.PubKey}, discPKs...)
	dClient := direct.NewClient(direct.GetAllEntries(keys, servers), log)

	conf := &dmsg.Config{MinSessions: 0}
	// Start the transit client WITHOUT blocking on Ready(). A dmsg-server's
	// INBOUND accept loop (bound after this returns) must come up regardless
	// of whether the server's own OUTBOUND transit client has reached a peer
	// yet. In a fleet-wide cold-start every server's transit client dials
	// every other server — all down — so Ready() never fires; blocking here
	// (the old direct.StartDmsg path) gated the listener and produced a
	// CIRCULAR BOOTSTRAP TRAP where no server can be the first to listen, so
	// the whole fleet stays down even though each binary is healthy. Serving
	// in the background lets THIS server accept inbound sessions immediately;
	// its own discovery registration completes the moment any peer (or an
	// inbound visor providing a relay path) appears, which cascades the fleet
	// back up. Mirrors direct.StartDmsg minus the <-Ready() wait.
	dmsgC := dmsg.NewClient(cfg.PubKey, cfg.SecKey, dClient, conf)
	dmsgC.SetLogger(log)
	go dmsgC.Serve(ctx)
	closeFn := func() {
		if cerr := dmsgC.Close(); cerr != nil {
			log.WithError(cerr).Debug("transit dmsg client close")
		}
	}
	return dmsgC, dClient, closeFn, nil
}
