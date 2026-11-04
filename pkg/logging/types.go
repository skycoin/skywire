// Package logging pkg/logging/types.go c0-com-log
package logging

import "github.com/sirupsen/logrus"

// The names below let code outside this package log without importing
// logrus, so the backend can move to log/slog behind them.

// FieldLogger is the leveled, structured logging interface used throughout skywire.
type FieldLogger = logrus.FieldLogger

// Fields is a set of structured log fields.
type Fields = logrus.Fields

// Entry is a log line in the making: a logger plus the fields attached to it.
type Entry = logrus.Entry

// Level is a log level. Lower values are more severe.
type Level = logrus.Level

// Hook is called for every entry at one of its levels.
type Hook = logrus.Hook

// LevelHooks holds hooks by level.
type LevelHooks = logrus.LevelHooks

// Formatter renders an entry as bytes.
type Formatter = logrus.Formatter

// JSONFormatter renders an entry as a JSON object on one line.
type JSONFormatter = logrus.JSONFormatter

// Log levels, most severe first.
const (
	PanicLevel = logrus.PanicLevel
	FatalLevel = logrus.FatalLevel
	ErrorLevel = logrus.ErrorLevel
	WarnLevel  = logrus.WarnLevel
	InfoLevel  = logrus.InfoLevel
	DebugLevel = logrus.DebugLevel
	TraceLevel = logrus.TraceLevel
)

// AllLevels lists every level, most severe first.
var AllLevels = logrus.AllLevels

// ParseLevel parses a level name as logrus does. Unlike LevelFromString it
// rejects the skywire aliases (notice, critical, none, off).
func ParseLevel(s string) (Level, error) { return logrus.ParseLevel(s) }

// New returns a standalone logger that writes to stderr at info level,
// independent of the master logger.
func New() *MasterLogger { return &MasterLogger{Logger: logrus.New()} }

// NewEntry returns an entry with no fields that logs through l.
func NewEntry(l *MasterLogger) *Entry { return logrus.NewEntry(l.Logger) }

// StandardLogger returns the process-wide default logger.
func StandardLogger() *MasterLogger { return &MasterLogger{Logger: logrus.StandardLogger()} }
