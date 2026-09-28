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

	"github.com/xtaci/kcp-go"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/storeconfig"
	"github.com/skycoin/skywire/pkg/deployment/ar/api"
	armetrics "github.com/skycoin/skywire/pkg/deployment/ar/metrics"
	"github.com/skycoin/skywire/pkg/deployment/ar/regcxo"
	"github.com/skycoin/skywire/pkg/deployment/ar/store"
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

const (
	redisPrefix = "address-resolver"
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

func New(cfg *Config, log *logging.Logger) services.Service {
	return &service{cfg: cfg, log: log}
}

type service struct {
	cfg *Config
	log *logging.Logger
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

	redisURL := cfg.Redis
	if redisURL == "" {
		redisURL = "redis://localhost:6379"
	}
	if !strings.HasPrefix(redisURL, redisScheme) {
		redisURL = redisScheme + redisURL
	}
	storeConfig := storeconfig.Config{
		Type:     storeconfig.Redis,
		URL:      redisURL,
		Password: storeconfig.RedisPassword(),
		PoolSize: cfg.RedisPoolSize,
	}
	if storeConfig.PoolSize == 0 {
		storeConfig.PoolSize = 10
	}
	if cfg.Testing {
		storeConfig.Type = storeconfig.Memory
	}

	metricsutil.ServePProf(logger, cfg.PprofAddr, "address-resolver")

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

	// Requests over dmsg are authenticated by the stream's key; only a
	// plain-HTTP surface checks nonces, so only that needs them durable.
	nonceConfig := storeConfig
	if !plainHTTP {
		nonceConfig.Type = storeconfig.Memory
	}
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
	logger.Infof("UDP listener (SUDPH) on %s", udpAddr)

	return &built{api: arAPI, store: transportStore, close: func() {
		arAPI.Close()
		_ = udpListener.Close() //nolint:errcheck
	}}, nil
}

// startCXO brings up the AR-bind aggregator and the bindings publisher
// on dmsgC under sk. Both are best-effort; the returned close stops
// whichever came up.
func (s *service) startCXO(ctx context.Context, dmsgC *dmsg.Client, sk cipher.SecKey, b *built, logger *logging.Logger) func() {
	var closers []func()
	// AR-bind-over-CXO aggregator: always-on fan-in path where visors publish
	// their AR bindings as a CXO feed instead of re-registering over a fresh
	// dmsg stream (each a full Noise handshake) on a timer. Inert until visors
	// subscribe (just a listener), purely additive to the authoritative
	// HTTP/UDP bind path, so it needs no gate. Needs the dmsg client; the API
	// is the Sink (IngestBindFromCXO). The node identity is bound to the AR's
	// service SecKey so gated visors accept its subscribe (see #4168).
	// Best-effort — HTTP/UDP registration is unaffected if it fails to start.
	agg, aerr := regcxo.New(dmsgC, sk, b.api, regcxo.Config{Logger: logger})
	if aerr != nil {
		logger.WithError(aerr).Error("Failed to start AR-bind-over-CXO aggregator, continuing without it")
	} else {
		agg.Run(ctx)
		closers = append(closers, func() { _ = agg.Close() }) //nolint:errcheck
		logger.WithField("feed_pk", agg.FeedPK()).
			WithField("port", skyenv.DmsgVisorARBindCXOPort).
			Info("AR-bind-over-CXO aggregator running")
	}

	// CXO bindings publisher: the READ side, keyed by peer public key, so a
	// caller can look one peer's addresses up over an already-open CXO
	// connection with Preview instead of an authenticated HTTP round-trip —
	// and without subscribing to (and holding) the whole set. Additive: GET
	// /resolve is unchanged and stays authoritative, and the feed is inert
	// until something reads it. Best-effort, like the aggregator above.
	bindPub, berr := api.StartBindingsCXOPublisher(dmsgC, sk, b.store, logger)
	if berr != nil {
		logger.WithError(berr).Error("Failed to start CXO bindings publisher, continuing without it")
	} else {
		b.api.SetBindingsCXOPublisher(bindPub)
		closers = append(closers, func() {
			b.api.SetBindingsCXOPublisher(nil)
			_ = bindPub.Close() //nolint:errcheck
		})
	}
	return func() {
		for i := len(closers) - 1; i >= 0; i-- {
			closers[i]()
		}
	}
}

// Embed runs address-resolver inside a host process: the API is
// returned for the host to mount under a path prefix on its own dmsg
// HTTP port, the SUDPH UDP listener opens as configured, and the CXO
// aggregator and publisher run on the host's dmsg client under the
// host's key.
func (s *service) Embed(ctx context.Context, host services.Host) (http.Handler, error) {
	logger := host.Log
	if logger == nil {
		logger = services.NewLogger("address_resolver", s.cfg.LogLevel)
	}
	b, err := s.build(ctx, logger, host.DmsgAddr, false)
	if err != nil {
		return nil, err
	}
	closeCXO := func() {}
	if host.DmsgClient != nil {
		closeCXO = s.startCXO(ctx, host.DmsgClient, host.SK, b, logger)
	}
	go func() {
		<-ctx.Done()
		closeCXO()
		b.close()
	}()
	return b.api, nil
}

func (s *service) Run(ctx context.Context) error {
	cfg := s.cfg

	tag := cfg.Tag
	if tag == "" {
		tag = "address_resolver"
	}
	logger := services.NewLogger(tag, cfg.LogLevel)
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
	surveyWL := deployment.Prod.SurveyWhitelist
	if cfg.TestEnvironment {
		surveyWL = deployment.Test.SurveyWhitelist
	}
	if len(cfg.SurveyWhitelist) > 0 {
		surveyWL = cfg.SurveyWhitelist
	}

	addr := cfg.Addr
	if addr == "" {
		addr = ":9093"
	}

	h, err := svcmode.Start(runCtx, svcmode.Config{
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

	if h.DmsgClient != nil {
		closeCXO := s.startCXO(runCtx, h.DmsgClient, sk, b, logger)
		defer closeCXO()
	}

	select {
	case <-runCtx.Done():
		return nil
	case err := <-h.Errors():
		logger.WithError(err).Error("listener failed")
		return err
	}
}
