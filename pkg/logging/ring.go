// Package logging pkg/logging/ring.go c0-com-log
//
// Per-app ring buffer on top of the Broadcaster hook.
//
// The Broadcaster is already the single logrus hook the visor installs on its
// master logger, so its Fire sees every entry. In addition to fanning entries
// out to live subscribers (the `--verbose` gRPC stream), it records the most
// recent app-scoped entries into a small bounded ring keyed by app name. This
// lets a consumer (the proxy status page, `cli proxy log`) read back the recent
// route/transport events and app log lines for one app WITHOUT having held a
// subscription open the whole time — reusing the exact same tap the verbose
// stream uses rather than adding a second log sink.
//
// Two buckets per app:
//
//   - events: layered router / route-group / transport / setup / mux entries
//     that happen on behalf of the app (tagged app_name=<n> by the router's
//     scopedLog / RouteGroup.SetAppName). These are the "[router] …" /
//     "[RouteGroup …] …" lines.
//   - logs: the app's own output (_module=proc:<app>:<key> / app:<app>).
//
// Both are bounded (ringPerApp) and the number of tracked apps is capped
// (ringMaxApps, LRU-evicted) so memory never grows unbounded.
package logging

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

const (
	// ringMaxApps bounds how many distinct app names the ring tracks at
	// once; the least-recently-seen app is evicted when a new one arrives
	// past this cap.
	ringMaxApps = 32
	// ringPerApp bounds each app's events bucket and (separately) its logs
	// bucket. Matches the status renderer's maxLogLines cap so nothing is
	// captured that could never be shown.
	ringPerApp = 200
)

// Record is a value-copy snapshot of a logrus entry captured into the ring.
// The entry pointer itself must never be retained — logrus pools and reuses
// Entry values after Fire returns — so the fields we care about are copied out
// at capture time.
type Record struct {
	Time    time.Time
	Level   logrus.Level
	Module  string
	Message string
	Fields  map[string]string
	// IsEvent is true for a layered route/transport event (events bucket),
	// false for the app's own log output (logs bucket).
	IsEvent bool
}

type appRing struct {
	events   []Record
	logs     []Record
	lastSeen time.Time
}

// record captures e into the per-app ring when it resolves to an app. Called
// from Fire on the hot logging path, so it does the cheap app-resolution first
// and only locks/copies when there is a bucket to write.
func (b *Broadcaster) record(e *logrus.Entry) {
	module := ""
	if v, ok := e.Data[logModuleKey]; ok {
		if m, ok := v.(string); ok {
			module = m
		}
	}

	app := ""
	isEvent := false
	if a := appFromModule(module); a != "" {
		app = a // app's own output → logs bucket
	} else if a := appFromFields(e); a != "" {
		app = a // layered (router/rg/tp) entry tagged with the app → events bucket
		isEvent = true
	}
	if app == "" {
		return
	}

	rec := Record{
		Time:    e.Time,
		Level:   e.Level,
		Module:  module,
		Message: e.Message,
		IsEvent: isEvent,
	}
	if len(e.Data) > 0 {
		rec.Fields = make(map[string]string, len(e.Data))
		for k, v := range e.Data {
			if k == logModuleKey {
				continue
			}
			if s, ok := v.(string); ok {
				rec.Fields[k] = s
			} else {
				rec.Fields[k] = fmt.Sprintf("%v", v)
			}
		}
	}

	b.ringMu.Lock()
	defer b.ringMu.Unlock()
	if b.rings == nil {
		b.rings = make(map[string]*appRing)
	}
	r := b.rings[app]
	if r == nil {
		if len(b.rings) >= ringMaxApps {
			b.evictOldestLocked()
		}
		r = &appRing{}
		b.rings[app] = r
	}
	r.lastSeen = rec.Time
	if isEvent {
		r.events = appendCapped(r.events, rec)
	} else {
		r.logs = appendCapped(r.logs, rec)
	}
}

// RecentByApp returns copies of the recent events and logs captured for app,
// filtered to entries at least as severe as minLevel, oldest first. Returns
// nil, nil when nothing has been captured for the app.
func (b *Broadcaster) RecentByApp(app string, minLevel logrus.Level) (events, logs []Record) {
	if b == nil || app == "" {
		return nil, nil
	}
	b.ringMu.Lock()
	defer b.ringMu.Unlock()
	r := b.rings[app]
	if r == nil {
		return nil, nil
	}
	return filterByLevel(r.events, minLevel), filterByLevel(r.logs, minLevel)
}

// RecentMerged returns events+logs for app merged into a single time-ordered
// slice (oldest first), filtered by minLevel. This is the "reading the log"
// view `cli proxy log` prints.
func (b *Broadcaster) RecentMerged(app string, minLevel logrus.Level) []Record {
	events, logs := b.RecentByApp(app, minLevel)
	if len(events) == 0 && len(logs) == 0 {
		return nil
	}
	out := make([]Record, 0, len(events)+len(logs))
	out = append(out, events...)
	out = append(out, logs...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Time.Before(out[j].Time) })
	return out
}

func filterByLevel(in []Record, minLevel logrus.Level) []Record {
	if len(in) == 0 {
		return nil
	}
	out := make([]Record, 0, len(in))
	for _, r := range in {
		// logrus levels descend from Panic(0) to Trace(6): "at least as
		// severe as minLevel" means a numerically smaller-or-equal level.
		if r.Level <= minLevel {
			out = append(out, r)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// evictOldestLocked drops the least-recently-seen app. Caller holds ringMu.
func (b *Broadcaster) evictOldestLocked() {
	var oldestKey string
	var oldest time.Time
	first := true
	for k, r := range b.rings {
		if first || r.lastSeen.Before(oldest) {
			oldestKey = k
			oldest = r.lastSeen
			first = false
		}
	}
	if oldestKey != "" {
		delete(b.rings, oldestKey)
	}
}

func appendCapped(buf []Record, rec Record) []Record {
	buf = append(buf, rec)
	if len(buf) > ringPerApp {
		// Drop the oldest, keeping the tail. Copy down so the backing
		// array doesn't grow without bound.
		n := copy(buf, buf[len(buf)-ringPerApp:])
		buf = buf[:n]
	}
	return buf
}

// recordFormatter renders a captured Record the same way the visor's
// MasterLogger (and the `--verbose` gRPC stream) render a live entry, so a
// read-back line is byte-identical to what streamed live.
var recordFormatter = &TextFormatter{
	FullTimestamp:      true,
	AlwaysQuoteStrings: true,
	QuoteEmptyFields:   true,
	ForceFormatting:    true,
	TimestampFormat:    "2006-01-02T15:04:05.0000Z07:00",
}

// Format renders the Record as a single log line (no trailing newline),
// matching the visor's text log format: [timestamp] LEVEL [module]: message
// key=value…
func (r Record) Format() string {
	data := logrus.Fields{}
	if r.Module != "" {
		data[logModuleKey] = r.Module
	}
	for k, v := range r.Fields {
		data[k] = v
	}
	entry := &logrus.Entry{
		Time:    r.Time,
		Level:   r.Level,
		Message: r.Message,
		Data:    data,
	}
	b, err := recordFormatter.Format(entry)
	if err != nil {
		return fmt.Sprintf("[%s] %s [%s]: %s", r.Time.Format(time.RFC3339Nano), r.Level, r.Module, r.Message)
	}
	return strings.TrimRight(string(b), "\n")
}

// appFromModule extracts the app name from a proc/app-scoped _module value
// (proc:<app>:<key> or app:<app>[:<key>]). Returns "" for layered-module
// entries (router, RouteGroup …, transport) which are resolved via fields.
func appFromModule(module string) string {
	if rest, ok := strings.CutPrefix(module, "proc:"); ok {
		if app, _, ok := strings.Cut(rest, ":"); ok {
			return app
		}
		return rest
	}
	if rest, ok := strings.CutPrefix(module, "app:"); ok {
		if app, _, ok := strings.Cut(rest, ":"); ok {
			return app
		}
		return rest
	}
	return ""
}

// appFromFields resolves the originating app from the entry's fields. The
// visor's layers use different field names for the same thing (router rg-scoped
// logs use app_name; proc-manager uses app/appName; launcher uses cmd), so any
// of them names the app.
func appFromFields(e *logrus.Entry) string {
	for _, k := range []string{"app_name", "app", "appName", "cmd"} {
		if v, ok := e.Data[k]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}
