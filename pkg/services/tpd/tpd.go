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
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/cxo/storeconfig"
	"github.com/skycoin/skywire/pkg/deployment/tpd/api"
	"github.com/skycoin/skywire/pkg/deployment/tpd/cxoaggregator"
	tpdiscmetrics "github.com/skycoin/skywire/pkg/deployment/tpd/metrics"
	"github.com/skycoin/skywire/pkg/deployment/tpd/store"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
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

const (
	redisPrefix = "transport-discovery"
	redisScheme = "redis://"
)

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
	cfg *Config
	log *logging.Logger
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

	redisURL := cfg.Redis
	if redisURL == "" {
		redisURL = "redis://localhost:6379"
	}
	if !strings.HasPrefix(redisURL, redisScheme) {
		redisURL = redisScheme + redisURL
	}

	storeCfg := storeconfig.Config{
		Type:     storeconfig.Redis,
		URL:      redisURL,
		Password: storeconfig.RedisPassword(),
		PoolSize: cfg.RedisPoolSize,
	}
	if storeCfg.PoolSize == 0 {
		storeCfg.PoolSize = 10
	}
	if cfg.Testing {
		storeCfg.Type = storeconfig.Memory
	}

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

	metricsutil.ServePProf(logger, cfg.PprofAddr, "transport-discovery")

	for _, k := range cfg.Whitelist {
		k = strings.TrimSpace(k)
		if k != "" {
			api.WhitelistPKs.Set(k)
		}
	}

	st, err := store.New(ctx, storeCfg, cfg.EntryTimeout.Std(), logger)
	if err != nil {
		closeAll()
		return nil, fmt.Errorf("transport-discovery: create store: %w", err)
	}
	closers = append(closers, st.Close)

	// Requests over dmsg are authenticated by the stream's key; only a
	// plain-HTTP surface checks nonces, so only that needs them durable.
	nonceStoreConfig := storeconfig.Config{
		Type:     storeconfig.Memory,
		URL:      redisURL,
		Password: storeconfig.RedisPassword(),
		PoolSize: storeCfg.PoolSize,
	}
	if plainHTTP && !cfg.Testing {
		nonceStoreConfig.Type = storeconfig.Redis
	}
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
	tpdAPI := api.New(logger, st, nonceStore, enableMetrics, m, dmsgAddr, storeDataPath)
	tpdAPI.SetEntryTimeout(cfg.EntryTimeout.Std())
	if uptimeRec != nil {
		tpdAPI.SetUptimeRecorder(uptimeRec)
	}
	logger.Infof("Transport entry timeout: %v", cfg.EntryTimeout)

	go tpdAPI.RunBackgroundTasks(ctx, logger)

	return &built{api: tpdAPI, st: st, logger: logger, close: closeAll}, nil
}

// Embed runs transport-discovery inside a host process: the API is
// returned for the host to mount under a path prefix on its own dmsg
// HTTP port, and the CXO aggregators and publishers run on the host's
// dmsg client under the host's key. Nothing listens.
func (s *service) Embed(ctx context.Context, host services.Host) (http.Handler, error) {
	logger := host.Log
	if logger == nil {
		logger = services.NewLogger("transport_discovery", s.cfg.LogLevel)
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
		s.startCXO(ctx, host.DmsgClient, b.st, b.api, host.SK, logger)
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

	if cfg.Tag == "" {
		cfg.Tag = "transport_discovery"
	}
	logger := services.NewLogger(cfg.Tag, cfg.LogLevel)
	_ = s.log // logger is replaced with a tag-scoped one

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
	surveyWL := deployment.Prod.SurveyWhitelist
	if cfg.TestEnvironment {
		surveyWL = deployment.Test.SurveyWhitelist
	}
	if len(cfg.SurveyWhitelist) > 0 {
		surveyWL = cfg.SurveyWhitelist
	}

	h, err := svcmode.Start(runCtx, svcmode.Config{
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

	if h.DmsgClient != nil {
		s.startCXO(runCtx, h.DmsgClient, b.st, tpdAPI, sk, logger)
	}

	select {
	case <-runCtx.Done():
		return nil
	case err := <-h.Errors():
		logger.WithError(err).Error("listener failed")
		return err
	}
}

// startCXO brings up the inbound aggregator and the outbound metrics
// + uptime publishers. Each one degrades to a warning on failure; we
// don't want a CXO bring-up problem to take down a perfectly healthy
// HTTP/DMSG TPD.
func (s *service) startCXO(
	ctx context.Context,
	dmsgC *dmsg.Client,
	st store.Store,
	tpdAPI *api.API,
	sk cipher.SecKey,
	logger *logging.Logger,
) {
	sink := &aggregatorSink{Store: st, api: tpdAPI}
	agg, err := cxoaggregator.New(dmsgC, sk, sink, cxoaggregator.Config{
		Logger: logging.MustGetLogger("tpd-cxo-aggregator"),
	})
	if err != nil {
		logger.WithError(err).Error("Failed to start CXO aggregator, continuing without it")
	} else {
		agg.Run(ctx)
		go func() {
			<-ctx.Done()
			agg.Close() //nolint:errcheck,gosec
		}()
		logger.WithField("feed_pk", agg.FeedPK()).Info("CXO aggregator running: accepting inbound visor stats feeds")
	}

	// Second aggregator for the visors' DEDICATED tp-list discovery feed
	// (DmsgVisorTPListCXOPort). That feed's Root is just the compact
	// transport-list snapshot leaf, so it fills completely in ~1 round-trip
	// — the durable cure for the ~10% transport under-report on busy hubs,
	// whose combined telemetry Root on port 50 can't finish its fill in the
	// announce conn's window. It shares the same sink: only the declarative
	// ReconcileTransportsFromCXO path fires (the tp-list feed carries no
	// per-transport telemetry leaves). Kept on its own node/port so a
	// visor's tp-list Root never head-collides with its telemetry Root.
	// A visor that publishes only the legacy combined feed (older binary)
	// simply never dials this port — the port-50 aggregator above still
	// reconciles its tp-list from the combined feed (back-compat fallback).
	tplAgg, err := cxoaggregator.New(dmsgC, sk, sink, cxoaggregator.Config{
		DmsgPort: skyenv.DmsgVisorTPListCXOPort,
		Logger:   logging.MustGetLogger("tpd-cxo-tplist-aggregator"),
	})
	if err != nil {
		logger.WithError(err).Error("Failed to start CXO tp-list aggregator, continuing without it")
	} else {
		tplAgg.Run(ctx)
		go func() {
			<-ctx.Done()
			tplAgg.Close() //nolint:errcheck,gosec
		}()
		logger.WithField("feed_pk", tplAgg.FeedPK()).WithField("dmsg_port", skyenv.DmsgVisorTPListCXOPort).
			Info("CXO tp-list aggregator running: accepting inbound visor tp-list discovery feeds")
	}

	if pub, perr := api.StartMetricsCXOPublisher(ctx, tpdAPI, dmsgC, sk, logger); perr != nil {
		logger.WithError(perr).Error("Failed to start CXO metrics publisher, continuing without it")
	} else {
		go func() {
			<-ctx.Done()
			pub.Close() //nolint:errcheck,gosec
		}()
	}

	if pub, perr := api.StartUptimeCXOPublisher(ctx, tpdAPI, dmsgC, sk, logger); perr != nil {
		logger.WithError(perr).Error("Failed to start CXO uptime publisher, continuing without it")
	} else {
		go func() {
			<-ctx.Done()
			pub.Close() //nolint:errcheck,gosec
		}()
	}

	if pub, perr := api.StartAllTransportsCXOPublisher(ctx, tpdAPI, dmsgC, sk, logger); perr != nil {
		logger.WithError(perr).Error("Failed to start CXO all-transports publisher, continuing without it")
	} else {
		go func() {
			<-ctx.Done()
			pub.Close() //nolint:errcheck,gosec
		}()
	}

	if pub, perr := api.StartStatsCXOPublisher(ctx, tpdAPI, dmsgC, sk, logger); perr != nil {
		logger.WithError(perr).Error("Failed to start CXO stats publisher, continuing without it")
	} else {
		go func() {
			<-ctx.Done()
			pub.Close() //nolint:errcheck,gosec
		}()
	}
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
