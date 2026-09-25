package skymail

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestParseSize(t *testing.T) {
	for in, want := range map[string]int64{
		"16MiB": 16 << 20, "1mib": 1 << 20, "500KB": 500000, "2GiB": 2 << 30,
		"1048576": 1 << 20, "1.5MiB": 3 << 19, "default": 0, "none": -1, "unlimited": -1,
	} {
		got, err := ParseSize(in)
		require.NoError(t, err, in)
		require.Equal(t, want, got, in)
	}
	for _, bad := range []string{"", "MiB", "-5MiB", "0", "lots"} {
		_, err := ParseSize(bad)
		require.Error(t, err, bad)
	}
}

func TestParseAge(t *testing.T) {
	for in, want := range map[string]time.Duration{
		"7d": 7 * 24 * time.Hour, "36h": 36 * time.Hour, "0.5d": 12 * time.Hour,
		"default": 0, "none": -1, "forever": -1,
	} {
		got, err := ParseAge(in)
		require.NoError(t, err, in)
		require.Equal(t, want, got, in)
	}
	for _, bad := range []string{"", "d", "-1d", "0s", "soon"} {
		_, err := ParseAge(bad)
		require.Error(t, err, bad)
	}
}

// TestFormatRoundTrips: what FormatSize/FormatAge write, Parse reads back.
func TestFormatRoundTrips(t *testing.T) {
	for _, n := range []int64{16 << 20, 3 << 19, 2048, 5, 2 << 30, -1} {
		got, err := ParseSize(FormatSize(n))
		require.NoError(t, err, FormatSize(n))
		require.InDelta(t, n, got, float64(n)/100+1, FormatSize(n))
	}
	for _, d := range []time.Duration{7 * 24 * time.Hour, 36 * time.Hour, -1} {
		got, err := ParseAge(FormatAge(d))
		require.NoError(t, err)
		require.Equal(t, d, got)
	}
	require.Equal(t, "16MiB", FormatSize(16<<20))
	require.Equal(t, "7d", FormatAge(7*24*time.Hour))
}
