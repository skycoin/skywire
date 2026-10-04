package logging

import (
	"errors"
	"io"
	"testing"

	"github.com/sirupsen/logrus"
)

type captureHook struct{ entries []*logrus.Entry }

func (h *captureHook) Levels() []logrus.Level { return logrus.AllLevels }

func (h *captureHook) Fire(e *logrus.Entry) error {
	h.entries = append(h.entries, e)
	return nil
}

func (h *captureHook) last() *logrus.Entry {
	if len(h.entries) == 0 {
		return nil
	}
	return h.entries[len(h.entries)-1]
}

func TestSlogBridge(t *testing.T) {
	l := logrus.New()
	l.SetOutput(io.Discard)
	l.SetLevel(logrus.InfoLevel)
	hook := &captureHook{}
	l.AddHook(hook)
	sl := (&Logger{FieldLogger: l.WithField("module", "wisp")}).Slog()

	sl.Debug("hidden")
	if n := len(hook.entries); n != 0 {
		t.Fatalf("debug passed an info-level logger: %d entries", n)
	}

	boom := errors.New("boom")
	sl.With("component", "wisp").WithGroup("g").Warn("stream closed", "id", 7, "err", boom)
	e := hook.last()
	if e == nil || e.Level != logrus.WarnLevel || e.Message != "stream closed" {
		t.Fatalf("unexpected entry %+v", e)
	}
	if e.Data["module"] != "wisp" || e.Data["component"] != "wisp" || e.Data["g.id"] != int64(7) {
		t.Fatalf("fields not carried: %v", e.Data)
	}
	if e.Data["g.err"] != boom {
		t.Fatalf("grouped err field: %v", e.Data)
	}

	sl.Info("plain", "err", boom)
	if hook.last().Data[logrus.ErrorKey] != boom {
		t.Fatalf("err not mapped to logrus error key: %v", hook.last().Data)
	}
}
