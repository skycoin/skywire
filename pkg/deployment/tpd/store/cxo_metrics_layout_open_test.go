package store

import (
	"testing"
	"time"
)

func TestOpenMetricsDays(t *testing.T) {
	day := time.Date(2026, 9, 4, 0, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		at   time.Duration
		want int
	}{
		{0, 2},
		{MetricsLateWindow - time.Second, 2},
		{MetricsLateWindow, 1},
		{12 * time.Hour, 1},
	} {
		if got := OpenMetricsDays(day.Add(c.at)); got != c.want {
			t.Errorf("OpenMetricsDays(midnight+%s) = %d, want %d", c.at, got, c.want)
		}
	}
}
