package statutil

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestAmountString covers the 1000-based human-readable formatting.
func TestAmountString(t *testing.T) {
	cases := []struct {
		in   Amount
		want string
	}{
		{0, "0"},
		{5, "5"},
		{999, "999"},
		{1000, "1k"},
		{1500, "1.5k"},
		{1_000_000, "1M"},
		{2_500_000, "2.5M"},
	}
	for _, c := range cases {
		assert.Equalf(t, c.want, c.in.String(), "Amount(%d)", uint32(c.in))
	}
}

// TestDurationWindowOne verifies a single-sample window tracks the most recent
// value exactly (the only window size for which the incremental average is
// exact).
func TestDurationWindowOne(t *testing.T) {
	d := NewDuration(1)
	assert.Equal(t, time.Duration(0), d.Value())

	assert.Equal(t, 5*time.Second, d.Add(5*time.Second))
	assert.Equal(t, 5*time.Second, d.Value())

	assert.Equal(t, 7*time.Second, d.Add(7*time.Second))
	assert.Equal(t, 7*time.Second, d.Value())
}

// TestDurationValueTracksAdd verifies Value reflects the last Add result across
// the circular buffer wraparound (more samples than the window).
func TestDurationValueTracksAdd(t *testing.T) {
	d := NewDuration(3)
	for _, v := range []time.Duration{1, 2, 3, 4, 5} { // 5 > window of 3 → wraps
		got := d.Add(v * time.Millisecond)
		assert.Equal(t, got, d.Value())
	}
}

// TestDurationAddStartTime verifies AddStartTime records a positive elapsed
// duration.
func TestDurationAddStartTime(t *testing.T) {
	d := NewDuration(1)
	avg := d.AddStartTime(time.Now().Add(-10 * time.Millisecond))
	assert.Positive(t, avg)
	assert.Equal(t, avg, d.Value())
}

// TestNewDurationPanics verifies a non-positive sample count panics.
func TestNewDurationPanics(t *testing.T) {
	assert.Panics(t, func() { NewDuration(0) })
	assert.Panics(t, func() { NewDuration(-1) })
}

// TestFloatWindowOne mirrors TestDurationWindowOne for Float.
func TestFloatWindowOne(t *testing.T) {
	f := NewFloat(1)
	assert.Equal(t, 0.0, f.Value())

	assert.Equal(t, 2.5, f.Add(2.5))
	assert.Equal(t, 2.5, f.Value())

	assert.Equal(t, 4.0, f.Add(4.0))
	assert.Equal(t, 4.0, f.Value())
}

// TestFloatValueTracksAdd verifies Float.Value tracks the last Add across
// wraparound.
func TestFloatValueTracksAdd(t *testing.T) {
	f := NewFloat(2)
	for _, v := range []float64{1, 2, 3, 4} { // wraps the window of 2
		got := f.Add(v)
		assert.Equal(t, got, f.Value())
	}
}

// TestNewFloatPanics verifies a non-positive sample count panics.
func TestNewFloatPanics(t *testing.T) {
	assert.Panics(t, func() { NewFloat(0) })
	assert.Panics(t, func() { NewFloat(-1) })
}
