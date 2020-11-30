package visor

import (
	"encoding/json"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"github.com/sirupsen/logrus"
	"github.com/skycoin/dmsg/cipher"
	coinCipher "github.com/skycoin/skycoin/src/cipher"
	"github.com/skycoin/skycoin/src/util/logging"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

func init() {
	RootCmd.AddCommand(updateConfigCmd)
}

var (
	addOutput        string
	addInput         string
	environment      string
	resetHypervisor  bool
	addHypervisorPKs string
)

func init() {
	updateConfigCmd.Flags().StringVarP(&addOutput, "output", "o", "skywire-config.json", "path of output config file.")
	updateConfigCmd.Flags().StringVarP(&addInput, "input", "i", "skywire-config.json", "path of input config file.")
	updateConfigCmd.Flags().StringVarP(&environment, "environment", "e", "production", "desired environment (values production or testing)")
	updateConfigCmd.Flags().StringVar(&addHypervisorPKs, "hypervisor-pks", "", "public keys of hypervisors that should be added to this visor")
	updateConfigCmd.Flags().BoolVar(&resetHypervisor, "reset", false, "resets hypervisor`s configuration")
}

var updateConfigCmd = &cobra.Command{
	Use:   "update-config",
	Short: "Updates a config file",
	PreRun: func(_ *cobra.Command, _ []string) {
		var err error
		if output, err = filepath.Abs(addOutput); err != nil {
			logger.WithError(err).Fatal("Invalid output provided.")
		}
	},
	Run: func(_ *cobra.Command, _ []string) {
		mLog := logging.NewMasterLogger()
		mLog.SetLevel(logrus.InfoLevel)
		f, err := os.Open(addInput) // nolint: gosec
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

		if addHypervisorPKs != "" {
			keys := strings.Split(addHypervisorPKs, ",")
			for _, key := range keys {
				keyParsed, err := coinCipher.PubKeyFromHex(strings.TrimSpace(key))
				if err != nil {
					logger.WithError(err).Fatalf("Failed to parse hypervisor private key: %s.", key)
				}
				conf.Hypervisors = append(conf.Hypervisors, cipher.PubKey(keyParsed))
			}
		}

		if environment == "production" {
			visorconfig.SetDefaultProductionValues(conf)
		}

		if environment == "testing" {
			visorconfig.SetDefaultTestingValues(conf)
		}

		if resetHypervisor {
			conf.Hypervisors = []cipher.PubKey{}
		}

		// Save config to file.
		if err := conf.Flush(); err != nil {
			logger.WithError(err).Fatal("Failed to flush config to file.")
		}

		// Print results.
		j, err := json.MarshalIndent(conf, "", "\t")
		if err != nil {
			logger.WithError(err).Fatal("An unexpected error occurred. Please contact a developer.")
		}
		logger.Infof("Updated file '%s' to: %s", output, j)
	},
}
