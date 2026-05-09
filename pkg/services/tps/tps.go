// Package tps pkg/services/tps/tps.go
//
// transport-setup as a pkg/services.Service. Listens on dmsg RPC
// for visors and exposes an HTTP API to manage transports remotely.
//
// Like setup-node, the block can either embed config.Config inline
// or point at a file via ConfigPath. E2E uses the file path; small
// all-in-one deployments can embed inline.
package tps

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/services"
	"github.com/skycoin/skywire/pkg/transport-setup/api"
	"github.com/skycoin/skywire/pkg/transport-setup/config"
)

// Type is the registry key used in services.json blocks.
const Type = "transport-setup"

func init() {
	services.Register(Type, factory)
}

// Config is the JSON configuration for transport-setup.
type Config struct {
	config.Config

	// ConfigPath, when non-empty, is the path to a JSON file
	// containing a config.Config. File wins over inline.
	ConfigPath string `json:"config_path,omitempty"`
	LogLevel   string `json:"log_level,omitempty"`
	Tag        string `json:"tag,omitempty"`
}

// LoadFile reads a transport-setup config file.
func LoadFile(path string) (*config.Config, error) {
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("transport-setup: read config %q: %w", path, err)
	}
	var c config.Config
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("transport-setup: parse config %q: %w", path, err)
	}
	return &c, nil
}

// ParseBlock decodes a services.json block into a Config.
func ParseBlock(raw []byte) (*Config, error) {
	var c Config
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("transport-setup: parse block: %w", err)
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
		tag = "transport_setup"
	}
	logger := logging.MustGetLogger(tag)
	if cfg.LogLevel != "" {
		if lvl, err := logging.LevelFromString(cfg.LogLevel); err == nil {
			logging.SetLevel(lvl)
		}
	}
	_ = s.log

	conf := &cfg.Config
	if cfg.ConfigPath != "" {
		fileConf, err := LoadFile(cfg.ConfigPath)
		if err != nil {
			return err
		}
		conf = fileConf
	}
	if conf.PK.Null() || conf.SK.Null() {
		return errors.New("transport-setup: public_key and secret_key required (inline or via config_path)")
	}

	tpsAPI := api.New(logger, *conf)

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	go func() {
		logger.Info("Starting dmsg RPC listener")
		if err := tpsAPI.ServeDmsg(runCtx); err != nil {
			if runCtx.Err() == nil {
				logger.WithError(err).Error("Dmsg server error")
			}
		}
	}()

	srv := &http.Server{
		Addr:              fmt.Sprintf(":%d", conf.Port),
		ReadHeaderTimeout: 2 * time.Second,
		IdleTimeout:       30 * time.Second,
		Handler:           tpsAPI,
	}
	logger.WithField("addr", srv.Addr).Info("Starting HTTP server")

	go func() { //nolint:gosec
		<-runCtx.Done()
		// Shutdown uses a fresh ctx because the parent is already
		// canceled — the timeout governs how long to wait for
		// in-flight requests to drain before forcing the listener
		// closed.
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			logger.WithError(err).Error("HTTP server shutdown error")
		}
	}()

	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Errorf("ListenAndServe: %v", err)
		return err
	}
	return nil
}
