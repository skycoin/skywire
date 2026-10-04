// Package logging pkg/logging/slog.go c0-com-log
package logging

import (
	"context"
	"log/slog"

	"github.com/sirupsen/logrus"
)

// Slog returns a *slog.Logger that forwards to this Logger, for libraries
// that take a log/slog logger (e.g. github.com/0magnet/wisp). Levels map
// onto logrus' levels, so the logger's configured level still gates output,
// and an "err" attribute becomes the logrus error field.
func (logger *Logger) Slog() *slog.Logger {
	return slog.New(&slogHandler{fl: logger.FieldLogger})
}

// slogHandler is a slog.Handler writing through a logrus.FieldLogger.
type slogHandler struct {
	fl     logrus.FieldLogger
	prefix string // group prefix for attribute keys
}

func (h *slogHandler) Enabled(_ context.Context, l slog.Level) bool {
	switch v := h.fl.(type) {
	case *logrus.Entry:
		return v.Logger.IsLevelEnabled(toLogrusLevel(l))
	case *logrus.Logger:
		return v.IsLevelEnabled(toLogrusLevel(l))
	}
	return true
}

func (h *slogHandler) Handle(_ context.Context, r slog.Record) error {
	fields := make(logrus.Fields, r.NumAttrs())
	r.Attrs(func(a slog.Attr) bool {
		h.addAttr(fields, h.prefix, a)
		return true
	})
	e := h.fl.WithFields(fields)
	if !r.Time.IsZero() {
		e = e.WithTime(r.Time)
	}
	e.Log(toLogrusLevel(r.Level), r.Message)
	return nil
}

func (h *slogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	fields := make(logrus.Fields, len(attrs))
	for _, a := range attrs {
		h.addAttr(fields, h.prefix, a)
	}
	return &slogHandler{fl: h.fl.WithFields(fields), prefix: h.prefix}
}

func (h *slogHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	return &slogHandler{fl: h.fl, prefix: h.prefix + name + "."}
}

func (h *slogHandler) addAttr(fields logrus.Fields, prefix string, a slog.Attr) {
	v := a.Value.Resolve()
	if v.Kind() == slog.KindGroup {
		p := prefix
		if a.Key != "" {
			p += a.Key + "."
		}
		for _, ga := range v.Group() {
			h.addAttr(fields, p, ga)
		}
		return
	}
	if a.Key == "" {
		return
	}
	key := prefix + a.Key
	if key == "err" {
		key = logrus.ErrorKey
	}
	fields[key] = v.Any()
}

func toLogrusLevel(l slog.Level) logrus.Level {
	switch {
	case l < slog.LevelDebug:
		return logrus.TraceLevel
	case l < slog.LevelInfo:
		return logrus.DebugLevel
	case l < slog.LevelWarn:
		return logrus.InfoLevel
	case l < slog.LevelError:
		return logrus.WarnLevel
	}
	return logrus.ErrorLevel
}
