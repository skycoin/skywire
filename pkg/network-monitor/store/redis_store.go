package store

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/go-redis/redis"

	"github.com/skycoin/skywire/internal/nm"
	"github.com/skycoin/skywire/pkg/cipher"
)

const (
	networkKey = "nm"
	visorKey   = "visor"
)

type redisStore struct {
	client *redis.Client
}

func newRedisStore(addr, password string, poolSize int) (*redisStore, error) {
	opt, err := redis.ParseURL(addr)
	if err != nil {
		return nil, fmt.Errorf("addr: %w", err)
	}

	opt.Password = password
	if poolSize != 0 {
		opt.PoolSize = poolSize
	}
	redisCl := redis.NewClient(opt)

	if err := redisCl.Ping().Err(); err != nil {
		log.Fatalf("Failed to connect to Redis cluster: %v", err)
	}

	return &redisStore{redisCl}, nil
}

func (s *redisStore) AddVisorSummary(_ context.Context, key cipher.PubKey, visorSum *nm.VisorSummary) error {

	data, err := json.Marshal(visorSum)
	if err != nil {
		return err
	}

	if _, err := s.client.Set(nmKey(visorKey, key.String()), string(data), 0).Result(); err != nil {
		return err
	}

	return nil
}

func (s *redisStore) GetVisorByPk(pk string) (entry *nm.VisorSummary, err error) {
	data, err := s.client.Get(nmKey(visorKey, pk)).Result()
	if err != nil {
		return &nm.VisorSummary{}, ErrVisorSumNotFound
	}

	if err := json.Unmarshal([]byte(data), &entry); err != nil {
		return nil, err
	}

	return entry, nil
}

func (s *redisStore) GetAllSummaries() (map[string]nm.Summary, error) {
	var visorKeys []string
	var err error
	response := make(map[string]nm.Summary)

	visorKeys, err = s.client.Keys(s.searchKey(visorKey)).Result()
	if err != nil {
		return response, err
	}

	for _, visorKey := range visorKeys {
		key := strings.Split(visorKey, ":")
		vSum, err := s.GetVisorByPk(key[2])
		if err != nil {
			return response, err
		}
		response[key[2]] = nm.Summary{
			Visor: vSum,
		}
	}

	return response, err
}

func nmKey(t string, key string) string {
	return fmt.Sprintf("nm:%v:%s", t, key)
}

func (s *redisStore) searchKey(key string) string {
	return fmt.Sprintf("*%v:%v*", networkKey, key)
}
