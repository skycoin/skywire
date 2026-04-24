package store

import (
	"context"
	"io"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/skycoin/skywire/pkg/servicedisc"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
)

//go:generate mockery --name Store --case underscore --inpackage

// VisorSummary holds a visor's uptime data for the SD /uptimes endpoint.
// v1: pk + online. v2 adds version + daily percentages. v3 adds timeline bitmaps.
type VisorSummary struct {
	PK       cipher.PubKey     `json:"pk"`
	Online   bool              `json:"on"`
	Version  string            `json:"version,omitempty"`
	Daily    map[string]string `json:"daily,omitempty"`
	Timeline map[string]string `json:"timeline,omitempty"`
}

// Store represents a DB implementation.
type Store interface {
	io.Closer
	Service(ctx context.Context, sType string, addr servicedisc.SWAddr) (*servicedisc.Service, *servicedisc.HTTPError)
	Services(ctx context.Context, sType, version, country string) ([]servicedisc.Service, *servicedisc.HTTPError)
	// ServicesByPK returns every service registration for a visor PK.
	// Used by the DHT mirror path so it can publish the visor's FULL
	// service list (vpn, skysocks, visor) under one DHT target rather
	// than overwriting the target with whichever type wrote last.
	ServicesByPK(ctx context.Context, pk cipher.PubKey) ([]servicedisc.Service, *servicedisc.HTTPError)
	UpdateService(ctx context.Context, se *servicedisc.Service) *servicedisc.HTTPError
	UpdateServiceAndHeartbeat(ctx context.Context, se *servicedisc.Service, version string) *servicedisc.HTTPError
	DeleteService(ctx context.Context, sType string, addr servicedisc.SWAddr) *servicedisc.HTTPError
	CountServiceTypes(ctx context.Context) (uint64, error)
	CountServices(ctx context.Context, serviceType string) (uint64, error)
	RecordHeartbeat(ctx context.Context, pk cipher.PubKey, version string) error
	GetAllVisorSummaries(ctx context.Context, v2 bool, timeline bool) ([]VisorSummary, error)
	GetDailyTimeline(ctx context.Context, pkHex string, now time.Time) map[string]string
}

// NewStore creates a new Redis store with TTL for service entries.
// The ttl parameter specifies how long service entries live before expiring.
// Services must send heartbeats before the TTL expires to stay registered.
func NewStore(ctx context.Context, client *redis.Client, logger *logging.Logger, ttl time.Duration) (Store, error) {
	return newRedisStore(ctx, client, logger, ttl)
}
