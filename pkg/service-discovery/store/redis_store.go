// Package store pkg/service-discovery/store/redis_store.go
package store

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"
	jsoniter "github.com/json-iterator/go"
	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/servicedisc"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
)

var json = jsoniter.ConfigFastest

const (
	serviceKeyPrefix   = "service:"
	serviceTypesSetKey = "service_types"
)

type redisStore struct {
	log    logrus.FieldLogger
	client *redis.Client
	ttl    time.Duration

	done     chan struct{}
	doneOnce sync.Once
}

func newRedisStore(ctx context.Context, client *redis.Client, logger *logging.Logger, ttl time.Duration) (*redisStore, error) {
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("redis connection check failed: %w", err)
	}

	s := &redisStore{
		log:    logger,
		client: client,
		ttl:    ttl,
		done:   make(chan struct{}),
	}

	// Start background cleanup of expired set members
	go s.cleanupExpiredServices(ctx)

	return s, nil
}

// cleanupExpiredServices periodically removes expired service keys from the sets
func (s *redisStore) cleanupExpiredServices(ctx context.Context) {
	ticker := time.NewTicker(s.ttl / 2)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.done:
			return
		case <-ticker.C:
			s.removeExpiredFromSets(ctx)
		}
	}
}

// removeExpiredFromSets checks all service type sets and removes members whose keys have expired
func (s *redisStore) removeExpiredFromSets(ctx context.Context) {
	// Get all service types
	serviceTypes, err := s.client.SMembers(ctx, serviceTypesSetKey).Result()
	if err != nil {
		s.log.WithError(err).Warn("Failed to get service types for cleanup")
		return
	}

	for _, sType := range serviceTypes {
		setKey := s.serviceTypeSetKey(sType)
		pubKeys, err := s.client.SMembers(ctx, setKey).Result()
		if err != nil {
			s.log.WithError(err).Warnf("Failed to get members of set %s", setKey)
			continue
		}

		for _, pk := range pubKeys {
			key := fmt.Sprintf("%s%s:%s", serviceKeyPrefix, sType, pk)
			exists, err := s.client.Exists(ctx, key).Result()
			if err != nil {
				continue
			}
			if exists == 0 {
				// Key expired, remove from set
				s.client.SRem(ctx, setKey, pk)
				s.log.Debugf("Removed expired service %s from set %s", pk, setKey)
			}
		}

		// If set is empty, remove it from service_types
		count, _ := s.client.SCard(ctx, setKey).Result() //nolint: errcheck
		if count == 0 {
			s.client.SRem(ctx, serviceTypesSetKey, sType)
		}
	}
}

func (s *redisStore) serviceKey(sType string, addr servicedisc.SWAddr) string {
	return fmt.Sprintf("%s%s:%s", serviceKeyPrefix, sType, addr.PubKey().String())
}

func (s *redisStore) serviceTypeSetKey(sType string) string {
	return fmt.Sprintf("%s%s", serviceKeyPrefix, sType)
}

func (s *redisStore) Service(ctx context.Context, sType string, addr servicedisc.SWAddr) (*servicedisc.Service, *servicedisc.HTTPError) {
	key := s.serviceKey(sType, addr)

	data, err := s.client.Get(ctx, key).Bytes()
	if err != nil {
		if err == redis.Nil {
			return nil, &servicedisc.HTTPError{
				HTTPStatus: http.StatusNotFound,
				Err:        "service not found",
			}
		}
		return nil, s.processErr(err, http.StatusInternalServerError)
	}

	var service servicedisc.Service
	if err := json.Unmarshal(data, &service); err != nil {
		return nil, s.processErr(err, http.StatusInternalServerError)
	}

	return &service, nil
}

func (s *redisStore) Services(ctx context.Context, sType, version, country string) ([]servicedisc.Service, *servicedisc.HTTPError) {
	setKey := s.serviceTypeSetKey(sType)

	pubKeys, err := s.client.SMembers(ctx, setKey).Result()
	if err != nil {
		return nil, s.processErr(err, http.StatusInternalServerError)
	}

	if len(pubKeys) == 0 {
		return []servicedisc.Service{}, nil
	}

	var keys []string
	for _, pk := range pubKeys {
		keys = append(keys, fmt.Sprintf("%s%s:%s", serviceKeyPrefix, sType, pk))
	}

	values, err := s.client.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, s.processErr(err, http.StatusInternalServerError)
	}

	var records []servicedisc.Service
	for i, val := range values {
		if val == nil {
			// Key expired, clean up set (async)
			if i < len(pubKeys) {
				go s.client.SRem(context.Background(), setKey, pubKeys[i])
			}
			continue
		}

		var service servicedisc.Service
		if err := json.Unmarshal([]byte(val.(string)), &service); err != nil {
			s.log.WithError(err).Warn("Failed to unmarshal service")
			continue
		}

		if version != "" && service.Version != version {
			continue
		}
		if country != "" && (service.Geo == nil || service.Geo.Country != country) {
			continue
		}

		if !service.DisplayNodeIP {
			service.LocalIPs = nil
		}
		service.DisplayNodeIP = false

		records = append(records, service)
	}

	return records, nil
}

func (s *redisStore) UpdateService(ctx context.Context, se *servicedisc.Service) *servicedisc.HTTPError {
	key := s.serviceKey(se.Type, se.Addr)
	setKey := s.serviceTypeSetKey(se.Type)

	data, err := json.Marshal(se)
	if err != nil {
		return s.processErr(err, http.StatusInternalServerError)
	}

	// Only apply TTL on re-registration (key already exists).
	// First-time registrations get no TTL for backward compatibility with old visors.
	exists, _ := s.client.Exists(ctx, key).Result() //nolint: errcheck
	ttl := time.Duration(0)
	if exists > 0 {
		ttl = s.ttl
	}

	pipe := s.client.Pipeline()
	pipe.Set(ctx, key, data, ttl)
	pipe.SAdd(ctx, setKey, se.Addr.PubKey().String())
	pipe.SAdd(ctx, serviceTypesSetKey, se.Type)

	if _, err := pipe.Exec(ctx); err != nil {
		return s.processErr(err, http.StatusInternalServerError)
	}

	return nil
}

func (s *redisStore) DeleteService(ctx context.Context, sType string, addr servicedisc.SWAddr) *servicedisc.HTTPError {
	key := s.serviceKey(sType, addr)
	setKey := s.serviceTypeSetKey(sType)
	pubKey := addr.PubKey().String()

	pipe := s.client.Pipeline()
	pipe.Del(ctx, key)
	pipe.SRem(ctx, setKey, pubKey)

	if _, err := pipe.Exec(ctx); err != nil {
		return s.processErr(err, http.StatusInternalServerError)
	}

	return nil
}

func (s *redisStore) CountServiceTypes(ctx context.Context) (uint64, error) {
	count, err := s.client.SCard(ctx, serviceTypesSetKey).Result()
	if err != nil {
		return 0, fmt.Errorf("Redis command returned unexpected error: %w", err)
	}

	if count < 0 {
		return 0, nil
	}
	return uint64(count), nil
}

func (s *redisStore) CountServices(ctx context.Context, serviceType string) (uint64, error) {
	setKey := s.serviceTypeSetKey(serviceType)

	count, err := s.client.SCard(ctx, setKey).Result()
	if err != nil {
		return 0, fmt.Errorf("Redis command returned unexpected error: %w", err)
	}

	if count < 0 {
		return 0, nil
	}
	return uint64(count), nil
}

//nolint:unparam
func (s *redisStore) processErr(err error, status int) *servicedisc.HTTPError {
	if err != nil {
		return &servicedisc.HTTPError{
			HTTPStatus: status,
			Err:        err.Error(),
		}
	}
	return nil
}

func (s *redisStore) Close() (err error) {
	s.doneOnce.Do(func() {
		close(s.done)
	})
	return s.client.Close()
}
