//+build !windows

package logging

import (
	"log/syslog"

	"github.com/sirupsen/logrus"
	logrussyslog "github.com/sirupsen/logrus/hooks/syslog"
)

func NewSysLogHook(network, raddr string, priority syslog.Priority, tag string) (logrus.Hook, error) {
	return logrussyslog.NewSyslogHook(network, raddr, priority, tag)
}
