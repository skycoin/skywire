package visor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

func TestHealthCache(t *testing.T) {
	clock := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	now := func() time.Time { return clock }
	probes, complete := 0, true
	probe := func() ([]visorapi.ServiceHealthEntry, bool) {
		probes++
		return []visorapi.ServiceHealthEntry{{Name: "Address Resolver", Status: "OK"}}, complete
	}

	t.Run("no max age probes every time", func(t *testing.T) {
		var c healthCache
		probes = 0
		c.get(0, now, probe)
		c.get(0, now, probe)
		require.Equal(t, 2, probes)
	})

	t.Run("callers share a probe until it is max age old", func(t *testing.T) {
		var c healthCache
		probes = 0
		require.Len(t, c.get(30*time.Second, now, probe), 1)
		clock = clock.Add(29 * time.Second)
		require.Len(t, c.get(30*time.Second, now, probe), 1)
		require.Equal(t, 1, probes)
		clock = clock.Add(time.Second)
		c.get(30*time.Second, now, probe)
		require.Equal(t, 2, probes)
	})

	t.Run("a probe before dmsg is ready is not kept", func(t *testing.T) {
		var c healthCache
		probes, complete = 0, false
		c.get(30*time.Second, now, probe)
		c.get(30*time.Second, now, probe)
		require.Equal(t, 2, probes)
		complete = true
	})
}
