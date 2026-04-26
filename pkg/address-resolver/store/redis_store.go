package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/netutil"
	"github.com/skycoin/skywire/pkg/transport/network/addrresolver"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

const (
	serviceName = "address-resolver"
)

type redisStore struct {
	client      *redis.Client
	ttl         time.Duration
	getAllCache *getAllCache
}

func newRedisStore(ctx context.Context, addr, password string, poolSize int, ttl time.Duration, logger *logging.Logger) (*redisStore, error) {
	opt, err := redis.ParseURL(addr)
	if err != nil {
		return nil, fmt.Errorf("addr: %w", err)
	}

	opt.Password = password

	if poolSize != 0 {
		opt.PoolSize = poolSize
	}

	opt.MinIdleConns = poolSize / 2          // Keep half the pool warm
	opt.MaxConnAge = 30 * time.Minute        // Max connection lifetime
	opt.PoolTimeout = 10 * time.Second       // Wait up to 10s for connection from pool
	opt.IdleTimeout = 5 * time.Minute        // Close idle connections after 5min
	opt.IdleCheckFrequency = 1 * time.Minute // How often to check for idle connections
	opt.DialTimeout = 5 * time.Second
	opt.ReadTimeout = 5 * time.Second
	opt.WriteTimeout = 5 * time.Second

	redisCl := redis.NewClient(opt)

	err = netutil.NewRetrier(logger, netutil.DefaultInitBackoff, netutil.DefaultMaxBackoff, 10, netutil.DefaultFactor).Do(ctx, func() error {
		_, err = redisCl.Ping(ctx).Result()
		return err
	})
	if err != nil {
		return nil, err
	}

	return &redisStore{
		client:      redisCl,
		ttl:         ttl,
		getAllCache: newGetAllCache(defaultGetAllCacheTTL),
	}, nil
}

func (s *redisStore) Bind(ctx context.Context, netType types.Type, pk cipher.PubKey, visorData addrresolver.VisorData) error {
	switch netType {
	case types.STCPR, types.SUDPH:
		return s.bindWithIndex(ctx, netType, pk, visorData)
	default:
		return ErrUnknownTransportType
	}
}

func (s *redisStore) DelBind(ctx context.Context, netType types.Type, pk cipher.PubKey) error {
	switch netType {
	case types.STCPR, types.SUDPH:
		return s.delBindWithIndex(ctx, netType, pk)
	default:
		return ErrUnknownTransportType
	}
}

func (s *redisStore) Resolve(ctx context.Context, netType types.Type, pk cipher.PubKey) (addrresolver.VisorData, error) {
	switch netType {
	case types.STCPR, types.SUDPH:
		key := getKey(string(netType), pk)
		return s.resolve(ctx, key)
	default:
		return addrresolver.VisorData{}, ErrUnknownTransportType
	}
}

func (s *redisStore) GetAll(ctx context.Context, netType types.Type) ([]string, error) {
	switch netType {
	case types.STCPR, types.SUDPH:
		if pks, ok := s.getAllCache.Get(netType); ok {
			return pks, nil
		}
		pks, err := s.getAllFromIndex(ctx, netType)
		if err != nil {
			return pks, err
		}
		s.getAllCache.Put(netType, pks)
		return pks, nil
	default:
		return nil, ErrUnknownTransportType
	}
}

func (s *redisStore) resolve(ctx context.Context, key string) (addrresolver.VisorData, error) {
	raw, err := s.client.Get(ctx, key).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return addrresolver.VisorData{}, ErrNoEntry
		}

		return addrresolver.VisorData{}, err
	}

	var data addrresolver.VisorData
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		return addrresolver.VisorData{}, err
	}

	return data, nil
}

// bindWithIndex pipelines the per-PK Set with an SAdd to the per-netType
// index set. The SAdd is idempotent on re-bind (90 s refresh cadence), so
// the index converges to the live set without explicit dedup.
func (s *redisStore) bindWithIndex(ctx context.Context, netType types.Type, pk cipher.PubKey, visorData addrresolver.VisorData) error {
	raw, err := json.Marshal(visorData)
	if err != nil {
		return err
	}
	pipe := s.client.Pipeline()
	pipe.Set(ctx, getKey(string(netType), pk), string(raw), s.ttl)
	pipe.SAdd(ctx, indexKey(string(netType)), pk.String())
	_, err = pipe.Exec(ctx)
	return err
}

// delBindWithIndex pipelines the per-PK Del with an SRem from the
// per-netType index set so the index doesn't keep stale members after
// an explicit deregister.
func (s *redisStore) delBindWithIndex(ctx context.Context, netType types.Type, pk cipher.PubKey) error {
	pipe := s.client.Pipeline()
	pipe.Del(ctx, getKey(string(netType), pk))
	pipe.SRem(ctx, indexKey(string(netType)), pk.String())
	_, err := pipe.Exec(ctx)
	return err
}

// getAllFromIndex reads the per-netType index set, then verifies each
// member's primary key still exists (TTL-evicted bindings can leave
// stale members behind when a visor crashes without DelBind). Stale
// members are SREM'd asynchronously so the next read is clean —
// mirrors the lazy-SREM pattern in pkg/service-discovery's ServicesByPK
// and the SD index lookup added in #2339.
//
// Replaces the old SCAN COUNT=30000 over address-resolver:<type>:* —
// at ~150K total redis keys, the SCAN was the largest source of redis
// CPU on AR's host even after the cache from #2343 collapsed bursts.
func (s *redisStore) getAllFromIndex(ctx context.Context, netType types.Type) ([]string, error) {
	idx := indexKey(string(netType))
	members, err := s.client.SMembers(ctx, idx).Result()
	if err != nil {
		return nil, err
	}
	if len(members) == 0 {
		return nil, nil
	}

	keys := make([]string, len(members))
	for i, m := range members {
		keys[i] = fmt.Sprintf("%s:%s:%s", serviceName, netType, m)
	}

	const existsBatch = 256
	live := make([]string, 0, len(members))
	var stale []interface{}
	for i := 0; i < len(keys); i += existsBatch {
		end := i + existsBatch
		if end > len(keys) {
			end = len(keys)
		}
		batchKeys := keys[i:end]
		batchPKs := members[i:end]
		// Issue one EXISTS per key in a pipeline so we can tell which
		// individual keys are missing (multi-key EXISTS only returns a
		// count). EXISTS is O(1) per key; the pipeline batches them
		// into a single round-trip.
		pipe := s.client.Pipeline()
		cmds := make([]*redis.IntCmd, len(batchKeys))
		for j, k := range batchKeys {
			cmds[j] = pipe.Exists(ctx, k)
		}
		if _, err := pipe.Exec(ctx); err != nil {
			return live, err
		}
		for j, c := range cmds {
			if c.Val() == 1 {
				live = append(live, batchPKs[j])
			} else {
				stale = append(stale, batchPKs[j])
			}
		}
	}

	if len(stale) > 0 {
		// Fire-and-forget: stale members will get cleaned on the next
		// read regardless, but doing it now keeps the index from
		// growing unbounded under churn. context.Background is
		// intentional — the request ctx may be canceled by the time
		// this runs, and the cleanup is bounded (single SREM with a
		// small stale slice).
		go func() { //nolint:gosec // intentional bg ctx, see comment above
			s.client.SRem(context.Background(), idx, stale...) //nolint:errcheck
		}()
	}

	return live, nil
}

func getKey(kind string, pk cipher.PubKey) string {
	return fmt.Sprintf("%s:%s:%s", serviceName, kind, pk.String())
}

func indexKey(kind string) string {
	return fmt.Sprintf("%s:%s:_index", serviceName, kind)
}
