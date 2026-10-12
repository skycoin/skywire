// Package ar pkg/services/ar/ar.go c2-vis-appsvc
//
// address-resolver as a pkg/services.Service.
package ar

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"

	kcp "github.com/0magnet/kcp-go/v5"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/node"
	"github.com/skycoin/skywire/pkg/cxo/storeconfig"
	"github.com/skycoin/skywire/pkg/deployment/ar/api"
	armetrics "github.com/skycoin/skywire/pkg/deployment/ar/metrics"
	"github.com/skycoin/skywire/pkg/deployment/ar/regcxo"
	"github.com/skycoin/skywire/pkg/deployment/ar/store"
	"github.com/skycoin/skywire/pkg/deployment/charts"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/httpauth"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/metricsutil"
	"github.com/skycoin/skywire/pkg/services"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/svcmode"
)

// Type is the registry key used in services.json blocks.
const Type = "address-resolver"

const redisPrefix = "address-resolver"

func init() {
	services.Register(Type, factory)
}

func factory(raw json.RawMessage, log *logging.Logger) (services.Service, error) {
	cfg, err := ParseBlock(raw)
	if err != nil {
		return nil, err
	}
	return New(cfg, log), nil
}

func New(cfg *Config, log *logging.Logger) services.Service {
	return &service{cfg: cfg, log: log}
}

type service struct {
	// chartsAPI is the running API, whose charts the host shows on its status page.
	chartsAPI atomic.Pointer[api.API]
	cfg       *Config
	log       *logging.Logger

	// state, set by build and startCXO, is reported by State.
	store, nonceStore string
	cxo               services.CXOSet
	// stats, set by Run, counts the process and its traffic for the status page.
	stats *charts.ServiceStats
}

// State implements services.Stater.
func (s *service) State() services.State {
	st := services.State{Store: s.store, NonceStore: s.nonceStore}
	s.cxo.State(&st)
	return st
}

// built is everything Run and Embed share: the API with its store, the
// SUDPH UDP listener, and no HTTP listener yet.
type built struct {
	api   *api.API
	store store.Store
	close func()
}

// build creates the store, the nonce store, the API and the SUDPH UDP
// listener. dmsgAddr is what the API reports on /health; plainHTTP says
// whether a plain-HTTP surface will be served, the only path that needs
// a durable nonce store.
func (s *service) build(ctx context.Context, logger *logging.Logger, dmsgAddr string, plainHTTP bool) (*built, error) {
	cfg := s.cfg

	storeConfig := cfg.StoreConfig()

	s.store = services.StoreKind(storeConfig.Type)
	transportStore, err := store.New(ctx, storeConfig, cfg.EntryTimeout.Std(), logger)
	if err != nil {
		return nil, fmt.Errorf("address-resolver: init store: %w", err)
	}

	for _, k := range cfg.Whitelist {
		k = strings.TrimSpace(k)
		if k != "" {
			api.WhitelistPKs.Set(k)
		}
	}

	nonceConfig := cfg.NonceStoreConfig(plainHTTP)
	s.nonceStore = services.StoreKind(nonceConfig.Type)
	nonceStore, err := httpauth.NewNonceStore(ctx, nonceConfig, redisPrefix)
	if err != nil {
		return nil, fmt.Errorf("address-resolver: init nonce store: %w", err)
	}

	metricsutil.ServeHTTPMetrics(logger, cfg.MetricsAddr)

	var m armetrics.Metrics
	if cfg.MetricsAddr == "" {
		m = armetrics.NewEmpty()
	} else {
		m = armetrics.NewVictoriaMetrics()
	}

	enableMetrics := cfg.MetricsAddr != ""
	arAPI := api.New(logger, transportStore, nonceStore, enableMetrics, m, dmsgAddr, cfg.PublicUDPAddr)
	s.chartsAPI.Store(arAPI)

	udpAddr := cfg.UDPAddr
	if udpAddr == "" {
		udpAddr = ":30178"
	}
	udpListener, err := kcp.Listen(udpAddr)
	if err != nil {
		arAPI.Close()
		return nil, fmt.Errorf("address-resolver: open UDP listener on %s: %w", udpAddr, err)
	}

	go arAPI.ListenUDP(udpListener)
	arAPI.Stats = s.stats
	arAPI.StartCharts(ctx, chartStore(storeConfig, logger), logger)
	logger.Infof("UDP listener (SUDPH) on %s", udpAddr)

	return &built{api: arAPI, store: transportStore, close: func() {
		arAPI.Close()
		_ = udpListener.Close() //nolint:errcheck
	}}, nil
}

// startCXO brings up the AR-bind aggregator (visors publish their
// bindings as a CXO feed instead of re-registering over a fresh dmsg
// stream on a timer) and the bindings publisher (peers look a key up with
// a CXO Preview), on dmsgC under sk, until ctx ends. When embedded, host
// lends the visor's node for the bind port. Each piece is best-effort.
func (s *service) startCXO(ctx context.Context, dmsgC *dmsg.Client, host services.CXOHost, sk cipher.SecKey, b *built, logger *logging.Logger) {
	s.cxo.StartAggregator(ctx, host, logger, "ar-bind", skyenv.DmsgVisorARBindCXOPort, func(n *node.Node) (services.Aggregator, error) {
		return regcxo.New(dmsgC, sk, b.api, regcxo.Config{Node: n, Logger: logger})
	})
	bindPub, err := api.StartBindingsCXOPublisher(dmsgC, sk, b.store, logger)
	if err == nil {
		b.api.SetBindingsCXOPublisher(bindPub)
		go func() {
			<-ctx.Done()
			b.api.SetBindingsCXOPublisher(nil)
		}()
	}
	s.cxo.AddPublisher(ctx, logger, "bindings", skyenv.DmsgARBindingsCXOPort, bindPub, err)
	reachPub, err := b.api.StartReachCXOPublisher(dmsgC, sk)
	s.cxo.AddPublisher(ctx, logger, "reach", skyenv.DmsgARReachCXOPort, reachPub, err)
}

// Embed runs address-resolver inside a host process: the API is
// returned for the host to mount under a path prefix on its own dmsg
// HTTP port, the SUDPH UDP listener opens as configured, and the CXO
// aggregator and publisher run on the host's dmsg client under the
// host's key.
func (s *service) Embed(ctx context.Context, host services.Host) (http.Handler, error) {
	logger := host.Log
	if logger == nil {
		logger = services.NewLogger(s.cfg.LogTag("address_resolver"), s.cfg.LogLevel)
	}
	b, err := s.build(ctx, logger, host.DmsgAddr, false)
	if err != nil {
		return nil, err
	}
	if host.DmsgClient != nil {
		s.startCXO(ctx, host.DmsgClient, host.CXO, host.SK, b, logger)
	}
	go func() {
		<-ctx.Done()
		b.close()
	}()
	return b.api, nil
}

func (s *service) Run(ctx context.Context) error {
	cfg := s.cfg

	logger := services.NewLogger(cfg.LogTag("address_resolver"), cfg.LogLevel)
	s.stats = charts.NewServiceStats("address resolver")
	s.cxo.Stats = s.stats
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

	b, err := s.build(runCtx, logger, dmsgAddr, true)
	if err != nil {
		return err
	}
	defer b.close()
	arAPI := b.api

	resolvedMode, err := svcmode.ResolveMode(cfg.Mode, !sk.Null())
	if err != nil {
		return fmt.Errorf("address-resolver: invalid mode: %w", err)
	}

	// dmsg-only discovery: see pkg/services/rf/rf.go for rationale.
	dmsgDiscDmsg := cfg.Dmsg.DiscoveryDmsg
	if dmsgDiscDmsg == "" {
		dmsgDiscDmsg = dmsg.DiscAddr(false)
	}
	embeddedServers := dmsgDiscEntries(cfg.Dmsg.Servers)
	surveyWL := cfg.SurveyKeys()

	addr := cfg.Addr
	if addr == "" {
		addr = ":9093"
	}

	h, err := svcmode.Start(runCtx, svcmode.Config{
		Stats:               s.stats,
		Mode:                resolvedMode,
		HTTPAddr:            addr,
		Handler:             arAPI,
		PK:                  pk,
		SK:                  sk,
		DmsgPort:            dmsgPort,
		DmsgDiscoveryDmsg:   dmsgDiscDmsg,
		DmsgServerType:      cfg.Dmsg.ServerType,
		EmbeddedDmsgServers: embeddedServers,
		SurveyWhitelist:     surveyWL,
		Log:                 logger,
		OnDmsgServersUpdated: func(svrs []string) {
			arAPI.DmsgServers = svrs
		},
	})
	if err != nil {
		return fmt.Errorf("address-resolver: start listeners: %w", err)
	}
	defer h.Close()

	if cfg.ChartsAddr != "" {
		logger.Infof("Serving the charts page on %s", cfg.ChartsAddr)
		go func() {
			if err := charts.Serve(runCtx, cfg.ChartsAddr, http.HandlerFunc(arAPI.ChartsPage)); err != nil {
				logger.WithError(err).Error("charts listener failed")
			}
		}()
	}

	if h.DmsgClient != nil {
		s.startCXO(runCtx, h.DmsgClient, nil, sk, b, logger)
	}

	select {
	case <-runCtx.Done():
		return nil
	case err := <-h.Errors():
		logger.WithError(err).Error("listener failed")
		return err
	}
}

// AggregatorPorts implements services.CXOAggregating.
func (s *service) AggregatorPorts() []uint16 {
	return []uint16{skyenv.DmsgVisorARBindCXOPort}
}

// chartStore keeps the chart samples in the resolver's redis.
func chartStore(sc storeconfig.Config, log *logging.Logger) charts.Store {
	if sc.Type != storeconfig.Redis {
		return charts.NewMemoryStore()
	}
	st, err := charts.NewRedisStore(sc.URL, sc.Password, redisPrefix)
	if err != nil {
		log.WithError(err).Warn("charts: redis unavailable, keeping samples in memory")
		return charts.NewMemoryStore()
	}
	return st
}

// ChartsPage is the charts page of the running service, for the host's status
// page. Nil before the service has started.
func (s *service) ChartsPage() *charts.Page {
	if a := s.chartsAPI.Load(); a != nil {
		return a.Charts()
	}
	return nil
}
