// Package geoip pkg/services/geoip/geoip.go c2-vis-appsvc
//
// The GeoIP lookup API as a pkg/services.Service, so a visor can embed it
// instead of running `skywire svc ip --api` in a container of its own.
//
// Block shape:
//
//	{ "type": "geoip", "addr": "127.0.0.1:9081" }
//
// Embedded, it answers over the visor's dmsg HTTP under /geoip, and on
// addr when one is given. Run standalone, it serves addr, by default :9081.
// "db" names an mmdb file to use instead of the embedded database.
package geoip

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/oschwald/geoip2-golang/v2"

	geodb "github.com/skycoin/skywire/pkg/geoip"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/services"
)

// Type is the registry key used in services.json blocks.
const Type = "geoip"

// DefaultAddr is where a standalone geoip service listens without "addr".
const DefaultAddr = ":9081"

func init() {
	services.Register(Type, factory)
}

// Config is the JSON shape of a geoip block.
type Config struct {
	Addr string `json:"addr,omitempty"`
	DB   string `json:"db,omitempty"`
}

type service struct {
	cfg Config
	log *logging.Logger
}

func factory(raw json.RawMessage, log *logging.Logger) (services.Service, error) {
	var c Config
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("geoip: parse block: %w", err)
	}
	return &service{cfg: c, log: log}, nil
}

// open uses the block's database file, or the one built into the binary,
// which the visor shares with its other lookups.
func (s *service) open() (*geoip2.Reader, error) {
	if s.cfg.DB != "" {
		return geoip2.Open(s.cfg.DB)
	}
	return geodb.Shared()
}

// Embed implements services.Embeddable.
func (s *service) Embed(_ context.Context, _ services.Host) (http.Handler, error) {
	db, err := s.open()
	if err != nil {
		return nil, fmt.Errorf("geoip: open database: %w", err)
	}
	return geodb.Handler(db), nil
}

// Run serves the API on its own until ctx ends.
func (s *service) Run(ctx context.Context) error {
	db, err := s.open()
	if err != nil {
		return fmt.Errorf("geoip: open database: %w", err)
	}
	addr := s.cfg.Addr
	if addr == "" {
		addr = DefaultAddr
	}
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: geodb.Handler(db), ReadHeaderTimeout: 10 * time.Second}
	go func() { //nolint:gosec // the shutdown runs after ctx has ended, so it cannot use it
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx) //nolint:errcheck
	}()
	s.log.WithField("addr", addr).Info("GeoIP API listening")
	if err := srv.Serve(lis); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
