package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/go-redis/redis/v8"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire-utilities/pkg/netutil"
	"github.com/skycoin/skywire/pkg/transport/network"
	"github.com/skycoin/skywire/pkg/transport/network/addrresolver"
)

const (
	serviceName = "address-resolver"
)

type redisStore struct {
	client *redis.Client
}

func newRedisStore(ctx context.Context, addr, password string, poolSize int, logger *logging.Logger) (*redisStore, error) {
	opt, err := redis.ParseURL(addr)
	if err != nil {
		return nil, fmt.Errorf("addr: %w", err)
	}

	opt.Password = password

	if poolSize != 0 {
		opt.PoolSize = poolSize
	}
	redisCl := redis.NewClient(opt)

	err = netutil.NewRetrier(logger, netutil.DefaultInitBackoff, netutil.DefaultMaxBackoff, 10, netutil.DefaultFactor).Do(ctx, func() error {
		_, err = redisCl.Ping(ctx).Result()
		return err
	})
	if err != nil {
		return nil, err
	}

	return &redisStore{redisCl}, nil
}

func (s *redisStore) Bind(ctx context.Context, netType network.Type, pk cipher.PubKey, visorData addrresolver.VisorData) error {
	switch netType {
	case network.STCPR, network.SUDPH:
		key := getKey(string(netType), pk)
		return s.bind(ctx, key, visorData)
	default:
		return ErrUnknownTransportType
	}
}

func (s *redisStore) DelBind(ctx context.Context, netType network.Type, pk cipher.PubKey) error {
	switch netType {
	case network.STCPR, network.SUDPH:
		key := getKey(string(netType), pk)
		return s.delBind(ctx, key)
	default:
		return ErrUnknownTransportType
	}
}

func (s *redisStore) Resolve(ctx context.Context, netType network.Type, pk cipher.PubKey) (addrresolver.VisorData, error) {
	switch netType {
	case network.STCPR, network.SUDPH:
		key := getKey(string(netType), pk)
		return s.resolve(ctx, key)
	default:
		return addrresolver.VisorData{}, ErrUnknownTransportType
	}
}

func (s *redisStore) GetAll(ctx context.Context, netType network.Type) ([]string, error) {
	switch netType {
	case network.STCPR, network.SUDPH:
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

	if _, err := s.client.Set(ctx, key, string(raw), 0).Result(); err != nil {
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
