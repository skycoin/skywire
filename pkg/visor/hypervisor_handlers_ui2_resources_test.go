package visor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

func TestUI2Rates(t *testing.T) {
	now := time.Now()
	tx, rx := ui2Rates("rate-test", &visorapi.HostStatsInfo{NetBytesSent: 1000, NetBytesRecv: 5000}, now)
	require.Equal(t, [2]float64{-1, -1}, [2]float64{tx, rx}, "no rate before a second sample")
	tx, rx = ui2Rates("rate-test", &visorapi.HostStatsInfo{NetBytesSent: 3000, NetBytesRecv: 6000}, now.Add(2*time.Second))
	require.Equal(t, [2]float64{1000, 500}, [2]float64{tx, rx})
	tx, _ = ui2Rates("rate-test", &visorapi.HostStatsInfo{NetBytesSent: 9000}, now.Add(2500*time.Millisecond))
	require.Equal(t, float64(1000), tx, "a sample within a second keeps the last rate")
	tx, _ = ui2Rates("rate-test", &visorapi.HostStatsInfo{NetBytesSent: 10}, now.Add(5*time.Second))
	require.Equal(t, float64(0), tx, "a counter reset is not a negative rate")
}
