// Package logging pkg/logging/hooks.go c0-com-log
package logging

import (
	"io"

	"github.com/sirupsen/logrus"
)

// DefaultHookLevel is the most verbose level a capture hook accepts unless the
// caller asks for something else.
//
// It is deliberately not logrus.AllLevels. A capture hook is a SECOND complete
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
const DefaultHookLevel = logrus.InfoLevel

// HookLevel returns the level named by a config's log_hook_level, or def when
// it is empty or not a level. debug puts debug lines into /debug/log.
func HookLevel(name string, def logrus.Level) logrus.Level {
	if name == "" {
		return def
	}
	lvl, err := LevelFromString(name)
	if err != nil {
		return def
	}
	return lvl
}

// LevelsUpTo returns the logrus levels at least as severe as level, i.e. the
// slice a logrus.Hook returns from Levels() to be fired only for those.
func LevelsUpTo(level logrus.Level) []logrus.Level {
	if level > logrus.TraceLevel {
		level = logrus.TraceLevel
	}
	out := make([]logrus.Level, level+1)
	copy(out, logrus.AllLevels[:level+1])
	return out
}

// WriteHook is a logrus.Hook that logs to an io.Writer
type WriteHook struct {
	w         io.Writer
	formatter logrus.Formatter
	levels    []logrus.Level
}

// NewWriteHook returns a new WriteHook accepting DefaultHookLevel and above.
func NewWriteHook(w io.Writer) *WriteHook {
	return NewWriteHookLevel(w, DefaultHookLevel)
}

// NewWriteHookLevel returns a new WriteHook that fires only for entries at
// least as severe as level. Use it when the call site knows what it wants to
// capture; NewWriteHook is the default.
func NewWriteHookLevel(w io.Writer, level logrus.Level) *WriteHook {
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
func (f *WriteHook) Levels() []logrus.Level {
	return f.levels
}

// Fire writes a logrus.Entry to the file
func (f *WriteHook) Fire(e *logrus.Entry) error {
	b, err := f.formatter.Format(e)
	if err != nil {
		return err
	}

	_, err = f.w.Write(b)
	return err
}
