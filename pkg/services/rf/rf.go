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
	"time"

	"github.com/skycoin/skywire/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/deployment/rf/api"
	"github.com/skycoin/skywire/pkg/deployment/tpd/store"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/metricsutil"
	"github.com/skycoin/skywire/pkg/services"
	"github.com/skycoin/skywire/pkg/svcmode"
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
	cfg *Config
	log *logging.Logger
}

// build creates the transport store and the API and warms the route
// graph. dmsgAddr is what the API reports on /health. The returned
// close releases the store.
func (s *service) build(ctx context.Context, logger *logging.Logger, dmsgAddr string) (*api.API, func(), error) {
	cfg := s.cfg

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
	go func() {
		<-ctx.Done()
		closeStore()
	}()
	return rfAPI, nil
}

func (s *service) Run(ctx context.Context) error {
	cfg := s.cfg

	logger := services.NewLogger(cfg.LogTag("route_finder"), cfg.LogLevel)
	defer cfg.StartPprof(logger)()

	pk := cfg.PubKey
	sk := cfg.SecKey
	if pk.Null() && !sk.Null() {
		if derived, err := sk.PubKey(); err != nil {
			logger.WithError(err).Warn("No SecKey found. Skipping serving on dmsghttp.")
		} else {
			pk = derived
		}
	}

	dmsgPort := cfg.DmsgPort
	if dmsgPort == 0 {
		dmsgPort = dmsg.DefaultDmsgHTTPPort
	}
	var dmsgAddr string
	if !pk.Null() {
		dmsgAddr = fmt.Sprintf("%s:%d", pk.Hex(), dmsgPort)
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	rfAPI, closeStore, err := s.build(runCtx, logger, dmsgAddr)
	if err != nil {
		return err
	}
	defer closeStore()

	resolvedMode, err := svcmode.ResolveMode(cfg.Mode, !sk.Null())
	if err != nil {
		return fmt.Errorf("route-finder: invalid mode: %w", err)
	}

	// dmsg-only discovery: the plain-HTTP cfg.Dmsg.Discovery field is
	// ignored. Operators configure cfg.Dmsg.DiscoveryDmsg explicitly,
	// or the embedded dmsg-PK URL (deployment.Prod.DmsgDiscoveryDmsg
	// via dmsg.DiscAddr) is used. svcmode routes discovery RPC over
	// dmsg-HTTP via the bootstrap dmsg client — no plain-HTTP egress.
	dmsgDiscDmsg := cfg.Dmsg.DiscoveryDmsg
	if dmsgDiscDmsg == "" {
		dmsgDiscDmsg = dmsg.DiscAddr(false)
	}
	embeddedServers := dmsgDiscEntries(cfg.Dmsg.Servers)
	surveyWL := cfg.SurveyKeys()

	addr := cfg.Addr
	if addr == "" {
		addr = ":9092"
	}

	h, err := svcmode.Start(runCtx, svcmode.Config{
		Mode:                resolvedMode,
		HTTPAddr:            addr,
		Handler:             rfAPI,
		PK:                  pk,
		SK:                  sk,
		DmsgPort:            dmsgPort,
		DmsgDiscoveryDmsg:   dmsgDiscDmsg,
		DmsgServerType:      cfg.Dmsg.ServerType,
		EmbeddedDmsgServers: embeddedServers,
		SurveyWhitelist:     surveyWL,
		Log:                 logger,
		OnDmsgServersUpdated: func(svrs []string) {
			rfAPI.DmsgServers = svrs
		},
	})
	if err != nil {
		return fmt.Errorf("route-finder: start listeners: %w", err)
	}
	defer h.Close()

	select {
	case <-runCtx.Done():
		return nil
	case err := <-h.Errors():
		logger.WithError(err).Error("listener failed")
		return err
	}
}

func dmsgDiscEntries(configServers []*disc.Entry) []disc.Entry {
	if len(configServers) == 0 {
		return dmsg.Prod.DmsgServers
	}
	out := make([]disc.Entry, 0, len(configServers))
	for _, e := range configServers {
		if e != nil {
			out = append(out, *e)
		}
	}
	return out
}
