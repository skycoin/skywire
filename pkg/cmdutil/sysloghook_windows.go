//go:build windows

// Package cmdutil pkg/cmdutil/sysloghook_windows.go c1-com-util
package cmdutil

import (
	"fmt"
	"strings"

	"github.com/skycoin/skywire/pkg/logging"
)

func (sf *ServiceFlags) sysLogHook(_ *logging.Logger, _ int) {
}

// LevelFromString returns a logging.Level and syslog.Priority from a string
// identifier. An unrecognized (or empty) identifier yields info level plus an
// error naming it — never debug, which would silently make an unattended
// service verbose in production.
func LevelFromString(s string) (logging.Level, int, error) {
	switch strings.ToLower(s) {
	case "debug":
		return logging.DebugLevel, 0, nil
	case "info", "notice":
		return logging.InfoLevel, 0, nil
	case "warn", "warning":
		return logging.WarnLevel, 0, nil
	case "error":
		return logging.ErrorLevel, 0, nil
	case "fatal", "critical":
		return logging.FatalLevel, 0, nil
	case "panic":
		return logging.PanicLevel, 0, nil
	case "trace":
		return logging.TraceLevel, 0, nil
	default:
		return logging.InfoLevel, 0, fmt.Errorf("%w: %q", ErrInvalidLogString, s)
	}
}
