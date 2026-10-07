package charts

import (
	"context"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestStoreRollsUpHours(t *testing.T) {
	ctx := context.Background()
	st := NewMemoryStore()
	base := time.Date(2026, 10, 6, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 24; i++ {
		at := base.Add(time.Duration(i) * Interval)
		require.NoError(t, st.Add(ctx, Sample{At: at, V: map[string]float64{"a": float64(i)}}))
	}
	raw, err := st.Range(ctx, base, base.Add(3*time.Hour), false)
	require.NoError(t, err)
	require.Len(t, raw, 24)

	hourly, err := st.Range(ctx, base, base.Add(3*time.Hour), true)
	require.NoError(t, err)
	require.Len(t, hourly, 1, "only the first hour is complete")
	require.Equal(t, base, hourly[0].At)
	require.InDelta(t, 5.5, hourly[0].V["a"], 1e-9)
}

func TestStoreTrimsRaw(t *testing.T) {
	ctx := context.Background()
	st := NewMemoryStore()
	old := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	require.NoError(t, st.Add(ctx, Sample{At: old, V: map[string]float64{"a": 1}}))
	now := old.Add(RawRetention + time.Hour)
	require.NoError(t, st.Add(ctx, Sample{At: now, V: map[string]float64{"a": 2}}))
	raw, err := st.Range(ctx, old, now.Add(time.Second), false)
	require.NoError(t, err)
	require.Len(t, raw, 1)
	require.Equal(t, now, raw[0].At)
}

func TestFrameBreaksOnGaps(t *testing.T) {
	base := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	f := NewFrame([]Sample{
		{At: base, V: map[string]float64{"tp.dmsg": 3, "tp.stcpr": 1}},
		{At: base.Add(Interval), V: map[string]float64{"tp.dmsg": 4}},
		{At: base.Add(time.Hour), V: map[string]float64{"tp.dmsg": 5}},
	}, Interval)
	require.Len(t, f.Times, 4)
	vals := f.Values("tp.dmsg", true)
	require.True(t, math.IsNaN(vals[2]))
	require.Equal(t, []float64{1, 0}, f.Values("tp.stcpr", true)[:2])
	require.True(t, math.IsNaN(f.Values("tp.stcpr", false)[1]))

	g := f.Group("tp.", 1)
	require.Equal(t, "dmsg", g[0].Name)
	require.Equal(t, "other", g[1].Name)
}

func TestPageServesOnlyRoot(t *testing.T) {
	base := time.Now().UTC().Add(-time.Hour)
	st := NewMemoryStore()
	for i := 0; i < 6; i++ {
		require.NoError(t, st.Add(context.Background(), Sample{At: base.Add(time.Duration(i) * Interval), V: map[string]float64{"x": float64(i)}}))
	}
	p := &Page{Title: "Test <service>", Build: func(ctx context.Context, r Range, now time.Time) (Content, error) {
		f, from, err := r.Frame(ctx, st, now)
		if err != nil {
			return Content{}, err
		}
		return Content{Charts: []Chart{{Title: "X", From: from, To: now, Times: f.Times,
			Series: []Series{{Name: "x</script>", Vals: f.Values("x", false)}}}}}, nil
	}}
	srv := httptest.NewServer(rootOnly(p))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/?range=7d")
	require.NoError(t, err)
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	_ = resp.Body.Close() //nolint:errcheck
	body := string(b)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Contains(t, body, "<svg")
	require.Contains(t, body, "Test &lt;service&gt;")
	require.NotContains(t, body, "x</script>")
	require.Contains(t, resp.Header.Get("Content-Security-Policy"), "default-src 'none'")

	resp, err = http.Get(srv.URL + "/transports/")
	require.NoError(t, err)
	_ = resp.Body.Close() //nolint:errcheck
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestLongRangeTicksOnMonths(t *testing.T) {
	from := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	to := time.Date(2027, 1, 10, 0, 0, 0, 0, time.UTC)
	c := Chart{Title: "y", From: from, To: to, Times: []time.Time{from, to}, Series: []Series{{Name: "a", Vals: []float64{1, 2}}}}
	svg := c.SVG("c0")
	require.Contains(t, svg, ">Mar<")
	require.Contains(t, svg, ">Jan 2027<")
	require.NotContains(t, svg, ">Jan<")
}

func TestAllZeroDrawsAFlatLine(t *testing.T) {
	now := time.Now().UTC()
	c := Chart{Title: "z", From: now.Add(-time.Hour), To: now, Times: []time.Time{now.Add(-time.Hour), now}, Series: []Series{{Name: "a", Vals: []float64{0, 0}}}}
	svg := c.SVG("c0")
	require.Contains(t, svg, "class='line'")
	require.NotContains(t, svg, "No samples")
}

func TestServeExtraPaths(t *testing.T) {
	page := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("page")) })   //nolint:errcheck
	graph := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("graph")) }) //nolint:errcheck
	srv := httptest.NewServer(rootOnly(page, Extra{Path: "/graph", Handler: graph}))
	defer srv.Close()
	for path, want := range map[string]int{"/": 200, "/graph": 200, "/graph/x": 404, "/health": 404} {
		resp, err := http.Get(srv.URL + path)
		require.NoError(t, err)
		_ = resp.Body.Close() //nolint:errcheck
		require.Equal(t, want, resp.StatusCode, path)
	}
}
