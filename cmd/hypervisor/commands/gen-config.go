package commands

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/SkycoinProject/skywire-mainnet/pkg/hypervisor"
	"github.com/SkycoinProject/skywire-mainnet/pkg/util/pathutil"
)

// nolint:gochecknoglobals
var (
	output        string
	replace       bool
	configLocType = pathutil.WorkingDirLoc
	testenv       bool
)

// nolint:gochecknoinits
func init() {
	outputUsage := "path of output config file. Uses default of 'type' flag if unspecified."
	replaceUsage := "whether to allow rewrite of a file that already exists."
	configLocTypeUsage := fmt.Sprintf("config generation mode. Valid values: %v", pathutil.AllConfigLocationTypes())

	rootCmd.AddCommand(genConfigCmd)
	genConfigCmd.Flags().StringVarP(&output, "output", "o", "", "path of output config file. Uses default of 'type' flag if unspecified.")
	genConfigCmd.Flags().BoolVarP(&replace, "replace", "r", false, "whether to allow rewrite of a file that already exists.")
	genConfigCmd.Flags().VarP(&configLocType, "type", "m", fmt.Sprintf("config generation mode. Valid values: %v", pathutil.AllConfigLocationTypes()))
	genConfigCmd.Flags().BoolVarP(&testenv, "testing-environment", "t", false, "whether to use production or test deployment service.")
	genConfigCmd.Flags().StringVarP(&output, "output", "o", "", outputUsage)
	genConfigCmd.Flags().BoolVarP(&replace, "replace", "r", false, replaceUsage)
	genConfigCmd.Flags().VarP(&configLocType, "type", "m", configLocTypeUsage)
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
			conf = hypervisor.GenerateWorkDirConfig(testenv)
		case pathutil.HomeLoc:
			conf = hypervisor.GenerateHomeConfig(testenv)
		case pathutil.LocalLoc:
			conf = hypervisor.GenerateLocalConfig(testenv)
		default:
			log.Fatalln("invalid config type:", configLocType)
		}
		pathutil.WriteJSONConfig(conf, output, replace)
	},
}
