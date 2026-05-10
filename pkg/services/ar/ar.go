// Package ar pkg/services/ar/ar.go
//
// address-resolver as a pkg/services.Service.
package ar

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/xtaci/kcp-go"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/address-resolver/api"
	armetrics "github.com/skycoin/skywire/pkg/address-resolver/metrics"
	"github.com/skycoin/skywire/pkg/address-resolver/store"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/httpauth"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/metricsutil"
	"github.com/skycoin/skywire/pkg/services"
	"github.com/skycoin/skywire/pkg/storeconfig"
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

func (s *service) Run(ctx context.Context) error {
	cfg := s.cfg

	tag := cfg.Tag
	if tag == "" {
		tag = "address_resolver"
	}
	logger := logging.MustGetLogger(tag)
	if cfg.LogLevel != "" {
		if lvl, err := logging.LevelFromString(cfg.LogLevel); err == nil {
			logging.SetLevel(lvl)
		}
	}
	_ = s.log // logger is replaced with a tag-scoped one

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

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	transportStore, err := store.New(runCtx, storeConfig, cfg.EntryTimeout.Std(), logger)
	if err != nil {
		return fmt.Errorf("address-resolver: init store: %w", err)
	}

	for _, k := range cfg.Whitelist {
		k = strings.TrimSpace(k)
		if k != "" {
			api.WhitelistPKs.Set(k)
		}
	}

	nonceStore, err := httpauth.NewNonceStore(runCtx, storeConfig, redisPrefix)
	if err != nil {
		return fmt.Errorf("address-resolver: init nonce store: %w", err)
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

	var m armetrics.Metrics
	if cfg.MetricsAddr == "" {
		m = armetrics.NewEmpty()
	} else {
		m = armetrics.NewVictoriaMetrics()
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
	arAPI := api.New(logger, transportStore, nonceStore, enableMetrics, m, dmsgAddr, cfg.PublicUDPAddr)

	udpAddr := cfg.UDPAddr
	if udpAddr == "" {
		udpAddr = ":30178"
	}
	udpListener, err := kcp.Listen(udpAddr)
	if err != nil {
		return fmt.Errorf("address-resolver: open UDP listener on %s: %w", udpAddr, err)
	}

	go arAPI.ListenUDP(udpListener)
	logger.Infof("UDP listener (SUDPH) on %s", udpAddr)

	resolvedMode, err := svcmode.ResolveMode(cfg.Mode, !sk.Null())
	if err != nil {
		return fmt.Errorf("address-resolver: invalid mode: %w", err)
	}

	dmsgDisc := cfg.Dmsg.Discovery
	if dmsgDisc == "" {
		dmsgDisc = dmsg.DiscURL(false)
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
		DmsgDiscovery:       dmsgDisc,
		DmsgServerType:      cfg.Dmsg.ServerType,
		EmbeddedDmsgServers: embeddedServers,
		SurveyWhitelist:     surveyWL,
		Log:                 logger,
		DisableDHT:          true,
		OnDmsgServersUpdated: func(svrs []string) {
			arAPI.DmsgServers = svrs
		},
	})
	if err != nil {
		return fmt.Errorf("address-resolver: start listeners: %w", err)
	}
	defer h.Close()

	defer arAPI.Close()
	select {
	case <-runCtx.Done():
		return nil
	case err := <-h.Errors():
		logger.WithError(err).Error("listener failed")
		return err
	}
}
