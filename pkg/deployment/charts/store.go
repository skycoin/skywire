// Package charts records a deployment service's own counts over time and
// renders them as a self-contained HTML page of SVG charts.
package charts

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"
)

const (
	// RawRetention is how long full-resolution samples are kept.
	RawRetention = 8 * 24 * time.Hour
	// HourlyRetention is how long the hourly means are kept.
	HourlyRetention = 400 * 24 * time.Hour
)

// Sample is one observation of every series at one instant.
type Sample struct {
	At time.Time          `json:"-"`
	V  map[string]float64 `json:"v"`
}

type wireSample struct {
	T int64              `json:"t"`
	V map[string]float64 `json:"v"`
}

// Store keeps raw samples and their hourly means.
type Store interface {
	Add(ctx context.Context, s Sample) error
	// Range returns samples in [from, to), oldest first, from the hourly
	// series when hourly is set.
	Range(ctx context.Context, from, to time.Time, hourly bool) ([]Sample, error)
}

// backend is the storage a store needs: two time-ordered sets and a marker.
type backend interface {
	add(ctx context.Context, hourly bool, s Sample) error
	rangeOf(ctx context.Context, hourly bool, from, to time.Time) ([]Sample, error)
	trim(ctx context.Context, hourly bool, before time.Time) error
	rolled(ctx context.Context) (time.Time, error)
	setRolled(ctx context.Context, hour time.Time) error
}

type store struct {
	b  backend
	mu sync.Mutex
}

// NewMemoryStore returns a Store that lives in process memory.
func NewMemoryStore() Store { return &store{b: &memBackend{}} }

// NewRedisStore returns a Store kept under prefix in the redis at url.
func NewRedisStore(url, password, prefix string) (Store, error) {
	opt, err := redis.ParseURL(url)
	if err != nil {
		return nil, err
	}
	if password != "" {
		opt.Password = password
	}
	opt.PoolSize = 2
	return &store{b: &redisBackend{c: redis.NewClient(opt), prefix: prefix + ":charts"}}, nil
}

func (st *store) Add(ctx context.Context, s Sample) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	if err := st.b.add(ctx, false, s); err != nil {
		return err
	}
	if err := st.b.trim(ctx, false, s.At.Add(-RawRetention)); err != nil {
		return err
	}
	return st.rollup(ctx, s.At.Truncate(time.Hour))
}

// rollup stores the mean of every complete hour before current that has not
// been rolled up yet.
func (st *store) rollup(ctx context.Context, current time.Time) error {
	last, err := st.b.rolled(ctx)
	if err != nil {
		return err
	}
	start := last.Add(time.Hour)
	if oldest := current.Add(-RawRetention); last.IsZero() || start.Before(oldest) {
		start = oldest
	}
	for h := start; h.Before(current); h = h.Add(time.Hour) {
		raw, err := st.b.rangeOf(ctx, false, h, h.Add(time.Hour))
		if err != nil {
			return err
		}
		if len(raw) > 0 {
			if err := st.b.add(ctx, true, Mean(h, raw)); err != nil {
				return err
			}
		}
		if err := st.b.setRolled(ctx, h); err != nil {
			return err
		}
	}
	return st.b.trim(ctx, true, current.Add(-HourlyRetention))
}

func (st *store) Range(ctx context.Context, from, to time.Time, hourly bool) ([]Sample, error) {
	return st.b.rangeOf(ctx, hourly, from, to)
}

// Mean averages samples into one sample at at. A series missing from some
// samples is averaged over the samples that carry it.
func Mean(at time.Time, samples []Sample) Sample {
	sum := map[string]float64{}
	n := map[string]int{}
	for _, s := range samples {
		for k, v := range s.V {
			sum[k] += v
			n[k]++
		}
	}
	out := Sample{At: at, V: make(map[string]float64, len(sum))}
	for k, v := range sum {
		out.V[k] = v / float64(n[k])
	}
	return out
}

type memBackend struct {
	raw, hourly []Sample
	last        time.Time
}

func (m *memBackend) set(hourly bool) *[]Sample {
	if hourly {
		return &m.hourly
	}
	return &m.raw
}

func (m *memBackend) add(_ context.Context, hourly bool, s Sample) error {
	p := m.set(hourly)
	i := sort.Search(len(*p), func(i int) bool { return !(*p)[i].At.Before(s.At) })
	if i < len(*p) && (*p)[i].At.Equal(s.At) {
		(*p)[i] = s
		return nil
	}
	*p = append(*p, Sample{})
	copy((*p)[i+1:], (*p)[i:])
	(*p)[i] = s
	return nil
}

func (m *memBackend) rangeOf(_ context.Context, hourly bool, from, to time.Time) ([]Sample, error) {
	var out []Sample
	for _, s := range *m.set(hourly) {
		if !s.At.Before(from) && s.At.Before(to) {
			out = append(out, s)
		}
	}
	return out, nil
}

func (m *memBackend) trim(_ context.Context, hourly bool, before time.Time) error {
	p := m.set(hourly)
	i := sort.Search(len(*p), func(i int) bool { return !(*p)[i].At.Before(before) })
	*p = append((*p)[:0], (*p)[i:]...)
	return nil
}

func (m *memBackend) rolled(context.Context) (time.Time, error) { return m.last, nil }

func (m *memBackend) setRolled(_ context.Context, h time.Time) error {
	m.last = h
	return nil
}

type redisBackend struct {
	c      *redis.Client
	prefix string
}

func (r *redisBackend) key(hourly bool) string {
	if hourly {
		return r.prefix + ":1h"
	}
	return r.prefix + ":raw"
}

func (r *redisBackend) add(ctx context.Context, hourly bool, s Sample) error {
	b, err := json.Marshal(wireSample{T: s.At.Unix(), V: s.V})
	if err != nil {
		return err
	}
	key, score := r.key(hourly), strconv.FormatInt(s.At.Unix(), 10)
	pipe := r.c.TxPipeline()
	pipe.ZRemRangeByScore(ctx, key, score, score)
	pipe.ZAdd(ctx, key, &redis.Z{Score: float64(s.At.Unix()), Member: b})
	_, err = pipe.Exec(ctx)
	return err
}

func (r *redisBackend) rangeOf(ctx context.Context, hourly bool, from, to time.Time) ([]Sample, error) {
	vals, err := r.c.ZRangeByScore(ctx, r.key(hourly), &redis.ZRangeBy{
		Min: strconv.FormatInt(from.Unix(), 10),
		Max: "(" + strconv.FormatInt(to.Unix(), 10),
	}).Result()
	if err != nil {
		return nil, err
	}
	out := make([]Sample, 0, len(vals))
	for _, v := range vals {
		var w wireSample
		if err := json.Unmarshal([]byte(v), &w); err != nil {
			continue
		}
		out = append(out, Sample{At: time.Unix(w.T, 0).UTC(), V: w.V})
	}
	return out, nil
}

func (r *redisBackend) trim(ctx context.Context, hourly bool, before time.Time) error {
	return r.c.ZRemRangeByScore(ctx, r.key(hourly), "-inf", "("+strconv.FormatInt(before.Unix(), 10)).Err()
}

func (r *redisBackend) rolled(ctx context.Context) (time.Time, error) {
	v, err := r.c.Get(ctx, r.prefix+":rolled").Int64()
	if err == redis.Nil {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("charts: read rollup marker: %w", err)
	}
	return time.Unix(v, 0).UTC(), nil
}

func (r *redisBackend) setRolled(ctx context.Context, h time.Time) error {
	return r.c.Set(ctx, r.prefix+":rolled", h.Unix(), 0).Err()
}
