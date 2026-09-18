// Package skysettings pkg/skysocks/skysettings/skysettings.go
//
// A process-scoped registry of the skysocks-client tuning knobs the mux bench
// sweeps. Every entry's DEFAULT is the constant the client compiled with, so a
// client with nothing set behaves byte for byte as it does today; a knob only
// changes behavior once it has been explicitly set.
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
	"encoding/hex"
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
// the IEEE-754 bit pattern of its float64, a bool as 0/1 and an enum as the
// index of its value in Def.Enum.
type Kind string

// The knob kinds.
const (
	KindBytes    Kind = "bytes"
	KindCount    Kind = "count"
	KindDuration Kind = "duration"
	KindRatio    Kind = "ratio"
	// KindBool is a flag: 0 or 1, printed and parsed as false/true. It is the
	// kind a DEFAULT-OFF mechanism is turned on with, which is how a new
	// dataplane rule reaches the rig without a flag gate — the knob defaults to
	// today's behavior and the operator flips it live.
	KindBool Kind = "bool"
	KindEnum Kind = "enum"
	// KindList is a comma-separated list of tokens — a set of public keys, a
	// set of transport types. It is the one kind whose payload is NOT an
	// int64, so it travels beside the numeric map rather than inside it (see
	// ApplyText); everything else about it is an ordinary knob.
	KindList Kind = "list"
)

// Knob names. Lowercase dotted, grouped by the machinery they steer.
const (
	PoolFillInterval    = "pool.fill_interval"
	PoolRetryBackoffMin = "pool.retry_backoff_base"
	PoolRetryBackoffMax = "pool.retry_backoff_max"
	PoolRetryRounds     = "pool.retry_rounds"
	PoolStandbyRTTStale = "pool.standby_rtt_stale"
	PoolSize            = "pool.size"
	PoolFreeze          = "pool.freeze"
	PoolExcludePKs      = "pool.exclude_pks"
	PoolRequireTpTypes  = "pool.require_tp_types"

	TunnelProbeInterval    = "tunnel.probe_interval"
	TunnelLivenessInterval = "tunnel.liveness_interval"
	TunnelProbeFailWindow  = "tunnel.probe_fail_window"
	TunnelRTTAlpha         = "tunnel.rtt_alpha"
	TunnelPromoteInterval  = "tunnel.promote_interval"
	TunnelPromoteMargin    = "tunnel.promote_margin"
	TunnelPromoteHold      = "tunnel.promote_hold"
	TunnelParkMinHold      = "tunnel.park_min_hold"

	TunnelGoodputAlpha         = "tunnel.goodput_alpha"
	TunnelGoodputFresh         = "tunnel.goodput_fresh"
	TunnelGoodputMinWindows    = "tunnel.goodput_min_windows"
	TunnelPromoteGoodputMargin = "tunnel.promote_goodput_margin"
	TunnelPromoteQuietBytes    = "tunnel.promote_quiet_bytes"
	TunnelPromoteIdleBps       = "tunnel.promote_idle_bps"

	TunnelAuditionWindow  = "tunnel.audition_window"
	TunnelAuditionEvery   = "tunnel.audition_every"
	TunnelExitOpenPenalty = "tunnel.exit_open_penalty"
	TunnelMeterSampleMin  = "tunnel.meter_sample_min"
	TunnelMeterCapDecay   = "tunnel.meter_cap_decay"
	TunnelMeterFresh      = "tunnel.meter_fresh"
	TunnelSnubAfter       = "tunnel.snub_after"
	TunnelSnubHold        = "tunnel.snub_hold"
	TunnelDepthMargin     = "tunnel.depth_margin"
	TunnelCount           = "tunnel.count"

	MuxCap   = "mux.cap"
	MuxWidth = "mux.width"

	RangePort = "range.port"

	ChunkMaxBytes          = "chunk.max_bytes"
	ChunkProbeBytes        = "chunk.probe_bytes"
	ChunkMinBytes          = "chunk.min_bytes"
	ChunkPerTunnel         = "chunk.per_tunnel"
	ChunkConcurrency       = "chunk.concurrency"
	ChunkTunnelConcurrency = "chunk.tunnel_concurrency"
	ChunkRetryBudget       = "chunk.retry_budget"
	ChunkIdleTimeout       = "chunk.idle_timeout"
	ChunkFreeRetries       = "chunk.free_retries"
	ChunkOutstandingFactor = "chunk.outstanding_factor"
	ChunkDepthDynamic      = "chunk.depth_dynamic"
	ChunkDepthMin          = "chunk.depth_min"
	ChunkDepthMax          = "chunk.depth_max"

	UploadStripeMinBytes = "upload.stripe_min_bytes"
	UploadChunkBytes     = "upload.chunk_bytes"
	UploadMemBytes       = "upload.mem_bytes"
	UploadConcurrency    = "upload.concurrency"
	UploadReplayMaxBytes = "upload.replay_max_bytes"
	UploadProbeTTL       = "upload.probe_ttl"
	UploadAckTimeout     = "upload.ack_timeout"
	UploadIdleTimeout    = "upload.idle_timeout"
	UploadDurableWait    = "upload.durable_wait"
	UploadResendPasses   = "upload.resend_passes" //nolint:gosec // G101: "passes" here is a retry count, not a password
	UploadEarlyTries     = "upload.early_tries"
	UploadEarlyWaitMax   = "upload.early_wait_max"
	UploadBusyBackoff    = "upload.busy_backoff"
	UploadBusyTries      = "upload.busy_tries"
	UploadCutTries       = "upload.cut_tries"
	UploadReplayTries    = "upload.replay_tries"
	UploadDepthDynamic   = "upload.depth_dynamic"
	UploadBurstPlan      = "upload.burst_plan"

	SpreadMaxShare  = "spread.max_share"
	SpreadMinRoutes = "spread.min_routes"
	SpreadEndgame   = "spread.endgame"
	SpreadWeight    = "spread.weight"
)

// The values SpreadWeight takes. rate is capacity-proportional assignment (the
// performance end of the spectrum); even assigns equally whatever a tunnel has
// been shown to carry (the privacy end).
const (
	SpreadWeightRate = "rate"
	SpreadWeightEven = "even"
)

// Def is a knob's static description: everything the CLI needs to parse a
// value for it and to print it back.
type Def struct {
	Name    string `json:"name"`
	Kind    Kind   `json:"kind"`
	Default int64  `json:"-"`
	Doc     string `json:"doc,omitempty"`
	// Enum lists the accepted values of a KindEnum knob, in payload order:
	// the knob's int64 IS the index into this slice.
	Enum []string `json:"enum,omitempty"`
}

type knob struct {
	def Def
	cur atomic.Int64
	set atomic.Bool
	// list is the payload of a KindList knob: the tokens currently in force.
	// nil and empty mean the same thing (the knob's default, which for every
	// list knob is "no entries" — a filter that filters nothing).
	list atomic.Pointer[[]string]
	// zeroOK marks a count whose OFF state is 0 — spread.min_routes, where
	// "no floor" is the default. Every other count refuses a non-positive
	// value, since 0 there means a stalled gate.
	zeroOK bool
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

// registerZeroable is register for a COUNT whose off state is 0 — a floor of
// none, a ceiling of "inherit". Every other count refuses a non-positive
// value, since 0 there means a stalled gate.
func registerZeroable(name string, def int64, doc string) {
	register(name, KindCount, def, doc)
	knobs[name].zeroOK = true
}

// registerList registers a KindList knob. Its default is ALWAYS the empty list
// — a candidate filter that admits everything — so an unset client behaves as
// it does today.
func registerList(name string, doc string) {
	register(name, KindList, 0, doc)
}

// registerEnum registers a KindEnum knob: values in payload order, the default
// given as an index into them.
func registerEnum(name string, values []string, def int64, doc string) {
	register(name, KindEnum, def, doc)
	knobs[name].def.Enum = values
}

func ratio(f float64) int64 { return int64(math.Float64bits(f)) } //nolint:gosec

func boolVal(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

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
	// The live twin of --standby-pool. Default 8 is skyenv.SkysocksClientStandbyPool
	// (restated rather than imported: this package takes no skywire import so the
	// CLI can read the catalog without linking the client). Unset, the boot flag
	// still wins; set, the pool grows or shrinks to it on the next fill tick.
	registerZeroable(PoolSize, 8,
		"CEILING on tunnels held to the exit including the active ones; overrides --standby-pool once set (0 = active tunnels only)")
	register(PoolFreeze, KindBool, boolVal(false),
		"hold the active set still: the promoter makes no discretionary swap and the pool neither fills nor shrinks. A dead tunnel is still replaced, and a reconcile the operator asks for still runs")
	registerList(PoolExcludePKs,
		"public keys the pool's dials must not use as a first hop or an intermediate (comma-separated; empty = no exclusion)")
	registerList(PoolRequireTpTypes,
		"transport types a pool dial's FIRST HOP must have, e.g. stcpr,sudph (comma-separated; empty = any type)")

	register(TunnelProbeInterval, KindDuration, int64(5*time.Second),
		"how often each tunnel's RTT is re-measured (also the settings-pull tick)")
	register(TunnelLivenessInterval, KindDuration, int64(15*time.Second),
		"how often the keepalive loop probes each tunnel for liveness")
	register(TunnelProbeFailWindow, KindDuration, int64(60*time.Second),
		"how long a liveness ping may stay unanswered before it counts as a FAILED probe; a tunnel is retired only when it is BOTH silent for the hard-dead window and has a failed probe, so a local dataplane stall no longer retires healthy standbys")
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
	register(TunnelGoodputAlpha, KindRatio, ratio(0.25),
		"weight of each carrying window in a tunnel's delivered-goodput EWMA")
	register(TunnelGoodputFresh, KindDuration, int64(2*time.Minute),
		"how long a tunnel's measured goodput stays usable for a promotion decision; past it the tunnel is unmeasured again and the audition re-measures it")
	register(TunnelGoodputMinWindows, KindCount, 2,
		"carrying windows a tunnel needs before its goodput counts as measured")
	register(TunnelPromoteGoodputMargin, KindRatio, ratio(1.5),
		"how much more a standby must have been measured DELIVERING than the weakest active before it may swap in")
	register(TunnelPromoteQuietBytes, KindBytes, 256<<10,
		"bytes an active tunnel may move between two promoter ticks and still count as idle; above it the tunnel is mid-transfer and is never parked")
	register(TunnelPromoteIdleBps, KindBytes, 64<<10,
		"measured goodput at or above which an active tunnel counts as PRODUCTIVE, so a lower round trip alone can never park it")
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
	register(TunnelSnubAfter, KindDuration, int64(20*time.Second),
		"no byte and no ack for this long, with work outstanding, snubs a tunnel (floored at 2x its smoothed RTT; kept above the longest measured reorder-wedge clear so a head-of-line-blocked tunnel is not re-issued)")
	register(TunnelSnubHold, KindDuration, int64(10*time.Second),
		"how long a snubbed tunnel sits out before it is re-tried with ONE chunk")
	register(TunnelDepthMargin, KindDuration, int64(50*time.Millisecond),
		"added to a tunnel's RTT in the bandwidth-delay queue depth")
	// The live twin of --tunnels. Default 2 is skyenv.SkysocksClientTunnels.
	// Setting it reconciles the ACTIVE set on the next tick — promoting from
	// the pool to grow, parking the worst active tunnel to shrink — without
	// restarting the app.
	register(TunnelCount, KindCount, 2,
		"size of the ACTIVE tunnel set; overrides --tunnels once set, reconciled by promote/park on the next tick")

	// Per-APP mux width. The visor-wide adaptive ceiling and floor
	// (`proxy mux cap|width --visor-wide`) remain the default for every app
	// that sets neither; these two are read by the visor's dial path for the
	// app that owns the dial, so one app's legs pin no longer lands on every
	// other app's route groups. 0 = inherit the visor-wide value.
	registerZeroable(MuxCap, 0,
		"ceiling on the mux legs this app's dials ask for (0 = the visor-wide adaptive ceiling)")
	registerZeroable(MuxWidth, 0,
		"mux legs this app's dials ask for (0 = the visor-wide adaptive width)")

	register(RangePort, KindCount, 80,
		"destination port the splitter treats as plaintext HTTP; overrides --range-port once set")

	register(ChunkMaxBytes, KindBytes, 4<<20,
		"range-split chunk ceiling; overrides --range-chunk-kib once set")
	register(ChunkConcurrency, KindCount, 8,
		"concurrent range-split chunk fetches; overrides --range-concurrency once set")
	register(ChunkTunnelConcurrency, KindCount, 4,
		"chunks one tunnel may carry at once on a split download the spread policy steers")
	register(ChunkProbeBytes, KindBytes, 2<<20,
		"bytes chunk0 asks for — the size probe, and the no-split threshold")
	register(ChunkMinBytes, KindBytes, 1<<20,
		"smallest chunk the planner will produce")
	register(ChunkPerTunnel, KindCount, 2,
		"chunks the planner aims to give each active tunnel")
	register(ChunkRetryBudget, KindDuration, int64(15*time.Second),
		"how long a failed chunk keeps being refetched before it gives up")
	register(ChunkIdleTimeout, KindDuration, int64(15*time.Second),
		"rolling no-progress deadline on a chunk body read")
	register(ChunkFreeRetries, KindCount, 8,
		"refetches a tunnel death may buy a chunk without charging its budget")
	register(ChunkOutstandingFactor, KindCount, 2,
		"outstanding chunk buffers as a multiple of the fetch concurrency")
	register(ChunkDepthDynamic, KindBool, 0,
		"size the per-tunnel chunk depth from each tunnel's bandwidth-delay product instead of chunk.per_tunnel")
	register(ChunkDepthMin, KindCount, 2,
		"floor on the dynamic per-tunnel depth (the fixed depth it replaces)")
	register(ChunkDepthMax, KindCount, 8,
		"ceiling on the dynamic per-tunnel depth")

	register(UploadStripeMinBytes, KindBytes, 4<<20,
		"smallest POST body addressed in chunks rather than sent as one stream")
	register(UploadChunkBytes, KindBytes, 4<<20,
		"CEILING on one striped-upload chunk; the size is planned from the object under it")
	register(UploadMemBytes, KindBytes, 32<<20,
		"ceiling on the upload chunk buffers alive at once, whatever the body size")
	register(UploadConcurrency, KindCount, 4,
		"chunks one tunnel may carry at once on a striped upload")
	register(UploadReplayMaxBytes, KindBytes, 8<<20,
		"largest generic POST body remembered so it can be replayed on another tunnel")
	register(UploadProbeTTL, KindDuration, int64(5*time.Minute),
		"how long an origin's X-Chunked-Upload answer is trusted")
	register(UploadAckTimeout, KindDuration, int64(60*time.Second),
		"how long a chunk waits for its ack once its body is out")
	register(UploadIdleTimeout, KindDuration, int64(30*time.Second),
		"rolling no-progress deadline on a chunk body write")
	register(UploadDurableWait, KindDuration, int64(5*time.Second),
		"how long an acked chunk waits for the sink's prefix to reach it before it is re-sent")
	register(UploadResendPasses, KindCount, 3,
		"passes one chunk may be sent again because the sink's prefix never reached it")
	register(UploadEarlyTries, KindCount, 20,
		"425s one chunk may wait out before the upload gives up")
	register(UploadEarlyWaitMax, KindDuration, int64(5*time.Second),
		"ceiling on the Retry-After a 425 may impose")
	register(UploadBusyBackoff, KindDuration, int64(time.Second),
		"wait between retries when the sink answers 503, its sessions all taken")
	register(UploadBusyTries, KindCount, 3,
		"503s one chunk waits out before the upload fails")
	register(UploadCutTries, KindCount, 1,
		"times one chunk is sent again after the sink refused it as short — the body was cut in flight, not by the sink")
	register(UploadReplayTries, KindCount, 2,
		"times a generic POST may be replayed after its tunnel died uncommitted")
	register(UploadDepthDynamic, KindBool, boolVal(false),
		"size the per-tunnel upload depth from the bandwidth-delay product instead of upload.concurrency; shares chunk.depth_min/max and tunnel.depth_margin")
	register(UploadBurstPlan, KindBool, boolVal(true),
		"place a striped upload whose chunks are ALL admitted at once by measured upload capacity and RTT, keeping the object's last chunk off the slow route")

	// The spread policy (docs/design/route-spread-policy.md). The cap and the floor
	// ship ON — max_share 0.4 over at least 3 routes, the values criterion 10 was
	// measured with (2026-09-18) — endgame stays off and the weight is the
	// capacity-proportional one, which is what the chunk assignment already aims at.
	register(SpreadMaxShare, KindRatio, ratio(0.4),
		"largest fraction of one object's bytes any single route may carry (1 = uncapped)")
	registerZeroable(SpreadMinRoutes, 3,
		"routes an object must be spread over, promoting standbys to reach it (0 = no floor)")
	register(SpreadEndgame, KindBool, boolVal(false),
		"duplicate the last chunks on the fastest idle route and take the first to finish")
	registerEnum(SpreadWeight, []string{SpreadWeightRate, SpreadWeightEven}, 0,
		"how a route's share is chosen: rate = proportional to measured capacity, even = equal")
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

// Bool reads a KindBool knob.
func Bool(name string) bool { return Raw(name) != 0 }

// Ratio reads a KindRatio knob.
func Ratio(name string) float64 { return math.Float64frombits(uint64(Raw(name))) } //nolint:gosec

// Enum reads a KindEnum knob as the value's name; "" for an unknown knob or an
// index the catalog does not hold.
func Enum(name string) string {
	k := lookup(name)
	if k == nil {
		return ""
	}
	i := k.cur.Load()
	if i < 0 || i >= int64(len(k.def.Enum)) {
		return ""
	}
	return k.def.Enum[i]
}

// Strings reads a KindList knob. The returned slice is the live payload and
// must not be mutated by the caller.
func Strings(name string) []string {
	k := lookup(name)
	if k == nil {
		return nil
	}
	if p := k.list.Load(); p != nil {
		return *p
	}
	return nil
}

// IsList reports whether the named knob carries a token list rather than an
// int64 — the one thing a caller parsing `key=value` has to know before it
// picks which map the value belongs in.
func IsList(name string) bool {
	k := lookup(name)
	return k != nil && k.def.Kind == KindList
}

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
	// Text is the rendered payload of a KindList knob, empty for every other
	// kind (whose payload is Value).
	Text string `json:"text,omitempty"`
}

// Snapshot returns every knob's current value, name-sorted.
func Snapshot() []Entry {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]Entry, 0, len(knobs))
	for _, n := range order {
		k := knobs[n]
		e := Entry{Def: k.def, Value: k.cur.Load(), Set: k.set.Load()}
		if k.def.Kind == KindList {
			if p := k.list.Load(); p != nil {
				e.Text = strings.Join(*p, ",")
			}
		}
		out = append(out, e)
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
		if k.def.Kind == KindList {
			// A list knob's payload does not fit an int64 and is installed by
			// ApplyText; leaving it out here is what keeps the two halves of
			// one pull from clearing each other's IsSet.
			continue
		}
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

// ApplyText installs the KindList knobs wholesale, exactly as Apply does for
// the numeric ones: a knob named in text takes that value, a knob absent from
// it goes back to the empty list. Malformed entries are dropped rather than
// refused — the CLI validates on the way in, and an app must not stall on a
// value a newer visor sent it. Reports whether anything changed.
func ApplyText(text map[string]string) bool {
	mu.RLock()
	defer mu.RUnlock()
	changed := false
	for _, n := range order {
		k := knobs[n]
		if k.def.Kind != KindList {
			continue
		}
		next := splitList(text[n])
		prev := k.list.Load()
		if prev == nil && len(next) == 0 {
			continue
		}
		if prev != nil && equalList(*prev, next) {
			continue
		}
		k.list.Store(&next)
		k.set.Store(len(next) > 0)
		changed = true
	}
	if changed {
		version.Add(1)
	}
	return changed
}

func equalList(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// splitList is the one tokenizer: comma-separated, whitespace trimmed, empties
// dropped. Lowercasing is left to the use site, since a public key is
// case-insensitive hex and a transport type is already lowercase.
func splitList(raw string) []string {
	var out []string
	for _, p := range strings.Split(raw, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ParseList validates and normalises a KindList value the way Parse does for
// an int64 one. A pool.exclude_pks entry must be a full 66-hex-character
// public key — a truncated key would silently exclude nothing.
func ParseList(name, raw string) (string, error) {
	k := lookup(name)
	if k == nil {
		return "", fmt.Errorf("unknown setting %q", name)
	}
	if k.def.Kind != KindList {
		return "", fmt.Errorf("%s: not a list setting", name)
	}
	toks := splitList(raw)
	for i, t := range toks {
		if name == PoolExcludePKs {
			if len(t) != 66 {
				return "", fmt.Errorf("%s: %q is not a 66-character public key (never abbreviate a key here)", name, t)
			}
			if _, err := hex.DecodeString(t); err != nil {
				return "", fmt.Errorf("%s: %q is not hex: %w", name, t, err)
			}
		}
		toks[i] = strings.ToLower(t)
	}
	return strings.Join(toks, ","), nil
}

// Reset returns every knob to its compiled default.
func Reset() bool {
	numeric := Apply(nil)
	text := ApplyText(nil)
	return numeric || text
}

// Parse turns a human value into the knob's int64 payload. Bytes accept a
// plain number of bytes or a IEC/SI suffix (4MiB, 4M, 512KiB); durations take
// Go syntax (250ms, 5s, 4m); ratios a float; counts an integer.
func Parse(name, raw string) (int64, error) {
	k := lookup(name)
	if k == nil {
		return 0, fmt.Errorf("unknown setting %q", name)
	}
	if k.def.Kind == KindList {
		return 0, fmt.Errorf("%s: a list setting, parse it with ParseList", name)
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
		if v < 0 || (v == 0 && !k.zeroOK) {
			return 0, fmt.Errorf("%s: must be positive, got %d", name, v)
		}
		return v, nil
	case KindBool:
		switch strings.ToLower(raw) {
		case "true", "yes", "on", "1":
			return 1, nil
		case "false", "no", "off", "0":
			return 0, nil
		}
		return 0, fmt.Errorf("%s: want true or false, got %q", name, raw)
	case KindEnum:
		for i, v := range k.def.Enum {
			if strings.EqualFold(raw, v) {
				return int64(i), nil
			}
		}
		return 0, fmt.Errorf("%s: want one of %s, got %q", name, strings.Join(k.def.Enum, "|"), raw)
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
	case KindList:
		return strings.Join(Strings(name), ",")
	case KindDuration:
		return time.Duration(v).String()
	case KindBytes:
		return formatBytes(v)
	case KindBool:
		return strconv.FormatBool(v != 0)
	case KindRatio:
		return strconv.FormatFloat(math.Float64frombits(uint64(v)), 'g', -1, 64) //nolint:gosec
	case KindCount:
		return strconv.FormatInt(v, 10)
	case KindEnum:
		if v >= 0 && v < int64(len(k.def.Enum)) {
			return k.def.Enum[v]
		}
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
