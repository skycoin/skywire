package store

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestTransportBeatMemoOnePerSlot(t *testing.T) {
	var m transportBeatMemo
	id := uuid.New()
	t0 := time.Date(2026, 9, 30, 10, 0, 10, 0, time.UTC)

	require.False(t, m.recorded(id, t0))
	m.note(id, t0)
	require.True(t, m.recorded(id, t0.Add(4*time.Minute)), "same slot")
	require.False(t, m.recorded(id, t0.Add(5*time.Minute)), "next slot")
	require.False(t, m.recorded(uuid.New(), t0), "per transport")

	// Slot 0 of the next day is not slot 0 of this one.
	m.note(id, time.Date(2026, 9, 30, 0, 1, 0, 0, time.UTC))
	require.False(t, m.recorded(id, time.Date(2026, 10, 1, 0, 1, 0, 0, time.UTC)))
}

func TestTransportBeatMemoSweepsOldDays(t *testing.T) {
	var m transportBeatMemo
	old, cur := uuid.New(), uuid.New()
	m.note(old, time.Date(2026, 9, 29, 23, 59, 0, 0, time.UTC))
	m.note(cur, time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC))
	m.mu.Lock()
	defer m.mu.Unlock()
	_, still := m.last[old]
	require.False(t, still)
	require.Len(t, m.last, 1)
}
