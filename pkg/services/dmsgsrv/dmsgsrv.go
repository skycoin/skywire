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
	"github.com/skycoin/skywire/pkg/dmsg/disc/dmsgfirst"
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
		MaxSessions:    cfg.MaxSessions,
		UpdateInterval: cfg.UpdateInterval,
		AuthPassphrase: s.cfg.AuthPassphrase,
		Peers:          peers,
	}

	deployments := cfg.NormalizedDeployments()
	if len(deployments) == 0 {
		return errors.New("dmsg-server: no dmsg-discoveries configured; set 'discovery' or 'dmsg' in config")
	}

	primaryHTTP := disc.NewHTTP(deployments[0].Discovery, &http.Client{}, log)
	srv := dmsg.NewServer(cfg.PubKey, cfg.SecKey, primaryHTTP, &srvConf, m)
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
			disc.NewHTTP(extra.Discovery, &http.Client{}, log),
			extra.AdvertisedAddress,
			extra.AdvertisedAddressV6,
			dmsgserver.PKFromDmsgURL(extra.DiscoveryDmsg),
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

	go s.serveDmsgSurfaces(runCtx, cancel, srv, deployments)

	<-runCtx.Done()
	return nil
}

// serveDmsgSurfaces blocks until the server's outbound dmsg client
// is ready, then upgrades each configured discovery to dmsgfirst,
// stands up the DMSG-side health/pprof endpoints, and conditionally
// hosts the integrated route setup-node.
func (s *service) serveDmsgSurfaces(
	ctx context.Context,
	cancel context.CancelFunc,
	srv *dmsg.Server,
	deployments []dmsgserver.Deployment,
) {
	cfg := &s.cfg.Config
	log := s.log

	select {
	case <-srv.Ready():
	case <-ctx.Done():
		return
	}

	serverEntry := &disc.Entry{
		Version: "0.0.1",
		Static:  cfg.PubKey,
		Server: &disc.Server{
			Address:           cfg.PublicAddress,
			AvailableSessions: cfg.MaxSessions,
		},
	}

	// Build the transit set: union of (a) per-discovery servers
	// from the config, (b) embedded deployment keyring, minus self.
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
	entries := direct.GetAllEntries(cipher.PubKeys{cfg.PubKey}, servers)
	dClient := direct.NewClient(entries, log)

	debugConfig := &dmsg.Config{MinSessions: 0}
	dmsgC, closeDebug, err := direct.StartDmsg(ctx, log, cfg.PubKey, cfg.SecKey, dClient, debugConfig)
	if err != nil {
		log.WithError(err).Error("failed to start dmsg client for server services")
		return
	}
	defer closeDebug()

	// Upgrade each configured discovery client from plain HTTP to
	// dmsgfirst when DiscoveryDmsg is set; otherwise stay on HTTP.
	upgraded := make([]disc.APIClient, len(deployments))
	for i, d := range deployments {
		pk := dmsgserver.PKFromDmsgURL(d.DiscoveryDmsg)
		if pk == (cipher.PubKey{}) {
			upgraded[i] = disc.NewHTTP(d.Discovery, &http.Client{}, log)
			log.WithField("url", d.Discovery).Debug("discovery_dmsg unset; staying on plain HTTP")
			continue
		}
		upgraded[i] = dmsgfirst.New(dmsgC, pk, d.Discovery, &http.Client{}, log)
		log.WithField("url", d.Discovery).WithField("pk", pk).Info("discovery upgraded to dmsg-first registration")
	}
	srv.SetDiscoveryClients(upgraded)

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

	go func() {
		if err := dmsghttp.ListenAndServe(ctx, cfg.SecKey, healthMux, dClient,
			dmsg.DefaultDmsgHTTPPort, dmsgC, log); err != nil {
			log.WithError(err).Error("DMSG HTTP health server stopped")
		}
	}()
	log.Infof("DMSG HTTP health endpoint available at %s", dmsgAddr)

	go func() {
		if debugErr := dmsghttp.ServeDebug(ctx, dmsgC, log, deployment.Prod.SurveyWhitelist); debugErr != nil {
			log.Errorf("dmsghttp.ServeDebug: %v", debugErr)
		}
	}()

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
