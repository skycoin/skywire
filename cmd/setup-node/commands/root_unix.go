//+build !windows

package commands

import (
	"log/syslog"

	"github.com/SkycoinProject/skycoin/src/util/logging"
	logrussyslog "github.com/sirupsen/logrus/hooks/syslog"
)

func setupSyslog(logger *logging.Logger) {
	hook, err := logrussyslog.NewSyslogHook("udp", syslogAddr, syslog.LOG_INFO, tag)
	if err != nil {
		logger.Fatalf("Unable to connect to syslog daemon on %v", syslogAddr)
	}
	logging.AddHook(hook)
}
