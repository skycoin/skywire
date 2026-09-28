// Package sd pkg/services/sd/sd.go c2-vis-appsvc
//
// service-discovery as a pkg/services.Service.
package sd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/node"
	"github.com/skycoin/skywire/pkg/cxo/storeconfig"
	"github.com/skycoin/skywire/pkg/deployment/sd/api"
	sdmetrics "github.com/skycoin/skywire/pkg/deployment/sd/metrics"
	"github.com/skycoin/skywire/pkg/deployment/sd/regcxo"
	"github.com/skycoin/skywire/pkg/deployment/sd/store"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/httpauth"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/metricsutil"
	"github.com/skycoin/skywire/pkg/services"
	"github.com/skycoin/skywire/pkg/skyenv"
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

	// state, set by build and startCXO, is reported by State.
	nonceStore string
	cxo        services.CXOSet
}

// State implements services.Stater. Service discovery always stores in
// redis.
func (s *service) State() services.State {
	st := services.State{Store: "redis", NonceStore: s.nonceStore}
	s.cxo.State(&st)
	return st
}

// build connects redis, creates the store, the nonce store and the API,
// and starts the API's background tasks. dmsgAddr is what the API
// reports on /health; plainHTTP says whether a plain-HTTP surface will
// be served, the only path that needs a durable nonce store.
func (s *service) build(ctx context.Context, log *logging.Logger, dmsgAddr string, plainHTTP bool) (*api.API, error) {
	cfg := s.cfg

	metricsutil.ServePProf(log, cfg.PprofAddr, "service-discovery")

	redisURL := cfg.Redis
	if redisURL == "" {
		redisURL = "redis://localhost:6379"
	}
	redisPassword := storeconfig.RedisPassword()
	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("service-discovery: parse redis URL: %w", err)
	}
	opt.Password = redisPassword

	redisClient := redis.NewClient(opt)
	if _, err := redisClient.Ping(ctx).Result(); err != nil {
		return nil, fmt.Errorf("service-discovery: redis ping failed: %w", err)
	}
	log.Printf("Redis connected.")

	db, err := store.NewStore(ctx, redisClient, log, cfg.EntryTimeout.Std())
	if err != nil {
		return nil, fmt.Errorf("service-discovery: init store: %w", err)
	}
	log.Printf("Service entry timeout: %v", cfg.EntryTimeout)

	// Requests over dmsg are authenticated by the stream's key; only a
	// plain-HTTP surface checks nonces, so only that needs them durable. The
	// store must exist either way: without it the API serves no nonce route
	// (clients fetch one before their first request, so none can register)
	// and skips the check that an entry's key is the caller's.
	var nonceDB httpauth.NonceStore
	s.nonceStore = "none"
	if !cfg.TestMode {
		nonceStoreConfig := storeconfig.Config{Type: storeconfig.Memory}
		if plainHTTP {
			nonceStoreConfig = storeconfig.Config{
				URL:      redisURL,
				Type:     storeconfig.Redis,
				Password: storeconfig.RedisPassword(),
			}
		}
		s.nonceStore = services.StoreKind(nonceStoreConfig.Type)
		nonceDB, err = httpauth.NewNonceStore(ctx, nonceStoreConfig, redisPrefix)
		if err != nil {
			return nil, fmt.Errorf("service-discovery: init nonce store: %w", err)
		}
	}

	metricsutil.ServeHTTPMetrics(log, cfg.MetricsAddr)

	var m sdmetrics.Metrics
	if cfg.MetricsAddr == "" {
		m = sdmetrics.NewEmpty()
	} else {
		m = sdmetrics.NewVictoriaMetrics()
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

	go sdAPI.RunBackgroundTasks(ctx, log)
	return sdAPI, nil
}

// startCXO brings up the services publisher and the SD-registration
// aggregator (visors publish their live service entries as a CXO feed
// instead of re-POSTing them over a fresh dmsg stream every 90 s), on dmsgC
// under sk, until ctx ends. When embedded, host lends the visor's node for
// the registration port. Each piece is best-effort.
func (s *service) startCXO(ctx context.Context, dmsgC *dmsg.Client, host services.CXOHost, sdAPI *api.API, sk cipher.SecKey, log *logging.Logger) {
	s.startServicesCXO(ctx, dmsgC, sdAPI, sk, log)
	s.cxo.StartAggregator(ctx, host, log, "sd-reg", skyenv.DmsgVisorSDRegCXOPort, func(n *node.Node) (services.Aggregator, error) {
		return regcxo.New(dmsgC, sk, sdAPI, regcxo.Config{Node: n, Logger: log})
	})
}

// Embed runs service-discovery inside a host process: the API is
// returned for the host to mount under a path prefix on its own dmsg
// HTTP port, and the CXO publisher and aggregator run on the host's
// dmsg client under the host's key. Nothing listens.
func (s *service) Embed(ctx context.Context, host services.Host) (http.Handler, error) {
	log := host.Log
	if log == nil {
		log = s.log
	}
	sdAPI, err := s.build(ctx, log, host.DmsgAddr, false)
	if err != nil {
		return nil, err
	}
	if host.DmsgClient != nil {
		s.startCXO(ctx, host.DmsgClient, host.CXO, sdAPI, host.SK, log)
	}
	return sdAPI, nil
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

	dmsgPort := cfg.DmsgPort
	if dmsgPort == 0 {
		dmsgPort = dmsg.DefaultDmsgHTTPPort
	}
	var dmsgAddr string
	if !pk.Null() {
		dmsgAddr = fmt.Sprintf("%s:%d", pk.Hex(), dmsgPort)
	}

	sdAPI, err := s.build(runCtx, log, dmsgAddr, true)
	if err != nil {
		return err
	}

	resolvedMode, err := svcmode.ResolveMode(cfg.Mode, !sk.Null())
	if err != nil {
		return fmt.Errorf("service-discovery: invalid mode: %w", err)
	}

	// dmsg-only discovery: see pkg/services/rf/rf.go for rationale.
	dmsgDiscDmsg := cfg.Dmsg.DiscoveryDmsg
	if dmsgDiscDmsg == "" {
		dmsgDiscDmsg = dmsg.DiscAddr(false)
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
		DmsgDiscoveryDmsg:   dmsgDiscDmsg,
		DmsgServerType:      cfg.Dmsg.ServerType,
		EmbeddedDmsgServers: embeddedServers,
		SurveyWhitelist:     surveyWL,
		Log:                 log,
		OnDmsgServersUpdated: func(svrs []string) {
			sdAPI.DmsgServers = svrs
		},
	})
	if err != nil {
		return fmt.Errorf("service-discovery: start listeners: %w", err)
	}
	defer h.Close()

	if h.DmsgClient != nil {
		s.startCXO(runCtx, h.DmsgClient, nil, sdAPI, sk, log)
	}

	select {
	case <-runCtx.Done():
		return nil
	case err := <-h.Errors():
		log.WithError(err).Error("listener failed")
		return err
	}
}

// servicesCXORepublishInterval is how often the SD services CXO feed
// is fully republished from the store so a fresh, fillable head Root is
// always available to newly-connecting subscribers. Matches the TPD
// uptime publisher cadence (60s); SD's redis state refreshes on the
// visor heartbeat cycle so faster ticks would just republish identical
// Roots.
const servicesCXORepublishInterval = 60 * time.Second

func (s *service) startServicesCXO(
	ctx context.Context,
	dmsgC *dmsg.Client,
	sdAPI *api.API,
	sk cipher.SecKey,
	log *logging.Logger,
) {
	pub, perr := api.StartServicesCXOPublisher(ctx, dmsgC, sk, log)
	s.cxo.AddPublisher(ctx, log, "services", skyenv.DmsgSDServicesCXOPort, pub, perr)
	if perr != nil {
		return
	}
	sdAPI.SetServicesCXOPublisher(pub)
	// Pre-warm the publisher's tree from the existing redis store so
	// the publisher's first Root carries the active service list
	// instead of being empty until the next register/heartbeat event
	// — without this a subscriber that connects in the post-restart
	// gap times out at firstSyncTimeout.
	sdAPI.WarmCXOFromStore(ctx)
	// Then republish the full service list on a ticker. Reactive
	// register/deregister events alone leave the feed with no fresh
	// Root during content-idle windows, and a cold-redis startup can
	// leave the initial warm empty — in both cases a fresh subscriber
	// finds no servable head Root (LastRoot returns nothing, or a Root
	// whose objects the cleanup sweep has since reclaimed) and times
	// out, falling back to dmsg-http. A periodic full republish keeps a
	// fresh, fully object-backed (fillable) head Root available at all
	// times and self-heals a cold-start miss — the same pattern the TPD
	// uptime / metrics / all-transports publishers use.
	go func() {
		t := time.NewTicker(servicesCXORepublishInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				sdAPI.WarmCXOFromStore(ctx)
			}
		}
	}()
}

// AggregatorPorts implements services.CXOAggregating.
func (s *service) AggregatorPorts() []uint16 {
	return []uint16{skyenv.DmsgVisorSDRegCXOPort}
}
