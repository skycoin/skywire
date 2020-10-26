package commands

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"path"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/pkg/hypervisor"
	"github.com/skycoin/skywire/pkg/util/pathutil"
)

// nolint:gochecknoglobals
var (
	output        string
	replace       bool
	package1     bool
	configLocType = pathutil.WorkingDirLoc
	testEnv       bool
	retainKeys    bool
)

// nolint:gochecknoinits
func init() {
	outputUsage := "specify path to output config file. Uses default of 'type' flag if unspecified."
	replaceUsage := "rewrite existing config."
	packageUsage := "use package default configuration."
	configLocTypeUsage := fmt.Sprintf("config generation mode. Valid values: %v", pathutil.AllConfigLocationTypes())
	testEnvUsage := "use production (default) or test deployment service."

	rootCmd.AddCommand(genConfigCmd)
	genConfigCmd.Flags().StringVarP(&output, "output", "o", "", outputUsage)
	genConfigCmd.Flags().BoolVarP(&replace, "replace", "r", false, replaceUsage)
	genConfigCmd.Flags().BoolVarP(&package1, "package", "p", false, packageUsage)
	genConfigCmd.Flags().VarP(&configLocType, "type", "m", configLocTypeUsage)
	genConfigCmd.Flags().BoolVarP(&testEnv, "testing-environment", "t", false, testEnvUsage)
	genConfigCmd.Flags().BoolVar(&retainKeys, "retain-keys", false, "retain current keys")
}

// nolint:gochecknoglobals
var genConfigCmd = &cobra.Command{
	Use:   "gen-config",
	Short: "generates a configuration file",
	PreRun: func(_ *cobra.Command, _ []string) {
		if output == "" {
			output = pathutil.HypervisorDefaults().Get(configLocType)
			log.Infof("no 'output,o' flag is empty, using default path: %s", output)
		}
		var err error
		if output, err = filepath.Abs(output); err != nil {
			log.WithError(err).Fatalln("invalid output provided")
		}
	},
	Run: func(_ *cobra.Command, _ []string) {
		var conf hypervisor.Config
		switch configLocType {
		case pathutil.WorkingDirLoc:
			conf = hypervisor.GenerateWorkDirConfig(testEnv)
		case pathutil.HomeLoc:
			conf = hypervisor.GenerateHomeConfig(testEnv)
		case pathutil.LocalLoc:
			conf = hypervisor.GenerateLocalConfig(testEnv)
		default:
			log.Fatalln("invalid config type:", configLocType)
		}
	 if replace && retainKeys && pathutil.Exists(output) {
			if err := fillInOldKeys(output, &conf); err != nil {
				log.Fatalln("Error retaining old keys", err)
			}
		}
		if package1 {
			conf = hypervisor.GeneratePackageConfig(testEnv)
	 }
		pathutil.WriteJSONConfig(conf, output, replace)
	},
}

func fillInOldKeys(confPath string, conf *hypervisor.Config) error {
	oldConfBytes, err := ioutil.ReadFile(path.Clean(confPath))
	if err != nil {
		return fmt.Errorf("error reading old config file: %w", err)
	}

	var oldConf hypervisor.Config
	if err := json.Unmarshal(oldConfBytes, &oldConf); err != nil {
		return fmt.Errorf("invalid old configuration file: %w", err)
	}

	conf.PK = oldConf.PK
	conf.SK = oldConf.SK
	return nil
}
