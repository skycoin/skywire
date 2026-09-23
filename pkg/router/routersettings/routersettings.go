// Package routersettings pkg/router/routersettings/routersettings.go c2-net-routing
//
// A process-scoped registry of the router / mux tuning knobs, so every
// dataplane constant the bench sweeps is settable and readable at runtime
// through `skywire cli route settings` instead of a rebuild and a fleet
// deploy. Every entry's DEFAULT is the constant pkg/router compiled with
// (asserted by TestCatalogDefaultsMatchConstants), so a visor that never sets
// one behaves byte for byte as it did.
//
// It is the pkg/skysocks/skysettings shape with two additions the router
// needed:
//
//   - a per-APP override layer, so a subject client and its paired reference
//     client can run different values on one visor. Overrides are resolved
//     into an immutable View once per route group and re-resolved only when
//     Version() moves — never per packet;
//   - a Min floor per knob, so a cadence that becomes a live ticker (the
//     send-window refresh) cannot be driven to a spin.
//
// The package is deliberately free of skywire imports: the CLI and the
// cliout shape read the catalog from here without linking pkg/router.
package routersettings

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
// the IEEE-754 bit pattern of its float64 and a bool as 0/1.
type Kind string

// The knob kinds.
const (
	KindBytes    Kind = "bytes"
	KindCount    Kind = "count"
	KindDuration Kind = "duration"
	KindRatio    Kind = "ratio"
	// KindBool is a flag: 0 or 1, printed and parsed as false/true. It is the
	// kind a DEFAULT-ON mechanism is turned OFF with, which is how a negotiated
	// capability (per-frame noise, SACK, HoL retransmit) stops being advertised
	// on NEW route groups without a flag gate or a rebuild.
	KindBool Kind = "bool"
	// KindList is a comma-separated list of tokens — a set of public keys, a
	// set of transport types. Its payload is NOT an int64, so it travels beside
	// the numeric map (see SetList/Strings) rather than inside it; a list knob
	// is visor-wide only, with no per-app override.
	KindList Kind = "list"
)

// Def is a knob's static description: everything a caller needs to parse a
// value for it and to print it back.
type Def struct {
	Name string `json:"name"`
	Kind Kind   `json:"kind"`
	// Default is the constant pkg/router compiled with.
	Default int64 `json:"-"`
	// Min is the floor a value is refused below (payload units, so nanoseconds
	// for a duration and float bits are NOT compared here — a ratio's floor is
	// held in MinRatio). Zero means "the kind's own rule": positive.
	Min      int64   `json:"-"`
	MinRatio float64 `json:"-"`
	// MaxRatio, when non-zero, is the exclusive ceiling of a KindRatio knob —
	// a fraction that must stay inside (min, max), such as a hysteresis margin
	// that could never be cleared at 1.
	MaxRatio float64 `json:"-"`
	Doc      string  `json:"doc,omitempty"`
}

// Knob is one registered setting. Callers hold the pointer (obtained once at
// package init) and read it directly, so a use site on the send path costs one
// atomic load and no map lookup.
type Knob struct {
	def    Def
	idx    int
	cur    atomic.Int64
	set    atomic.Bool
	zeroOK bool
	// signed marks a count whose sentinel values run BELOW zero (dial.tunnel_legs
	// uses -1 for "follow the visor's mux width"), so Min may itself be negative
	// and the positive-only rule does not apply.
	signed bool
	// list is the payload of a KindList knob: the tokens currently in force.
	// nil and empty mean the same thing (the knob's default, which for every
	// list knob is "no entries" — a filter that filters nothing).
	list atomic.Pointer[[]string]
}

// Name is the knob's catalog name.
func (k *Knob) Name() string { return k.def.Name }

// Def returns the static description.
func (k *Knob) Def() Def { return k.def }

// Raw is the current payload.
func (k *Knob) Raw() int64 { return k.cur.Load() }

// Ratio reads a KindRatio knob.
func (k *Knob) Ratio() float64 { return math.Float64frombits(uint64(k.cur.Load())) } //nolint:gosec

// Duration reads a KindDuration knob.
func (k *Knob) Duration() time.Duration { return time.Duration(k.cur.Load()) }

// Bytes reads a KindBytes knob.
func (k *Knob) Bytes() int64 { return k.cur.Load() }

// Int reads a KindCount (or KindBytes) knob as an int.
func (k *Knob) Int() int { return int(k.cur.Load()) }

// Bool reads a KindBool knob.
func (k *Knob) Bool() bool { return k.cur.Load() != 0 }

// Strings reads a KindList knob's tokens. Nil-safe; nil and empty both mean
// "no entries", the default that filters nothing.
func (k *Knob) Strings() []string {
	if k == nil {
		return nil
	}
	if p := k.list.Load(); p != nil {
		return *p
	}
	return nil
}

// IsSet reports whether the knob has been explicitly set — the difference
// between "the compiled default" and "set to a value that equals the default".
func (k *Knob) IsSet() bool { return k.set.Load() }

var (
	mu      sync.RWMutex
	byName  = map[string]*Knob{}
	order   []*Knob
	appVals = map[string]map[string]int64{}
	version atomic.Uint64

	// views caches one resolved View per app plus the global view under "".
	// Dropped wholesale on any change; rebuilt lazily by Resolve.
	views = map[string]*View{}
)

// Register adds a knob to the catalog and returns its handle. Panics on a
// duplicate name: the catalog is built at init from package-level vars, so a
// clash is a programming error, not a runtime condition.
func Register(name string, kind Kind, def int64, doc string) *Knob {
	return register(Def{Name: name, Kind: kind, Default: def, Doc: doc})
}

// RegisterMin is Register with an explicit floor in payload units — the sane
// floor a live cadence needs so it cannot be driven to a spin.
func RegisterMin(name string, kind Kind, def, min int64, doc string) *Knob {
	return register(Def{Name: name, Kind: kind, Default: def, Min: min, Doc: doc})
}

// RegisterRatio adds a KindRatio knob with a float floor (exclusive of values
// below it; a ratio at the floor is accepted).
func RegisterRatio(name string, def, min float64, doc string) *Knob {
	return register(Def{Name: name, Kind: KindRatio, Default: RatioBits(def), MinRatio: min, Doc: doc})
}

// RegisterRatioRange is RegisterRatio with an exclusive ceiling as well: a
// fraction that must stay inside (min, max).
func RegisterRatioRange(name string, def, min, max float64, doc string) *Knob {
	return register(Def{Name: name, Kind: KindRatio, Default: RatioBits(def), MinRatio: min, MaxRatio: max, Doc: doc})
}

// RegisterBool adds a KindBool knob.
func RegisterBool(name string, def bool, doc string) *Knob {
	return register(Def{Name: name, Kind: KindBool, Default: BoolBits(def), Doc: doc})
}

// RegisterZeroable is Register for a count whose OFF state is 0.
func RegisterZeroable(name string, kind Kind, def int64, doc string) *Knob {
	k := register(Def{Name: name, Kind: kind, Default: def, Doc: doc})
	k.zeroOK = true
	return k
}

// RegisterSigned is Register for a count whose OFF or sentinel values run
// BELOW zero — dial.tunnel_legs uses -1 for "follow the visor's mux width" —
// with an explicit floor that may itself be negative.
func RegisterSigned(name string, kind Kind, def, min int64, doc string) *Knob {
	k := register(Def{Name: name, Kind: kind, Default: def, Min: min, Doc: doc})
	k.signed = true
	return k
}

// RegisterScale adds a KindRatio knob that accepts 0 — a multiplier whose zero
// means "drop this term from the score", not "unset". Negative is still
// refused.
func RegisterScale(name string, def float64, doc string) *Knob {
	k := register(Def{Name: name, Kind: KindRatio, Default: RatioBits(def), Doc: doc})
	k.zeroOK = true
	return k
}

// RegisterList adds a KindList knob. Its default is ALWAYS the empty list — a
// candidate filter that admits everything — so an unset visor behaves as it
// does today.
func RegisterList(name string, doc string) *Knob {
	return register(Def{Name: name, Kind: KindList, Doc: doc})
}

func register(d Def) *Knob {
	mu.Lock()
	defer mu.Unlock()
	if _, dup := byName[d.Name]; dup {
		panic("routersettings: duplicate knob " + d.Name)
	}
	k := &Knob{def: d, idx: len(order)}
	k.cur.Store(d.Default)
	byName[d.Name] = k
	order = append(order, k)
	views = map[string]*View{}
	return k
}

// RatioBits encodes a float64 as the int64 payload of a KindRatio knob.
func RatioBits(f float64) int64 { return int64(math.Float64bits(f)) } //nolint:gosec

// BoolBits encodes a bool as the int64 payload of a KindBool knob.
func BoolBits(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// Lookup returns the knob, or nil.
func Lookup(name string) *Knob {
	mu.RLock()
	defer mu.RUnlock()
	return byName[name]
}

// Version counts applications that changed something. A holder that re-resolves
// its View on a change notification compares it across ticks.
func Version() uint64 { return version.Load() }

// View is one app's resolved values: the global value of every knob with that
// app's overrides folded in. Immutable — a change builds a new one — so a route
// group can hold the pointer and read it without a lock.
type View struct {
	app  string
	ver  uint64
	vals []int64
}

// App is the app name the view resolves for; "" is the visor-wide view.
func (v *View) App() string {
	if v == nil {
		return ""
	}
	return v.app
}

// Version is the catalog version the view was built at.
func (v *View) Version() uint64 {
	if v == nil {
		return 0
	}
	return v.ver
}

func (v *View) raw(k *Knob) int64 {
	if v == nil || k == nil || k.idx >= len(v.vals) {
		if k == nil {
			return 0
		}
		return k.cur.Load()
	}
	return v.vals[k.idx]
}

// Ratio reads a KindRatio knob through the view.
func (v *View) Ratio(k *Knob) float64 { return math.Float64frombits(uint64(v.raw(k))) } //nolint:gosec

// Duration reads a KindDuration knob through the view.
func (v *View) Duration(k *Knob) time.Duration { return time.Duration(v.raw(k)) }

// Bytes reads a KindBytes knob through the view.
func (v *View) Bytes(k *Knob) int64 { return v.raw(k) }

// Int reads a KindCount knob through the view.
func (v *View) Int(k *Knob) int { return int(v.raw(k)) }

// Bool reads a KindBool knob through the view.
func (v *View) Bool(k *Knob) bool { return v.raw(k) != 0 }

// Resolve returns the immutable view for app ("" for the visor-wide values).
// Cheap to call, but a caller on a data path should hold the pointer and
// re-resolve only when Version() moves — that is what Holder does.
func Resolve(app string) *View {
	mu.RLock()
	if v, ok := views[app]; ok {
		mu.RUnlock()
		return v
	}
	mu.RUnlock()

	mu.Lock()
	defer mu.Unlock()
	if v, ok := views[app]; ok {
		return v
	}
	v := &View{app: app, ver: version.Load(), vals: make([]int64, len(order))}
	for i, k := range order {
		v.vals[i] = k.cur.Load()
	}
	if ov := appVals[app]; app != "" && ov != nil {
		for name, val := range ov {
			if k := byName[name]; k != nil {
				v.vals[k.idx] = val
			}
		}
	}
	views[app] = v
	return v
}

// Holder carries an app's resolved view for one route group. Reads are a plain
// atomic pointer load; Refresh is what re-resolves, and is called once when the
// group is built and again only on a change notification (the group's
// send-window service tick), never per packet.
type Holder struct {
	app atomic.Pointer[string]
	v   atomic.Pointer[View]
}

// NewHolder resolves app's view once and returns a holder for it.
func NewHolder(app string) *Holder {
	h := &Holder{}
	h.SetApp(app)
	return h
}

// SetApp re-points the holder at another app's values and resolves at once.
// The route group learns its app name after it is built (SetAppName), so the
// holder starts on the visor-wide view and moves when the tag arrives.
func (h *Holder) SetApp(app string) {
	if h == nil {
		return
	}
	a := app
	h.app.Store(&a)
	h.v.Store(Resolve(app))
}

// View is the resolved values. Nil-safe: a nil holder reads the live globals.
func (h *Holder) View() *View {
	if h == nil {
		return nil
	}
	return h.v.Load()
}

// Refresh re-resolves if the catalog moved since the view was built. Reports
// whether anything was re-resolved, so a caller that resets a ticker on change
// can act on it.
func (h *Holder) Refresh() bool {
	if h == nil {
		return false
	}
	cur := h.v.Load()
	if cur != nil && cur.ver == version.Load() {
		return false
	}
	app := ""
	if a := h.app.Load(); a != nil {
		app = *a
	}
	h.v.Store(Resolve(app))
	return true
}

func bump() {
	views = map[string]*View{}
	version.Add(1)
}

// SetValue installs a payload for one knob visor-wide. The value must already
// have passed Parse (or be a default), which is what enforces the floors.
func SetValue(name string, v int64) error {
	mu.Lock()
	defer mu.Unlock()
	k := byName[name]
	if k == nil {
		return fmt.Errorf("unknown setting %q", name)
	}
	if err := check(k, v); err != nil {
		return err
	}
	k.cur.Store(v)
	k.set.Store(true)
	bump()
	return nil
}

// Set parses raw for the named knob and installs it visor-wide.
func Set(name, raw string) error {
	if k := Lookup(name); k != nil && k.def.Kind == KindList {
		return SetList(name, raw)
	}
	v, err := Parse(name, raw)
	if err != nil {
		return err
	}
	return SetValue(name, v)
}

// SetList installs a KindList knob's tokens visor-wide, replacing whatever it
// currently holds. An empty raw value clears it back to "no entries", the
// default. List knobs have no per-app override.
func SetList(name, raw string) error {
	mu.Lock()
	defer mu.Unlock()
	k := byName[name]
	if k == nil {
		return fmt.Errorf("unknown setting %q", name)
	}
	if k.def.Kind != KindList {
		return fmt.Errorf("%s: not a list knob", name)
	}
	toks := splitCSV(raw)
	k.list.Store(&toks)
	k.set.Store(true)
	bump()
	return nil
}

func splitCSV(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// SetApp parses raw and installs it as an override for route groups owned by
// app. An empty app is the visor-wide set. List knobs refuse a non-empty app:
// they have no per-app override.
func SetApp(app, name, raw string) error {
	if app == "" {
		return Set(name, raw)
	}
	if k := Lookup(name); k != nil && k.def.Kind == KindList {
		return fmt.Errorf("%s: list knobs are visor-wide only", name)
	}
	v, err := Parse(name, raw)
	if err != nil {
		return err
	}
	mu.Lock()
	defer mu.Unlock()
	k := byName[name]
	if k == nil {
		return fmt.Errorf("unknown setting %q", name)
	}
	if err := check(k, v); err != nil {
		return err
	}
	if appVals[app] == nil {
		appVals[app] = map[string]int64{}
	}
	appVals[app][name] = v
	bump()
	return nil
}

// Reset returns every knob to its compiled default and drops every per-app
// override — the whole control surface back to the binary's own behavior.
func Reset() {
	mu.Lock()
	defer mu.Unlock()
	for _, k := range order {
		k.cur.Store(k.def.Default)
		k.set.Store(false)
		if k.def.Kind == KindList {
			k.list.Store(nil)
		}
	}
	appVals = map[string]map[string]int64{}
	bump()
}

// ResetApp drops one app's overrides, so its groups fall back to the
// visor-wide values.
func ResetApp(app string) {
	if app == "" {
		Reset()
		return
	}
	mu.Lock()
	defer mu.Unlock()
	delete(appVals, app)
	bump()
}

func check(k *Knob, v int64) error {
	switch k.def.Kind {
	case KindRatio:
		f := math.Float64frombits(uint64(v)) //nolint:gosec
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return fmt.Errorf("%s: must be a finite number", k.def.Name)
		}
		if f < k.def.MinRatio || (k.def.MinRatio == 0 && f <= 0 && !k.zeroOK) {
			return fmt.Errorf("%s: must be at least %g, got %g", k.def.Name, minRatioOf(k), f)
		}
		if k.def.MaxRatio > 0 && f >= k.def.MaxRatio {
			return fmt.Errorf("%s: must be below %g, got %g", k.def.Name, k.def.MaxRatio, f)
		}
	case KindBool:
		if v != 0 && v != 1 {
			return fmt.Errorf("%s: want 0 or 1", k.def.Name)
		}
	case KindDuration, KindBytes, KindCount:
		if k.signed {
			// A signed count carries sentinel values below zero; Min is the
			// real floor and the positive-only rule below does not apply.
			if v < k.def.Min {
				return fmt.Errorf("%s: must be at least %s, got %s",
					k.def.Name, FormatKnob(k, k.def.Min), FormatKnob(k, v))
			}
			return nil
		}
		if k.def.Min > 0 {
			if v < k.def.Min {
				return fmt.Errorf("%s: must be at least %s, got %s",
					k.def.Name, FormatKnob(k, k.def.Min), FormatKnob(k, v))
			}
			return nil
		}
		if v < 0 || (v == 0 && !k.zeroOK) {
			return fmt.Errorf("%s: must be positive, got %s", k.def.Name, FormatKnob(k, v))
		}
	}
	return nil
}

func minRatioOf(k *Knob) float64 {
	if k.def.MinRatio > 0 {
		return k.def.MinRatio
	}
	return 0
}

// Entry is one row of a Snapshot: the knob, its live value and where it came
// from.
type Entry struct {
	Def
	// Value is the live payload; Formatted is that payload the way Parse takes
	// it back, so a snapshot round-trips through `route settings k=v`.
	Value     int64  `json:"-"`
	Formatted string `json:"value"`
	// DefaultStr is the compiled default, formatted.
	DefaultStr string `json:"default"`
	Set        bool   `json:"set"`
}

// Snapshot returns every knob's live value, name-sorted. Format then Parse is
// the identity on every kind, so the map a caller saves restores the visor.
func Snapshot() []Entry {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]Entry, 0, len(order))
	for _, k := range order {
		v := k.cur.Load()
		defStr := FormatKnob(k, k.def.Default)
		if k.def.Kind == KindList {
			// The compiled default of every list knob is "no entries"; the
			// default int64 payload (0) formatted as a list would be misread.
			defStr = ""
		}
		out = append(out, Entry{
			Def:        k.def,
			Value:      v,
			Formatted:  FormatKnob(k, v),
			DefaultStr: defStr,
			Set:        k.set.Load(),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Overrides returns the knobs explicitly set visor-wide, formatted — the map
// that is persisted to the visor config and replayed at boot.
func Overrides() map[string]string {
	mu.RLock()
	defer mu.RUnlock()
	out := map[string]string{}
	for _, k := range order {
		if k.set.Load() {
			out[k.def.Name] = FormatKnob(k, k.cur.Load())
		}
	}
	return out
}

// AppOverrides returns every app's overrides, formatted, for persistence.
func AppOverrides() map[string]map[string]string {
	mu.RLock()
	defer mu.RUnlock()
	out := map[string]map[string]string{}
	for app, vals := range appVals {
		m := map[string]string{}
		for name, v := range vals {
			if k := byName[name]; k != nil {
				m[name] = FormatKnob(k, v)
			}
		}
		if len(m) > 0 {
			out[app] = m
		}
	}
	return out
}

// Apply installs a map of formatted values visor-wide, leaving knobs the map
// does not name alone. Unknown names are reported rather than ignored: a typo
// in `route settings` must not read as success. Applied in name order so a
// partial failure is reproducible.
func Apply(vals map[string]string) error { return applyTo("", vals) }

// ApplyApp installs a map of formatted values as app's overrides.
func ApplyApp(app string, vals map[string]string) error { return applyTo(app, vals) }

func applyTo(app string, vals map[string]string) error {
	names := make([]string, 0, len(vals))
	for n := range vals {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if err := SetApp(app, n, vals[n]); err != nil {
			return err
		}
	}
	return nil
}

// Catalog lists every knob, name-sorted, with its compiled default formatted.
func Catalog() []Def {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]Def, 0, len(order))
	for _, k := range order {
		out = append(out, k.def)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Names lists every knob name, sorted.
func Names() []string {
	defs := Catalog()
	out := make([]string, 0, len(defs))
	for _, d := range defs {
		out = append(out, d.Name)
	}
	return out
}

// Parse turns a human value into the knob's int64 payload. Bytes accept a
// plain byte count or an IEC/SI suffix (8MiB, 64K); durations take Go syntax
// (250ms, 5s); ratios a float; counts an integer; bools true/false.
func Parse(name, raw string) (int64, error) {
	k := Lookup(name)
	if k == nil {
		return 0, fmt.Errorf("unknown setting %q", name)
	}
	raw = strings.TrimSpace(raw)
	if k.def.Kind == KindList {
		// A list knob's payload is not an int64, and its default is the EMPTY
		// list, so "" is a valid value here (the round-trip of every default
		// is what makes `route settings --json` a restorable save). Parse
		// exists for lists only so a caller that validates before applying
		// (the CLI) does not choke on the kind; the tokens go through SetList.
		return 0, nil
	}
	if raw == "" {
		return 0, fmt.Errorf("%s: empty value", name)
	}
	var (
		v   int64
		err error
	)
	switch k.def.Kind {
	case KindDuration:
		var d time.Duration
		if d, err = time.ParseDuration(raw); err != nil {
			return 0, fmt.Errorf("%s: %w", name, err)
		}
		v = int64(d)
	case KindBytes:
		if v, err = parseBytes(raw); err != nil {
			return 0, fmt.Errorf("%s: %w", name, err)
		}
	case KindCount:
		if v, err = strconv.ParseInt(raw, 10, 64); err != nil {
			return 0, fmt.Errorf("%s: %w", name, err)
		}
	case KindBool:
		switch strings.ToLower(raw) {
		case "true", "yes", "on", "1":
			v = 1
		case "false", "no", "off", "0":
			v = 0
		default:
			return 0, fmt.Errorf("%s: want true or false, got %q", name, raw)
		}
	case KindRatio:
		var f float64
		if f, err = strconv.ParseFloat(raw, 64); err != nil {
			return 0, fmt.Errorf("%s: %w", name, err)
		}
		v = RatioBits(f)
	case KindList:
		// A list knob's payload is not an int64; Parse exists here only so a
		// caller that validates before applying (the CLI) does not choke on
		// its kind. The actual value is installed by Set/SetList.
		return 0, nil
	default:
		return 0, fmt.Errorf("%s: unhandled kind %q", name, k.def.Kind)
	}
	if err := check(k, v); err != nil {
		return 0, err
	}
	return v, nil
}

// Format renders a payload the way Parse accepts it back.
func Format(name string, v int64) string {
	k := Lookup(name)
	if k == nil {
		return strconv.FormatInt(v, 10)
	}
	return FormatKnob(k, v)
}

// FormatKnob is Format for a handle the caller already holds.
func FormatKnob(k *Knob, v int64) string {
	if k.def.Kind == KindList {
		return strings.Join(k.Strings(), ",")
	}
	switch k.def.Kind {
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
