// Package ui2 pkg/visor/ui2/logs.go c3-vis-core
package ui2

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// LogLevel orders log levels as the Angular logs tab does.
type LogLevel int

// The levels, lowest first.
const (
	LevelUnknown LogLevel = iota + 1
	LevelTrace
	LevelDebug
	LevelInfo
	LevelWarn
	LevelError
	LevelFatal
	LevelPanic
)

// LogLevels are the filter buttons, as the Angular tab labels them.
var LogLevels = []struct {
	Level LogLevel
	Label string
}{
	{LevelUnknown, "All"}, {LevelDebug, "Debug+"}, {LevelInfo, "Info+"}, {LevelWarn, "Warn+"},
	{LevelError, "Error+"}, {LevelFatal, "Fatal+"}, {LevelPanic, "Panic"},
}

// LogMax is how many entries the tab keeps, as the Angular one does.
const LogMax = 1000

// LogEntry is one runtime log line.
type LogEntry struct {
	Time   string
	Level  LogLevel
	Msg    string
	Func   string
	Module string
	Extra  []KV
}

// KV is one extra field of a log line.
type KV struct{ Name, Value string }

// LevelName is the level as the chip shows it.
func (e LogEntry) LevelName() string {
	switch e.Level {
	case LevelPanic:
		return "PANIC"
	case LevelFatal:
		return "FATAL"
	case LevelError:
		return "ERROR"
	case LevelWarn:
		return "WARN"
	case LevelInfo:
		return "INFO"
	case LevelDebug:
		return "DEBUG"
	case LevelTrace:
		return "TRACE"
	}
	return "-"
}

// LevelClass is the CSS class for the level chip.
func (e LogEntry) LevelClass() string {
	switch {
	case e.Level >= LevelError:
		return "lv-err"
	case e.Level == LevelWarn:
		return "lv-warn"
	case e.Level == LevelInfo:
		return "lv-info"
	}
	return "lv-dim"
}

var dupPrefix = regexp.MustCompile(`(?i)^\[\d{4}-\d{2}-\d{2}T[0-9:.,+\-Z]*\]\s+(PANIC|FATAL|ERROR|WARN(?:ING)?|INFO|DEBUG|TRACE)\s+`)

// ParseLogEntry decodes one JSON log line. ok is false for a malformed one.
func ParseLogEntry(raw string) (LogEntry, bool) {
	var m map[string]any
	if json.Unmarshal([]byte(raw), &m) != nil {
		return LogEntry{}, false
	}
	str := func(k string) string {
		if v, ok := m[k]; ok && v != nil {
			if s, ok := v.(string); ok {
				return s
			}
			return fmt.Sprint(v)
		}
		return ""
	}
	e := LogEntry{
		Time:   str("time"),
		Level:  parseLevel(str("level")),
		Msg:    dupPrefix.ReplaceAllString(str("msg"), ""),
		Func:   str("func"),
		Module: str("_module"),
	}
	if s := str("error"); s != "" {
		e.Extra = append(e.Extra, KV{"error", s})
	}
	var keys []string
	for k := range m {
		switch k {
		case "time", "_module", "msg", "func", "level", "log_line", "error":
		default:
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		e.Extra = append(e.Extra, KV{k, str(k)})
	}
	return e, true
}

func parseLevel(s string) LogLevel {
	s = strings.ToLower(s)
	for _, l := range []struct {
		name string
		lv   LogLevel
	}{{"panic", LevelPanic}, {"fatal", LevelFatal}, {"error", LevelError}, {"warn", LevelWarn}, {"info", LevelInfo}, {"debug", LevelDebug}, {"trace", LevelTrace}} {
		if strings.Contains(s, l.name) {
			return l.lv
		}
	}
	return LevelUnknown
}

// LogFilter is the level, module and text filter of the tab.
type LogFilter struct {
	MinLevel LogLevel
	Module   string
	Text     string
	Paused   bool
	moduleRe *regexp.Regexp
}

// ParseLogFilter reads the filter from a query. A bad module regex is
// returned as the error and filters nothing.
func ParseLogFilter(q url.Values) (LogFilter, error) {
	f := LogFilter{MinLevel: LevelUnknown, Module: q.Get("module"), Text: q.Get("q"), Paused: q.Get("paused") != ""}
	if n, err := strconv.Atoi(q.Get("level")); err == nil && n >= int(LevelUnknown) && n <= int(LevelPanic) {
		f.MinLevel = LogLevel(n)
	}
	if f.Module != "" {
		re, err := regexp.Compile(f.Module)
		if err != nil {
			return f, err
		}
		f.moduleRe = re
	}
	return f, nil
}

// Query is the filter as URL query parameters, with extra pairs appended.
func (f LogFilter) Query(extra ...string) string {
	q := url.Values{}
	if f.MinLevel > LevelUnknown {
		q.Set("level", strconv.Itoa(int(f.MinLevel)))
	}
	if f.Module != "" {
		q.Set("module", f.Module)
	}
	if f.Text != "" {
		q.Set("q", f.Text)
	}
	if f.Paused {
		q.Set("paused", "1")
	}
	for i := 0; i+1 < len(extra); i += 2 {
		q.Set(extra[i], extra[i+1])
	}
	return q.Encode()
}

// Match reports whether e passes the filter.
func (f LogFilter) Match(e LogEntry) bool {
	if e.Level < f.MinLevel {
		return false
	}
	if f.moduleRe != nil && !f.moduleRe.MatchString(e.Module) {
		return false
	}
	if t := strings.ToLower(f.Text); t != "" {
		if strings.Contains(strings.ToLower(e.Msg), t) || strings.Contains(strings.ToLower(e.Module), t) {
			return true
		}
		for _, kv := range e.Extra {
			if strings.Contains(strings.ToLower(kv.Name), t) || strings.Contains(strings.ToLower(kv.Value), t) {
				return true
			}
		}
		return false
	}
	return true
}

// Logs is a batch of entries for the tab.
type Logs struct {
	PK      string
	Filter  LogFilter
	Entries []LogEntry
	Cursor  int64
	Dropped int64
	Total   int64
	More    bool // older entries were cut to LogMax
	Err     string
}

// FilterLogs parses raw lines and keeps those f matches, the last LogMax.
func FilterLogs(raws []string, f LogFilter) (out []LogEntry, more bool) {
	for _, r := range raws {
		if e, ok := ParseLogEntry(r); ok && f.Match(e) {
			out = append(out, e)
		}
	}
	if len(out) > LogMax {
		return out[len(out)-LogMax:], true
	}
	return out, false
}

func (l Logs) url(rest string, extra ...string) string {
	return "visors/" + l.PK + "/logs" + rest + "?" + l.Filter.Query(extra...)
}
