// Package logging pkg/logging/logger.go c0-com-log
package logging

import (
	"os"
	"time"

	"github.com/sirupsen/logrus"
)

// Logger wraps logrus.FieldLogger
type Logger struct {
	logrus.FieldLogger
}

// Critical adds special critical-level fields for specially highlighted logging,
// since logrus lacks a distinct critical field and does not have configurable log levels
func (logger *Logger) Critical() logrus.FieldLogger {
	return logger.WithField(logPriorityKey, logPriorityCritical)
}

// WithTime overrides time, used by logger.
func (logger *Logger) WithTime(t time.Time) *logrus.Entry {
	return logger.WithFields(logrus.Fields{}).WithTime(t)
}

// Tracef logs at trace level (below Debug). The embedded logrus.FieldLogger
// interface omits the Trace methods, so a *Logger otherwise can't emit at
// trace without reaching through WithField; this forwards through the entry.
// Used to keep low-value diagnostics (e.g. the CLI's CXO fetch-chain hit/miss
// notes) off ordinary stdout, since the CLI's package logger runs at Debug.
func (logger *Logger) Tracef(format string, args ...interface{}) {
	logger.WithFields(logrus.Fields{}).Tracef(format, args...)
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

// MasterLogger wraps logrus.Logger and is able to create new package-aware loggers
type MasterLogger struct {
	*logrus.Logger
}

// NewMasterLogger creates a new package-aware logger with formatting string
func NewMasterLogger() *MasterLogger {
	hooks := make(logrus.LevelHooks)

	return &MasterLogger{
		Logger: &logrus.Logger{
			Out: os.Stderr,
			Formatter: &TextFormatter{
				FullTimestamp:      true,
				AlwaysQuoteStrings: true,
				QuoteEmptyFields:   true,
				ForceFormatting:    true,
				DisableColors:      false,
				ForceColors:        false,
				TimestampFormat:    "2006-01-02T15:04:05.0000Z07:00",
			},
			Hooks: hooks,
			Level: logrus.DebugLevel,
		},
	}
}

// PackageLogger instantiates a package-aware logger
func (logger *MasterLogger) PackageLogger(moduleName string) *Logger {
	return &Logger{
		FieldLogger: logger.WithField(logModuleKey, moduleName),
	}
}

// EnableColors enables colored logging
func (logger *MasterLogger) EnableColors() {
	logger.Formatter.(*TextFormatter).DisableColors = false
}

// DisableColors disables colored logging
func (logger *MasterLogger) DisableColors() {
	logger.Formatter.(*TextFormatter).DisableColors = true
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
func (logger *MasterLogger) ScopedLogger(module string, level logrus.Level) *Logger {
	hooks := make(logrus.LevelHooks, len(logger.Hooks))
	for lvl, hs := range logger.Hooks {
		hooks[lvl] = append([]logrus.Hook(nil), hs...)
	}
	scoped := &MasterLogger{Logger: &logrus.Logger{
		Out:          logger.Out,
		Formatter:    logger.Formatter,
		Hooks:        hooks,
		Level:        level,
		ExitFunc:     logger.ExitFunc,
		ReportCaller: logger.ReportCaller,
	}}
	return scoped.PackageLogger(module)
}

// Level reports the minimum level this logger emits at. Loggers built by
// PackageLogger or ScopedLogger report their own master's level; anything else
// falls back to the process-global level.
func (logger *Logger) Level() logrus.Level {
	if e, ok := logger.FieldLogger.(*logrus.Entry); ok && e.Logger != nil {
		return e.Logger.GetLevel()
	}
	return GetLevel()
}
