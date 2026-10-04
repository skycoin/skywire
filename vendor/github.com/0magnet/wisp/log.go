package wisp

import (
	"fmt"
	"log/slog"
)

// dlog is the package's debug logging over a *slog.Logger: every message
// this package logs is a debug detail of a session or a stream, so this is
// the whole of what it needs. A nil logger is slog.Default().
type dlog struct{ l *slog.Logger }

// defaultLogger is the logger a Config without one gets.
func defaultLogger(component string) *slog.Logger {
	return slog.Default().With("component", component)
}

func (d dlog) logger() *slog.Logger {
	if d.l == nil {
		return slog.Default()
	}
	return d.l
}

func (d dlog) Debug(msg string) { d.logger().Debug(msg) }

func (d dlog) Debugf(format string, args ...any) { d.logger().Debug(fmt.Sprintf(format, args...)) }

// WithError is a debug message about err.
func (d dlog) WithError(err error) dlogErr { return dlogErr{d, err} }

// dlogErr is a debug message with the error it is about.
type dlogErr struct {
	d   dlog
	err error
}

func (e dlogErr) Debug(msg string) { e.d.logger().Debug(msg, "err", e.err) }

func (e dlogErr) Debugf(format string, args ...any) {
	e.d.logger().Debug(fmt.Sprintf(format, args...), "err", e.err)
}
