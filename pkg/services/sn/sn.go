// Package sn pkg/services/sn/sn.go
//
// setup-node as a pkg/services.Service. Listens on dmsg port 36 for
// route-setup RPC requests; optionally builds a transport manager
// for the cascade-route-setup path that reaches visors over STCPR.
//
// Block shape supports either inline router.SetupConfig fields or a
// `config_path` indirection (the existing per-service JSON file).
// E2E uses the file path; tighter "all-in-one" deployments can
// embed the config inline.
package sn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/app/appevent"
	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgcurl"
	"github.com/skycoin/skywire/pkg/dmsg/dmsghttp"
	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/metricsutil"
	"github.com/skycoin/skywire/pkg/router"
	"github.com/skycoin/skywire/pkg/router/setupmetrics"
	"github.com/skycoin/skywire/pkg/services"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/transport/network"
	"github.com/skycoin/skywire/pkg/transport/network/addrresolver"
	"github.com/skycoin/skywire/pkg/transport/tpdclient"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

// Type is the registry key used in services.json blocks.
const Type = "setup-node"

func init() {
	services.Register(Type, factory)
}

// Config is the JSON configuration for setup-node. Embeds
// router.SetupConfig so callers can either set those fields inline
// in the block (small all-in-one deployments) or point at a file
// via ConfigPath (production / CI e2e — the file wins when set).
type Config struct {
	router.SetupConfig

	// ConfigPath, when non-empty, is the path to a JSON file
	// containing a router.SetupConfig. Read at Run() time; values
	// from the file overwrite any inline equivalents. Mutually
	// exclusive in spirit (inline config is the alternative).
	ConfigPath string `json:"config_path,omitempty"`

	MetricsAddr string `json:"metrics_addr,omitempty"`
	PProfMode   string `json:"pprof_mode,omitempty"`
	PProfAddr   string `json:"pprof_addr,omitempty"`
	Tag         string `json:"tag,omitempty"`
}

// LoadFile reads a setup-node config file. Returns the inner
// SetupConfig directly because the file-based path doesn't carry
// the supervisor-only fields (MetricsAddr, PProfMode, etc.).
func LoadFile(path string) (*router.SetupConfig, error) {
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("setup-node: read config %q: %w", path, err)
	}
	var sc router.SetupConfig
	if err := json.Unmarshal(data, &sc); err != nil {
		return nil, fmt.Errorf("setup-node: parse config %q: %w", path, err)
	}
	return &sc, nil
}

// ParseBlock decodes a services.json block into a Config.
func ParseBlock(raw []byte) (*Config, error) {
	var c Config
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("setup-node: parse block: %w", err)
	}
	return &c, nil
}

func factory(raw json.RawMessage, log *logging.Logger) (services.Service, error) {
	cfg, err := ParseBlock(raw)
	if err != nil {
		return nil, err
	}
	return New(cfg, log), nil
}

// New builds a Service from an already-parsed Config.
func New(cfg *Config, log *logging.Logger) services.Service {
	return &service{cfg: cfg, log: log}
}

type service struct {
	cfg *Config
	log *logging.Logger
}

func (s *service) Run(ctx context.Context) error {
	cfg := s.cfg
	tag := cfg.Tag
	if tag == "" {
		tag = "setup_node"
	}
	log := logging.MustGetLogger(tag)
	mLog := logging.NewMasterLogger()
	_ = s.log // logger replaced

	if cfg.PProfMode == "http" {
		metricsutil.ServePProf(log, cfg.PProfAddr, "setup-node")
	}

	// Resolve the SetupConfig: file wins over inline.
	conf := &cfg.SetupConfig
	if cfg.ConfigPath != "" {
		fileSetup, err := LoadFile(cfg.ConfigPath)
		if err != nil {
			return err
		}
		conf = fileSetup
	}
	if conf.PK.Null() || conf.SK.Null() {
		return errors.New("setup-node: public_key and secret_key required (inline or via config_path)")
	}

	log.Infof("Config: %#v", conf)
	sn, err := router.NewNode(conf)
	if err != nil {
		return fmt.Errorf("setup-node: create node: %w", err)
	}

	collector := setupmetrics.NewCollector(setupmetrics.CollectorConfig{})
	if cfg.MetricsAddr != "" {
		_ = setupmetrics.NewVictoriaMetrics()
		metricsutil.ServeHTTPMetrics(log, cfg.MetricsAddr)
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Cascade route-setup transport manager: STCPR transports labeled
	// "setup" reach visors directly, enabling route setup without DMSG.
	if conf.Transport != nil && conf.Cascade != nil {
		s.startCascade(runCtx, conf, sn, log, mLog)
	}

	// DMSG HTTP /health and /stats served via the setup-node's own
	// DMSG client (avoids "no associated listener" from a duplicate
	// client with the same PK).
	s.startDMSGHealth(runCtx, conf, sn, collector, log)

	if err := sn.Serve(runCtx, collector); err != nil {
		return fmt.Errorf("setup-node: serve: %w", err)
	}
	return nil
}

func (s *service) startCascade(
	ctx context.Context,
	conf *router.SetupConfig,
	sn *router.Node,
	log *logging.Logger,
	mLog *logging.MasterLogger,
) {
	tpLabel := transport.LabelSetup
	if conf.Transport.DefaultLabel != "" {
		tpLabel = transport.Label(conf.Transport.DefaultLabel)
	}

	snDmsgC := sn.DmsgClient()

	var tpdC transport.DiscoveryClient
	if conf.TransportDiscovery != "" {
		httpC, hcErr := getHTTPClient(snDmsgC, conf.TransportDiscovery)
		if hcErr != nil {
			log.WithError(hcErr).Warn("Failed to build TPD HTTP client — using mock")
			tpdC = transport.NewDiscoveryMock()
		} else {
			var tpdErr error
			tpdC, tpdErr = tpdclient.NewHTTP(
				conf.TransportDiscovery,
				conf.PK, conf.SK,
				httpC, "",
				mLog,
			)
			if tpdErr != nil {
				log.WithError(tpdErr).Warn("Failed to create TPD client — using mock")
				tpdC = transport.NewDiscoveryMock()
			}
		}
	} else {
		tpdC = transport.NewDiscoveryMock()
	}

	tpMConf := transport.ManagerConfig{
		PubKey:           conf.PK,
		SecKey:           conf.SK,
		DiscoveryClient:  tpdC,
		LogStore:         transport.InMemoryTransportLogStore(),
		Version:          buildinfo.Version(),
		ARTransportLimit: -1, // RSN never registers with AR
	}

	var arC addrresolver.APIClient
	if conf.Transport.AddressResolver != "" {
		httpC, hcErr := getHTTPClient(snDmsgC, conf.Transport.AddressResolver)
		if hcErr != nil {
			log.WithError(hcErr).Warn("Failed to build AR HTTP client — AR disabled")
		} else {
			// sn is a server-side proxy / forwarder; it doesn't
			// register itself as a visor at the AR, so the v6 HTTP
			// client is intentionally nil here. The visor-side
			// init_transport supplies httpCV6 when the operator's AR
			// URL is plain HTTP.
			arC, _ = addrresolver.NewHTTP( //nolint:errcheck
				conf.Transport.AddressResolver,
				conf.PK, conf.SK,
				httpC, nil, "",
				log, mLog,
			)
		}
	}

	factory := network.ClientFactory{
		PK:         conf.PK,
		SK:         conf.SK,
		ListenAddr: conf.Transport.STCPRAddr,
		ARClient:   arC,
		EB:         appevent.NewBroadcaster(log, 0),
		MLogger:    mLog,
	}

	tpM, tpErr := transport.NewManager(log, arC, factory.EB, &tpMConf, factory)
	if tpErr != nil {
		log.WithError(tpErr).Warn("Failed to create transport manager — cascade disabled")
		return
	}
	tpM.InitClient(ctx, types.STCPR, 0)
	log.WithField("stcpr_addr", conf.Transport.STCPRAddr).
		WithField("label", tpLabel).
		Info("RSN transport manager started (STCPR)")
	sn.InitCascade(conf, tpM)
	_ = tpLabel
}

func (s *service) startDMSGHealth(
	ctx context.Context,
	conf *router.SetupConfig,
	sn *router.Node,
	collector *setupmetrics.Collector,
	log *logging.Logger,
) {
	startedAt := time.Now()
	dmsgAddr := fmt.Sprintf("%s:%d", conf.PK.Hex(), dmsg.DefaultDmsgHTTPPort)

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := httputil.HealthCheckResponse{
			ServiceName: "setup-node",
			BuildInfo:   buildinfo.Get(),
			StartedAt:   startedAt,
			DmsgAddr:    dmsgAddr,
			DmsgServers: sn.DmsgClient().ConnectedServersPK(),
		}
		json.NewEncoder(w).Encode(resp) //nolint:errcheck,gosec
	})
	mux.HandleFunc("/stats", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		snap := collector.Snapshot()
		// Sanitize: strip src/dst PKs to avoid leaking visor topology.
		for i := range snap.RecentFailures {
			snap.RecentFailures[i].SrcPK = ""
			snap.RecentFailures[i].DstPK = ""
		}
		for i := range snap.TopDestinations {
			snap.TopDestinations[i].PK = ""
		}
		for i := range snap.TopFailedDestinations {
			snap.TopFailedDestinations[i].PK = ""
		}
		json.NewEncoder(w).Encode(snap) //nolint:errcheck,gosec
	})

	snClient := sn.DmsgClient()
	go func() {
		if err := dmsghttp.ListenAndServe(ctx, conf.SK, mux, nil,
			dmsg.DefaultDmsgHTTPPort, snClient, log); err != nil {
			log.WithError(err).Error("DMSG HTTP health server stopped")
		}
	}()
	go func() {
		if err := dmsghttp.ServeDebug(ctx, snClient, log, deployment.Prod.SurveyWhitelist); err != nil {
			log.WithError(err).Error("DMSG HTTP debug server stopped")
		}
	}()
	log.Infof("DMSG HTTP health endpoint available at %s", dmsgAddr)
}

// getHTTPClient returns an *http.Client for the given service URL,
// dispatching on URL scheme: dmsg:// URLs get a client backed by a
// dmsghttp.HTTPTransport routed through snDmsgC, http(s):// URLs
// get a plain http.Client.
func getHTTPClient(snDmsgC *dmsg.Client, service string) (*http.Client, error) {
	if service == "" {
		return nil, fmt.Errorf("service URL is empty")
	}
	var serviceURL dmsgcurl.URL
	if err := serviceURL.Fill(service); err != nil {
		return plainHTTPClient(), nil
	}
	if serviceURL.Scheme != "dmsg" {
		return plainHTTPClient(), nil
	}
	if snDmsgC == nil {
		return nil, fmt.Errorf("dmsg URL %q requires a DMSG client; none available", service)
	}
	return &http.Client{
		Transport: dmsghttp.MakeHTTPTransport(context.Background(), snDmsgC),
	}, nil
}

func plainHTTPClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			DisableKeepAlives: true,
			IdleConnTimeout:   5 * time.Second,
		},
	}
}
