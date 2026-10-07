package charts

import (
	"math"
	"sort"
	"strings"
	"time"
)

// Bucket averages samples into buckets of width d, one sample per bucket.
func Bucket(samples []Sample, d time.Duration) []Sample {
	var out []Sample
	for i := 0; i < len(samples); {
		start := samples[i].At.Truncate(d)
		j := i
		for j < len(samples) && samples[j].At.Truncate(d).Equal(start) {
			j++
		}
		out = append(out, Mean(start, samples[i:j]))
		i = j
	}
	return out
}

// Frame lines samples up for charting. A row is nil where the series was not
// being recorded, so a chart breaks there instead of bridging the gap.
type Frame struct {
	Times []time.Time
	rows  []map[string]float64
}

// NewFrame builds a frame from samples spaced step apart. A gap longer than
// two and a half steps gets an empty row.
func NewFrame(samples []Sample, step time.Duration) *Frame {
	f := &Frame{}
	var prev time.Time
	for _, s := range samples {
		if !prev.IsZero() && s.At.Sub(prev) > step*5/2 {
			f.Times = append(f.Times, prev.Add(step))
			f.rows = append(f.rows, nil)
		}
		f.Times = append(f.Times, s.At)
		f.rows = append(f.rows, s.V)
		prev = s.At
	}
	return f
}

// Values returns key's series. A sample without key reads as zero when
// missingZero is set, which suits counts of things that can drop to none.
func (f *Frame) Values(key string, missingZero bool) []float64 {
	out := make([]float64, len(f.rows))
	for i, r := range f.rows {
		v, ok := r[key]
		switch {
		case r == nil:
			out[i] = math.NaN()
		case !ok && !missingZero:
			out[i] = math.NaN()
		default:
			out[i] = v
		}
	}
	return out
}

// Latest is key's value in the newest sample that carries it.
func (f *Frame) Latest(key string) (float64, bool) {
	for i := len(f.rows) - 1; i >= 0; i-- {
		if v, ok := f.rows[i][key]; ok {
			return v, true
		}
	}
	return 0, false
}

// Keys returns the keys under prefix, largest latest value first.
func (f *Frame) Keys(prefix string) []string {
	seen := map[string]float64{}
	for _, r := range f.rows {
		for k := range r {
			if strings.HasPrefix(k, prefix) {
				seen[k] = 0
			}
		}
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		seen[k], _ = f.Latest(k)
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if seen[keys[i]] != seen[keys[j]] {
			return seen[keys[i]] > seen[keys[j]]
		}
		return keys[i] < keys[j]
	})
	return keys
}

// Group returns one series per key under prefix, named without the prefix.
// Past the top keys the rest are summed into "other".
func (f *Frame) Group(prefix string, top int) []Series {
	keys := f.Keys(prefix)
	var out []Series
	var other []float64
	for i, k := range keys {
		vals := f.Values(k, true)
		if top > 0 && i >= top {
			if other == nil {
				other = make([]float64, len(vals))
			}
			for j, v := range vals {
				other[j] += v
			}
			continue
		}
		out = append(out, Series{Name: strings.TrimPrefix(k, prefix), Vals: vals})
	}
	if other != nil {
		out = append(out, Series{Name: "other", Vals: other})
	}
	return out
}
