//go:build !no_ci
// +build !no_ci

package store

import (
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/geo"
	"github.com/skycoin/skywire/pkg/logging"
)

func TestMemory(t *testing.T) {
	s := NewMemoryStore()
	testUptime(t, s)
}

func testUptime(t *testing.T, store Store) {
	pk, _ := cipher.GenerateKeyPair()

	const iterations = 15
	for i := 0; i < iterations; i++ {
		err := store.UpdateUptime(pk.String(), "127.0.0.1", "")
		require.NoError(t, err)
	}

	now := time.Now()

	wantUptime := UptimeDef{
		Key:    pk.String(),
		Online: true,
	}

	wantVisor := VisorDef{
		Lat: 1,
		Lon: 1,
	}

	t.Run("all pub keys", func(t *testing.T) {
		uptimes, err := store.GetAllUptimes(now.Year(), now.Month(), now.Year(), now.Month())
		require.NoError(t, err)
		require.Len(t, uptimes, 1)
		require.Equal(t, wantUptime, uptimes[0])
	})

	t.Run("specified pub keys", func(t *testing.T) {
		uptimes, err := store.GetUptimes([]string{pk.String()}, now.Year(), now.Month(), now.Year(), now.Month())
		require.NoError(t, err)
		require.Len(t, uptimes, 1)
		require.Equal(t, wantUptime, uptimes[0])
	})

	t.Run("wrong date", func(t *testing.T) {
		date := time.Now().AddDate(0, -3, 0)
		uptimes, err := store.GetAllUptimes(date.Year(), date.Month(), date.Year(), date.Month())
		require.NoError(t, err)
		require.Len(t, uptimes, 0)
	})

	t.Run("visors", func(t *testing.T) {
		geoFunc := func(ip net.IP) (*geo.LocationData, error) {
			wantIP := net.IPv4(127, 0, 0, 1)
			if wantIP.Equal(ip) {
				return &geo.LocationData{
					Lat: 1,
					Lon: 1,
				}, nil
			}

			return nil, errors.New("unexpected ip")
		}

		visors, err := store.GetAllVisors(geoFunc)
		require.NoError(t, err)
		require.Len(t, visors, 1)
		require.Equal(t, wantVisor, visors[0])
	})
}

// --- additional memory-store coverage ----------------------------------------

func TestNewMemoryViaFactory(t *testing.T) {
	s, err := New(logging.MustGetLogger("t"), nil, true) // testing=true → memory store
	require.NoError(t, err)
	require.NotNil(t, s)
}

func TestMemoryStore_VisorsIPsAndCounts(t *testing.T) {
	s := NewMemoryStore()
	pk1, _ := cipher.GenerateKeyPair()
	pk2, _ := cipher.GenerateKeyPair()
	require.NoError(t, s.UpdateUptime(pk1.String(), "127.0.0.1", "v1"))
	require.NoError(t, s.UpdateUptime(pk2.String(), "127.0.0.2", "v1"))
	// A no-IP update exercises the ip=="" branch of UpdateUptime.
	require.NoError(t, s.UpdateUptime(pk1.String(), "", "v1"))

	now := time.Now()
	monthKey := fmt.Sprintf("%d:%d", now.Year(), now.Month())

	t.Run("all", func(t *testing.T) {
		ips, err := s.GetVisorsIPs("all")
		require.NoError(t, err)
		require.NotEmpty(t, ips)
	})
	t.Run("specific month", func(t *testing.T) {
		ips, err := s.GetVisorsIPs(monthKey)
		require.NoError(t, err)
		require.NotEmpty(t, ips)
	})
	t.Run("unknown month errors", func(t *testing.T) {
		_, err := s.GetVisorsIPs("1999:1")
		require.Error(t, err)
	})

	cur, err := s.GetNumberOfUptimesInCurrentMonth()
	require.NoError(t, err)
	require.Positive(t, cur)

	byMonth, err := s.GetNumberOfUptimesByYearAndMonth(now.Year(), now.Month())
	require.NoError(t, err)
	require.Positive(t, byMonth)

	past, err := s.GetNumberOfUptimesByYearAndMonth(1999, time.January)
	require.NoError(t, err)
	require.Zero(t, past)
}

func TestMemoryStore_HistoryStubs(t *testing.T) {
	s := NewMemoryStore()

	h, err := s.GetDailyUpdateHistory()
	require.NoError(t, err)
	require.NotNil(t, h)

	require.NoError(t, s.DeleteEntries(nil))

	oldest, err := s.GetOldestEntry()
	require.NoError(t, err)
	require.Equal(t, DailyUptimeHistory{}, oldest)

	day, err := s.GetSpecificDayData(time.Now())
	require.NoError(t, err)
	require.Empty(t, day)

	s.Close()
}

// --- response makers (called directly to cover their branches) ---------------

func TestMakeUptimeResponse(t *testing.T) {
	// Propagated error path.
	_, err := makeUptimeResponse(nil, nil, nil, errors.New("boom"))
	require.Error(t, err)

	// Empty keys → empty response.
	resp, err := makeUptimeResponse(nil, map[string]string{}, map[string]string{}, nil)
	require.NoError(t, err)
	require.Empty(t, resp)

	// Two keys → the multi-key sort branch runs; one has a recent
	// timestamp (online), the other an unparseable one (stays offline).
	pkA, _ := cipher.GenerateKeyPair()
	pkB, _ := cipher.GenerateKeyPair()
	lastTS := map[string]string{
		pkA.String(): fmt.Sprintf("%d", time.Now().Unix()),
		pkB.String(): "not-a-number",
	}
	resp, err = makeUptimeResponse([]string{pkA.String(), pkB.String()}, lastTS,
		map[string]string{pkA.String(): "v1.0.0"}, nil)
	require.NoError(t, err)
	require.Len(t, resp, 2)
	// Sorted ascending by key; verify exactly one is online.
	online := 0
	for _, e := range resp {
		if e.Online {
			online++
		}
	}
	require.Equal(t, 1, online)
}

func TestMakeVisorsResponse(t *testing.T) {
	geoOK := func(net.IP) (*geo.LocationData, error) { return &geo.LocationData{Lat: 1.234, Lon: 5.678}, nil }
	geoErr := func(net.IP) (*geo.LocationData, error) { return nil, errors.New("geo lookup failed") }

	// Success path rounds to 2 decimals.
	resp, err := makeVisorsResponse(map[string]string{"pk1": "1.2.3.4"}, geoOK)
	require.NoError(t, err)
	require.Len(t, resp, 1)
	require.Equal(t, 1.23, resp[0].Lat)

	// Geo error → the entry is skipped (continue branch).
	resp, err = makeVisorsResponse(map[string]string{"pk1": "1.2.3.4"}, geoErr)
	require.NoError(t, err)
	require.Empty(t, resp)
}
