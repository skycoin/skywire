/*
Package logging provides application logging utilities
*/
// Package logging pkg/logging/logging.go c0-com-log
package logging

import (
	"fmt"
	"io"
	"strings"
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

// LevelFromString returns a Level from a string identifier.
//
// An unrecognized (or empty) identifier returns InfoLevel along with an
// error naming the offending string. It used to fall back to DebugLevel, which
// meant a missing or misspelled "log_level" silently ran a service at debug in
// production for callers that logged the error and carried on.
func LevelFromString(s string) (Level, error) {
	switch strings.ToLower(s) {
	case "debug":
		return DebugLevel, nil
	case "info", "notice":
		return InfoLevel, nil
	case "warn", "warning":
		return WarnLevel, nil
	case "error":
		return ErrorLevel, nil
	case "fatal", "critical":
		return FatalLevel, nil
	case "panic":
		return PanicLevel, nil
	case "none", "off":
		// There is nothing quieter than panic, logged just before a crash.
		return PanicLevel, nil
	case "trace":
		return TraceLevel, nil
	default:
		return InfoLevel, fmt.Errorf("unrecognized log level %q; want one of debug, info, warn, error, fatal, panic, none, trace", s)
	}
}

// MustGetLogger returns a package-aware logger from the master logger
func MustGetLogger(module string) *Logger {
	return log.PackageLogger(module)
}

// AddHook adds a hook to the global logger
func AddHook(hook Hook) {
	log.AddHook(hook)
}

// SetExitFunc sets what a Fatal on the global logger calls after logging, in
// place of os.Exit, and returns the previous function so the caller can put
// it back; nil means os.Exit. For a host that runs command code inside its
// own process (the phone core's in-process `config gen`), where a Fatal would
// end the host. Not safe to call concurrently with a Fatal on this logger.
func SetExitFunc(fn func(int)) (prev func(int)) {
	prev = log.ExitFunc
	log.ExitFunc = fn
	return prev
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
func SetLevel(level Level) {
	log.SetLevel(level)
}

// GetLevel returns the logger level
func GetLevel() Level {
	return log.GetLevel()
}

// SetOutputTo sets the logger's output to an io.Writer
func SetOutputTo(w io.Writer) {
	log.SetOutput(w)
}

// Disable disables the logger completely
func Disable() {
	log.SetOutput(io.Discard)
}

// DebugEnabled reports whether the master logger emits at debug level or
// below. Use it to skip building log fields on a hot path:
// WithField allocates a new Entry and copies the field map whether or not
// the level is enabled, so an ungated WithField chain costs the same when
// the line is never printed.
func DebugEnabled() bool { return log.GetLevel() >= DebugLevel }

// TraceEnabled reports whether the master logger emits at trace level.
// Same rationale as DebugEnabled; trace carries the per-stream and
// per-frame diagnostics that are useless in aggregate and expensive to
// format at volume.
func TraceEnabled() bool { return log.GetLevel() >= TraceLevel }

// Trace emits msg at trace level through a FieldLogger, whose
// interface omits the Trace methods (see Logger.Tracef). Callers on a hot
// path must guard it with TraceEnabled: reaching the entry allocates.
func Trace(l FieldLogger, msg string) { l.WithFields(Fields{}).Trace(msg) }

// ScopedLogger returns a package-aware logger derived from the global master
// logger whose minimum level is independent of it. See
// MasterLogger.ScopedLogger.
func ScopedLogger(module string, level Level) *Logger {
	return log.ScopedLogger(module, level)
}
