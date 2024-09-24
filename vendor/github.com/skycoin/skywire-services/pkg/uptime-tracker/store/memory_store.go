// Package store pkg/uptime-tracker/store/store.go
package store

import (
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/skycoin/skywire-utilities/pkg/geo"
)

type memStore struct {
	visors map[int]map[time.Month]map[string]string
	lastTS map[string]string
	ips    map[string]map[string]string
	mu     sync.RWMutex
}

// NewMemoryStore creates new uptimes memory store.
func NewMemoryStore() Store {
	return &memStore{
		visors: make(map[int]map[time.Month]map[string]string),
		lastTS: make(map[string]string),
		ips:    make(map[string]map[string]string),
	}
}

func (s *memStore) UpdateUptime(pk, ip, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	seconds := UptimeSeconds

	now := time.Now()
	year := now.Year()
	month := now.Month()

	if _, ok := s.visors[year]; !ok {
		s.visors[year] = make(map[time.Month]map[string]string)
	}

	if _, ok := s.visors[year][month]; !ok {
		s.visors[year][month] = make(map[string]string)
	}

	if _, ok := s.ips[fmt.Sprintf("%d:%d", year, month)]; !ok {
		s.ips[fmt.Sprintf("%d:%d", year, month)] = make(map[string]string)
	}

	var uptime int64
	uptimeStr, ok := s.visors[year][month][pk]
	if !ok {
		uptime = int64(seconds)
	} else {
		var err error
		uptime, err = strconv.ParseInt(uptimeStr, 10, 64)
		if err != nil {
			return fmt.Errorf("error parsing old uptime value for visor with PK %s: %w", pk, err)
		}

		uptime += int64(seconds)
	}

	s.visors[year][month][pk] = strconv.FormatInt(uptime, 10)
	s.lastTS[pk] = strconv.FormatInt(now.Unix(), 10)
	if ip != "" {
		s.ips[fmt.Sprintf("%d:%d", year, month)] = map[string]string{pk: ip}
	}

	return nil
}

func (s *memStore) GetAllUptimes(startYear int, startMonth time.Month, endYear int, endMonth time.Month) (UptimeResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	startDate := time.Date(startYear, startMonth, 1, 0, 0, 0, 0, time.Now().Location())
	endDate := time.Date(endYear, endMonth, 1, 0, 0, 0, 0, time.Now().Location())

	var keys []string
	for ; startDate.Before(endDate) || startDate.Equal(endDate); startDate = startDate.AddDate(0, 1, 0) {
		if _, ok := s.visors[startDate.Year()]; !ok {
			continue
		}

		monthUptimes, ok := s.visors[startDate.Year()][startDate.Month()]
		if !ok {
			// no date for this year/month pair, should keep looking
			continue
		}

		for pk := range monthUptimes {
			keys = append(keys, pk)
		}
	}

	return makeUptimeResponse(keys, s.lastTS, map[string]string{}, nil)
}

func (s *memStore) GetUptimes(pubKeys []string, startYear int, startMonth time.Month, endYear int, endMonth time.Month) (UptimeResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	startDate := time.Date(startYear, startMonth, 1, 0, 0, 0, 0, time.Now().Location())
	endDate := time.Date(endYear, endMonth, 1, 0, 0, 0, 0, time.Now().Location())

	var keys []string
	for ; startDate.Before(endDate) || startDate.Equal(endDate); startDate = startDate.AddDate(0, 1, 0) {
		if _, ok := s.visors[startDate.Year()]; !ok {
			continue
		}

		if _, ok := s.visors[startDate.Year()][startDate.Month()]; !ok {
			// no date for this year/month pair, should keep looking
			continue
		}

		for _, pk := range pubKeys {
			_, ok := s.visors[startDate.Year()][startDate.Month()][pk]
			if !ok {
				continue
			}

			keys = append(keys, pk)
		}

	}

	lastTSMap := make(map[string]string)

	for _, pk := range pubKeys {
		ts, ok := s.lastTS[pk]
		if !ok {
			continue
		}

		lastTSMap[pk] = ts
	}

	return makeUptimeResponse(keys, lastTSMap, map[string]string{}, nil)
}

func (s *memStore) GetAllVisors(locDetails geo.LocationDetails) (VisorsResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ips := make(map[string]string)

	now := time.Now()
	startYear, startMonth := now.Year(), now.Month()
	startDate := time.Date(startYear, startMonth, 1, 0, 0, 0, 0, time.Now().Location())

	for pk, ip := range s.ips[fmt.Sprintf("%d:%d", startDate.Year(), startDate.Month())] {
		ips[pk] = ip
	}

	return makeVisorsResponse(ips, locDetails)
}

func (s *memStore) GetVisorsIPs(month string) (map[string]visorIPsResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	response := make(map[string]visorIPsResponse)

	switch month {
	case "all":
		for _, uptimeIP := range s.ips {
			for pk, ip := range uptimeIP {
				response[ip] = visorIPsResponse{Count: response[ip].Count + 1, PublicKeys: append(response[ip].PublicKeys, pk)}
			}
		}
	default:
		if records, ok := s.ips[month]; ok {
			for pk, ip := range records {
				response[ip] = visorIPsResponse{Count: response[ip].Count + 1, PublicKeys: append(response[ip].PublicKeys, pk)}
			}
		} else {
			return response, fmt.Errorf("no record found for requested month")
		}
	}

	return response, nil
}

func (s *memStore) GetNumberOfUptimesInCurrentMonth() (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	now := time.Now()
	startDate := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	endDate := startDate.AddDate(0, 1, 0)

	uptimesCount := 0
	for ; startDate.Before(endDate) || startDate.Equal(endDate); startDate = startDate.AddDate(0, 1, 0) {
		if _, ok := s.visors[startDate.Year()]; !ok {
			continue
		}

		monthUptimes, ok := s.visors[startDate.Year()][startDate.Month()]
		if !ok {
			// no date for this year/month pair, should keep looking
			continue
		}

		uptimesCount = uptimesCount + len(monthUptimes)
	}
	return uptimesCount, nil
}

func (s *memStore) GetNumberOfUptimesByYearAndMonth(year int, month time.Month) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	now := time.Now()
	startDate := time.Date(year, month, 1, 0, 0, 0, 0, now.Location())
	endDate := startDate.AddDate(0, 1, 0)

	uptimesCount := 0
	for ; startDate.Before(endDate) || startDate.Equal(endDate); startDate = startDate.AddDate(0, 1, 0) {
		if _, ok := s.visors[startDate.Year()]; !ok {
			continue
		}

		monthUptimes, ok := s.visors[startDate.Year()][startDate.Month()]
		if !ok {
			// no date for this year/month pair, should keep looking
			continue
		}

		uptimesCount = uptimesCount + len(monthUptimes)
	}
	return uptimesCount, nil
}

func (s *memStore) Close() {

}

func (s *memStore) GetDailyUpdateHistory() (map[string]map[string]string, error) {
	return map[string]map[string]string{}, nil
}

func (s *memStore) DeleteEntries([]DailyUptimeHistory) error {
	return nil
}

func (s *memStore) GetOldestEntry() (DailyUptimeHistory, error) {
	return DailyUptimeHistory{}, nil
}

func (s *memStore) GetSpecificDayData(_ time.Time) ([]DailyUptimeHistory, error) {
	return []DailyUptimeHistory{}, nil
}
