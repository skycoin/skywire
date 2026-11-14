//go:build !windows

// Package cmdutil pkg/cmdutil/sysloghook_unix.go c1-com-util
package cmdutil

import (
	"fmt"
	"log/syslog"
	"strings"

	"github.com/skycoin/skywire/pkg/logging"
)

func (sf *ServiceFlags) sysLogHook(log *logging.Logger, sysLvl int) {
	w, err := syslog.Dial(sf.SyslogNet, sf.Syslog, syslog.Priority(sysLvl), sf.Tag)
	if err != nil {
		log.WithError(err).
			WithField("net", sf.SyslogNet).
			WithField("addr", sf.Syslog).
			Fatal("Failed to connect to syslog daemon.")
	}
	logging.AddHook(syslogHook{w})
}

// syslogHook sends every entry, rendered by its logger's formatter, to
// syslog at the matching severity.
type syslogHook struct{ w *syslog.Writer }

func (syslogHook) Levels() []logging.Level { return logging.AllLevels }

func (h syslogHook) Fire(e *logging.Entry) error {
	line, err := e.String()
	if err != nil {
		return err
	}
	switch e.Level {
	case logging.PanicLevel, logging.FatalLevel:
		return h.w.Crit(line)
	case logging.ErrorLevel:
		return h.w.Err(line)
	case logging.WarnLevel:
		return h.w.Warning(line)
	case logging.InfoLevel:
		return h.w.Info(line)
	default:
		return h.w.Debug(line)
	}
}

// LevelFromString returns a logging.Level and syslog.Priority from a string
// identifier. An unrecognized (or empty) identifier yields info level plus an
// error naming it — never debug, which would silently make an unattended
// service verbose in production.
func LevelFromString(s string) (logging.Level, int, error) {
	switch strings.ToLower(s) {
	case "debug":
		return logging.DebugLevel, int(syslog.LOG_DEBUG), nil
	case "info", "notice":
		return logging.InfoLevel, int(syslog.LOG_INFO), nil
	case "warn", "warning":
		return logging.WarnLevel, int(syslog.LOG_WARNING), nil
	case "error":
		return logging.ErrorLevel, int(syslog.LOG_ERR), nil
	case "fatal", "critical":
		return logging.FatalLevel, int(syslog.LOG_CRIT), nil
	case "panic":
		return logging.PanicLevel, int(syslog.LOG_EMERG), nil
	case "trace":
		return logging.TraceLevel, int(syslog.LOG_DEBUG), nil
	default:
		return logging.InfoLevel, int(syslog.LOG_INFO), fmt.Errorf("%w: %q", ErrInvalidLogString, s)
	}
}
