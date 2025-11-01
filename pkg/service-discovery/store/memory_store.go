// Package store pkg/service-discovery/store/memory_store.go
package store

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"

	"github.com/skycoin/skywire/pkg/servicedisc"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
)

// MemoryStore holds entire database in memory and syncs periodically
type MemoryStore struct {
	pgStore      *postgresStore
	log          logrus.FieldLogger
	reloadPeriod time.Duration

	services map[string]map[string]*servicedisc.Service
	mu       sync.RWMutex

	done     chan struct{}
	doneOnce sync.Once
}

// NewMemoryStore creates a new memory store that holds entire DB in memory
func NewMemoryStore(db *gorm.DB, logger *logging.Logger, reloadPeriod time.Duration) (*MemoryStore, error) {
	pgStore, err := newPostgresStore(db, logger)
	if err != nil {
		return nil, err
	}

	if reloadPeriod == 0 {
		reloadPeriod = 5 * time.Minute // default reload period
	}

	ms := &MemoryStore{
		pgStore:      pgStore,
		log:          logger,
		reloadPeriod: reloadPeriod,
		services:     make(map[string]map[string]*servicedisc.Service),
		done:         make(chan struct{}),
	}

	if err := ms.reloadFromDB(context.Background()); err != nil {
		return nil, fmt.Errorf("failed to load initial data from database: %w", err)
	}

	go ms.periodicReload()

	logger.Infof("Memory store initialized with %d services, reload period: %v", ms.countAllServices(), reloadPeriod)

	return ms, nil
}

// periodicReload reloads entire database into memory every reloadPeriod
func (ms *MemoryStore) periodicReload() {
	ticker := time.NewTicker(ms.reloadPeriod)
	defer ticker.Stop()

	for {
		select {
		case <-ms.done:
			ms.log.Info("Stopping periodic reload")
			return
		case <-ticker.C:
			ms.log.Debug("Starting periodic reload from database")
			if err := ms.reloadFromDB(context.Background()); err != nil {
				ms.log.WithError(err).Error("Failed to reload from database")
			} else {
				ms.log.Debugf("Successfully reloaded database into memory, total services: %d", ms.countAllServices())
			}
		}
	}
}

// reloadFromDB loads entire database into memory
func (ms *MemoryStore) reloadFromDB(ctx context.Context) error {
	var serviceTypes []string
	if err := ms.pgStore.client.Model(&servicedisc.Service{}).Distinct("type").Pluck("type", &serviceTypes).Error; err != nil {
		return fmt.Errorf("failed to get service types: %w", err)
	}

	newServices := make(map[string]map[string]*servicedisc.Service)

	for _, sType := range serviceTypes {
		services, httpErr := ms.pgStore.Services(ctx, sType, "", "")
		if httpErr != nil {
			return fmt.Errorf("failed to load services for type %s: %s", sType, httpErr.Err)
		}

		if newServices[sType] == nil {
			newServices[sType] = make(map[string]*servicedisc.Service)
		}

		for i := range services {
			service := &services[i]
			addrKey, err := service.Addr.MarshalText()
			if err != nil {
				ms.log.WithError(err).Warnf("Failed to marshal address for service")
				continue
			}
			newServices[sType][string(addrKey)] = service
		}
	}

	ms.mu.Lock()
	ms.services = newServices
	ms.mu.Unlock()

	return nil
}

// Service retrieves a single service from memory
func (ms *MemoryStore) Service(_ context.Context, sType string, addr servicedisc.SWAddr) (*servicedisc.Service, *servicedisc.HTTPError) {
	addrKey, err := addr.MarshalText()
	if err != nil {
		return nil, &servicedisc.HTTPError{
			HTTPStatus: 500,
			Err:        err.Error(),
		}
	}

	ms.mu.RLock()
	defer ms.mu.RUnlock()

	if typeMap, ok := ms.services[sType]; ok {
		if service, ok := typeMap[string(addrKey)]; ok {
			serviceCopy := *service
			return &serviceCopy, nil
		}
	}

	return nil, &servicedisc.HTTPError{
		HTTPStatus: 404,
		Err:        "service not found",
	}
}

// Services retrieves services from memory with optional filtering
func (ms *MemoryStore) Services(_ context.Context, sType, version, country string) ([]servicedisc.Service, *servicedisc.HTTPError) {
	ms.mu.RLock()
	defer ms.mu.RUnlock()

	typeMap, ok := ms.services[sType]
	if !ok {
		return []servicedisc.Service{}, nil
	}

	var result []servicedisc.Service
	for _, service := range typeMap {
		if version != "" && service.Version != version {
			continue
		}
		if country != "" && (service.Geo == nil || service.Geo.Country != country) {
			continue
		}

		serviceCopy := *service
		result = append(result, serviceCopy)
	}

	return result, nil
}

// UpdateService updates a service in both memory and database
func (ms *MemoryStore) UpdateService(ctx context.Context, se *servicedisc.Service) *servicedisc.HTTPError {
	if httpErr := ms.pgStore.UpdateService(ctx, se); httpErr != nil {
		return httpErr
	}

	addrKey, err := se.Addr.MarshalText()
	if err != nil {
		ms.log.WithError(err).Error("Failed to marshal address after DB update")
		return nil
	}

	ms.mu.Lock()
	defer ms.mu.Unlock()

	if ms.services[se.Type] == nil {
		ms.services[se.Type] = make(map[string]*servicedisc.Service)
	}

	serviceCopy := *se
	ms.services[se.Type][string(addrKey)] = &serviceCopy

	ms.log.Debugf("Updated service in memory: type=%s, addr=%s", se.Type, string(addrKey))

	return nil
}

// DeleteService deletes a service from both memory and database
func (ms *MemoryStore) DeleteService(ctx context.Context, sType string, addr servicedisc.SWAddr) *servicedisc.HTTPError {
	if httpErr := ms.pgStore.DeleteService(ctx, sType, addr); httpErr != nil {
		return httpErr
	}

	addrKey, err := addr.MarshalText()
	if err != nil {
		ms.log.WithError(err).Error("Failed to marshal address after DB delete")
		return nil
	}

	ms.mu.Lock()
	defer ms.mu.Unlock()

	pubKey := addr.PubKey().String()
	if typeMap, ok := ms.services[sType]; ok {
		for key := range typeMap {
			if len(key) >= len(pubKey) {
				if key[:len(pubKey)] == pubKey || key == string(addrKey) {
					delete(typeMap, key)
					ms.log.Debugf("Deleted service from memory: type=%s, addr=%s", sType, key)
				}
			}
		}
	}

	return nil
}

// CountServiceTypes counts unique service types from memory
func (ms *MemoryStore) CountServiceTypes(_ context.Context) (uint64, error) {
	ms.mu.RLock()
	defer ms.mu.RUnlock()

	return uint64(len(ms.services)), nil
}

// CountServices counts services by type from memory
func (ms *MemoryStore) CountServices(_ context.Context, serviceType string) (uint64, error) {
	ms.mu.RLock()
	defer ms.mu.RUnlock()

	if typeMap, ok := ms.services[serviceType]; ok {
		return uint64(len(typeMap)), nil
	}

	return 0, nil
}

// Close stops the periodic reload and closes the database connection
func (ms *MemoryStore) Close() error {
	ms.doneOnce.Do(func() {
		close(ms.done)
	})
	return ms.pgStore.Close()
}

// countAllServices returns total number of services in memory (for logging)
func (ms *MemoryStore) countAllServices() int {
	count := 0
	for _, typeMap := range ms.services {
		count += len(typeMap)
	}
	return count
}
