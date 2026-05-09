// Package stun pkg/services/stun/stun.go
//
// stun-server as a pkg/services.Service. Implements RFC 3489 NAT
// discovery; requires two distinct IPs for full NAT-type detection
// (the `change-address` STUN attribute path needs the secondary).
package stun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	skycoinlogging "github.com/skycoin/skycoin/src/util/logging"

	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/services"
	"github.com/skycoin/skywire/pkg/stunserver"
)

// Type is the registry key used in services.json blocks.
const Type = "stun-server"

func init() {
	services.Register(Type, factory)
}

// Config is the JSON configuration for the STUN server. Tiny —
// stun-server has no redis, no dmsg, no svcmode listener.
type Config struct {
	PrimaryIP   string `json:"primary_ip"`
	SecondaryIP string `json:"secondary_ip"`
	Port        int    `json:"port,omitempty"`     // default 3478
	AltPort     int    `json:"alt_port,omitempty"` // default 3479
	LogLevel    string `json:"log_level,omitempty"`
	Tag         string `json:"tag,omitempty"`
}

// ParseBlock decodes a services.json block into a Config.
func ParseBlock(raw []byte) (*Config, error) {
	var c Config
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("stun-server: parse block: %w", err)
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
	if cfg.PrimaryIP == "" || cfg.SecondaryIP == "" {
		return errors.New("stun-server: both primary_ip and secondary_ip are required")
	}
	port := cfg.Port
	if port == 0 {
		port = 3478
	}
	altPort := cfg.AltPort
	if altPort == 0 {
		altPort = 3479
	}

	tag := cfg.Tag
	if tag == "" {
		tag = "stun"
	}
	// stunserver.Server.Log uses skycoin's logging package, not
	// pkg/logging — supply a separate logger instance for it.
	skyLogger := skycoinlogging.MustGetLogger(tag)
	logger := logging.MustGetLogger(tag)
	if cfg.LogLevel != "" {
		if lvl, err := logging.LevelFromString(cfg.LogLevel); err == nil {
			logging.SetLevel(lvl)
		}
	}
	_ = s.log // logger is replaced with a tag-scoped one
	_ = logger

	srv := &stunserver.Server{
		PrimaryIP:   cfg.PrimaryIP,
		SecondaryIP: cfg.SecondaryIP,
		PrimaryPort: port,
		AltPort:     altPort,
		Software:    "skywire-stun",
		Log:         skyLogger,
	}
	if err := srv.Start(ctx); err != nil {
		return fmt.Errorf("stun-server: start: %w", err)
	}
	return nil
}
