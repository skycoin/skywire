//go:build !no_ci
// +build !no_ci

package store

import (
	"errors"
	"net"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/geo"
	"github.com/stretchr/testify/require"
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
