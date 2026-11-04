//go:build !windows

// Package cmdutil pkg/cmdutil/sysloghook_unix.go c0-com-util
package cmdutil

import (
	"fmt"
	"log/syslog"
	"strings"

	logrussyslog "github.com/sirupsen/logrus/hooks/syslog"

	"github.com/skycoin/skywire/pkg/logging"
)

func (sf *ServiceFlags) sysLogHook(log *logging.Logger, sysLvl int) {
	hook, err := logrussyslog.NewSyslogHook(sf.SyslogNet, sf.Syslog, syslog.Priority(sysLvl), sf.Tag)
	if err != nil {
		log.WithError(err).
			WithField("net", sf.SyslogNet).
			WithField("addr", sf.Syslog).
			Fatal("Failed to connect to syslog daemon.")
	}
	logging.AddHook(hook)
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
