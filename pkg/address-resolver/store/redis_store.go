package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/netutil"
	"github.com/skycoin/skywire/pkg/transport/network/addrresolver"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

const (
	serviceName = "address-resolver"
)

type redisStore struct {
	client *redis.Client
	ttl    time.Duration
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

	return &redisStore{client: redisCl, ttl: ttl}, nil
}

func (s *redisStore) Bind(ctx context.Context, netType types.Type, pk cipher.PubKey, visorData addrresolver.VisorData) error {
	switch netType {
	case types.STCPR, types.SUDPH:
		key := getKey(string(netType), pk)
		return s.bind(ctx, key, visorData)
	default:
		return ErrUnknownTransportType
	}
}

func (s *redisStore) DelBind(ctx context.Context, netType types.Type, pk cipher.PubKey) error {
	switch netType {
	case types.STCPR, types.SUDPH:
		key := getKey(string(netType), pk)
		return s.delBind(ctx, key)
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
		key := getScanKey(string(netType))
		return s.getAll(ctx, key)
	default:
		return nil, ErrUnknownTransportType
	}
}

func (s *redisStore) bind(ctx context.Context, key string, visorData addrresolver.VisorData) error {
	raw, err := json.Marshal(visorData)
	if err != nil {
		return err
	}

	// Only apply TTL on re-registration (key already exists).
	// First-time registrations get no TTL for backward compatibility with old visors.
	exists, _ := s.client.Exists(ctx, key).Result() //nolint:errcheck
	ttl := time.Duration(0)
	if exists > 0 {
		ttl = s.ttl
	}

	if _, err := s.client.Set(ctx, key, string(raw), ttl).Result(); err != nil {
		return err
	}

	return nil
}

func (s *redisStore) delBind(ctx context.Context, key string) error {
	if _, err := s.client.Del(ctx, key).Result(); err != nil {
		return err
	}
	return nil
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

func (s *redisStore) getAll(ctx context.Context, key string) ([]string, error) {
	var pks []string
	var cursor uint64
	// todo(erichkaestner): return to reasonable batch size
	// after the old keys are cleaned out by network-monitor
	iter := s.client.Scan(ctx, cursor, key, 30000).Iterator()

	for iter.Next(ctx) {
		key := strings.Split(iter.Val(), ":")
		pks = append(pks, key[2])
	}

	if err := iter.Err(); err != nil {
		return pks, err
	}

	return pks, nil
}

func getKey(kind string, pk cipher.PubKey) string {
	return fmt.Sprintf("%s:%s:%s", serviceName, kind, pk.String())
}

func getScanKey(kind string) string {
	return fmt.Sprintf("%s:%s*", serviceName, kind)
}
