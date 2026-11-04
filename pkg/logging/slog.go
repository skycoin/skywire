// Package logging pkg/logging/slog.go c0-com-log
package logging

import (
	"context"
	"log/slog"
	"maps"
	"time"
)

// Slog returns a *slog.Logger that writes through this Logger: same fields,
// level, formatter and hooks. Levels map onto Level, so the configured level
// still gates output, and an "err" attribute is stored under ErrorKey.
func (logger *Logger) Slog() *slog.Logger {
	return slog.New(&Handler{e: logger.WithFields(nil)})
}

// Slog returns a *slog.Logger that writes through this master logger.
func (logger *MasterLogger) Slog() *slog.Logger {
	return slog.New(&Handler{e: logger.entry()})
}

// NewSlog returns a *slog.Logger for module on the master logger, the slog
// counterpart of MustGetLogger.
func NewSlog(module string) *slog.Logger { return MustGetLogger(module).Slog() }

// Handler is a slog.Handler that renders records exactly as the rest of the
// logger's entries are rendered and fires the same hooks.
type Handler struct {
	e      *Entry // logger and fields from WithAttrs
	prefix string // group prefix for attribute keys
}

// Enabled reports whether the logger emits records at l.
func (h *Handler) Enabled(_ context.Context, l slog.Level) bool {
	return h.e.Logger.IsLevelEnabled(fromSlogLevel(l))
}

// Handle writes r.
func (h *Handler) Handle(ctx context.Context, r slog.Record) error {
	data := make(Fields, len(h.e.Data)+r.NumAttrs())
	maps.Copy(data, h.e.Data)
	r.Attrs(func(a slog.Attr) bool {
		addAttr(data, h.prefix, a)
		return true
	})
	t := r.Time
	if t.IsZero() {
		t = time.Now()
	}
	h.e.Logger.emit(&Entry{
		Logger:  h.e.Logger,
		Data:    data,
		Time:    t,
		Level:   fromSlogLevel(r.Level),
		Message: r.Message,
		Context: ctx,
	})
	return nil
}

// WithAttrs returns a handler that adds attrs to every record.
func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	fields := make(Fields, len(attrs))
	for _, a := range attrs {
		addAttr(fields, h.prefix, a)
	}
	return &Handler{e: h.e.WithFields(fields), prefix: h.prefix}
}

// WithGroup returns a handler that prefixes later attribute keys with name.
func (h *Handler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	return &Handler{e: h.e, prefix: h.prefix + name + "."}
}

func addAttr(fields Fields, prefix string, a slog.Attr) {
	v := a.Value.Resolve()
	if v.Kind() == slog.KindGroup {
		p := prefix
		if a.Key != "" {
			p += a.Key + "."
		}
		for _, ga := range v.Group() {
			addAttr(fields, p, ga)
		}
		return
	}
	if a.Key == "" {
		return
	}
	key := prefix + a.Key
	if key == "err" {
		key = ErrorKey
	}
	fields[key] = v.Any()
}

// fromSlogLevel maps a slog level onto the nearest Level at or below its
// severity.
func fromSlogLevel(l slog.Level) Level {
	switch {
	case l < slog.LevelDebug:
		return TraceLevel
	case l < slog.LevelInfo:
		return DebugLevel
	case l < slog.LevelWarn:
		return InfoLevel
	case l < slog.LevelError:
		return WarnLevel
	}
	return ErrorLevel
}
