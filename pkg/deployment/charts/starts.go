package charts

import (
	"context"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-redis/redis/v8"
)

// Start is one start of the service and the build it ran, drawn as a marker
// on every chart so a change in a series can be matched to a deploy.
type Start struct {
	At      time.Time `json:"-"`
	Version string    `json:"version"`
	Commit  string    `json:"commit,omitempty"`
}

type wireStart struct {
	T       int64  `json:"t"`
	Version string `json:"version"`
	Commit  string `json:"commit,omitempty"`
}

func (st *store) AddStart(ctx context.Context, s Start) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	if err := st.b.addStart(ctx, s); err != nil {
		return err
	}
	return st.b.trimStarts(ctx, s.At.Add(-HourlyRetention))
}

func (st *store) Starts(ctx context.Context, from, to time.Time) ([]Start, error) {
	return st.b.startsOf(ctx, from, to)
}

func (m *memBackend) addStart(_ context.Context, s Start) error {
	i := sort.Search(len(m.starts), func(i int) bool { return m.starts[i].At.After(s.At) })
	m.starts = append(m.starts, Start{})
	copy(m.starts[i+1:], m.starts[i:])
	m.starts[i] = s
	return nil
}

func (m *memBackend) startsOf(_ context.Context, from, to time.Time) ([]Start, error) {
	var out []Start
	for _, s := range m.starts {
		if !s.At.Before(from) && s.At.Before(to) {
			out = append(out, s)
		}
	}
	return out, nil
}

func (m *memBackend) trimStarts(_ context.Context, before time.Time) error {
	i := sort.Search(len(m.starts), func(i int) bool { return !m.starts[i].At.Before(before) })
	m.starts = append(m.starts[:0], m.starts[i:]...)
	return nil
}

func (r *redisBackend) startsKey() string { return r.prefix + ":starts" }

func (r *redisBackend) addStart(ctx context.Context, s Start) error {
	b, err := json.Marshal(wireStart{T: s.At.Unix(), Version: s.Version, Commit: s.Commit})
	if err != nil {
		return err
	}
	return r.c.ZAdd(ctx, r.startsKey(), &redis.Z{Score: float64(s.At.Unix()), Member: b}).Err()
}

func (r *redisBackend) startsOf(ctx context.Context, from, to time.Time) ([]Start, error) {
	vals, err := r.c.ZRangeByScore(ctx, r.startsKey(), &redis.ZRangeBy{
		Min: strconv.FormatInt(from.Unix(), 10),
		Max: "(" + strconv.FormatInt(to.Unix(), 10),
	}).Result()
	if err != nil {
		return nil, err
	}
	out := make([]Start, 0, len(vals))
	for _, v := range vals {
		var w wireStart
		if err := json.Unmarshal([]byte(v), &w); err != nil {
			continue
		}
		out = append(out, Start{At: time.Unix(w.T, 0).UTC(), Version: w.Version, Commit: w.Commit})
	}
	return out, nil
}

func (r *redisBackend) trimStarts(ctx context.Context, before time.Time) error {
	return r.c.ZRemRangeByScore(ctx, r.startsKey(), "-inf", "("+strconv.FormatInt(before.Unix(), 10)).Err()
}

// Label names the build, adding the short commit when the version lacks it.
func (s Start) Label() string {
	v := s.Version
	if v == "" {
		v = "unknown version"
	}
	if c := s.Commit; len(c) >= 9 && !strings.Contains(v, c[:9]) {
		v += " (" + c[:9] + ")"
	}
	return v
}
