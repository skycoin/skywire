package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/go-redis/redis/v8"

	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire-utilities/pkg/netutil"
	"github.com/skycoin/skywire/internal/lc"
)

const (
	batchSize   = int64(1000)
	serviceName = "liveness-checker"
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

func (s *redisStore) AddServiceSummary(ctx context.Context, key string, visorSum *lc.ServiceSummary) error {

	data, err := json.Marshal(visorSum)
	if err != nil {
		return err
	}

	if _, err := s.client.Set(ctx, s.lcKey(key), string(data), 0).Result(); err != nil {
		return err
	}

	return nil
}

func (s *redisStore) GetServiceByName(ctx context.Context, key string) (sSum *lc.ServiceSummary, err error) {

	data, err := s.client.Get(ctx, s.lcKey(key)).Result()
	if err != nil {
		return nil, ErrServiceNotFound
	}
	if err := json.Unmarshal([]byte(data), &sSum); err != nil {
		return nil, err
	}

	return sSum, nil
}

func (s *redisStore) GetServiceSummaries(ctx context.Context) (map[string]*lc.ServiceSummary, error) {
	response := make(map[string]*lc.ServiceSummary)

	serviceKeys, err := s.getServiceKeys(ctx)
	if err != nil {
		return response, err
	}

	for _, serviceKey := range serviceKeys {
		sSum, err := s.GetServiceByName(ctx, serviceKey)
		if err != nil {
			return response, err
		}
		response[serviceKey] = sSum
	}

	return response, nil
}

func (s *redisStore) getServiceKeys(ctx context.Context) ([]string, error) {
	var pks []string
	var cursor uint64

	iter := s.client.Scan(ctx, cursor, s.searchKey(), batchSize).Iterator()

	for iter.Next(ctx) {
		key := strings.ReplaceAll(iter.Val(), "liveness-checker:", "")
		pks = append(pks, key)
	}

	if err := iter.Err(); err != nil {
		return pks, err
	}

	return pks, nil
}

func (s *redisStore) lcKey(key string) string {
	return fmt.Sprintf("%v:%v", serviceName, key)
}

func (s *redisStore) searchKey() string {
	return fmt.Sprintf("%v*", serviceName)
}
