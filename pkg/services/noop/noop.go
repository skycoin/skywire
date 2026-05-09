// Package noop pkg/services/noop/noop.go
//
// "noop" service — validates the pkg/services framework end-to-end
// without bringing in any real service's dependencies. Used by
// `skywire svc run --config services.json` smoke tests and as the
// minimal example of how a service plugs into the registry.
//
// Block shape:
//
//	{ "type": "noop", "name": "demo", "tick_seconds": 5 }
//
// Behavior: logs "tick" every tick_seconds (default 30) until ctx
// cancels. tick_seconds <= 0 disables ticking — service still runs
// to completion when ctx cancels but produces no output.
package noop

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/services"
)

// Type is the registry key used in services.json.
const Type = "noop"

func init() {
	services.Register(Type, factory)
}

// Config is the JSON shape of a noop block. Fields are deliberately
// minimal — this exists to validate the pipeline, not as a real
// service.
type Config struct {
	Type        string `json:"type"`
	Name        string `json:"name,omitempty"`
	TickSeconds int    `json:"tick_seconds,omitempty"`
}

// service is the runnable instance built from a block. Lowercase
// because operators interact with the type via the Factory + raw
// JSON, not by constructing it directly.
type service struct {
	cfg Config
	log *logging.Logger
}

func factory(raw json.RawMessage, log *logging.Logger) (services.Service, error) {
	var c Config
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("noop: parse config: %w", err)
	}
	return &service{cfg: c, log: log}, nil
}

func (s *service) Run(ctx context.Context) error {
	tick := s.cfg.TickSeconds
	if tick == 0 {
		tick = 30
	}
	if tick < 0 {
		// Negative disables ticking entirely — useful as a
		// "this service exists but does nothing" placeholder.
		s.log.Info("noop service running (no tick)")
		<-ctx.Done()
		return nil
	}
	t := time.NewTicker(time.Duration(tick) * time.Second)
	defer t.Stop()
	s.log.WithField("interval_seconds", tick).Info("noop service ticking")
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			s.log.Info("tick")
		}
	}
}
