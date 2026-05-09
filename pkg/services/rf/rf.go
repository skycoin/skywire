// Package rf pkg/services/rf/rf.go
//
// route-finder as a pkg/services.Service.
package rf

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/metricsutil"
	"github.com/skycoin/skywire/pkg/route-finder/api"
	"github.com/skycoin/skywire/pkg/services"
	"github.com/skycoin/skywire/pkg/storeconfig"
	"github.com/skycoin/skywire/pkg/svcmode"
	"github.com/skycoin/skywire/pkg/transport-discovery/store"
)

// Type is the registry key used in services.json blocks.
const Type = "route-finder"

const redisScheme = "redis://"

func init() {
	services.Register(Type, factory)
}

// Config is the JSON configuration for route-finder.
type Config struct {
	Path string `json:"-"`

	PubKey cipher.PubKey `json:"public_key,omitempty"`
	SecKey cipher.SecKey `json:"secret_key,omitempty"`

	Addr            string          `json:"addr,omitempty"`
	MetricsAddr     string          `json:"metrics_addr,omitempty"`
	PprofAddr       string          `json:"pprof_addr,omitempty"`
	Redis           string          `json:"redis,omitempty"`
	RedisPoolSize   int             `json:"redis_pool_size,omitempty"`
	LogLevel        string          `json:"log_level,omitempty"`
	Tag             string          `json:"tag,omitempty"`
	Testing         bool            `json:"testing,omitempty"`
	Mode            string          `json:"mode,omitempty"`
	SurveyWhitelist []cipher.PubKey `json:"survey_whitelist,omitempty"`
	TestEnvironment bool            `json:"test_environment,omitempty"`

	// DmsgPort is the dmsghttp listener port (default 80).
	DmsgPort uint16 `json:"dmsg_port,omitempty"`

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

func (s *service) Run(ctx context.Context) error {
	cfg := s.cfg

	tag := cfg.Tag
	if tag == "" {
		tag = "route_finder"
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

	metricsutil.ServePProf(logger, cfg.PprofAddr, "route-finder")

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Route finder uses a longer TTL since it only reads transport data
	// and doesn't need the same expiration as TPD.
	transportStore, err := store.New(runCtx, storeConfig, 10*time.Minute, logger)
	if err != nil {
		return fmt.Errorf("route-finder: init store: %w", err)
	}
	defer transportStore.Close()

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

	dmsgPort := cfg.DmsgPort
	if dmsgPort == 0 {
		dmsgPort = dmsg.DefaultDmsgHTTPPort
	}
	var dmsgAddr string
	if !pk.Null() {
		dmsgAddr = fmt.Sprintf("%s:%d", pk.Hex(), dmsgPort)
	}

	enableMetrics := cfg.MetricsAddr != ""
	rfAPI := api.New(transportStore, logger, enableMetrics, dmsgAddr)

	resolvedMode, err := svcmode.ResolveMode(cfg.Mode, !sk.Null())
	if err != nil {
		return fmt.Errorf("route-finder: invalid mode: %w", err)
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
		addr = ":9092"
	}

	h, err := svcmode.Start(runCtx, svcmode.Config{
		Mode:                resolvedMode,
		HTTPAddr:            addr,
		Handler:             rfAPI,
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
