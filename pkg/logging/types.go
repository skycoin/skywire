// Package logging pkg/logging/types.go c0-com-log
package logging

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// Level is a log level. Lower values are more severe, so a logger emits an
// entry when the entry's level is at most the logger's.
type Level uint32

// Log levels, most severe first.
const (
	PanicLevel Level = iota
	FatalLevel
	ErrorLevel
	WarnLevel
	InfoLevel
	DebugLevel
	TraceLevel
)

// AllLevels lists every level, most severe first.
var AllLevels = []Level{PanicLevel, FatalLevel, ErrorLevel, WarnLevel, InfoLevel, DebugLevel, TraceLevel}

// String returns the lowercase level name. Warn is "warning".
func (l Level) String() string {
	switch l {
	case TraceLevel:
		return "trace"
	case DebugLevel:
		return "debug"
	case InfoLevel:
		return "info"
	case WarnLevel:
		return "warning"
	case ErrorLevel:
		return "error"
	case FatalLevel:
		return "fatal"
	case PanicLevel:
		return "panic"
	}
	return "unknown"
}

// MarshalText implements encoding.TextMarshaler.
func (l Level) MarshalText() ([]byte, error) {
	if l > TraceLevel {
		return nil, fmt.Errorf("not a valid log level %d", l)
	}
	return []byte(l.String()), nil
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (l *Level) UnmarshalText(text []byte) error {
	v, err := ParseLevel(string(text))
	if err != nil {
		return err
	}
	*l = v
	return nil
}

// ParseLevel parses a level name. Unlike LevelFromString it rejects the
// skywire aliases (notice, critical, none, off).
func ParseLevel(s string) (Level, error) {
	switch strings.ToLower(s) {
	case "panic":
		return PanicLevel, nil
	case "fatal":
		return FatalLevel, nil
	case "error":
		return ErrorLevel, nil
	case "warn", "warning":
		return WarnLevel, nil
	case "info":
		return InfoLevel, nil
	case "debug":
		return DebugLevel, nil
	case "trace":
		return TraceLevel, nil
	}
	return 0, fmt.Errorf("not a valid log level: %q", s)
}

// Fields is a set of structured log fields.
type Fields map[string]any

// ErrorKey is the field WithError stores the error under.
const ErrorKey = "error"

// FieldLogger is the leveled, structured logging interface used throughout
// skywire. *Entry, *Logger and *MasterLogger implement it.
type FieldLogger interface {
	WithField(key string, value any) *Entry
	WithFields(fields Fields) *Entry
	WithError(err error) *Entry

	Debugf(format string, args ...any)
	Infof(format string, args ...any)
	Printf(format string, args ...any)
	Warnf(format string, args ...any)
	Warningf(format string, args ...any)
	Errorf(format string, args ...any)
	Fatalf(format string, args ...any)
	Panicf(format string, args ...any)

	Debug(args ...any)
	Info(args ...any)
	Print(args ...any)
	Warn(args ...any)
	Warning(args ...any)
	Error(args ...any)
	Fatal(args ...any)
	Panic(args ...any)

	Debugln(args ...any)
	Infoln(args ...any)
	Println(args ...any)
	Warnln(args ...any)
	Warningln(args ...any)
	Errorln(args ...any)
	Fatalln(args ...any)
	Panicln(args ...any)
}

// Hook is called for every entry at one of its levels, before the entry is
// written.
type Hook interface {
	Levels() []Level
	Fire(*Entry) error
}

// LevelHooks holds hooks by level.
type LevelHooks map[Level][]Hook

// Add registers hook for each of its levels.
func (h LevelHooks) Add(hook Hook) {
	for _, l := range hook.Levels() {
		h[l] = append(h[l], hook)
	}
}

// Fire calls every hook registered for level, stopping at the first error.
func (h LevelHooks) Fire(level Level, e *Entry) error {
	for _, hook := range h[level] {
		if err := hook.Fire(e); err != nil {
			return err
		}
	}
	return nil
}

// Formatter renders an entry as bytes. It may write into e.Buffer.
type Formatter interface {
	Format(e *Entry) ([]byte, error)
}

// New returns a standalone logger that writes to stderr at info level,
// independent of the master logger.
func New() *MasterLogger {
	m := &MasterLogger{
		Out:       os.Stderr,
		Formatter: &TextFormatter{},
		Hooks:     make(LevelHooks),
		ExitFunc:  os.Exit,
	}
	m.SetLevel(InfoLevel)
	return m
}

// NewEntry returns an entry with no fields that logs through l.
func NewEntry(l *MasterLogger) *Entry { return &Entry{Logger: l, Data: make(Fields, 6)} }

// StandardLogger returns the process-wide master logger.
func StandardLogger() *MasterLogger { return log }

// Discard returns a standalone logger that writes nothing.
func Discard() *MasterLogger {
	l := New()
	l.SetOutput(io.Discard)
	l.SetLevel(PanicLevel)
	return l
}
