// Package visor pkg/visor/temp_loglevel.go c3-vis-core
package visor

import (
	"fmt"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/visor/logserver"
)

// maxTempLogLevelTTL bounds a temporary log level, so a debugging session
// nobody ends still ends.
const maxTempLogLevelTTL = time.Hour

// tempLogLevel is a log level set for a bounded time through the log
// server's /debug/loglevel. It is never written to the config: the timer, a
// DELETE, or a restart puts back the level the visor was running at.
type tempLogLevel struct {
	mu    sync.Mutex
	timer *time.Timer
	gen   uint64       // bumped per set or clear, so a stale timer does nothing
	base  logrus.Level // restored when the timer fires
	level logrus.Level
	until time.Time
}

var _ logserver.LogLevelController = (*Visor)(nil)

// LogLevelStatus implements logserver.LogLevelController.
func (v *Visor) LogLevelStatus() logserver.LogLevelStatus {
	t := &v.tempLog
	t.mu.Lock()
	defer t.mu.Unlock()
	return v.logLevelStatusLocked()
}

// SetTempLogLevel implements logserver.LogLevelController. Setting a level
// while one is already in force replaces its level and ttl but keeps the
// original base.
func (v *Visor) SetTempLogLevel(level logrus.Level, ttl time.Duration) (logserver.LogLevelStatus, error) {
	if ttl <= 0 || ttl > maxTempLogLevelTTL {
		return logserver.LogLevelStatus{}, fmt.Errorf("ttl %s out of range (0, %s]", ttl, maxTempLogLevelTTL)
	}
	ml := v.conf.MasterLogger()
	if ml == nil {
		return logserver.LogLevelStatus{}, fmt.Errorf("visor has no master logger")
	}
	t := &v.tempLog
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.timer == nil {
		t.base = ml.GetLevel()
	} else {
		t.timer.Stop()
	}
	t.gen++
	gen := t.gen
	t.level = level
	t.until = time.Now().Add(ttl)
	t.timer = time.AfterFunc(ttl, func() { v.expireTempLogLevel(gen) })
	setLogLevel(ml, level)
	v.log.WithField("level", level).WithField("base", t.base).
		WithField("until", t.until.Format(time.RFC3339)).Info("Log level set temporarily")
	return v.logLevelStatusLocked(), nil
}

// ClearTempLogLevel implements logserver.LogLevelController.
func (v *Visor) ClearTempLogLevel() logserver.LogLevelStatus {
	t := &v.tempLog
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.timer != nil {
		t.timer.Stop()
		v.restoreLogLevelLocked()
	}
	return v.logLevelStatusLocked()
}

func (v *Visor) expireTempLogLevel(gen uint64) {
	t := &v.tempLog
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.gen != gen || t.timer == nil {
		return
	}
	v.restoreLogLevelLocked()
}

func (v *Visor) restoreLogLevelLocked() {
	t := &v.tempLog
	t.gen++
	t.timer = nil
	ml := v.conf.MasterLogger()
	// A level set some other way in the meantime (log_level from the
	// hypervisor) stands.
	if ml.GetLevel() != t.level {
		return
	}
	setLogLevel(ml, t.base)
	v.log.WithField("level", t.base).Info("Temporary log level ended")
}

func (v *Visor) logLevelStatusLocked() logserver.LogLevelStatus {
	t := &v.tempLog
	var st logserver.LogLevelStatus
	if ml := v.conf.MasterLogger(); ml != nil {
		st.Level = ml.GetLevel().String()
	}
	if t.timer != nil {
		until := t.until
		st.Base = t.base.String()
		st.Until = &until
	}
	return st
}

// setLogLevel sets the visor's level and the process-wide one that the
// embedded services and library packages log through.
func setLogLevel(ml *logging.MasterLogger, level logrus.Level) {
	if ml != nil {
		ml.SetLevel(level)
	}
	logging.SetLevel(level)
}
