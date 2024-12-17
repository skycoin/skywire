// Package store pkg/service-discovery/store/postgres_store.go
package store

import (
	"context"
	"fmt"
	"net/http"
	"sync"

	"github.com/chen3feng/safecast"
	"github.com/lib/pq"
	"github.com/sirupsen/logrus"
	"github.com/skycoin/skywire/pkg/servicedisc"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/geo"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"gorm.io/gorm"
)

type postgresStore struct {
	log    logrus.FieldLogger
	client *gorm.DB

	done     chan struct{}
	doneOnce sync.Once
}

func newPostgresStore(cl *gorm.DB, logger *logging.Logger) (*postgresStore, error) {
	if err := cl.AutoMigrate(servicedisc.Service{}); err != nil {
		logger.Warn("failed to complete automigrate process")
		return &postgresStore{}, err
	}

	s := &postgresStore{
		log:    logger,
		client: cl,
		done:   make(chan struct{}),
	}
	return s, nil
}

func (s *postgresStore) Service(_ context.Context, sType string, addr servicedisc.SWAddr) (*servicedisc.Service, *servicedisc.HTTPError) {
	var serviceRecord servicedisc.Service
	addrValue, err := addr.MarshalText()
	if err != nil {
		return &serviceRecord, s.processErr(err, http.StatusInternalServerError)
	}
	if err := s.client.Where("addr = ? AND type = ?", addrValue, sType).First(&serviceRecord).Error; err != nil {
		return &serviceRecord, s.processErr(err, http.StatusInternalServerError)
	}

	return &serviceRecord, nil
}

func (s *postgresStore) Services(_ context.Context, sType, version, country string) ([]servicedisc.Service, *servicedisc.HTTPError) {
	var records []servicedisc.Service
	tx := s.client.Table("services").Where("type = ?", sType)

	if version != "" {
		tx = tx.Where("version = ?", version)
	}

	if country != "" {
		tx = tx.Where("country = ?", country)
	}

	rows, err := tx.Rows()
	if err != nil {
		return records, s.processErr(err, http.StatusInternalServerError)
	}

	for rows.Next() {
		var record servicedisc.Service
		err := tx.ScanRows(rows, &record)
		if err != nil {
			continue
		}
		if !record.DisplayNodeIP {
			var emptyArray pq.StringArray
			record.LocalIPs = emptyArray
		}
		record.DisplayNodeIP = false
		records = append(records, record)
	}

	return records, nil
}

func (s *postgresStore) UpdateService(_ context.Context, se *servicedisc.Service) *servicedisc.HTTPError {
	var serviceRecord servicedisc.Service
	serviceRecord.Addr = se.Addr
	serviceRecord.Type = se.Type

	if se.Geo != nil {
		if se.Geo.Lat != 0 || se.Geo.Lon != 0 || se.Geo.Country != "" || se.Geo.Region != "" {
			serviceRecord.Geo = &geo.LocationData{}
			serviceRecord.Geo.Lat = se.Geo.Lat
			serviceRecord.Geo.Lon = se.Geo.Lon
			serviceRecord.Geo.Country = se.Geo.Country
			serviceRecord.Geo.Region = se.Geo.Region
		}
	}

	serviceRecord.Version = se.Version
	serviceRecord.LocalIPs = se.LocalIPs
	serviceRecord.DisplayNodeIP = se.DisplayNodeIP

	if err := s.storeRecord(serviceRecord); err != nil {
		return s.processErr(err, http.StatusInternalServerError)
	}

	return nil
}

func (s *postgresStore) DeleteService(_ context.Context, sType string, addr servicedisc.SWAddr) *servicedisc.HTTPError {
	if err := s.client.Where("addr LIKE ? AND type = ?", fmt.Sprint("%"+addr.PubKey().String()+"%"), sType).Delete(&servicedisc.Service{}).Error; err != nil {
		return s.processErr(err, http.StatusInternalServerError)
	}
	return nil
}

func (s *postgresStore) CountServiceTypes(_ context.Context) (uint64, error) {
	var countTypes int64
	if err := s.client.Model(&servicedisc.Service{}).Distinct("type").Count(&countTypes).Error; err != nil {
		return uint64(0), fmt.Errorf("Postgres command returned unexpected error: %w", err)
	}

	u, ok := safecast.To[uint64](countTypes)
	if ok {
		return u, nil
	}
	return uint64(0), fmt.Errorf("uint64 conversion overflow")

}

func (s *postgresStore) CountServices(ctx context.Context, serviceType string) (uint64, error) {
	service, sErr := s.Services(ctx, serviceType, "", "")
	if sErr != nil {
		return uint64(0), fmt.Errorf("Postgres command returned unexpected error: %w", sErr)
	}

	return uint64(len(service)), nil
}

func (s *postgresStore) processErr(err error, status int) *servicedisc.HTTPError { //nolint
	if err != nil {
		return &servicedisc.HTTPError{
			HTTPStatus: status,
			Err:        err.Error(),
		}
	}
	return nil
}

func (s *postgresStore) Close() (err error) {
	s.doneOnce.Do(func() {
		close(s.done)
	})
	return err
}

func (s *postgresStore) storeRecord(record servicedisc.Service) error {
	updateTx := s.client.Model(&servicedisc.Service{}).Where("addr LIKE ? AND type = ?", record.Addr.PubKey().String()+"%", record.Type).Updates(&servicedisc.Service{
		Geo:           record.Geo,
		LocalIPs:      record.LocalIPs,
		Version:       record.Version,
		DisplayNodeIP: record.DisplayNodeIP,
	})

	if updateTx.RowsAffected == 1 {
		return nil
	}

	if updateTx.Error != nil || updateTx.RowsAffected == 0 {
		createTX := s.client.Model(&servicedisc.Service{}).Where("addr = ? AND type = ?", record.Addr, record.Type).FirstOrCreate(&record)
		if createTX.RowsAffected == 1 {
			return nil
		}
		return createTX.Error
	}

	return nil
}
