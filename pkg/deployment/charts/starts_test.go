package charts

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Every start in a chart's window is drawn on it, in its data for the hover
// readout, and the latest is named in the header.
func TestPageMarksStarts(t *testing.T) {
	ctx := context.Background()
	st := NewMemoryStore()
	now := time.Now().UTC().Truncate(Interval)
	for i := 0; i < 12; i++ {
		at := now.Add(-time.Duration(12-i) * Interval)
		require.NoError(t, st.Add(ctx, Sample{At: at, V: map[string]float64{"x": float64(i)}}))
	}
	old := Start{At: now.Add(-40 * time.Minute), Version: "v1.3.100-0-aaaaaaaaa", Commit: "aaaaaaaaa111"}
	cur := Start{At: now.Add(-20 * time.Minute), Version: "v1.3.100", Commit: "bbbbbbbbb222"}
	require.NoError(t, st.AddStart(ctx, Start{At: now.Add(-HourlyRetention - time.Hour), Version: "gone"}))
	require.NoError(t, st.AddStart(ctx, cur))
	require.NoError(t, st.AddStart(ctx, old))

	got, err := st.Starts(ctx, now.Add(-HourlyRetention-2*time.Hour), now.Add(time.Second))
	require.NoError(t, err)
	require.Equal(t, []string{"v1.3.100-0-aaaaaaaaa", "v1.3.100 (bbbbbbbbb)"}, []string{got[0].Label(), got[1].Label()},
		"starts come back oldest first, trimmed to the hourly retention")

	p := &Page{Title: "T", Store: st, Build: func(ctx context.Context, r Range, now time.Time) (Content, error) {
		f, from, err := r.Frame(ctx, st, now)
		if err != nil {
			return Content{}, err
		}
		return Content{Charts: []Chart{{Title: "X", From: from, To: now, Times: f.Times,
			Series: []Series{{Name: "x", Vals: f.Values("x", false)}}}}}, nil
	}}
	c, err := p.render(ctx, Ranges[0])
	require.NoError(t, err)
	html := string(c.body)
	require.Equal(t, 2, strings.Count(html, "<g class='start'>"))
	require.Contains(t, html, "Running v1.3.100 (bbbbbbbbb) since")
	require.Contains(t, html, `"v":"v1.3.100-0-aaaaaaaaa"`)
}
