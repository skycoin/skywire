// Package dht pkg/dht/backend_redis.go
//
// RedisBackend persists DHT items to Redis. Suitable for deployment
// services that already run Redis and need fast startup with full
// dataset intact.
package dht

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/go-redis/redis/v8"
)

const redisKeyPrefix = "dht:"

// RedisBackend implements Backend using Redis.
type RedisBackend struct {
	client *redis.Client
	// ttl is the Redis key TTL. Items are refreshed on each Save.
	// Set to 0 for no expiry (deployment services should use 0 since
	// the DHT store handles its own TTL/eviction).
	ttl time.Duration
}

// NewRedisBackend creates a Redis-backed DHT persistence layer.
// addr is the Redis address (e.g., "localhost:6379").
// password is empty for unauthenticated connections.
// db is the Redis database number.
// ttl is the key TTL (0 = no expiry).
func NewRedisBackend(addr, password string, db int, ttl time.Duration) (*RedisBackend, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: password,
		DB:       db,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("dht redis: ping: %w", err)
	}
	return &RedisBackend{client: client, ttl: ttl}, nil
}

func (r *RedisBackend) redisKey(target NodeID) string {
	return redisKeyPrefix + fmt.Sprintf("%x", target[:])
}

// Save persists a single item.
func (r *RedisBackend) Save(target NodeID, item MutableItem) error {
	data, err := json.Marshal(item)
	if err != nil {
		return fmt.Errorf("dht redis: marshal: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return r.client.Set(ctx, r.redisKey(target), data, r.ttl).Err()
}

// Delete removes a single item.
func (r *RedisBackend) Delete(target NodeID) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return r.client.Del(ctx, r.redisKey(target)).Err()
}

// Load returns all persisted items by scanning the dht: key prefix.
func (r *RedisBackend) Load() (map[NodeID]MutableItem, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Collect all keys first, then fetch values in pipeline batches.
	var allKeys []string
	var cursor uint64
	for {
		keys, nextCursor, err := r.client.Scan(ctx, cursor, redisKeyPrefix+"*", 1000).Result()
		if err != nil {
			return nil, fmt.Errorf("dht redis: scan: %w", err)
		}
		allKeys = append(allKeys, keys...)
		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}

	items := make(map[NodeID]MutableItem, len(allKeys))
	// Pipeline in batches of 500 to avoid memory spikes.
	const batchSize = 500
	for i := 0; i < len(allKeys); i += batchSize {
		end := i + batchSize
		if end > len(allKeys) {
			end = len(allKeys)
		}
		batch := allKeys[i:end]
		pipe := r.client.Pipeline()
		cmds := make([]*redis.StringCmd, len(batch))
		for j, key := range batch {
			cmds[j] = pipe.Get(ctx, key)
		}
		_, _ = pipe.Exec(ctx) //nolint:errcheck
		for j, cmd := range cmds {
			data, err := cmd.Bytes()
			if err != nil {
				continue
			}
			var item MutableItem
			if err := json.Unmarshal(data, &item); err != nil {
				continue
			}
			hexStr := batch[j][len(redisKeyPrefix):]
			var id NodeID
			if len(hexStr) == len(id)*2 {
				for k := 0; k < len(id); k++ {
					fmt.Sscanf(hexStr[k*2:k*2+2], "%02x", &id[k]) //nolint:errcheck,gosec
				}
			}
			items[id] = item
		}
	}
	return items, nil
}

// Close closes the Redis client.
func (r *RedisBackend) Close() error {
	return r.client.Close()
}
