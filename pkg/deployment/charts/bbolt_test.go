package charts

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/0magnet/bbolt"
	"github.com/stretchr/testify/require"
)

func TestBboltStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "charts.db")
	db, err := bbolt.Open(path, 0o600, nil)
	require.NoError(t, err)
	st, err := NewBboltStore(db, "visor")
	require.NoError(t, err)
	testStore(t, st)

	// Samples outlive the process, and old ones are trimmed.
	require.NoError(t, db.Close())
	db, err = bbolt.Open(path, 0o600, nil)
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck
	st, err = NewBboltStore(db, "visor")
	require.NoError(t, err)
	ctx := context.Background()
	base := time.Date(2026, 10, 6, 10, 0, 0, 0, time.UTC)
	raw, err := st.Range(ctx, base, base.Add(3*time.Hour), false)
	require.NoError(t, err)
	require.Len(t, raw, 24)

	later := base.Add(RawRetention + 2*time.Hour)
	require.NoError(t, st.Add(ctx, Sample{At: later, V: map[string]float64{"a": 1}}))
	raw, err = st.Range(ctx, base, later.Add(time.Second), false)
	require.NoError(t, err)
	require.Len(t, raw, 1)
	hourly, err := st.Range(ctx, base, later, true)
	require.NoError(t, err)
	require.Len(t, hourly, 1, "the old hourly mean stays")
}
