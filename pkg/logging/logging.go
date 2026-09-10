/*
Package logging provides application logging utilities
*/
// Package logging pkg/logging/logging.go c0-com-log
package logging

import (
	"fmt"
	"io"
	"strings"

	"github.com/sirupsen/logrus"
)

var log = NewMasterLogger()

const (
	// logModuleKey is the key used for the module name data entry
	logModuleKey = "_module"
	// logPriorityKey is the log entry key for priority log statements
	logPriorityKey = "_priority"
	// logPriorityCritical is the log entry value for priority log statements
	logPriorityCritical = "CRITICAL"
)

// LevelFromString returns a logrus.Level from a string identifier.
//
// An unrecognized (or empty) identifier returns logrus.InfoLevel along with an
// error naming the offending string. It used to fall back to DebugLevel, which
// meant a missing or misspelled "log_level" silently ran a service at debug in
// production for callers that logged the error and carried on.
func LevelFromString(s string) (logrus.Level, error) {
	switch strings.ToLower(s) {
	case "debug":
		return logrus.DebugLevel, nil
	case "info", "notice":
		return logrus.InfoLevel, nil
	case "warn", "warning":
		return logrus.WarnLevel, nil
	case "error":
		return logrus.ErrorLevel, nil
	case "fatal", "critical":
		return logrus.FatalLevel, nil
	case "panic":
		return logrus.PanicLevel, nil
	case "trace":
		return logrus.TraceLevel, nil
	default:
		return logrus.InfoLevel, fmt.Errorf("unrecognized log level %q; want one of debug, info, warn, error, fatal, panic, trace", s)
	}
}

// MustGetLogger returns a package-aware logger from the master logger
func MustGetLogger(module string) *Logger {
	return log.PackageLogger(module)
}

// AddHook adds a hook to the global logger
func AddHook(hook logrus.Hook) {
	log.AddHook(hook)
}

// EnableColors enables colored logging
func EnableColors() {
	log.EnableColors()
}

// DisableColors disables colored logging
func DisableColors() {
	log.DisableColors()
}

// SetLevel sets the logger's minimum log level
func SetLevel(level logrus.Level) {
	log.SetLevel(level)
}

// GetLevel returns the logger level
func GetLevel() logrus.Level {
	return log.GetLevel()
}

// SetOutputTo sets the logger's output to an io.Writer
func SetOutputTo(w io.Writer) {
	log.Out = w
}

// Disable disables the logger completely
func Disable() {
	log.Out = io.Discard
}

// DebugEnabled reports whether the master logger emits at debug level or
// below. Use it to skip building log fields on a hot path: logrus's
// WithField allocates a new Entry and copies the field map whether or not
// the level is enabled, so an ungated WithField chain costs the same when
// the line is never printed.
func DebugEnabled() bool { return log.GetLevel() >= logrus.DebugLevel }

// TraceEnabled reports whether the master logger emits at trace level.
// Same rationale as DebugEnabled; trace carries the per-stream and
// per-frame diagnostics that are useless in aggregate and expensive to
// format at volume.
func TraceEnabled() bool { return log.GetLevel() >= logrus.TraceLevel }

// Trace emits msg at trace level through a logrus.FieldLogger, whose
// interface omits the Trace methods (see Logger.Tracef). Callers on a hot
// path must guard it with TraceEnabled: reaching the entry allocates.
func Trace(l logrus.FieldLogger, msg string) { l.WithFields(logrus.Fields{}).Trace(msg) }

// ScopedLogger returns a package-aware logger derived from the global master
// logger whose minimum level is independent of it. See
// MasterLogger.ScopedLogger.
func ScopedLogger(module string, level logrus.Level) *Logger {
	return log.ScopedLogger(module, level)
}
