package visor

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"path"
	"path/filepath"

	"github.com/SkycoinProject/dmsg/cipher"
	"github.com/spf13/cobra"

	"github.com/SkycoinProject/skywire-mainnet/pkg/util/pathutil"
	"github.com/SkycoinProject/skywire-mainnet/pkg/visor"
)

func init() {
	RootCmd.AddCommand(genConfigCmd)
}

var (
	sk            cipher.SecKey
	output        string
	replace       bool
	retainKeys    bool
	configLocType = pathutil.WorkingDirLoc
	testenv       bool
)

func init() {
	genConfigCmd.Flags().VarP(&sk, "secret-key", "s", "if unspecified, a random key pair will be generated.")
	genConfigCmd.Flags().StringVarP(&output, "output", "o", "", "path of output config file. Uses default of 'type' flag if unspecified.")
	genConfigCmd.Flags().BoolVarP(&replace, "replace", "r", false, "whether to allow rewrite of a file that already exists.")
	genConfigCmd.Flags().BoolVar(&retainKeys, "retain-keys", false, "retain current keys")
	genConfigCmd.Flags().BoolVarP(&testenv, "testing-environment", "t", false, "whether to use production or test deployment service.")

	// TODO(evanlinjin): Re-implement this at a later stage.
	//genConfigCmd.Flags().VarP(&configLocType, "type", "m", fmt.Sprintf("config generation mode. Valid values: %v", pathutil.AllConfigLocationTypes()))
}

var genConfigCmd = &cobra.Command{
	Use:   "gen-config",
	Short: "Generates a config file",
	PreRun: func(_ *cobra.Command, _ []string) {
		if output == "" {
			output = pathutil.VisorDefaults().Get(configLocType)
			logger.Infof("No 'output' set; using default path: %s", output)
		}
		var err error
		if output, err = filepath.Abs(output); err != nil {
			logger.WithError(err).Fatalln("invalid output provided")
		}
	},
	Run: func(_ *cobra.Command, _ []string) {
		var conf *visor.Config

		// TODO(evanlinjin): Decide whether we still need this feature in the future.
		// https://github.com/SkycoinProject/skywire-mainnet/pull/360#discussion_r425080223
		switch configLocType {
		case pathutil.WorkingDirLoc:
			var err error
			if conf, err = visor.DefaultConfig(nil, output, visor.NewKeyPair()); err != nil {
				logger.WithError(err).Fatal("Failed to create default config.")
			}
		default:
			logger.Fatalln("invalid config type:", configLocType)
		}
		if replace && retainKeys && pathutil.Exists(output) {
			if err := fillInOldKeys(output, conf); err != nil {
				logger.WithError(err).Fatalln("Error retaining old keys")
			}
		}
		pathutil.WriteJSONConfig(conf, output, replace)
	},
}

func fillInOldKeys(confPath string, conf *visor.Config) error {
	oldConfBytes, err := ioutil.ReadFile(path.Clean(confPath))
	if err != nil {
		return fmt.Errorf("error reading old config file: %w", err)
	}

	var oldConf visor.Config
	if err := json.Unmarshal(oldConfBytes, &oldConf); err != nil {
		return fmt.Errorf("invalid old configuration file: %w", err)
	}

	conf.KeyPair = oldConf.KeyPair

	return nil
}
