// Package sd pkg/services/sd/sd.go
//
// service-discovery as a pkg/services.Service.
package sd

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/go-redis/redis/v8"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/httpauth"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/metricsutil"
	"github.com/skycoin/skywire/pkg/service-discovery/api"
	sdmetrics "github.com/skycoin/skywire/pkg/service-discovery/metrics"
	"github.com/skycoin/skywire/pkg/service-discovery/store"
	"github.com/skycoin/skywire/pkg/services"
	"github.com/skycoin/skywire/pkg/storeconfig"
	"github.com/skycoin/skywire/pkg/svcmode"
)

// Type is the registry key used in services.json blocks.
const Type = "service-discovery"

const redisPrefix = "service-discovery"

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

func (s *service) Run(ctx context.Context) error {
	cfg := s.cfg
	log := s.log

	pk := cfg.PubKey
	sk := cfg.SecKey
	if pk.Null() && !sk.Null() {
		if derived, err := sk.PubKey(); err != nil {
			log.WithError(err).Warn("No SecKey found. Skipping serving on dmsghttp.")
		} else {
			pk = derived
		}
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	metricsutil.ServePProf(log, cfg.PprofAddr, "service-discovery")

	redisURL := cfg.Redis
	if redisURL == "" {
		redisURL = "redis://localhost:6379"
	}
	redisPassword := storeconfig.RedisPassword()
	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		return fmt.Errorf("service-discovery: parse redis URL: %w", err)
	}
	opt.Password = redisPassword

	redisClient := redis.NewClient(opt)
	if _, err := redisClient.Ping(runCtx).Result(); err != nil {
		return fmt.Errorf("service-discovery: redis ping failed: %w", err)
	}
	log.Printf("Redis connected.")

	db, err := store.NewStore(runCtx, redisClient, log, cfg.EntryTimeout.Std())
	if err != nil {
		return fmt.Errorf("service-discovery: init store: %w", err)
	}
	log.Printf("Service entry timeout: %v", cfg.EntryTimeout)

	var nonceDB httpauth.NonceStore
	if !cfg.TestMode {
		nonceStoreConfig := storeconfig.Config{
			URL:      redisURL,
			Type:     storeconfig.Redis,
			Password: storeconfig.RedisPassword(),
		}
		nonceDB, err = httpauth.NewNonceStore(runCtx, nonceStoreConfig, redisPrefix)
		if err != nil {
			return fmt.Errorf("service-discovery: init nonce store: %w", err)
		}
	}

	metricsutil.ServeHTTPMetrics(log, cfg.MetricsAddr)

	var m sdmetrics.Metrics
	if cfg.MetricsAddr == "" {
		m = sdmetrics.NewEmpty()
	} else {
		m = sdmetrics.NewVictoriaMetrics()
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
	geoipURL := cfg.GeoIP
	if geoipURL == "" {
		geoipURL = deployment.Prod.GeoIP
	}
	sdAPI := api.New(log, db, nonceDB, enableMetrics, m, dmsgAddr, geoipURL)

	for _, k := range cfg.Whitelist {
		k = strings.TrimSpace(k)
		if k != "" {
			api.WhitelistPKs.Set(k)
		}
	}

	go sdAPI.RunBackgroundTasks(runCtx, log)

	resolvedMode, err := svcmode.ResolveMode(cfg.Mode, !sk.Null())
	if err != nil {
		return fmt.Errorf("service-discovery: invalid mode: %w", err)
	}

	dmsgDisc := cfg.Dmsg.Discovery
	if dmsgDisc == "" {
		dmsgDisc = dmsg.DiscURL(false)
	}
	embeddedServers := dmsgDiscEntries(cfg.Dmsg.Servers)
	surveyWL := deployment.Prod.SurveyWhitelist
	if len(cfg.SurveyWhitelist) > 0 {
		surveyWL = cfg.SurveyWhitelist
	}

	addr := cfg.Addr
	if addr == "" {
		addr = ":9098"
	}
	h, err := svcmode.Start(runCtx, svcmode.Config{
		Mode:                resolvedMode,
		HTTPAddr:            addr,
		Handler:             sdAPI,
		PK:                  pk,
		SK:                  sk,
		DmsgPort:            dmsgPort,
		DmsgDiscovery:       dmsgDisc,
		DmsgServerType:      cfg.Dmsg.ServerType,
		EmbeddedDmsgServers: embeddedServers,
		SurveyWhitelist:     surveyWL,
		Log:                 log,
		DisableDHT:          true,
		OnDmsgServersUpdated: func(svrs []string) {
			sdAPI.DmsgServers = svrs
		},
	})
	if err != nil {
		return fmt.Errorf("service-discovery: start listeners: %w", err)
	}
	defer h.Close()

	if h.DmsgClient != nil {
		s.startServicesCXO(runCtx, h.DmsgClient, sdAPI, sk, log)
	}

	select {
	case <-runCtx.Done():
		return nil
	case err := <-h.Errors():
		log.WithError(err).Error("listener failed")
		return err
	}
}

func (s *service) startServicesCXO(
	ctx context.Context,
	dmsgC *dmsg.Client,
	sdAPI *api.API,
	sk cipher.SecKey,
	log *logging.Logger,
) {
	pub, perr := api.StartServicesCXOPublisher(ctx, dmsgC, sk, log)
	if perr != nil {
		log.WithError(perr).Error("Failed to start CXO services publisher, continuing without it")
		return
	}
	sdAPI.SetServicesCXOPublisher(pub)
	go func() {
		<-ctx.Done()
		pub.Close() //nolint:errcheck,gosec
	}()
}
