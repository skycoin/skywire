package config

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/skycoin/skycoin/src/util/logging"

	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

//Print prints the config
func Print(mLog *logging.MasterLogger) {
	conf, err := visorconfig.ReadFile(print)
	if err != nil {
		mLog.Fatal("Failed:", err)
	}
	j, err := json.MarshalIndent(conf, "", "\t")
	if err != nil {
		mLog.WithError(err).Fatal("An unexpected error occurred. Please contact a developer.")
	}
	if !stdout {
		mLog.Infof("Config file: '%s' config: %s", output, j)
	} else {
		fmt.Printf("%s", j)
	}
	os.Exit(0)
}
