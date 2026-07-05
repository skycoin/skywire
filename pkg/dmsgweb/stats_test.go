package dmsgweb

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStatsNilReceiver(t *testing.T) {
	var s *Stats // nil

	// RecordRequest on a nil Stats returns a usable no-op closure.
	done := s.RecordRequest()
	require.NotNil(t, done)
	require.NotPanics(t, func() { done(nil); done(errors.New("x")) })

	// Snapshot on a nil Stats is the zero value.
	require.Equal(t, StatsSnapshot{}, s.Snapshot())
}

func TestStatsSuccessAndFailure(t *testing.T) {
	s := NewStats()

	// A successful request.
	done := s.RecordRequest()
	done(nil)

	// A failed request.
	done = s.RecordRequest()
	done(errors.New("boom"))

	snap := s.Snapshot()
	require.EqualValues(t, 2, snap.TotalRequests)
	require.EqualValues(t, 1, snap.Successful)
	require.EqualValues(t, 1, snap.Failed)
	require.EqualValues(t, 0, snap.Active) // both completed

	// Started clock set by NewStats → StartedAt populated, uptime >= 0.
	require.False(t, snap.StartedAt.IsZero())
	require.GreaterOrEqual(t, snap.UptimeSec, int64(0))

	// Last-* timestamps recorded.
	require.NotNil(t, snap.LastRequestAt)
	require.NotNil(t, snap.LastSuccessAt)
	require.NotNil(t, snap.LastFailureAt)
	require.Equal(t, "boom", snap.LastError)
}

func TestStatsActiveInFlight(t *testing.T) {
	s := NewStats()
	done1 := s.RecordRequest()
	done2 := s.RecordRequest()

	// Two requests in flight, neither completed.
	snap := s.Snapshot()
	require.EqualValues(t, 2, snap.Active)
	require.EqualValues(t, 2, snap.TotalRequests)
	require.Nil(t, snap.LastSuccessAt) // nothing finished yet
	require.Nil(t, snap.LastFailureAt)

	done1(nil)
	done2(nil)
	require.EqualValues(t, 0, s.Snapshot().Active)
}

func TestStatsErrorTruncation(t *testing.T) {
	s := NewStats()
	longMsg := strings.Repeat("e", 1000)
	done := s.RecordRequest()
	done(errors.New(longMsg))

	snap := s.Snapshot()
	require.Len(t, snap.LastError, 512) // 509 + "..."
	require.True(t, strings.HasSuffix(snap.LastError, "..."))
}

func TestStatsCountersSurviveSnapshots(t *testing.T) {
	s := NewStats()
	for range 5 {
		done := s.RecordRequest()
		done(nil)
	}
	// Two snapshots return consistent cumulative counts.
	require.EqualValues(t, 5, s.Snapshot().Successful)
	require.EqualValues(t, 5, s.Snapshot().Successful)
}

func TestStatsZeroStartedNoUptime(t *testing.T) {
	// A bare &Stats{} (no NewStats) has no started clock → StartedAt zero
	// and UptimeSec stays 0 (the st != 0 branch is skipped).
	s := &Stats{}
	snap := s.Snapshot()
	require.True(t, snap.StartedAt.IsZero())
	require.EqualValues(t, 0, snap.UptimeSec)
}
