//+build !windows

package logging

import (
	"log/syslog"

	"github.com/sirupsen/logrus"
	logrussyslog "github.com/sirupsen/logrus/hooks/syslog"
)

type SysLogHook struct {
	h *logrussyslog.SyslogHook
}

func NewSysLogHook(network, raddr string, priority syslog.Priority, tag string) (logrus.Hook, error) {
	return logrussyslog.NewSyslogHook(network, raddr, priority, tag)
}

func (h *SysLogHook) Levels() []logrus.Level {
	return h.Levels()
}

func (h *SysLogHook) Fire(e *logrus.Entry) error {
	return h.Fire(e)
}
