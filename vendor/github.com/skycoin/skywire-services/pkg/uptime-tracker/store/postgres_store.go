package store

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/skycoin/skywire-utilities/pkg/geo"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"gorm.io/gorm"
)

type postgresStore struct {
	log     *logging.Logger
	client  *gorm.DB
	cacheMu sync.RWMutex
	cache   map[string]int64
	closeC  chan struct{}
}

// NewPostgresStore creates new uptimes postgres store.
func NewPostgresStore(logger *logging.Logger, cl *gorm.DB) (Store, error) {
	// automigrate
	if err := cl.AutoMigrate(DailyUptimeHistory{}); err != nil {
		logger.Warn("failed to complete automigrate process")
	}

	s := &postgresStore{
		log:    logger,
		client: cl,
		cache:  make(map[string]int64),
		closeC: make(chan struct{}),
	}
	return s, nil
}

func (s *postgresStore) UpdateUptime(pk, ip, version string) error {
	seconds := UptimeSeconds

	now := time.Now()

	// checking cache for timestamp
	duration := time.Duration(seconds) * time.Second
	roundedTS := now.Round(duration).Unix()
	if prevTS, ok := s.getCache(pk); ok && prevTS == roundedTS {
		// Already seen within the current interval.
		return nil
	}

	// get existing data of daily record
	var dailyUptimeRecord DailyUptimeHistory
	startDailyDate := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	dailyErr := s.client.
		Where("created_at >=  ? AND pub_key = ?", startDailyDate, pk).
		First(&dailyUptimeRecord).Error
	if dailyErr != nil && dailyErr != gorm.ErrRecordNotFound {
		return dailyErr
	}

	if dailyUptimeRecord.PubKey == "" {
		dailyUptimeRecord.PubKey = pk
	}

	dailyUptimeRecord.DailyOnline += seconds
	if ip != "" {
		ips := []string{}
		if len(dailyUptimeRecord.IPs) > 0 {
			ips = strings.Split(dailyUptimeRecord.IPs, ",")
		}
		ips = append(ips, ip)
		dailyUptimeRecord.IPs = uniqueIPs(ips)
		dailyUptimeRecord.LastIP = ip
	}
	dailyUptimeRecord.Version = version
	if err := s.client.Save(&dailyUptimeRecord).Error; err != nil {
		return fmt.Errorf("failed to create/update uptime record: %w", err)
	}

	// update cache
	duration = time.Duration(seconds) * time.Second
	roundedTS = now.Round(duration).Unix()
	s.setCache(pk, roundedTS)

	return nil
}

func (s *postgresStore) GetAllUptimes(startYear int, startMonth time.Month, endYear int, endMonth time.Month) (UptimeResponse, error) {
	startDate := time.Date(startYear, startMonth, 1, 0, 0, 0, 0, time.Now().Location())
	endDate := time.Date(endYear, endMonth, 1, 0, 0, 0, 0, time.Now().Location())

	var keys []string
	lastTSs := make(map[string]string)
	versions := make(map[string]string)
	var murError error
	var uptimesRecords []DailyUptimeHistory
	for ; startDate.Before(endDate) || startDate.Equal(endDate); startDate = startDate.AddDate(0, 1, 0) {
		if err := s.client.Where("created_at BETWEEN ? AND ?", startDate, startDate.AddDate(0, 1, 0).Add(-1*time.Second)).Order("id DESC").Find(&uptimesRecords).Error; err != nil {
			murError = errors.New("failed on fetching data from pg store")
			break
		}
		for _, record := range uptimesRecords {
			if _, ok := lastTSs[record.PubKey]; !ok {
				if lastTSs[record.PubKey] <= fmt.Sprint(record.UpdatedAt.Unix()) {
					lastTSs[record.PubKey] = fmt.Sprint(record.UpdatedAt.Unix())
				}
				versions[record.PubKey] = record.Version
				keys = append(keys, record.PubKey)
			}
		}
	}

	return makeUptimeResponse(keys, lastTSs, versions, murError)
}

func (s *postgresStore) GetUptimes(pubKeys []string, startYear int, startMonth time.Month, endYear int, endMonth time.Month) (UptimeResponse, error) {
	startDate := time.Date(startYear, startMonth, 1, 0, 0, 0, 0, time.Now().Location())
	endDate := time.Date(endYear, endMonth, 1, 0, 0, 0, 0, time.Now().Location())

	var keys []string
	versions := make(map[string]string)
	var uptimesRecords []DailyUptimeHistory
	var murError error
	lastTSs := make(map[string]string)
	for ; startDate.Before(endDate) || startDate.Equal(endDate); startDate = startDate.AddDate(0, 1, 0) {
		if err := s.client.Where("created_at BETWEEN ? AND ? AND pub_key = ?", startDate, startDate.AddDate(0, 1, 0).Add(-1*time.Second), pubKeys).Order("id DESC").Find(&uptimesRecords).Error; err != nil {
			murError = errors.New("failed on fetching data from pg store")
		}
		for _, record := range uptimesRecords {
			if _, ok := lastTSs[record.PubKey]; !ok {
				if lastTSs[record.PubKey] <= fmt.Sprint(record.UpdatedAt.Unix()) {
					lastTSs[record.PubKey] = fmt.Sprint(record.UpdatedAt.Unix())
				}
				versions[record.PubKey] = record.Version
				keys = append(keys, record.PubKey)
			}
		}
	}

	return makeUptimeResponse(keys, lastTSs, versions, murError)
}

func (s *postgresStore) GetAllVisors(locDetails geo.LocationDetails) (VisorsResponse, error) {
	ips := make(map[string]string)
	var uptimesRecords []DailyUptimeHistory

	now := time.Now()
	startYear, startMonth := now.Year(), now.Month()
	startDate := time.Date(startYear, startMonth, 1, 0, 0, 0, 0, time.Now().Location())
	if err := s.client.Where("created_at >= ?", startDate).Order("id DESC").Find(&uptimesRecords).Error; err != nil {
		return VisorsResponse{}, err
	}

	for _, record := range uptimesRecords {
		if _, ok := ips[record.PubKey]; !ok {
			ips[record.PubKey] = record.LastIP
		}
	}

	return makeVisorsResponse(ips, locDetails)
}

func (s *postgresStore) GetDailyUpdateHistory() (map[string]map[string]string, error) {
	var uptimesRecords []DailyUptimeHistory
	now := time.Now()
	startDate := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Now().Location()).AddDate(0, 0, -7)
	if err := s.client.Where("created_at >= ?", startDate).Find(&uptimesRecords).Error; err != nil {
		return map[string]map[string]string{}, err
	}
	result := make(map[string]map[string]string)
	for _, record := range uptimesRecords {
		if result[record.PubKey] == nil {
			result[record.PubKey] = make(map[string]string)
		}
		if 100*float64(record.DailyOnline)/(60*60*24) > 100 {
			result[record.PubKey][record.CreatedAt.Format("2006-01-02")] = "100"
		} else {
			result[record.PubKey][record.CreatedAt.Format("2006-01-02")] = fmt.Sprintf("%.2f", 100*float64(record.DailyOnline)/(60*60*24))
		}
	}
	return result, nil
}

func (s *postgresStore) GetVisorsIPs(monthValue string) (map[string]visorIPsResponse, error) {
	var timeValue time.Time

	if monthValue != "all" {
		monthSlice := strings.Split(monthValue, ":")
		if len(monthSlice) != 2 {
			return nil, fmt.Errorf("malformed month request")
		}
		year, err := strconv.Atoi(monthSlice[0])
		if err != nil {
			return nil, fmt.Errorf("malformed month request")
		}
		month, err := strconv.Atoi(monthSlice[1])
		if err != nil {
			return nil, fmt.Errorf("malformed month request")
		}

		timeValue = time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.Now().Location())
	}

	ipMap, err := s.readAllUptimeIPMembers(timeValue)
	if err != nil {
		return nil, err
	}

	response := make(map[string]visorIPsResponse)

	for pk, ip := range ipMap {
		response[ip] = visorIPsResponse{Count: response[ip].Count + 1, PublicKeys: append(response[ip].PublicKeys, pk)}
	}

	return response, nil
}

type visorIPsResponse struct {
	Count      int      `json:"count"`
	PublicKeys []string `json:"public_keys"`
}

func (s *postgresStore) Close() {
	close(s.closeC)
}

func (s *postgresStore) readAllUptimeIPMembers(timeValue time.Time) (map[string]string, error) {
	var uptimesRecords []DailyUptimeHistory
	response := make(map[string]string)

	if timeValue.IsZero() {
		if err := s.client.Order("id DESC").Find(&uptimesRecords).Error; err != nil {
			return response, err
		}
	} else {
		if err := s.client.Where("created_at BETWEEN ? AND ?", timeValue, timeValue.AddDate(0, 1, 0).Add(-1*time.Second)).Order("id DESC").Find(&uptimesRecords).Error; err != nil {
			return response, err
		}
	}

	for _, record := range uptimesRecords {
		if _, ok := response[record.PubKey]; !ok {
			response[record.PubKey] = record.LastIP
		}
	}

	if len(response) == 0 {
		return response, fmt.Errorf("no record found for requested month")
	}

	return response, nil
}

func (s *postgresStore) getCache(pk string) (int64, bool) {
	s.cacheMu.RLock()
	defer s.cacheMu.RUnlock()

	v, ok := s.cache[pk]
	return v, ok
}

func (s *postgresStore) setCache(pk string, ts int64) {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()

	s.cache[pk] = ts
}

func (s *postgresStore) GetNumberOfUptimesInCurrentMonth() (int, error) {
	var counter int64
	now := time.Date(time.Now().Year(), time.Now().Month(), 1, 0, 0, 0, 0, time.Now().Location())
	err := s.client.Model(&DailyUptimeHistory{}).Where("created_at BETWEEN ? AND ?", now, now.AddDate(0, 1, 0).Add(-1*time.Second)).Group("pub_key").Count(&counter).Error
	return int(counter), err
}

func (s *postgresStore) GetNumberOfUptimesByYearAndMonth(year int, month time.Month) (int, error) {
	var counter int64
	timeValue := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.Now().Location())
	err := s.client.Model(&DailyUptimeHistory{}).Where("created_at BETWEEN ? AND ?", timeValue, timeValue.AddDate(0, 1, 0).Add(-1*time.Second)).Group("pub_key").Count(&counter).Error
	return int(counter), err
}

func (s *postgresStore) DeleteEntries(data []DailyUptimeHistory) error {
	for _, entry := range data {
		err := s.client.Delete(&DailyUptimeHistory{}, entry.ID).Error
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *postgresStore) GetOldestEntry() (DailyUptimeHistory, error) {
	var data DailyUptimeHistory
	err := s.client.Limit(1).Order("created_at asc").Find(&data).Error
	return data, err
}

func (s *postgresStore) GetSpecificDayData(timeValue time.Time) ([]DailyUptimeHistory, error) {
	var data []DailyUptimeHistory
	err := s.client.Where("created_at BETWEEN ? AND ?", timeValue, timeValue.AddDate(0, 0, 1).Add(-1*time.Second)).Find(&data).Error
	return data, err
}

// DailyUptimeHistory is gorm.Model for daily uptime history table
type DailyUptimeHistory struct {
	ID          uint `gorm:"primarykey" json:"-"`
	CreatedAt   time.Time
	UpdatedAt   time.Time
	PubKey      string
	DailyOnline int
	Version     string
	IPs         string
	LastIP      string
}

func uniqueIPs(ips []string) string {
	keys := make(map[string]bool)
	uniqueList := []string{}
	for _, entry := range ips {
		if _, value := keys[entry]; !value {
			keys[entry] = true
			uniqueList = append(uniqueList, entry)
		}
	}

	return strings.Join(uniqueList, ",")
}
