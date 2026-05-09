// Package visor pkg/services/visor/visor.go
//
// skywire-visor as a pkg/services.Service. Lets `skywire svc run`
// host a visor alongside the deployment services (or alongside
// other visors) in one process. Useful for:
//   - laptop / single-host all-in-one demos
//   - CI e2e collapses where one visor can co-locate with the
//     services container
//   - embedded / hackathon deployments where running multiple
//     binaries is awkward
//
// Block schema is `{ "type": "visor", "config_path": "..." }`. The
// visor's own JSON config is too large to comfortably embed inline
// in services.json (~200 lines of nested fields), so this wrapper
// only supports the file-path indirection. The standalone
// `skywire-visor` and `skywire visor` commands are unchanged.
package visor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/services"
	"github.com/skycoin/skywire/pkg/visor"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// Type is the registry key used in services.json blocks.
const Type = "visor"

func init() {
	services.Register(Type, factory)
}

// Config is the JSON shape of a visor block in services.json.
type Config struct {
	// ConfigPath is the path to the visor's own JSON config file.
	// Required — the visor config is too large to embed inline.
	ConfigPath string `json:"config_path"`
}

// ParseBlock decodes a services.json block into a Config.
func ParseBlock(raw []byte) (*Config, error) {
	var c Config
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("visor: parse block: %w", err)
	}
	if c.ConfigPath == "" {
		return nil, errors.New("visor: config_path is required")
	}
	return &c, nil
}

func factory(raw json.RawMessage, log *logging.Logger) (services.Service, error) {
	cfg, err := ParseBlock(raw)
	if err != nil {
		return nil, err
	}
	return &service{cfg: cfg, log: log}, nil
}

type service struct {
	cfg *Config
	log *logging.Logger
}

// Run loads the visor's config file and hands off to the visor's
// main run loop with the supervisor's ctx as the parent. When the
// supervisor cancels its ctx (SIGINT, sibling-service error), the
// visor unwinds via the same SignalContext path the standalone
// binary uses.
func (s *service) Run(ctx context.Context) error {
	path := filepath.Clean(s.cfg.ConfigPath)
	f, err := os.Open(path) //nolint:gosec
	if err != nil {
		return fmt.Errorf("visor: open config %q: %w", path, err)
	}
	defer f.Close() //nolint:errcheck

	bi := buildinfo.Get()
	conf, compat, err := visorconfig.Parse(s.log, f, path, bi)
	if err != nil {
		return fmt.Errorf("visor: parse config %q: %w", path, err)
	}
	if !compat {
		return fmt.Errorf("visor: config %q version is incompatible with this binary", path)
	}
	return visor.Run(ctx, conf)
}
