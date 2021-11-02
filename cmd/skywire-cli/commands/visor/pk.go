package visor

import (
	"fmt"
	"io/ioutil"
	"os"

	"github.com/skycoin/skywire/pkg/visor/visorconfig"

	"github.com/sirupsen/logrus"
	"github.com/skycoin/skycoin/src/util/logging"
	"github.com/spf13/cobra"
)

var addInput string

func init() {
	RootCmd.AddCommand(pkCmd)
	RootCmd.AddCommand(hvpkCmd)

	pkCmd.Flags().StringVarP(&addInput, "input", "i", "", "path of input config file.")
	hvpkCmd.Flags().StringVarP(&addInput, "input", "i", "", "path of input config file.")
}

var pkCmd = &cobra.Command{
	Use:   "pk",
	Short: "Obtains the public key of the visor",
	Run: func(_ *cobra.Command, _ []string) {
		if addInput != "" {
			conf := readConfig(addInput)
			fmt.Println(conf.PK.Hex())
		} else {
			client := rpcClient()
			overview, err := client.Overview()
			if err != nil {
				logger.Fatal("Failed to connect:", err)
			}
			fmt.Println(overview.PubKey)
		}
	},
}

var hvpkCmd = &cobra.Command{
	Use:   "hvpk",
	Short: "Obtains the public key of the visor",
	Run: func(_ *cobra.Command, _ []string) {
		if addInput != "" {
			conf := readConfig(addInput)
			fmt.Println(conf.Hypervisors)
		} else {
			client := rpcClient()
			overview, err := client.Overview()
			if err != nil {
				logger.Fatal("Failed to connect:", err)
			}
			fmt.Println(overview.Hypervisors)
		}
	},
}

func readConfig(path string) *visorconfig.V1 {
	mLog := logging.NewMasterLogger()
	mLog.SetLevel(logrus.InfoLevel)

	f, err := os.Open(path) // nolint: gosec
	if err != nil {
		mLog.WithError(err).
			WithField("filepath", addInput).
			Fatal("Failed to read config file.")
	}

	raw, err := ioutil.ReadAll(f)
	if err != nil {
		mLog.WithError(err).Fatal("Failed to read config.")
	}

	conf, ok := visorconfig.Parse(mLog, addInput, raw)
	if ok != nil {
		mLog.WithError(err).Fatal("Failed to parse config.")
	}
	return conf
}
