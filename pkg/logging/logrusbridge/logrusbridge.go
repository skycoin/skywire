// Package logrusbridge pkg/logging/logrusbridge/logrusbridge.go c0-com-log
//
// Package logrusbridge hands a pkg/logging logger to dependencies that still
// take a logrus.FieldLogger (the skycoin skydex engines).
package logrusbridge

import (
	"io"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/logging"
)

// New returns a logrus logger that writes nothing itself and forwards every
// entry, with its fields and time, to l. l's level decides what is written.
func New(l logging.FieldLogger) logrus.FieldLogger {
	lr := logrus.New()
	lr.SetOutput(io.Discard)
	lr.SetLevel(logrus.TraceLevel)
	lr.ExitFunc = func(code int) { logging.ExitFunc(code) }
	lr.AddHook(hook{l})
	return lr
}

type hook struct{ l logging.FieldLogger }

func (hook) Levels() []logrus.Level { return logrus.AllLevels }

func (h hook) Fire(e *logrus.Entry) error {
	// The level numbering is the same in both packages.
	h.l.WithFields(logging.Fields(e.Data)).WithTime(e.Time).Log(logging.Level(e.Level), e.Message)
	return nil
}
