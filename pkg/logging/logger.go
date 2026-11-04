// Package logging pkg/logging/logger.go c0-com-log
package logging

import (
	"bytes"
	"context"
	"io"
	"os"
	"slices"
	"sync"
	"sync/atomic"
	"time"
)

// Logger is a package-aware logger: a FieldLogger carrying the module name.
type Logger struct {
	FieldLogger
}

// Critical adds special critical-level fields for specially highlighted
// logging.
func (logger *Logger) Critical() FieldLogger {
	return logger.WithField(logPriorityKey, logPriorityCritical)
}

// WithTime overrides the time an entry is stamped with.
func (logger *Logger) WithTime(t time.Time) *Entry {
	return logger.WithFields(Fields{}).WithTime(t)
}

// Tracef logs at trace level (below Debug). FieldLogger omits the Trace
// methods, so this forwards through an entry. Used to keep low-value
// diagnostics (e.g. the CLI's CXO fetch-chain hit/miss notes) off ordinary
// stdout, since the CLI's package logger runs at Debug.
func (logger *Logger) Tracef(format string, args ...any) {
	logger.WithFields(Fields{}).Tracef(format, args...)
}

// WithAppName returns a derived Logger that attaches app_name=<name>
// to every entry. Empty name is a no-op (returns the receiver).
// Used by router-side scoping so 'cli proxy start --verbose' can
// match on the field.
func (logger *Logger) WithAppName(name string) *Logger {
	if name == "" {
		return logger
	}
	return &Logger{FieldLogger: logger.WithField("app_name", name)}
}

// MasterLogger owns an output, a formatter, hooks and a level, and creates
// package-aware loggers that write through it.
type MasterLogger struct {
	// Out is where formatted entries are written. Use SetOutput to change it
	// while the logger is in use.
	Out io.Writer
	// Formatter renders entries for Out.
	Formatter Formatter
	// Hooks are fired, by level, for every entry before it is written.
	Hooks LevelHooks
	// ExitFunc ends the program after a Fatal line. Nil means os.Exit.
	ExitFunc func(int)

	level atomic.Uint32
	mu    sync.Mutex
}

// Stderr is where a logger with no output of its own writes: the process's
// stderr, or that of an in-process run (cmdutil replaces it).
var Stderr = func() io.Writer { return os.Stderr }

// stderrWriter writes to whatever Stderr is at the time of each write.
type stderrWriter struct{}

func (stderrWriter) Write(p []byte) (int, error) { return Stderr().Write(p) }

// ExitFunc ends the program after a Fatal line. A host that runs commands in
// its own process replaces it (cmdutil.Exit).
var ExitFunc = os.Exit

// NewMasterLogger creates a new package-aware logger with formatting string
func NewMasterLogger() *MasterLogger {
	m := &MasterLogger{
		Out: stderrWriter{},
		Formatter: &TextFormatter{
			FullTimestamp:      true,
			AlwaysQuoteStrings: true,
			QuoteEmptyFields:   true,
			ForceFormatting:    true,
			DisableColors:      false,
			ForceColors:        false,
			TimestampFormat:    "2006-01-02T15:04:05.0000Z07:00",
		},
		Hooks:    make(LevelHooks),
		ExitFunc: func(code int) { ExitFunc(code) },
	}
	m.SetLevel(DebugLevel)
	return m
}

// PackageLogger instantiates a package-aware logger
func (logger *MasterLogger) PackageLogger(moduleName string) *Logger {
	return &Logger{
		FieldLogger: logger.WithField(logModuleKey, moduleName),
	}
}

// EnableColors enables colored logging
func (logger *MasterLogger) EnableColors() {
	logger.formatter().(*TextFormatter).DisableColors = false
}

// DisableColors disables colored logging
func (logger *MasterLogger) DisableColors() {
	logger.formatter().(*TextFormatter).DisableColors = true
}

// ScopedLogger returns a package-aware logger whose minimum level is
// independent of this master logger's, so setting it cannot change what any
// other logger in the process emits.
//
// Output and formatter are shared with the parent; hooks are copied as of this
// call. A scoped logger therefore still writes where everything else writes —
// only the level is its own. Hooks installed on the parent afterwards do not
// reach it, which is fine for the one caller that needs this: services install
// their hooks (syslog) at startup, before any service builds its logger.
func (logger *MasterLogger) ScopedLogger(module string, level Level) *Logger {
	logger.mu.Lock()
	hooks := make(LevelHooks, len(logger.Hooks))
	for lvl, hs := range logger.Hooks {
		hooks[lvl] = slices.Clone(hs)
	}
	scoped := &MasterLogger{
		Out:       logger.Out,
		Formatter: logger.Formatter,
		Hooks:     hooks,
		ExitFunc:  logger.ExitFunc,
	}
	logger.mu.Unlock()
	scoped.SetLevel(level)
	return scoped.PackageLogger(module)
}

// SetLevel sets the most verbose level the logger emits.
func (logger *MasterLogger) SetLevel(level Level) { logger.level.Store(uint32(level)) }

// GetLevel returns the most verbose level the logger emits.
func (logger *MasterLogger) GetLevel() Level { return Level(logger.level.Load()) }

// IsLevelEnabled reports whether entries at level are emitted.
func (logger *MasterLogger) IsLevelEnabled(level Level) bool { return logger.GetLevel() >= level }

// SetOutput sets where formatted entries are written.
func (logger *MasterLogger) SetOutput(w io.Writer) {
	logger.mu.Lock()
	logger.Out = w
	logger.mu.Unlock()
}

// SetFormatter sets the formatter for Out.
func (logger *MasterLogger) SetFormatter(f Formatter) {
	logger.mu.Lock()
	logger.Formatter = f
	logger.mu.Unlock()
}

// AddHook adds a hook for each of the hook's levels.
func (logger *MasterLogger) AddHook(hook Hook) {
	logger.mu.Lock()
	logger.Hooks.Add(hook)
	logger.mu.Unlock()
}

// ReplaceHooks installs hooks and returns the previous set.
func (logger *MasterLogger) ReplaceHooks(hooks LevelHooks) LevelHooks {
	logger.mu.Lock()
	old := logger.Hooks
	logger.Hooks = hooks
	logger.mu.Unlock()
	return old
}

// Exit runs ExitFunc, or os.Exit when it is nil.
func (logger *MasterLogger) Exit(code int) {
	if logger.ExitFunc == nil {
		os.Exit(code)
	}
	logger.ExitFunc(code)
}

func (logger *MasterLogger) formatter() Formatter {
	logger.mu.Lock()
	defer logger.mu.Unlock()
	return logger.Formatter
}

var bufPool = sync.Pool{New: func() any { return new(bytes.Buffer) }}

// emit fires the hooks for a finished entry, then formats it and writes it
// to Out. The formatter gets a copy of the entry carrying a pooled buffer,
// so a hook that kept e (the Broadcaster does) never sees that buffer.
func (logger *MasterLogger) emit(e *Entry) {
	logger.mu.Lock()
	hooks := slices.Clone(logger.Hooks[e.Level])
	f := logger.Formatter
	logger.mu.Unlock()

	for _, h := range hooks {
		if err := h.Fire(e); err != nil {
			stderrf("Failed to fire hook: %v\n", err)
			break
		}
	}

	buf := bufPool.Get().(*bytes.Buffer)
	buf.Reset()
	fe := *e
	fe.Buffer = buf
	b, err := f.Format(&fe)
	if err != nil {
		stderrf("Failed to format entry: %v\n", err)
	} else {
		logger.mu.Lock()
		if _, err := logger.Out.Write(b); err != nil {
			stderrf("Failed to write to log: %v\n", err)
		}
		logger.mu.Unlock()
	}
	bufPool.Put(buf)
}

func (logger *MasterLogger) entry() *Entry { return &Entry{Logger: logger} }

// WithField returns an entry with key set to value.
func (logger *MasterLogger) WithField(key string, value any) *Entry {
	return logger.entry().WithField(key, value)
}

// WithFields returns an entry with fields set.
func (logger *MasterLogger) WithFields(fields Fields) *Entry {
	return logger.entry().WithFields(fields)
}

// WithError returns an entry with err under ErrorKey.
func (logger *MasterLogger) WithError(err error) *Entry { return logger.entry().WithError(err) }

// WithContext returns an entry carrying ctx.
func (logger *MasterLogger) WithContext(ctx context.Context) *Entry {
	return logger.entry().WithContext(ctx)
}

// WithTime returns an entry stamped with t.
func (logger *MasterLogger) WithTime(t time.Time) *Entry { return logger.entry().WithTime(t) }

// Log logs args at level.
func (logger *MasterLogger) Log(level Level, args ...any) { logger.entry().Log(level, args...) }

// Logf logs a formatted message at level.
func (logger *MasterLogger) Logf(level Level, format string, args ...any) {
	logger.entry().Logf(level, format, args...)
}

// Logln logs args at level, spaced as fmt.Sprintln spaces them.
func (logger *MasterLogger) Logln(level Level, args ...any) { logger.entry().Logln(level, args...) }

// Trace logs at TraceLevel.
func (logger *MasterLogger) Trace(args ...any) { logger.entry().Trace(args...) }

// Debug logs at DebugLevel.
func (logger *MasterLogger) Debug(args ...any) { logger.entry().Debug(args...) }

// Info logs at InfoLevel.
func (logger *MasterLogger) Info(args ...any) { logger.entry().Info(args...) }

// Print logs at InfoLevel.
func (logger *MasterLogger) Print(args ...any) { logger.entry().Print(args...) }

// Warn logs at WarnLevel.
func (logger *MasterLogger) Warn(args ...any) { logger.entry().Warn(args...) }

// Warning logs at WarnLevel.
func (logger *MasterLogger) Warning(args ...any) { logger.entry().Warning(args...) }

// Error logs at ErrorLevel.
func (logger *MasterLogger) Error(args ...any) { logger.entry().Error(args...) }

// Fatal logs at FatalLevel, then exits.
func (logger *MasterLogger) Fatal(args ...any) { logger.entry().Fatal(args...) }

// Panic logs at PanicLevel, then panics.
func (logger *MasterLogger) Panic(args ...any) { logger.entry().Panic(args...) }

// Tracef logs at TraceLevel.
func (logger *MasterLogger) Tracef(format string, args ...any) {
	logger.entry().Tracef(format, args...)
}

// Debugf logs at DebugLevel.
func (logger *MasterLogger) Debugf(format string, args ...any) {
	logger.entry().Debugf(format, args...)
}

// Infof logs at InfoLevel.
func (logger *MasterLogger) Infof(format string, args ...any) { logger.entry().Infof(format, args...) }

// Printf logs at InfoLevel.
func (logger *MasterLogger) Printf(format string, args ...any) {
	logger.entry().Printf(format, args...)
}

// Warnf logs at WarnLevel.
func (logger *MasterLogger) Warnf(format string, args ...any) { logger.entry().Warnf(format, args...) }

// Warningf logs at WarnLevel.
func (logger *MasterLogger) Warningf(format string, args ...any) {
	logger.entry().Warningf(format, args...)
}

// Errorf logs at ErrorLevel.
func (logger *MasterLogger) Errorf(format string, args ...any) {
	logger.entry().Errorf(format, args...)
}

// Fatalf logs at FatalLevel, then exits.
func (logger *MasterLogger) Fatalf(format string, args ...any) {
	logger.entry().Fatalf(format, args...)
}

// Panicf logs at PanicLevel, then panics.
func (logger *MasterLogger) Panicf(format string, args ...any) {
	logger.entry().Panicf(format, args...)
}

// Traceln logs at TraceLevel.
func (logger *MasterLogger) Traceln(args ...any) { logger.entry().Traceln(args...) }

// Debugln logs at DebugLevel.
func (logger *MasterLogger) Debugln(args ...any) { logger.entry().Debugln(args...) }

// Infoln logs at InfoLevel.
func (logger *MasterLogger) Infoln(args ...any) { logger.entry().Infoln(args...) }

// Println logs at InfoLevel.
func (logger *MasterLogger) Println(args ...any) { logger.entry().Println(args...) }

// Warnln logs at WarnLevel.
func (logger *MasterLogger) Warnln(args ...any) { logger.entry().Warnln(args...) }

// Warningln logs at WarnLevel.
func (logger *MasterLogger) Warningln(args ...any) { logger.entry().Warningln(args...) }

// Errorln logs at ErrorLevel.
func (logger *MasterLogger) Errorln(args ...any) { logger.entry().Errorln(args...) }

// Fatalln logs at FatalLevel, then exits.
func (logger *MasterLogger) Fatalln(args ...any) { logger.entry().Fatalln(args...) }

// Panicln logs at PanicLevel, then panics.
func (logger *MasterLogger) Panicln(args ...any) { logger.entry().Panicln(args...) }

// Writer returns a pipe whose lines are logged at InfoLevel.
func (logger *MasterLogger) Writer() *io.PipeWriter { return logger.entry().Writer() }

// WriterLevel returns a pipe whose lines are logged at level.
func (logger *MasterLogger) WriterLevel(level Level) *io.PipeWriter {
	return logger.entry().WriterLevel(level)
}

// Level reports the minimum level this logger emits at. Loggers built by
// PackageLogger or ScopedLogger report their own master's level; anything else
// falls back to the process-global level.
func (logger *Logger) Level() Level {
	if e, ok := logger.FieldLogger.(*Entry); ok && e.Logger != nil {
		return e.Logger.GetLevel()
	}
	return GetLevel()
}

// IsLevelEnabled reports whether entries at level would be written, so a
// hot path can skip building fields for a disabled level.
func (logger *Logger) IsLevelEnabled(level Level) bool {
	switch l := logger.FieldLogger.(type) {
	case *Entry:
		return l.Logger.IsLevelEnabled(level)
	case *MasterLogger:
		return l.IsLevelEnabled(level)
	}
	return true
}
