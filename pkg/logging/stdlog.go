// Package logging pkg/logging/stdlog.go c0-com-log
//
// Adapter that lets third-party libraries which only accept a stdlib
// *log.Logger (e.g. armon/go-socks5's Config.Logger) funnel their output
// through logrus, so their lines get the same timestamp format, colors, and
// hooks as the rest of skywire instead of being written raw to stdout.
package logging

import (
	stdlog "log"
	"strings"

	"github.com/sirupsen/logrus"
)

// logrusWriter adapts a logrus FieldLogger to io.Writer. Each Write is one
// log record; a leading level tag (e.g. "[ERR]", as armon/go-socks5 emits)
// is stripped and mapped to the corresponding logrus level.
type logrusWriter struct {
	log logrus.FieldLogger
	// maxLevel caps severity: no record is emitted more severe than this
	// level. Zero means unset (no cap). Because logrus levels are
	// more-severe = smaller number, the clamp raises the numeric value.
	maxLevel logrus.Level
}

func (w logrusWriter) Write(p []byte) (int, error) {
	msg := strings.TrimRight(string(p), "\n")
	level := logrus.InfoLevel
	if strings.HasPrefix(msg, "[") {
		if end := strings.IndexByte(msg, ']'); end > 0 {
			switch strings.ToUpper(msg[1:end]) {
			case "ERR", "ERROR":
				level = logrus.ErrorLevel
			case "WARN", "WARNING":
				level = logrus.WarnLevel
			case "DEBUG":
				level = logrus.DebugLevel
			}
			msg = strings.TrimSpace(msg[end+1:])
		}
	}
	// Clamp severity to maxLevel. logrus levels are more-severe = smaller
	// number, so a level below the cap is raised up to it (0 = unset = no cap).
	if w.maxLevel != 0 && level < w.maxLevel {
		level = w.maxLevel
	}
	switch level {
	case logrus.ErrorLevel:
		w.log.Error(msg)
	case logrus.WarnLevel:
		w.log.Warn(msg)
	case logrus.DebugLevel:
		w.log.Debug(msg)
	default:
		w.log.Info(msg)
	}
	return len(p), nil
}

// NewStdLogger returns a stdlib *log.Logger that routes every line it
// receives through the given logrus logger (level auto-detected from a
// leading "[ERR]"/"[WARN]"/"[DEBUG]" tag, defaulting to info). Hand it to
// libraries that insist on *log.Logger so their output matches skywire's
// log format instead of writing uncolored lines straight to stdout.
func NewStdLogger(log logrus.FieldLogger) *stdlog.Logger {
	return stdlog.New(logrusWriter{log: log}, "", 0)
}

// NewStdLoggerLevel is like NewStdLogger but caps severity: no line is logged
// more severe than `maxLevel`. Use it for libraries (e.g. a SOCKS proxy serving
// external clients) whose "[ERR]" lines are routine, client-driven conditions
// rather than faults of this process.
func NewStdLoggerLevel(log logrus.FieldLogger, maxLevel logrus.Level) *stdlog.Logger {
	return stdlog.New(logrusWriter{log: log, maxLevel: maxLevel}, "", 0)
}
