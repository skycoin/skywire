// Package rf pkg/services/rf/rf.go c2-vis-appsvc
//
// route-finder as a pkg/services.Service.
package rf

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync/atomic"
	"time"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/deployment/charts"
	"github.com/skycoin/skywire/pkg/deployment/rf/api"
	rfstore "github.com/skycoin/skywire/pkg/deployment/rf/store"
	"github.com/skycoin/skywire/pkg/deployment/tpd/store"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsghttp"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/metricsutil"
	"github.com/skycoin/skywire/pkg/services"
)

// Type is the registry key used in services.json blocks.
const Type = "route-finder"

func init() {
	services.Register(Type, factory)
}

// Config is the JSON configuration for route-finder.
type Config struct {
	Path string `json:"-"`

	services.Common

	// Dmsg is the dmsg-related config block.
	Dmsg cmdutil.DmsgConfig `json:"dmsg,omitempty"`

	// ChartsAddr serves only the charts page over plain HTTP. Empty disables it.
	ChartsAddr string `json:"charts_addr,omitempty"`

	// DisableLandmarkRouting answers every route with the exhaustive search
	// instead of composing far pairs through hub nodes (see store.SetLandmarkRouting).
	DisableLandmarkRouting bool `json:"disable_landmark_routing,omitempty"`
}

// LoadFile reads and strict-parses a Config from path.
func LoadFile(path string) (*Config, error) {
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("read config %q: %w", path, err)
	}
	var c Config
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("parse config %q: %w", path, err)
	}
	c.Path = path
	return &c, nil
}

// ParseBlock decodes a services.json block into a Config.
func ParseBlock(raw []byte) (*Config, error) {
	var c Config
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("route-finder: parse block: %w", err)
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
	// chartsAPI is the running API, whose charts the host shows on its status page.
	chartsAPI atomic.Pointer[api.API]
	cfg       *Config
	log       *logging.Logger
	// stats, set by Run, counts the process and its traffic for the status page.
	stats *charts.ServiceStats
}

// build creates the transport store and the API and warms the route
// graph. dmsgAddr is what the API reports on /health. The returned
// close releases the store.
func (s *service) build(ctx context.Context, logger *logging.Logger, dmsgAddr string) (*api.API, func(), error) {
	cfg := s.cfg
	rfstore.SetLandmarkRouting(!cfg.DisableLandmarkRouting)

	storeConfig := cfg.StoreConfig()

	// The route finder only reads transport data, so its TTL defaults longer
	// than TPD's.
	ttl := cfg.EntryTimeout.Std()
	if ttl == 0 {
		ttl = 10 * time.Minute
	}
	transportStore, err := store.New(ctx, storeConfig, ttl, logger)
	if err != nil {
		return nil, nil, fmt.Errorf("route-finder: init store: %w", err)
	}

	metricsutil.ServeHTTPMetrics(logger, cfg.MetricsAddr)

	enableMetrics := cfg.MetricsAddr != ""
	rfAPI := api.New(transportStore, logger, enableMetrics, dmsgAddr)
	s.chartsAPI.Store(rfAPI)
	// A TPD run in the same process (svc run) holds the transport set in
	// memory; the graph is built from it rather than reread from redis.
	rfAPI.ShareTransportsFrom(storeConfig.URL)
	// Warm the shared route graph in the background (bound to the server context)
	// so route requests reuse it instead of each building a per-source graph.
	rfAPI.StartGraphCache(ctx)
	return rfAPI, transportStore.Close, nil
}

// Embed runs route-finder inside a host process: the API is returned
// for the host to mount under a path prefix on its own dmsg HTTP port.
// Nothing listens.
func (s *service) Embed(ctx context.Context, host services.Host) (http.Handler, error) {
	logger := host.Log
	if logger == nil {
		logger = services.NewLogger(s.cfg.LogTag("route_finder"), s.cfg.LogLevel)
	}
	rfAPI, closeStore, err := s.build(ctx, logger, host.DmsgAddr)
	if err != nil {
		return nil, err
	}
	// Charts as Run has them, so the host's status page shows the route finder.
	var sn *api.SetupNodes
	if host.DmsgClient != nil {
		sn = &api.SetupNodes{PKs: s.cfg.setupNodes(), Port: dmsg.DefaultDmsgHTTPPort,
			Client: &http.Client{Transport: dmsghttp.MakeHTTPTransport(ctx, host.DmsgClient)}}
	}
	rfAPI.StartCharts(ctx, services.OpenChartsStore(s.cfg.StoreConfig(), "route-finder", logger), sn, logger)
	go func() {
		<-ctx.Done()
		closeStore()
	}()
	return rfAPI, nil
}

func (s *service) Run(ctx context.Context) error {
	cfg := s.cfg

	logger := services.NewLogger(cfg.LogTag("route_finder"), cfg.LogLevel)
	s.stats = charts.NewServiceStats("route finder")
	defer cfg.StartPprof(logger)()

	id := cfg.Identity(logger)

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	rfAPI, closeStore, err := s.build(runCtx, logger, id.DmsgAddr)
	if err != nil {
		return err
	}
	defer closeStore()

	h, err := cfg.Listen(runCtx, id, services.Listener{
		Name:        "route-finder",
		DefaultAddr: ":9092",
		Handler:     rfAPI,
		Dmsg:        cfg.Dmsg,
		Stats:       s.stats,
		OnDmsgServersUpdated: func(svrs []string) {
			rfAPI.DmsgServers = svrs
		},
	}, logger)
	if err != nil {
		return err
	}
	defer h.Close()

	var sn *api.SetupNodes
	if h.DmsgClient != nil {
		sn = &api.SetupNodes{PKs: cfg.setupNodes(), Port: dmsg.DefaultDmsgHTTPPort,
			Client: &http.Client{Transport: dmsghttp.MakeHTTPTransport(runCtx, h.DmsgClient)}}
	}
	rfAPI.Stats = s.stats
	rfAPI.StartCharts(runCtx, services.OpenChartsStore(cfg.StoreConfig(), "route-finder", logger), sn, logger)
	services.ServeCharts(runCtx, cfg.ChartsAddr, rfAPI.ChartsPage, logger)

	return services.Wait(runCtx, h, logger)
}

// setupNodes are the deployment's route setup nodes.
func (c *Config) setupNodes() []cipher.PubKey {
	return deployment.Prod.RouteSetupNodes
}

// ChartsPage is the charts page of the running service, for the host's status
// page. Nil before the service has started.
func (s *service) ChartsPage() *charts.Page {
	if a := s.chartsAPI.Load(); a != nil {
		return a.Charts()
	}
	return nil
}
