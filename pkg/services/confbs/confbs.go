// Package confbs pkg/services/confbs/confbs.go c2-vis-appsvc
//
// config-bootstrapper as a pkg/services.Service, so a visor can run it
// under its own key.
package confbs

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/skycoin/skywire/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/deployment/conf/api"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/services"
	"github.com/skycoin/skywire/pkg/svcmode"
)

// Type is the registry key used in services.json blocks.
const Type = "config-bootstrapper"

func init() {
	services.Register(Type, factory)
}

// Config is the JSON configuration for config-bootstrapper.
type Config struct {
	services.Common

	// ConfigPath is the file with the stun servers, setup nodes and
	// transport setup keys to serve. A missing file serves the defaults.
	ConfigPath string `json:"config_path,omitempty"`
	// Domain is the domain of the endpoints (default skywire.skycoin.com).
	Domain string             `json:"domain,omitempty"`
	Dmsg   cmdutil.DmsgConfig `json:"dmsg,omitempty"`
}

// ParseBlock decodes a services.json block into a Config.
func ParseBlock(raw []byte) (*Config, error) {
	var c Config
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("config-bootstrapper: parse block: %w", err)
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

func readConfig(path string) (api.Config, error) {
	var c api.Config
	if path == "" {
		return c, nil
	}
	data, err := os.ReadFile(path) //nolint:gosec
	if os.IsNotExist(err) {
		return c, nil
	}
	if err != nil {
		return c, fmt.Errorf("config-bootstrapper: read config %q: %w", path, err)
	}
	if err := json.Unmarshal(data, &c); err != nil {
		return c, fmt.Errorf("config-bootstrapper: parse config %q: %w", path, err)
	}
	return c, nil
}

func (s *service) Run(ctx context.Context) error {
	cfg := s.cfg
	log := services.NewLogger(cfg.LogTag("config_bootstrapper"), cfg.LogLevel)
	defer cfg.StartPprof(log)()

	conf, err := readConfig(cfg.ConfigPath)
	if err != nil {
		return err
	}
	sk, pk := cfg.SecKey, cfg.PubKey
	if pk.Null() && !sk.Null() {
		if pk, err = sk.PubKey(); err != nil {
			return fmt.Errorf("config-bootstrapper: %w", err)
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
	domain := cfg.Domain
	if domain == "" {
		domain = "skywire.skycoin.com"
	}
	conAPI := api.New(log, conf, domain, dmsgAddr)
	defer conAPI.Close()

	mode, err := svcmode.ResolveMode(cfg.Mode, !sk.Null())
	if err != nil {
		return fmt.Errorf("config-bootstrapper: invalid mode: %w", err)
	}
	dmsgDiscDmsg := cfg.Dmsg.DiscoveryDmsg
	if dmsgDiscDmsg == "" {
		dmsgDiscDmsg = dmsg.DiscAddr(false)
	}
	addr := cfg.Addr
	if addr == "" {
		addr = ":9082"
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	h, err := svcmode.Start(runCtx, svcmode.Config{
		Mode:                mode,
		HTTPAddr:            addr,
		Handler:             conAPI,
		PK:                  pk,
		SK:                  sk,
		DmsgPort:            dmsgPort,
		DmsgDiscoveryDmsg:   dmsgDiscDmsg,
		DmsgServerType:      cfg.Dmsg.ServerType,
		EmbeddedDmsgServers: dmsgServers(cfg.Dmsg.Servers),
		SurveyWhitelist:     cfg.SurveyKeys(),
		Log:                 log,
	})
	if err != nil {
		return fmt.Errorf("config-bootstrapper: start listeners: %w", err)
	}
	defer h.Close()

	// The services config as a CXO feed signed by this key, so visors follow
	// deployment key changes without a release.
	if h.DmsgClient != nil {
		sp, err := api.StartServicesCXOPublisher(runCtx, conAPI, h.DmsgClient, sk, log)
		if err != nil {
			log.WithError(err).Warn("CXO services publisher not started")
		} else {
			defer sp.Close() //nolint:errcheck
		}
	}

	select {
	case <-runCtx.Done():
		return nil
	case err := <-h.Errors():
		return err
	}
}

func dmsgServers(configServers []*disc.Entry) []disc.Entry {
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
