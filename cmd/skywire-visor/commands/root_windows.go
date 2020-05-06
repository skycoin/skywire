//+build windows

package commands

import "github.com/SkycoinProject/skycoin/src/util/logging"

func (cfg *runCfg) startLogger() *runCfg {
	cfg.masterLogger = logging.NewMasterLogger()
	cfg.logger = cfg.masterLogger.PackageLogger(cfg.tag)

	return cfg
}
