// Package services pkg/services/loglevel.go c2-vis-appsvc
//
// Per-service log levels.
//
// Every service block may carry its own "log_level". Historically each
// service applied it by calling the package-global logging.SetLevel, which
// is fine for a single-service binary (`skywire svc ar`) but wrong under
// `skywire svc run`, where several services share one process: the last
// service constructed decided the level for every other service, so a
// per-service log_level was misleading and one block set to "debug" turned
// on debug logging for the whole process.
//
// NewLogger keeps the single-service behavior exactly as it was and gives
// each service its own independently-leveled logger when the process hosts
// more than one.
package services

import (
	"sync/atomic"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/logging"
)

// hosted is the number of services this process runs. Zero — the default,
// since the per-service cobra commands never call setHosted — means a
// single-service binary.
var hosted atomic.Int32

// setHosted records how many services this process hosts. Run calls it
// before building anything.
func setHosted(n int) { hosted.Store(int32(n)) } //nolint:gosec

// NewLogger returns the logger a service should log through, given the
// "log_level" from its own config block and the tag it logs under.
//
// An empty level leaves everything alone. An unparseable one is reported at
// warn — naming the offending string and the level actually used — and falls
// back to info, so a typo in a config file can no longer put a production
// service into debug without saying so.
//
// When this process hosts a single service, the level is applied to the
// process-global master logger as before, so library packages (dmsg,
// httpauth, the stores) are quieted along with the service. When several
// services share the process, the level applies to the returned logger only;
// Run separately picks the shared global level from all the blocks.
func NewLogger(tag, level string) *logging.Logger {
	logger := logging.MustGetLogger(tag)
	if level == "" {
		return logger
	}
	lvl := parseLevel(logger, tag, level)
	if hosted.Load() > 1 {
		return logging.ScopedLogger(tag, lvl)
	}
	logging.SetLevel(lvl)
	return logger
}

// parseLevel converts level, warning through logger when it is not a level
// name. The returned level is usable either way: LevelFromString falls back
// to info on error.
func parseLevel(logger *logging.Logger, tag, level string) logrus.Level {
	lvl, err := logging.LevelFromString(level)
	if err != nil {
		logger.WithError(err).Warnf("%s: bad log_level %q; logging at %q instead", tag, level, lvl)
	}
	return lvl
}

// sharedLogLevel returns the level to apply to the process-global master
// logger when several services share the process, and whether any block
// asked for one at all.
//
// It is the quietest level any service asked for. The global is what the
// shared library packages log through, and there is no per-service answer
// for those; picking the quietest means hosting an extra service can never
// make the process noisier than every block asked for, and — unlike the
// last-one-wins behavior this replaces — the result does not depend on the
// order the blocks happen to be built in.
func sharedLogLevel(log *logging.Logger, file File) (logrus.Level, bool) {
	var (
		quietest logrus.Level
		found    bool
	)
	for _, b := range file.Services {
		if b.LogLevel == "" {
			continue
		}
		lvl := parseLevel(log, b.Label(), b.LogLevel)
		if !found || lvl < quietest {
			quietest, found = lvl, true
		}
	}
	return quietest, found
}
