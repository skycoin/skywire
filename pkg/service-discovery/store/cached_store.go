// Package store pkg/service-discovery/store/cashed_store.go
package store

import (
	"context"
	"fmt"
	"sync"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"

	"github.com/skycoin/skywire/pkg/servicedisc"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
)

// CashedStore holds entire database in memory and syncs periodically
type CashedStore struct {
	pgStore *postgresStore
	log     logrus.FieldLogger

	services map[string]map[string]*servicedisc.Service
	mu       sync.RWMutex

	done     chan struct{}
	doneOnce sync.Once
}

// NewCachedStore creates a new memory store that holds entire DB in memory
func NewCachedStore(db *gorm.DB, logger *logging.Logger) (*CashedStore, error) {
	pgStore, err := newPostgresStore(db, logger)
	if err != nil {
		return nil, err
	}

	ms := &CashedStore{
		pgStore:  pgStore,
		log:      logger,
		services: make(map[string]map[string]*servicedisc.Service),
		done:     make(chan struct{}),
	}

	if err := ms.reloadFromDB(context.Background()); err != nil {
		return nil, fmt.Errorf("failed to load initial data from database: %w", err)
	}

	logger.Infof("Memory store initialized with %d services", ms.countAllServices())

	return ms, nil
}

// reloadFromDB loads entire database into memory
func (cs *CashedStore) reloadFromDB(ctx context.Context) error {
	var serviceTypes []string
	if err := cs.pgStore.client.Model(&servicedisc.Service{}).Distinct("type").Pluck("type", &serviceTypes).Error; err != nil {
		return fmt.Errorf("failed to get service types: %w", err)
	}

	newServices := make(map[string]map[string]*servicedisc.Service)

	for _, sType := range serviceTypes {
		services, httpErr := cs.pgStore.Services(ctx, sType, "", "")
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
				cs.log.WithError(err).Warnf("Failed to marshal address for service")
				continue
			}
			newServices[sType][string(addrKey)] = service
		}
	}

	cs.mu.Lock()
	cs.services = newServices
	cs.mu.Unlock()

	return nil
}

// Service retrieves a single service from memory
func (cs *CashedStore) Service(_ context.Context, sType string, addr servicedisc.SWAddr) (*servicedisc.Service, *servicedisc.HTTPError) {
	addrKey, err := addr.MarshalText()
	if err != nil {
		return nil, &servicedisc.HTTPError{
			HTTPStatus: 500,
			Err:        err.Error(),
		}
	}

	cs.mu.RLock()
	defer cs.mu.RUnlock()

	if typeMap, ok := cs.services[sType]; ok {
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
func (cs *CashedStore) Services(_ context.Context, sType, version, country string) ([]servicedisc.Service, *servicedisc.HTTPError) {
	cs.mu.RLock()
	defer cs.mu.RUnlock()

	typeMap, ok := cs.services[sType]
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
func (cs *CashedStore) UpdateService(ctx context.Context, se *servicedisc.Service) *servicedisc.HTTPError {
	if httpErr := cs.pgStore.UpdateService(ctx, se); httpErr != nil {
		return httpErr
	}

	addrKey, err := se.Addr.MarshalText()
	if err != nil {
		cs.log.WithError(err).Error("Failed to marshal address after DB update")
		return nil
	}

	cs.mu.Lock()
	defer cs.mu.Unlock()

	if cs.services[se.Type] == nil {
		cs.services[se.Type] = make(map[string]*servicedisc.Service)
	}

	serviceCopy := *se
	cs.services[se.Type][string(addrKey)] = &serviceCopy

	cs.log.Debugf("Updated service in memory: type=%s, addr=%s", se.Type, string(addrKey))

	return nil
}

// DeleteService deletes a service from both memory and database
func (cs *CashedStore) DeleteService(ctx context.Context, sType string, addr servicedisc.SWAddr) *servicedisc.HTTPError {
	if httpErr := cs.pgStore.DeleteService(ctx, sType, addr); httpErr != nil {
		return httpErr
	}

	addrKey, err := addr.MarshalText()
	if err != nil {
		cs.log.WithError(err).Error("Failed to marshal address after DB delete")
		return nil
	}

	cs.mu.Lock()
	defer cs.mu.Unlock()

	pubKey := addr.PubKey().String()
	if typeMap, ok := cs.services[sType]; ok {
		for key := range typeMap {
			if len(key) >= len(pubKey) {
				if key[:len(pubKey)] == pubKey || key == string(addrKey) {
					delete(typeMap, key)
					cs.log.Debugf("Deleted service from memory: type=%s, addr=%s", sType, key)
				}
			}
		}
	}

	return nil
}

// CountServiceTypes counts unique service types from memory
func (cs *CashedStore) CountServiceTypes(_ context.Context) (uint64, error) {
	cs.mu.RLock()
	defer cs.mu.RUnlock()

	return uint64(len(cs.services)), nil
}

// CountServices counts services by type from memory
func (cs *CashedStore) CountServices(_ context.Context, serviceType string) (uint64, error) {
	cs.mu.RLock()
	defer cs.mu.RUnlock()

	if typeMap, ok := cs.services[serviceType]; ok {
		return uint64(len(typeMap)), nil
	}

	return 0, nil
}

// Close stops the periodic reload and closes the database connection
func (cs *CashedStore) Close() error {
	cs.doneOnce.Do(func() {
		close(cs.done)
	})
	return cs.pgStore.Close()
}

// countAllServices returns total number of services in memory (for logging)
func (cs *CashedStore) countAllServices() int {
	count := 0
	for _, typeMap := range cs.services {
		count += len(typeMap)
	}
	return count
}
