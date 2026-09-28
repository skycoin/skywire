// Package store pkg/deployment/sd/store/memory_store.go c4-net-discovery
package store

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/servicedisc"
)

// memoryStore is the in-process Store: the same semantics as the redis
// store (entries expire ttl after their last update, heartbeats keep seven
// days of daily counts and 5-minute timelines), for a test network or a
// deployment that runs without redis.
type memoryStore struct {
	ttl time.Duration

	mu sync.Mutex
	// services[type][pkHex] is a registration and its expiry.
	services map[string]map[string]memEntry
	// uptime[pkHex][date] is a visor's heartbeat record for that day.
	uptime map[string]map[string]*memUptime
}

type memEntry struct {
	svc     servicedisc.Service
	expires time.Time
}

type memUptime struct {
	count    int
	version  string
	timeline [timelineSlots]bool
}

// NewMemoryStore returns a Store kept in memory. ttl is how long an entry
// lives without an update; zero means entries never expire.
func NewMemoryStore(ttl time.Duration) Store {
	return &memoryStore{
		ttl:      ttl,
		services: make(map[string]map[string]memEntry),
		uptime:   make(map[string]map[string]*memUptime),
	}
}

func (s *memoryStore) live(e memEntry, now time.Time) bool {
	return e.expires.IsZero() || now.Before(e.expires)
}

func notFound() *servicedisc.HTTPError {
	return &servicedisc.HTTPError{HTTPStatus: http.StatusNotFound, Err: "service not found"}
}

func (s *memoryStore) Service(_ context.Context, sType string, addr servicedisc.SWAddr) (*servicedisc.Service, *servicedisc.HTTPError) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.services[sType][addr.PubKey().String()]
	if !ok || !s.live(e, time.Now()) {
		return nil, notFound()
	}
	svc := e.svc
	return &svc, nil
}

func (s *memoryStore) Services(_ context.Context, sType, version, country string) ([]servicedisc.Service, *servicedisc.HTTPError) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	out := []servicedisc.Service{}
	for _, e := range s.services[sType] {
		if !s.live(e, now) {
			continue
		}
		svc := e.svc
		if version != "" && svc.Version != version {
			continue
		}
		if country != "" && (svc.Geo == nil || svc.Geo.Country != country) {
			continue
		}
		if !svc.DisplayNodeIP {
			svc.LocalIPs = nil
		}
		svc.DisplayNodeIP = false
		out = append(out, svc)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Addr.PubKey().Hex() < out[j].Addr.PubKey().Hex() })
	return out, nil
}

func (s *memoryStore) ServicesByPK(_ context.Context, pk cipher.PubKey) ([]servicedisc.Service, *servicedisc.HTTPError) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	var out []servicedisc.Service
	for _, byPK := range s.services {
		if e, ok := byPK[pk.String()]; ok && s.live(e, now) {
			out = append(out, e.svc)
		}
	}
	return out, nil
}

func (s *memoryStore) UpdateService(_ context.Context, se *servicedisc.Service) *servicedisc.HTTPError {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.put(se, time.Now())
	return nil
}

func (s *memoryStore) put(se *servicedisc.Service, now time.Time) {
	byPK, ok := s.services[se.Type]
	if !ok {
		byPK = make(map[string]memEntry)
		s.services[se.Type] = byPK
	}
	e := memEntry{svc: *se}
	if s.ttl > 0 {
		e.expires = now.Add(s.ttl)
	}
	byPK[se.Addr.PubKey().String()] = e
}

func (s *memoryStore) UpdateServiceAndHeartbeat(_ context.Context, se *servicedisc.Service, version string) *servicedisc.HTTPError {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.put(se, now)
	s.heartbeat(se.Addr.PubKey().Hex(), version, now.UTC())
	return nil
}

func (s *memoryStore) DeleteService(_ context.Context, sType string, addr servicedisc.SWAddr) *servicedisc.HTTPError {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.services[sType], addr.PubKey().String())
	return nil
}

func (s *memoryStore) CountServiceTypes(_ context.Context) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	var n uint64
	for _, byPK := range s.services {
		for _, e := range byPK {
			if s.live(e, now) {
				n++
				break
			}
		}
	}
	return n, nil
}

func (s *memoryStore) CountServices(_ context.Context, serviceType string) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	var n uint64
	for _, e := range s.services[serviceType] {
		if s.live(e, now) {
			n++
		}
	}
	return n, nil
}

func (s *memoryStore) RecordHeartbeat(_ context.Context, pk cipher.PubKey, version string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.heartbeat(pk.Hex(), version, time.Now().UTC())
	return nil
}

// heartbeat records one heartbeat and drops days past the history window.
// Must be called with s.mu held.
func (s *memoryStore) heartbeat(pkHex, version string, now time.Time) {
	days, ok := s.uptime[pkHex]
	if !ok {
		days = make(map[string]*memUptime)
		s.uptime[pkHex] = days
	}
	date := now.Format("2006-01-02")
	u, ok := days[date]
	if !ok {
		u = &memUptime{}
		days[date] = u
	}
	u.count++
	u.version = version
	u.timeline[currentTimelineSlot(now)] = true
	oldest := now.AddDate(0, 0, -(uptimeHistoryDays + 1)).Format("2006-01-02")
	for d := range days {
		if d < oldest {
			delete(days, d)
		}
	}
}

func (s *memoryStore) GetAllVisorSummaries(_ context.Context, v2 bool, timeline bool) ([]VisorSummary, error) {
	now := time.Now().UTC()
	today := now.Format("2006-01-02")
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []VisorSummary{}
	for pkHex, days := range s.uptime {
		u, ok := days[today]
		if !ok {
			continue
		}
		var pk cipher.PubKey
		if err := pk.UnmarshalText([]byte(pkHex)); err != nil {
			continue
		}
		sum := VisorSummary{PK: pk, Online: s.online(pk, now), Version: u.version}
		if v2 || timeline {
			sum.Daily = s.daily(pkHex, now)
		}
		if timeline {
			sum.Timeline = s.timelines(pkHex, now)
		}
		out = append(out, sum)
	}
	return out, nil
}

// online reports whether pk has any live registration. s.mu held.
func (s *memoryStore) online(pk cipher.PubKey, now time.Time) bool {
	for _, byPK := range s.services {
		if e, ok := byPK[pk.String()]; ok && s.live(e, now) {
			return true
		}
	}
	return false
}

// daily is the uptime percentage per day over the history window. s.mu held.
func (s *memoryStore) daily(pkHex string, now time.Time) map[string]string {
	out := make(map[string]string)
	for i := 0; i < uptimeHistoryDays; i++ {
		date := now.AddDate(0, 0, -i).Format("2006-01-02")
		u, ok := s.uptime[pkHex][date]
		if !ok {
			continue
		}
		pct := float64(u.count) / expectedHeartbeatsPerDay * 100
		if pct > 100 {
			pct = 100
		}
		out[date] = fmt.Sprintf("%.2f", pct)
	}
	return out
}

// timelines renders each day's 5-minute slots: '.' heard, ' ' missed. s.mu held.
func (s *memoryStore) timelines(pkHex string, now time.Time) map[string]string {
	out := make(map[string]string)
	for i := 0; i < uptimeHistoryDays; i++ {
		date := now.AddDate(0, 0, -i).Format("2006-01-02")
		u, ok := s.uptime[pkHex][date]
		if !ok {
			continue
		}
		var buf [timelineSlots]byte
		for slot, heard := range u.timeline {
			buf[slot] = ' '
			if heard {
				buf[slot] = '.'
			}
		}
		out[date] = string(buf[:])
	}
	return out
}

func (s *memoryStore) GetDailyTimeline(_ context.Context, pkHex string, now time.Time) map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.timelines(pkHex, now)
}

func (s *memoryStore) Close() error { return nil }
