// Package tpd pkg/services/tpd/tpd.go c2-vis-appsvc
//
// transport-discovery as a pkg/services.Service. The standalone
// `skywire svc tpd` cobra command and the multi-service supervisor
// (`skywire svc run`) both end up calling Run(ctx) — the per-flag
// CLI path lives in cmd/svc/transport-discovery and constructs the
// Config from flags + optional --config file before handing off.
package tpd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/cxo/cxosub"
	"github.com/skycoin/skywire/pkg/cxo/node"
	"github.com/skycoin/skywire/pkg/cxo/storeconfig"
	"github.com/skycoin/skywire/pkg/deployment/charts"
	"github.com/skycoin/skywire/pkg/deployment/netgraph"
	"github.com/skycoin/skywire/pkg/deployment/tpd/api"
	"github.com/skycoin/skywire/pkg/deployment/tpd/cxoaggregator"
	tpdiscmetrics "github.com/skycoin/skywire/pkg/deployment/tpd/metrics"
	"github.com/skycoin/skywire/pkg/deployment/tpd/store"
	"github.com/skycoin/skywire/pkg/dmsg/discovery/serverfeed"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsghttp"
	"github.com/skycoin/skywire/pkg/httpauth"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/metricsutil"
	"github.com/skycoin/skywire/pkg/services"
	"github.com/skycoin/skywire/pkg/serviceuptime"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/svcmode"
	"github.com/skycoin/skywire/pkg/transport"
)

// Type is the registry key used in services.json blocks.
const Type = "transport-discovery"

const redisPrefix = "transport-discovery"

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

// New builds a Service from an already-parsed Config.
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

// built is everything Run and Embed share: the API and the stores
// behind it, up and running, with no listener attached yet.
type built struct {
	api    *api.API
	st     store.Store
	logger *logging.Logger
	close  func()
}

// build creates the store, the nonce store and the API and starts the
// API's background tasks. dmsgAddr is what the API reports on /health;
// plainHTTP says whether a plain-HTTP surface will be served, which is
// the only path that needs a durable nonce store.
func (s *service) build(ctx context.Context, logger *logging.Logger, dmsgAddr string, plainHTTP bool) (*built, error) {
	cfg := s.cfg
	var closers []func()
	closeAll := func() {
		for i := len(closers) - 1; i >= 0; i-- {
			closers[i]()
		}
	}

	storeCfg := cfg.StoreConfig()

	// Service-self uptime recorder. Opened before subsystem init so
	// a panic in the redis or DMSG bring-up still leaves a session
	// row with the running binary's version. Failure is non-fatal —
	// TPD continues without the /uptime/* surface.
	var uptimeRec *serviceuptime.Recorder
	if cfg.UptimeDB != "" {
		rec, rErr := serviceuptime.New(cfg.UptimeDB, serviceuptime.Config{
			Service: "transport-discovery",
			Version: buildinfo.Version(),
			Commit:  buildinfo.Commit(),
		})
		if rErr != nil {
			logger.WithError(rErr).Warn("Service-self uptime recorder unavailable")
		} else {
			uptimeRec = rec
			closers = append(closers, func() { _ = uptimeRec.Close() }) //nolint:errcheck
			uptimeRec.Start()
		}
	}

	for _, k := range cfg.Whitelist {
		k = strings.TrimSpace(k)
		if k != "" {
			api.WhitelistPKs.Set(k)
		}
	}

	s.store = services.StoreKind(storeCfg.Type)
	st, err := store.New(ctx, storeCfg, cfg.EntryTimeout.Std(), logger)
	if err != nil {
		closeAll()
		return nil, fmt.Errorf("transport-discovery: create store: %w", err)
	}
	closers = append(closers, st.Close)
	// TPD writes every transport, so it holds the whole set in memory and
	// serves the whole-set reads from it — its own publishers, and a route
	// finder in the same process (store.SharedLiveStore).
	live := false
	if ls, ok := st.(interface {
		EnableLiveSet(ctx context.Context, url string) error
	}); ok {
		live = true
		if err := ls.EnableLiveSet(ctx, storeCfg.URL); err != nil {
			closeAll()
			return nil, fmt.Errorf("transport-discovery: load live transport set: %w", err)
		}
	} else {
		// An in-memory store is the whole set already.
		store.ShareStore(ctx, storeCfg.URL, st)
	}

	nonceStoreConfig := cfg.NonceStoreConfig(plainHTTP)
	s.nonceStore = services.StoreKind(nonceStoreConfig.Type)
	nonceStore, err := httpauth.NewNonceStore(ctx, nonceStoreConfig, redisPrefix)
	if err != nil {
		closeAll()
		return nil, fmt.Errorf("transport-discovery: init nonce store: %w", err)
	}

	metricsutil.ServeHTTPMetrics(logger, cfg.MetricsAddr)

	var m tpdiscmetrics.Metrics
	if cfg.MetricsAddr == "" {
		m = tpdiscmetrics.NewEmpty()
	} else {
		m = tpdiscmetrics.NewVictoriaMetrics()
	}

	enableMetrics := cfg.MetricsAddr != ""
	storeDataPath := cfg.StoreDataPath
	if storeDataPath == "" {
		storeDataPath = "/var/lib/skywire/tpd/bandwidth"
	}
	if la, ok := st.(interface{ SetLeafArchive(string) }); ok {
		la.SetLeafArchive(filepath.Join(storeDataPath, "leaves"))
	}
	tpdAPI := api.New(logger, st, nonceStore, enableMetrics, m, dmsgAddr, storeDataPath)
	s.chartsAPI.Store(tpdAPI)
	tpdAPI.SetEntryTimeout(cfg.EntryTimeout.Std())
	if live {
		if err := tpdAPI.SeedReconcile(ctx); err != nil {
			logger.WithError(err).Warn("could not seed the reconcile from the live set; the first reports re-register")
		}
	}
	if uptimeRec != nil {
		tpdAPI.SetUptimeRecorder(uptimeRec)
	}
	logger.Infof("Transport entry timeout: %v", cfg.EntryTimeout)

	go tpdAPI.RunBackgroundTasks(ctx, logger)
	if cs, err := s.chartStore(storeCfg); err != nil {
		logger.WithError(err).Warn("charts unavailable")
	} else {
		tpdAPI.Stats = s.stats
		tpdAPI.StartCharts(ctx, cs, logger)
	}

	return &built{api: tpdAPI, st: st, logger: logger, close: closeAll}, nil
}

// Embed runs transport-discovery inside a host process: the API is
// returned for the host to mount under a path prefix on its own dmsg
// HTTP port, and the CXO aggregators and publishers run on the host's
// dmsg client under the host's key. Nothing listens.
func (s *service) Embed(ctx context.Context, host services.Host) (http.Handler, error) {
	logger := host.Log
	if logger == nil {
		logger = services.NewLogger(s.cfg.LogTag("transport_discovery"), s.cfg.LogLevel)
	}
	b, err := s.build(ctx, logger, host.DmsgAddr, false)
	if err != nil {
		return nil, err
	}
	go func() {
		<-ctx.Done()
		b.close()
	}()
	if host.DmsgClient != nil {
		s.startCXO(ctx, host.DmsgClient, host.CXO, b.st, b.api, host.SK, logger)
	}
	return b.api, nil
}

// Run is the long-lived run loop. Mirrors the previous
// `skywire svc tpd` cobra Run callback — same redis store init,
// same nonce store, same API construction, same svcmode.Start, same
// optional CXO aggregator + metrics/uptime publishers — but takes
// its config from the Config struct rather than package vars.
func (s *service) Run(ctx context.Context) error {
	cfg := s.cfg

	logger := services.NewLogger(cfg.LogTag("transport_discovery"), cfg.LogLevel)
	s.stats = charts.NewServiceStats("transport discovery")
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
	tpdAPI := b.api

	addr := cfg.Addr
	if addr == "" {
		addr = ":9091"
	}
	logger.Infof("Listening on %s", addr)

	resolvedMode, err := svcmode.ResolveMode(cfg.Mode, !sk.Null())
	if err != nil {
		return fmt.Errorf("transport-discovery: invalid mode: %w", err)
	}

	// dmsg-only discovery: see pkg/services/rf/rf.go for rationale.
	dmsgDiscDmsg := cfg.Dmsg.DiscoveryDmsg
	if dmsgDiscDmsg == "" {
		dmsgDiscDmsg = dmsg.DiscAddr(false)
	}
	embeddedServers := dmsgDiscEntries(cfg.Dmsg.Servers)
	surveyWL := cfg.SurveyKeys()

	h, err := svcmode.Start(runCtx, svcmode.Config{
		Stats:               s.stats,
		Mode:                resolvedMode,
		HTTPAddr:            addr,
		Handler:             tpdAPI,
		PK:                  pk,
		SK:                  sk,
		DmsgPort:            dmsgPort,
		DmsgDiscoveryDmsg:   dmsgDiscDmsg,
		DmsgServerType:      cfg.Dmsg.ServerType,
		EmbeddedDmsgServers: embeddedServers,
		SurveyWhitelist:     surveyWL,
		Log:                 logger,
		OnDmsgServersUpdated: func(s []string) {
			tpdAPI.DmsgServers = s
		},
	})
	if err != nil {
		return fmt.Errorf("transport-discovery: start listeners: %w", err)
	}
	defer h.Close()

	if cfg.ChartsAddr != "" {
		logger.Infof("Serving the charts page on %s", cfg.ChartsAddr)
		go func() {
			if err := charts.Serve(runCtx, cfg.ChartsAddr, http.HandlerFunc(tpdAPI.ChartsPage),
				charts.Extra{Path: "/graph", Handler: http.HandlerFunc(tpdAPI.GraphPage)},
				charts.Extra{Path: "/graph/engine.wasm", Handler: http.HandlerFunc(netgraph.EngineWasm)},
				charts.Extra{Path: "/graph/engine.js", Handler: http.HandlerFunc(netgraph.EngineLoader)}); err != nil {
				logger.WithError(err).Error("charts listener failed")
			}
		}()
	}

	if h.DmsgClient != nil {
		s.startCXO(runCtx, h.DmsgClient, nil, b.st, tpdAPI, sk, logger)
		tpdAPI.SetDmsgDiscovery(&http.Client{Transport: dmsghttp.MakeHTTPTransport(runCtx, h.DmsgClient)}, dmsgDiscDmsg)
		if mgr := dmsgdFeed(h.DmsgClient, dmsgDiscDmsg, logger); mgr != nil {
			tpdAPI.SetDmsgDiscoveryFeed(mgr)
			defer mgr.Close()
		}
	}

	select {
	case <-runCtx.Done():
		return nil
	case err := <-h.Errors():
		logger.WithError(err).Error("listener failed")
		return err
	}
}

// startCXO brings up TPD's CXO aggregators (visor telemetry, and the
// dedicated tp-list feed) and its publishers on dmsgC under sk. When
// embedded, host lends the visor's nodes: the aggregators run on the
// visor's publisher node for their port and take the visor's own feed
// in-process. Each piece is best-effort.
func (s *service) startCXO(
	ctx context.Context,
	dmsgC *dmsg.Client,
	host services.CXOHost,
	st store.Store,
	tpdAPI *api.API,
	sk cipher.SecKey,
	logger *logging.Logger,
) {
	sink := &aggregatorSink{Store: st, api: tpdAPI}
	s.cxo.StartAggregator(ctx, host, logger, "telemetry", skyenv.DmsgCXOPort, func(n *node.Node) (services.Aggregator, error) {
		return cxoaggregator.New(dmsgC, sk, sink, cxoaggregator.Config{
			Node:   n,
			Logger: logging.MustGetLogger("tpd-cxo-aggregator"),
		})
	})
	// The visors' DEDICATED tp-list feed (opt-in on the visor side): its
	// Root is just the transport-list snapshot leaf, so it fills in about one
	// round-trip. Same sink; only the declarative reconcile path fires. A
	// visor that publishes only the combined feed never dials this port.
	s.cxo.StartAggregator(ctx, host, logger, "tp-list", skyenv.DmsgVisorTPListCXOPort, func(n *node.Node) (services.Aggregator, error) {
		return cxoaggregator.New(dmsgC, sk, sink, cxoaggregator.Config{
			DmsgPort: skyenv.DmsgVisorTPListCXOPort,
			Node:     n,
			Logger:   logging.MustGetLogger("tpd-cxo-tplist-aggregator"),
		})
	})

	mp, err := api.StartMetricsCXOPublisher(ctx, tpdAPI, dmsgC, sk, logger)
	s.cxo.AddPublisher(ctx, logger, "metrics", skyenv.DmsgTPDMetricsCXOPort, mp, err)
	up, err := api.StartUptimeCXOPublisher(ctx, tpdAPI, dmsgC, sk, logger)
	s.cxo.AddPublisher(ctx, logger, "uptime", skyenv.DmsgTPDUptimeCXOPort, up, err)
	rp, err := api.StartRoutingCXOPublisher(ctx, tpdAPI, dmsgC, sk, logger)
	s.cxo.AddPublisher(ctx, logger, "routing", skyenv.DmsgTPDRoutingCXOPort, rp, err)
	sp, err := api.StartStatsCXOPublisher(ctx, tpdAPI, dmsgC, sk, logger)
	s.cxo.AddPublisher(ctx, logger, "stats", skyenv.DmsgTPDStatsCXOPort, sp, err)
	kp, err := api.StartPerKeyCXOPublisher(ctx, tpdAPI, dmsgC, sk, logger)
	s.cxo.AddPublisher(ctx, logger, "perkey", skyenv.DmsgTPDPerKeyCXOPort, kp, err)
	vp, err := api.StartVisorBWCXOPublisher(ctx, tpdAPI, dmsgC, sk, logger)
	s.cxo.AddPublisher(ctx, logger, "visorbw", skyenv.DmsgTPDVisorBWCXOPort, vp, err)
}

// aggregatorSink composes the cxoaggregator.Sink contract from the
// two collaborators that own the relevant pieces:
//
//   - store.Store satisfies the telemetry half (UpdateBandwidth,
//     UpdateLatency, RecordTransportHeartbeat, IngestTransportTimeline)
//     directly via the embedded interface.
//   - The API satisfies the metadata half (RegisterTransportFromCXO,
//     DeregisterTransportFromCXO) because those need mirrorEdges in
//     addition to a redis write, and mirrorEdges lives on the API.
type aggregatorSink struct {
	store.Store
	api *api.API
}

// ApplyTelemetry forwards a telemetry batch to the store (store.TelemetryBatchStore),
// which the embedded store.Store interface does not expose.
func (s *aggregatorSink) ApplyTelemetry(ctx context.Context, updates []store.TelemetryUpdate) error {
	return s.Store.(store.TelemetryBatchStore).ApplyTelemetry(ctx, updates)
}

// RecordTransportHeartbeats forwards a heartbeat batch to the store
// (store.BatchStore).
func (s *aggregatorSink) RecordTransportHeartbeats(ctx context.Context, entries []*transport.Entry, at time.Time) error {
	return s.Store.(store.BatchStore).RecordTransportHeartbeats(ctx, entries, at)
}

func (s *aggregatorSink) RegisterTransportFromCXO(ctx context.Context, entry *transport.Entry, reporter cipher.PubKey, version string) error {
	return s.api.RegisterTransportFromCXO(ctx, entry, reporter, version)
}

func (s *aggregatorSink) ReconcileTransportsFromCXO(ctx context.Context, entries []*transport.Entry, reporter cipher.PubKey, version string) error {
	return s.api.ReconcileTransportsFromCXO(ctx, entries, reporter, version)
}

// RefreshTransportsFromCXO implements cxoaggregator.Sink.
func (s *aggregatorSink) RefreshTransportsFromCXO(ctx context.Context, entries []*transport.Entry, reporter cipher.PubKey, version string) error {
	return s.api.RefreshTransportsFromCXO(ctx, entries, reporter, version)
}

func (s *aggregatorSink) DeregisterTransportFromCXO(ctx context.Context, id uuid.UUID, reporter cipher.PubKey) error {
	return s.api.DeregisterTransportFromCXO(ctx, id, reporter)
}

// Default values for the cobra command's flag defaults that get
// overridden by Config field zero-values. Exposed so the cmd
// package can reference one source of truth.
var (
	DefaultEntryTimeout  = 5 * time.Minute
	DefaultStoreDataPath = "/var/lib/skywire/tpd/bandwidth"
	DefaultUptimeDB      = "/var/lib/skywire/tpd/uptime.db"
	DefaultRedisPoolSize = 10
	_                    = cmdutil.DmsgConfig{} // keep cmdutil import in this file when refactored
)

// AggregatorPorts implements services.CXOAggregating.
func (s *service) AggregatorPorts() []uint16 {
	return []uint16{skyenv.DmsgCXOPort, skyenv.DmsgVisorTPListCXOPort}
}

// chartStore keeps the chart samples next to the transports.
func (s *service) chartStore(sc storeconfig.Config) (charts.Store, error) {
	if sc.Type != storeconfig.Redis {
		return charts.NewMemoryStore(), nil
	}
	return charts.NewRedisStore(sc.URL, sc.Password, redisPrefix)
}

// dmsgdFeed holds dmsg discovery's clients-by-server feed for the marking of
// dmsg server visors, or returns nil when dmsg discovery has no dmsg key.
func dmsgdFeed(dmsgC *dmsg.Client, discURL string, log *logging.Logger) *cxosub.Manager {
	pk := cmdutil.PKFromDmsgURL(discURL)
	if pk.Null() {
		return nil
	}
	mgr := cxosub.NewManager(cxosub.Deps{
		Dmsg: func() *dmsg.Client { return dmsgC },
		FeedSpec: func(f cxosub.Feed) (cipher.PubKey, uint16, string, error) {
			if f != cxosub.FeedDMSGDClientsByServer {
				return cipher.PubKey{}, 0, "", fmt.Errorf("feed %s is not held here", cxosub.FeedString(f))
			}
			return pk, skyenv.DmsgDMSGDClientsByServerCXOPort, serverfeed.Prefix, nil
		},
		Log: log,
	}, 0)
	mgr.Pin(cxosub.FeedDMSGDClientsByServer)
	return mgr
}

// ChartsPage is the charts page of the running service, for the host's status
// page. Nil before the service has started.
func (s *service) ChartsPage() *charts.Page {
	if a := s.chartsAPI.Load(); a != nil {
		return a.Charts()
	}
	return nil
}
