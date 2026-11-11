// Package logging pkg/logging/hooks.go c0-com-log
package logging

import (
	"io"
)

// DefaultHookLevel is the most verbose level a capture hook accepts unless the
// caller asks for something else.
//
// It is deliberately not AllLevels. A capture hook is a SECOND complete
// formatting pass over every entry it accepts: WriteHook.Fire below runs
// TextFormatter.Format into a fresh buffer for each entry, on top of the pass
// the logger already did for stdout. Measured on a production dmsg-server
// running at debug: ~67k lines/min reached the hook, ~10 MB/min of formatted
// output poured into a 256 KiB RingBuffer (DefaultRingBufferBytes). That is
// ~1.5 seconds of retained history on the /debug/log endpoint the ring feeds,
// bought by doubling the formatting cost of every log line in the process.
//
// Info and above is what /debug/log is actually read for, and the services
// default to info anyway (skyenv.LogLevel), so this changes nothing about what
// the endpoint returns at the normal level — it only keeps the debug/trace
// firehose out of the second formatter.
const DefaultHookLevel = InfoLevel

// HookLevel returns the level named by a config's log_hook_level, or def when
// it is empty or not a level. debug puts debug lines into /debug/log.
func HookLevel(name string, def Level) Level {
	if name == "" {
		return def
	}
	lvl, err := LevelFromString(name)
	if err != nil {
		return def
	}
	return lvl
}

// LevelsUpTo returns the levels at least as severe as level, i.e. the
// slice a Hook returns from Levels() to be fired only for those.
func LevelsUpTo(level Level) []Level {
	if level > TraceLevel {
		level = TraceLevel
	}
	out := make([]Level, level+1)
	copy(out, AllLevels[:level+1])
	return out
}

// WriteHook is a Hook that logs to an io.Writer
type WriteHook struct {
	w         io.Writer
	formatter Formatter
	levels    []Level
}

// NewWriteHook returns a new WriteHook accepting DefaultHookLevel and above.
func NewWriteHook(w io.Writer) *WriteHook {
	return NewWriteHookLevel(w, DefaultHookLevel)
}

// NewWriteHookLevel returns a new WriteHook that fires only for entries at
// least as severe as level. Use it when the call site knows what it wants to
// capture; NewWriteHook is the default.
func NewWriteHookLevel(w io.Writer, level Level) *WriteHook {
	return &WriteHook{
		w: w,
		formatter: &TextFormatter{
			DisableColors:      true,
			FullTimestamp:      true,
			AlwaysQuoteStrings: true,
			QuoteEmptyFields:   true,
			ForceFormatting:    true,
		},
		levels: LevelsUpTo(level),
	}
}

// Levels returns the levels accepted by the WriteHook. Entries below the
// configured level never reach Fire, and so never pay the second
// TextFormatter.Format pass.
func (f *WriteHook) Levels() []Level {
	return f.levels
}

// Fire writes a Entry to the file
func (f *WriteHook) Fire(e *Entry) error {
	b, err := f.formatter.Format(e)
	if err != nil {
		return err
	}

	_, err = f.w.Write(b)
	return err
}

// NewFormatterHook returns a WriteHook that renders entries at least as
// severe as level with f, e.g. a JSONFormatter for a rotating log file.
func NewFormatterHook(w io.Writer, level Level, f Formatter) *WriteHook {
	return &WriteHook{w: w, formatter: f, levels: LevelsUpTo(level)}
}
