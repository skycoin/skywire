// Package skysettings pkg/skysocks/skysettings/skysettings.go
//
// A process-scoped registry of the skysocks-client tuning knobs the mux bench
// sweeps. Every entry's DEFAULT is the constant the client compiled with, so a
// client with nothing set behaves byte for byte as it does today; a knob only
// changes behaviour once it has been explicitly set.
//
// The shape is pkg/router/policy/preset's atomics generalised over a map: one
// atomic per knob, written by the settings pull and read at the use site. No
// locks on the read path, no goroutine, no persistence — the visor holds the
// values for a running app and the app pulls them on a tick it already has.
//
// The package is deliberately free of skywire imports: the CLI reads the
// catalog from here to parse `key=value` without linking pkg/skysocks.
package skysettings

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Kind is how a knob's int64 payload is read and written. Everything fits in
// an int64: bytes and counts directly, a duration as nanoseconds, a ratio as
// the IEEE-754 bit pattern of its float64.
type Kind string

// The knob kinds.
const (
	KindBytes    Kind = "bytes"
	KindCount    Kind = "count"
	KindDuration Kind = "duration"
	KindRatio    Kind = "ratio"
)

// Knob names. Lowercase dotted, grouped by the machinery they steer.
const (
	PoolFillInterval    = "pool.fill_interval"
	PoolRetryBackoffMin = "pool.retry_backoff_base"
	PoolRetryBackoffMax = "pool.retry_backoff_max"
	PoolRetryRounds     = "pool.retry_rounds"
	PoolStandbyRTTStale = "pool.standby_rtt_stale"

	TunnelProbeInterval    = "tunnel.probe_interval"
	TunnelLivenessInterval = "tunnel.liveness_interval"
	TunnelRTTAlpha         = "tunnel.rtt_alpha"
	TunnelPromoteInterval  = "tunnel.promote_interval"
	TunnelPromoteMargin    = "tunnel.promote_margin"
	TunnelPromoteHold      = "tunnel.promote_hold"
	TunnelParkMinHold      = "tunnel.park_min_hold"
	TunnelAuditionWindow   = "tunnel.audition_window"
	TunnelAuditionEvery    = "tunnel.audition_every"
	TunnelExitOpenPenalty  = "tunnel.exit_open_penalty"
	TunnelMeterSampleMin   = "tunnel.meter_sample_min"
	TunnelMeterCapDecay    = "tunnel.meter_cap_decay"
	TunnelMeterFresh       = "tunnel.meter_fresh"

	ChunkMaxBytes          = "chunk.max_bytes"
	ChunkConcurrency       = "chunk.concurrency"
	ChunkRetryBudget       = "chunk.retry_budget"
	ChunkIdleTimeout       = "chunk.idle_timeout"
	ChunkFreeRetries       = "chunk.free_retries"
	ChunkOutstandingFactor = "chunk.outstanding_factor"
)

// Def is a knob's static description: everything the CLI needs to parse a
// value for it and to print it back.
type Def struct {
	Name    string `json:"name"`
	Kind    Kind   `json:"kind"`
	Default int64  `json:"-"`
	Doc     string `json:"doc,omitempty"`
}

type knob struct {
	def Def
	cur atomic.Int64
	set atomic.Bool
}

var (
	mu      sync.RWMutex
	knobs   = map[string]*knob{}
	order   []string
	version atomic.Uint64
)

func register(name string, kind Kind, def int64, doc string) {
	k := &knob{def: Def{Name: name, Kind: kind, Default: def, Doc: doc}}
	k.cur.Store(def)
	knobs[name] = k
	order = append(order, name)
}

func ratio(f float64) int64 { return int64(math.Float64bits(f)) } //nolint:gosec

func init() {
	register(PoolFillInterval, KindDuration, int64(2*time.Second),
		"how often the keepalive loop adds one more standby tunnel")
	register(PoolRetryBackoffMin, KindDuration, int64(30*time.Second),
		"first wait after a round of failed standby-pool dials")
	register(PoolRetryBackoffMax, KindDuration, int64(4*time.Minute),
		"ceiling on the standby-pool dial backoff")
	register(PoolRetryRounds, KindCount, 3,
		"rounds of failed dials before the pool fill rests until a death")
	register(PoolStandbyRTTStale, KindDuration, int64(15*time.Second),
		"how long a standby tunnel may be silent and still be promoted")

	register(TunnelProbeInterval, KindDuration, int64(5*time.Second),
		"how often each tunnel's RTT is re-measured (also the settings-pull tick)")
	register(TunnelLivenessInterval, KindDuration, int64(15*time.Second),
		"how often the keepalive loop probes each tunnel for liveness")
	register(TunnelRTTAlpha, KindRatio, ratio(0.25),
		"weight of each new ping in a tunnel's RTT EWMA")
	register(TunnelPromoteInterval, KindDuration, int64(5*time.Second),
		"how often the promoter considers a standby/active swap")
	register(TunnelPromoteMargin, KindRatio, ratio(1.25),
		"how much better a standby must measure before it may swap in")
	register(TunnelPromoteHold, KindDuration, int64(15*time.Second),
		"how long an advantage must hold before the swap fires")
	register(TunnelParkMinHold, KindDuration, int64(30*time.Second),
		"how long a parked tunnel stays out of the candidate set")
	register(TunnelAuditionWindow, KindDuration, int64(30*time.Second),
		"how long an audition offer stands before it expires")
	register(TunnelAuditionEvery, KindDuration, int64(60*time.Second),
		"minimum gap between two auditions of the same tunnel")
	register(TunnelExitOpenPenalty, KindDuration, int64(10*time.Second),
		"how long a tunnel sits out the picks after an exit open timed out")
	register(TunnelMeterSampleMin, KindDuration, int64(500*time.Millisecond),
		"shortest interval a tunnel capacity sample is taken over")
	register(TunnelMeterCapDecay, KindRatio, ratio(0.9),
		"per-sample decay applied to a busy tunnel's capacity estimate")
	register(TunnelMeterFresh, KindDuration, int64(2*time.Second),
		"how long a busy window's capacity estimate stays authoritative")

	register(ChunkMaxBytes, KindBytes, 4<<20,
		"range-split chunk ceiling; overrides --range-chunk-kib once set")
	register(ChunkConcurrency, KindCount, 8,
		"concurrent range-split chunk fetches; overrides --range-concurrency once set")
	register(ChunkRetryBudget, KindDuration, int64(15*time.Second),
		"how long a failed chunk keeps being refetched before it gives up")
	register(ChunkIdleTimeout, KindDuration, int64(15*time.Second),
		"rolling no-progress deadline on a chunk body read")
	register(ChunkFreeRetries, KindCount, 8,
		"refetches a tunnel death may buy a chunk without charging its budget")
	register(ChunkOutstandingFactor, KindCount, 2,
		"outstanding chunk buffers as a multiple of the fetch concurrency")
}

func lookup(name string) *knob {
	mu.RLock()
	k := knobs[name]
	mu.RUnlock()
	return k
}

// Raw returns the knob's current int64 payload, or 0 for an unknown name.
func Raw(name string) int64 {
	if k := lookup(name); k != nil {
		return k.cur.Load()
	}
	return 0
}

// Bytes reads a KindBytes knob.
func Bytes(name string) int64 { return Raw(name) }

// Count reads a KindCount knob.
func Count(name string) int { return int(Raw(name)) }

// Dur reads a KindDuration knob.
func Dur(name string) time.Duration { return time.Duration(Raw(name)) }

// Ratio reads a KindRatio knob.
func Ratio(name string) float64 { return math.Float64frombits(uint64(Raw(name))) } //nolint:gosec

// IsSet reports whether the knob has been explicitly set — the difference
// between "the compiled default" and "set to a value that happens to equal
// the default". Use sites that OVERRIDE a per-client configuration (the
// range-split chunk size and concurrency, which also have boot flags) read
// this to decide whether the knob or the flag wins.
func IsSet(name string) bool {
	if k := lookup(name); k != nil {
		return k.set.Load()
	}
	return false
}

// Version counts applications that changed something. A caller that resets a
// ticker on change compares it across pulls.
func Version() uint64 { return version.Load() }

// Catalog lists every knob, name-sorted, with its compiled default.
func Catalog() []Def {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]Def, 0, len(knobs))
	for _, n := range order {
		out = append(out, knobs[n].def)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Entry is one row of a Snapshot.
type Entry struct {
	Def
	// Value is the current payload, Set says whether it was explicitly set.
	Value int64 `json:"-"`
	Set   bool  `json:"set"`
}

// Snapshot returns every knob's current value, name-sorted.
func Snapshot() []Entry {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]Entry, 0, len(knobs))
	for _, n := range order {
		k := knobs[n]
		out = append(out, Entry{Def: k.def, Value: k.cur.Load(), Set: k.set.Load()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Apply installs values wholesale: a knob named in vals takes that value, a
// knob absent from vals reverts to its compiled default. That makes a reset
// nothing more than dropping the key on the visor side. Unknown names are
// ignored (an older app pulling a newer visor's map). Reports whether
// anything changed, and bumps Version if so.
func Apply(vals map[string]int64) bool {
	mu.RLock()
	defer mu.RUnlock()
	changed := false
	for _, n := range order {
		k := knobs[n]
		v, ok := vals[n]
		if !ok {
			v = k.def.Default
		}
		// Both swaps ALWAYS run: || would short-circuit the second one away the
		// moment the value moved, leaving IsSet stale.
		valMoved := k.cur.Swap(v) != v
		setMoved := k.set.Swap(ok) != ok
		if valMoved || setMoved {
			changed = true
		}
	}
	if changed {
		version.Add(1)
	}
	return changed
}

// Reset returns every knob to its compiled default.
func Reset() bool { return Apply(nil) }

// Parse turns a human value into the knob's int64 payload. Bytes accept a
// plain number of bytes or a IEC/SI suffix (4MiB, 4M, 512KiB); durations take
// Go syntax (250ms, 5s, 4m); ratios a float; counts an integer.
func Parse(name, raw string) (int64, error) {
	k := lookup(name)
	if k == nil {
		return 0, fmt.Errorf("unknown setting %q", name)
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, fmt.Errorf("%s: empty value", name)
	}
	switch k.def.Kind {
	case KindDuration:
		d, err := time.ParseDuration(raw)
		if err != nil {
			return 0, fmt.Errorf("%s: %w", name, err)
		}
		if d <= 0 {
			return 0, fmt.Errorf("%s: must be positive, got %s", name, d)
		}
		return int64(d), nil
	case KindBytes:
		v, err := parseBytes(raw)
		if err != nil {
			return 0, fmt.Errorf("%s: %w", name, err)
		}
		if v <= 0 {
			return 0, fmt.Errorf("%s: must be positive, got %d", name, v)
		}
		return v, nil
	case KindCount:
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("%s: %w", name, err)
		}
		if v <= 0 {
			return 0, fmt.Errorf("%s: must be positive, got %d", name, v)
		}
		return v, nil
	case KindRatio:
		f, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return 0, fmt.Errorf("%s: %w", name, err)
		}
		if f <= 0 || math.IsInf(f, 0) || math.IsNaN(f) {
			return 0, fmt.Errorf("%s: must be a positive finite number, got %q", name, raw)
		}
		return ratio(f), nil
	}
	return 0, fmt.Errorf("%s: unhandled kind %q", name, k.def.Kind)
}

// Format renders a payload the way Parse accepts it back — Format then Parse
// is the identity on every kind.
func Format(name string, v int64) string {
	k := lookup(name)
	if k == nil {
		return strconv.FormatInt(v, 10)
	}
	switch k.def.Kind {
	case KindDuration:
		return time.Duration(v).String()
	case KindBytes:
		return formatBytes(v)
	case KindRatio:
		return strconv.FormatFloat(math.Float64frombits(uint64(v)), 'g', -1, 64) //nolint:gosec
	case KindCount:
		return strconv.FormatInt(v, 10)
	}
	return strconv.FormatInt(v, 10)
}

var byteUnits = []struct {
	suffix string
	mult   int64
}{
	{"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10},
	{"GB", 1e9}, {"MB", 1e6}, {"KB", 1e3},
	{"G", 1 << 30}, {"M", 1 << 20}, {"K", 1 << 10},
	{"B", 1},
}

func parseBytes(raw string) (int64, error) {
	s := strings.TrimSpace(raw)
	for _, u := range byteUnits {
		if len(s) > len(u.suffix) && strings.EqualFold(s[len(s)-len(u.suffix):], u.suffix) {
			n, err := strconv.ParseFloat(strings.TrimSpace(s[:len(s)-len(u.suffix)]), 64)
			if err != nil {
				return 0, err
			}
			return int64(n * float64(u.mult)), nil
		}
	}
	return strconv.ParseInt(s, 10, 64)
}

func formatBytes(v int64) string {
	for _, u := range []struct {
		suffix string
		mult   int64
	}{{"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10}} {
		if v >= u.mult && v%u.mult == 0 {
			return strconv.FormatInt(v/u.mult, 10) + u.suffix
		}
	}
	return strconv.FormatInt(v, 10)
}
