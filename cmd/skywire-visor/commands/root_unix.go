//+build !windows

package commands

import (
	"io/ioutil"
	"log/syslog"

	"github.com/SkycoinProject/skycoin/src/util/logging"
	logrussyslog "github.com/sirupsen/logrus/hooks/syslog"
)

func (cfg *runCfg) startLogger() *runCfg {
	cfg.masterLogger = logging.NewMasterLogger()
	cfg.logger = cfg.masterLogger.PackageLogger(cfg.tag)

	if cfg.syslogAddr != "none" {
		hook, err := logrussyslog.NewSyslogHook("udp", cfg.syslogAddr, syslog.LOG_INFO, cfg.tag)
		if err != nil {
			cfg.logger.Error("Unable to connect to syslog daemon:", err)
		} else {
			cfg.masterLogger.AddHook(hook)
			cfg.masterLogger.Out = ioutil.Discard
		}
	}

	return cfg
}
