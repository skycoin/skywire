// Package mobilecore pkg/mobilecore/logsink.go c4-vis-core
package mobilecore

import (
	"sync"
	"sync/atomic"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/visor"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// LogSink receives one formatted log line per entry, with its logrus level
// (0 panic … 6 trace). It is called on the goroutine that logged, so it must
// not block and must not log.
type LogSink func(level int32, line string)

// The sink is the host's copy of the core's log: on iOS the process's stderr
// goes nowhere the app can read, so this is what Android's captured process
// log is on the phone — it keeps working while the visor's API is down.
var (
	sink         atomic.Pointer[LogSink]
	sinkHookOnce sync.Once
	sinkHookVal  = &sinkHook{
		formatter: &logging.TextFormatter{
			FullTimestamp:      true,
			TimestampFormat:    "2006-01-02T15:04:05.0000Z07:00",
			ForceFormatting:    true,
			DisableColors:      true,
			AlwaysQuoteStrings: true,
			QuoteEmptyFields:   true,
		},
	}
)

// SetLogSink installs fn as the log sink, replacing the previous one; nil
// removes it. It can be called before Start, and at any time after.
func SetLogSink(fn LogSink) {
	if fn == nil {
		sink.Store(nil)
		return
	}
	sink.Store(&fn)
	installProcessSinkHook()
}

// installProcessSinkHook hooks the sink onto the loggers that outlive a
// visor: the visor package's global one and the one the apps and the CLI
// code get from logging.MustGetLogger. Once per process — a hook added on
// every start would repeat every line.
func installProcessSinkHook() {
	sinkHookOnce.Do(func() {
		visor.ProcessLogger().AddHook(sinkHookVal)
		logging.AddHook(sinkHookVal)
	})
}

// hookConfigLogger hooks the sink onto a freshly parsed config's master
// logger, the one the visor's modules log through. Each start parses the
// config anew, so this adds one hook to a new logger, never a second hook to
// the same one.
func hookConfigLogger(conf *visorconfig.V1) {
	conf.MasterLogger().AddHook(sinkHookVal)
}

// sinkHook forwards log entries to the current sink.
type sinkHook struct {
	formatter logrus.Formatter
}

func (h *sinkHook) Levels() []logrus.Level { return logrus.AllLevels }

func (h *sinkHook) Fire(e *logrus.Entry) error {
	fn := sink.Load()
	if fn == nil {
		return nil
	}
	line, err := h.formatter.Format(e)
	if err != nil {
		return nil //nolint:nilerr // a line that cannot be formatted is dropped, not an error for the caller
	}
	if n := len(line); n > 0 && line[n-1] == '\n' {
		line = line[:n-1]
	}
	(*fn)(int32(e.Level), string(line)) //nolint:gosec // logrus levels are 0..6
	return nil
}
