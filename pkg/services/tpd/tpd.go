// Package tpd pkg/services/tpd/tpd.go
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
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/httpauth"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/metricsutil"
	"github.com/skycoin/skywire/pkg/services"
	"github.com/skycoin/skywire/pkg/serviceuptime"
	"github.com/skycoin/skywire/pkg/storeconfig"
	"github.com/skycoin/skywire/pkg/svcmode"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/transport-discovery/api"
	"github.com/skycoin/skywire/pkg/transport-discovery/cxoaggregator"
	tpdiscmetrics "github.com/skycoin/skywire/pkg/transport-discovery/metrics"
	"github.com/skycoin/skywire/pkg/transport-discovery/store"
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
	logger := logging.MustGetLogger(cfg.Tag)
	_ = s.log // logger is replaced with a tag-scoped one
	if cfg.LogLevel != "" {
		if lvl, err := logging.LevelFromString(cfg.LogLevel); err == nil {
			logging.SetLevel(lvl)
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
			defer func() { _ = uptimeRec.Close() }() //nolint:errcheck
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

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	st, err := store.New(runCtx, storeCfg, cfg.EntryTimeout.Std(), logger)
	if err != nil {
		return fmt.Errorf("transport-discovery: create store: %w", err)
	}
	defer st.Close()

	nonceStoreConfig := storeconfig.Config{
		Type:     storeconfig.Memory,
		URL:      redisURL,
		Password: storeconfig.RedisPassword(),
		PoolSize: storeCfg.PoolSize,
	}
	if !cfg.Testing {
		nonceStoreConfig.Type = storeconfig.Redis
	}
	nonceStore, err := httpauth.NewNonceStore(runCtx, nonceStoreConfig, redisPrefix)
	if err != nil {
		return fmt.Errorf("transport-discovery: init nonce store: %w", err)
	}

	pk := cfg.PubKey
	sk := cfg.SecKey
	if pk.Null() && !sk.Null() {
		if derived, err := sk.PubKey(); err != nil {
			logger.WithError(err).Warn("No SecKey found. Skipping serving on dmsghttp.")
		} else {
			pk = derived
		}
	}

	metricsutil.ServeHTTPMetrics(logger, cfg.MetricsAddr)

	var m tpdiscmetrics.Metrics
	if cfg.MetricsAddr == "" {
		m = tpdiscmetrics.NewEmpty()
	} else {
		m = tpdiscmetrics.NewVictoriaMetrics()
	}

	dmsgPort := cfg.DmsgPort
	if dmsgPort == 0 {
		dmsgPort = dmsg.DefaultDmsgHTTPPort
	}
	var dmsgAddr string
	if !pk.Null() {
		dmsgAddr = fmt.Sprintf("%s:%d", pk.Hex(), dmsgPort)
	}

	enableMetrics := cfg.MetricsAddr != ""
	storeDataPath := cfg.StoreDataPath
	if storeDataPath == "" {
		storeDataPath = "/var/lib/skywire/tpd/bandwidth"
	}
	tpdAPI := api.New(logger, st, nonceStore, enableMetrics, m, dmsgAddr, storeDataPath)
	if uptimeRec != nil {
		tpdAPI.SetUptimeRecorder(uptimeRec)
	}

	addr := cfg.Addr
	if addr == "" {
		addr = ":9091"
	}
	logger.Infof("Listening on %s", addr)
	logger.Infof("Transport entry timeout: %v", cfg.EntryTimeout)

	go tpdAPI.RunBackgroundTasks(runCtx, logger)

	resolvedMode, err := svcmode.ResolveMode(cfg.Mode, !sk.Null())
	if err != nil {
		return fmt.Errorf("transport-discovery: invalid mode: %w", err)
	}

	dmsgDisc := cfg.Dmsg.Discovery
	if dmsgDisc == "" {
		dmsgDisc = deployment.Prod.DmsgDiscovery
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
		DmsgDiscovery:       dmsgDisc,
		DmsgServerType:      cfg.Dmsg.ServerType,
		EmbeddedDmsgServers: embeddedServers,
		SurveyWhitelist:     surveyWL,
		Log:                 logger,
		DisableDHT:          true,
		OnDmsgServersUpdated: func(s []string) {
			tpdAPI.DmsgServers = s
		},
	})
	if err != nil {
		return fmt.Errorf("transport-discovery: start listeners: %w", err)
	}
	defer h.Close()

	if cfg.EnableCXO && h.DmsgClient != nil {
		s.startCXO(runCtx, h, st, tpdAPI, sk, logger)
	} else if cfg.EnableCXO {
		logger.Warn("CXO requested but dmsg is not enabled (--mode=http); aggregator/publisher disabled")
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
	h *svcmode.Handle,
	st store.Store,
	tpdAPI *api.API,
	sk cipher.SecKey,
	logger *logging.Logger,
) {
	sink := &aggregatorSink{Store: st, api: tpdAPI}
	agg, err := cxoaggregator.New(h.DmsgClient, sink, cxoaggregator.Config{
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

	if pub, perr := api.StartMetricsCXOPublisher(ctx, tpdAPI, h.DmsgClient, sk, logger); perr != nil {
		logger.WithError(perr).Error("Failed to start CXO metrics publisher, continuing without it")
	} else {
		go func() {
			<-ctx.Done()
			pub.Close() //nolint:errcheck,gosec
		}()
	}

	if pub, perr := api.StartUptimeCXOPublisher(ctx, tpdAPI, h.DmsgClient, sk, logger); perr != nil {
		logger.WithError(perr).Error("Failed to start CXO uptime publisher, continuing without it")
	} else {
		go func() {
			<-ctx.Done()
			pub.Close() //nolint:errcheck,gosec
		}()
	}

	if pub, perr := api.StartAllTransportsCXOPublisher(ctx, tpdAPI, h.DmsgClient, sk, logger); perr != nil {
		logger.WithError(perr).Error("Failed to start CXO all-transports publisher, continuing without it")
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
