// Package httpauth pkg/httpauth/redis-store.go
package httpauth

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
)

type redisStore struct {
	client *redis.Client
	prefix string
}

func newRedisStore(ctx context.Context, addr, password, prefix string) (*redisStore, error) {
	opt, err := redis.ParseURL(addr)
	if err != nil {
		return nil, fmt.Errorf("addr: %w", err)
	}

	opt.Password = password
	opt.ReadTimeout = time.Minute
	opt.WriteTimeout = 5 * time.Second
	opt.PoolTimeout = 10 * time.Second
	opt.IdleCheckFrequency = 5 * time.Second
	opt.PoolSize = 200

	if prefix != "" {
		prefix += ":"
	}

	redisCl := redis.NewClient(opt)
	if err := redisCl.Ping(ctx).Err(); err != nil {
		log.Fatalf("Failed to connect to Redis cluster: %v", err)
	}

	store := &redisStore{
		client: redisCl,
		prefix: prefix,
	}

	return store, nil
}

func (s *redisStore) key(v string) string {
	return s.prefix + v
}

func (s *redisStore) Nonce(ctx context.Context, remotePK cipher.PubKey) (Nonce, error) {
	nonce, err := s.client.Get(ctx, s.key(fmt.Sprintf("nonces:%s", remotePK))).Result()
	if err != nil {
		return 0, nil
	}

	n, err := strconv.Atoi(nonce)
	if err != nil {
		return 0, fmt.Errorf("malformed nonce: %s", nonce)
	}
	return Nonce(n), nil
}

func (s *redisStore) IncrementNonce(ctx context.Context, remotePK cipher.PubKey) (Nonce, error) {
	nonce, err := s.client.Incr(ctx, s.key(fmt.Sprintf("nonces:%s", remotePK))).Result()
	if err != nil {
		return 0, fmt.Errorf("redis: %w", err)
	}

	_, err = s.client.SAdd(ctx, s.key("nonces"), remotePK).Result()
	if err != nil {
		return 0, fmt.Errorf("redis: %w", err)
	}

	return Nonce(nonce), nil
}

func (s *redisStore) Count(ctx context.Context) (n int, err error) {
	size, err := s.client.SCard(ctx, s.key("nonces")).Result()
	if err != nil {
		return 0, fmt.Errorf("redis: %w", err)
	}

	return int(size), nil
}
