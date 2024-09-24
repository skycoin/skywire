package store

import (
	"time"

	"github.com/skycoin/skywire-utilities/pkg/geo"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"gorm.io/gorm"
)

const (
	// UptimeSeconds is a time (in seconds) that for each update request from visor will added to its uptime value
	UptimeSeconds = 300
)

// Store is a wrapper for `UptimeStore`.
type Store interface {
	GetAllUptimes(startYear int, startMonth time.Month, endYear int, endMonth time.Month) (UptimeResponse, error)
	GetUptimes(pubKeys []string, startYear int, startMonth time.Month, endYear int, endMonth time.Month) (UptimeResponse, error)
	GetAllVisors(locDetails geo.LocationDetails) (VisorsResponse, error)
	GetVisorsIPs(month string) (map[string]visorIPsResponse, error)
	GetNumberOfUptimesInCurrentMonth() (int, error)
	GetNumberOfUptimesByYearAndMonth(year int, month time.Month) (int, error)
	UpdateUptime(pk, ip, version string) error
	GetDailyUpdateHistory() (map[string]map[string]string, error)
	DeleteEntries([]DailyUptimeHistory) error
	GetOldestEntry() (DailyUptimeHistory, error)
	GetSpecificDayData(time time.Time) ([]DailyUptimeHistory, error)
	Close()
}

// New constructs a new Store of requested type.
func New(logger *logging.Logger, gormDB *gorm.DB, testing bool) (Store, error) {
	if testing {
		return NewMemoryStore(), nil
	}
	return NewPostgresStore(logger, gormDB)
}
