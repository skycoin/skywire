package cliconfig

import (
	"encoding/json"
	"fmt"

	coincipher "github.com/skycoin/skycoin/src/cipher"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/visor/privacyconfig"
)

var (
	displayNodeIP bool
	rewardAddress string
)

func init() {

	privacyConfigCmd.Flags().SortFlags = false
	RootCmd.AddCommand(privacyConfigCmd)
	privacyConfigCmd.AddCommand(setPrivacyConfigCmd)
	privacyConfigCmd.AddCommand(getPrivacyConfigCmd)
	setPrivacyConfigCmd.Flags().BoolVarP(&displayNodeIP, "publicip", "i", false, "display node ip")
	// default is genesis address for skycoin blockchain ; for testing
	setPrivacyConfigCmd.Flags().StringVarP(&rewardAddress, "address", "a", "2jBbGxZRGoQG1mqhPBnXnLTxK6oxsTf8os6", "reward address")

	path := skyenv.LocalPath + "/" + skyenv.PrivFile
	setPrivacyConfigCmd.Flags().StringVarP(&output, "out", "o", "", "write privacy config to: "+path)
	getPrivacyConfigCmd.Flags().StringVarP(&output, "out", "o", "", "read privacy config from: "+path)

	if skyenv.OS == "win" {
		pText = "use .msi installation path: "
	}
	if skyenv.OS == "linux" {
		pText = "use path for package: "
	}
	if skyenv.OS == "mac" {
		pText = "use mac installation path: "
	}
	setPrivacyConfigCmd.Flags().BoolVarP(&isPkgEnv, "pkg", "p", false, pText+skyenv.PackageConfig().LocalPath)
	getPrivacyConfigCmd.Flags().BoolVarP(&isPkgEnv, "pkg", "p", false, pText+skyenv.PackageConfig().LocalPath)

	userPath := skyenv.UserConfig().LocalPath
	if userPath != "" {
		setPrivacyConfigCmd.Flags().BoolVarP(&isUsrEnv, "user", "u", false, "use paths for user space: "+userPath)
		getPrivacyConfigCmd.Flags().BoolVarP(&isUsrEnv, "user", "u", false, "use paths for user space: "+userPath)
	}

}

var privacyConfigCmd = &cobra.Command{
	SilenceErrors: true,
	SilenceUsage:  true,
	Use:           "priv",
	Short:         "rewards & privacy setting",
	Long: `rewards & privacy setting

Sets the skycoin rewards address and ip public for the visor.
The config is written to the root of the default local directory
Run this command with root permissions for visors running as root via systemd
this config is served via dmsghttp along with transport logs
and the system hardware survey for automating rewards distribution`,
}

var setPrivacyConfigCmd = &cobra.Command{
	Use:   "set <address>",
	Short: "set reward address & node privacy",
	Long:  "set reward address & node privacy",
	Run: func(cmd *cobra.Command, args []string) {

		getOutput()

		if len(args) > 0 {
			if args[0] != "" {
				rewardAddress = args[0]
			}
		}

		cAddr, err := coincipher.DecodeBase58Address(rewardAddress)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("invalid address specified: %v", err))
		}

		confP := privacyconfig.Privacy{
			DisplayNodeIP: displayNodeIP,
			RewardAddress: cAddr,
		}

		j, err := privacyconfig.SetReward(confP, output)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		output := fmt.Sprintf("Updated file '%s' to:\n%s\n", output, j)
		var jsonOutput map[string]interface{}
		err = json.Unmarshal(j, &jsonOutput)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("Failed to unmarshal json: %v", err))
		}
		internal.PrintOutput(cmd.Flags(), jsonOutput, output)
	},
}

var getPrivacyConfigCmd = &cobra.Command{
	Use:   "get",
	Short: "read reward address & privacy setting from file",
	Long:  `read reward address & privacy setting from file`,
	Run: func(cmd *cobra.Command, args []string) {
		getOutput()

		j, err := privacyconfig.GetReward(output)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		var jsonOutput map[string]interface{}
		err = json.Unmarshal(j, &jsonOutput)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("Failed to unmarshal json: %v", err))
		}
		internal.PrintOutput(cmd.Flags(), jsonOutput, string(j)+"\n")
	},
}

func getOutput() {
	// these flags overwrite each other
	if (isUsrEnv) && (isPkgEnv) {
		logger.Fatal("Use of mutually exclusive flags: -u --user and -p --pkg")
	}
	if output == "" {
		output = skyenv.LocalPath + "/" + skyenv.PrivFile
	}
	if isPkgEnv {
		confPath = skyenv.PackageConfig().LocalPath + "/" + skyenv.PrivFile
		output = confPath
	}
	if isUsrEnv {
		confPath = skyenv.UserConfig().LocalPath + "/" + skyenv.PrivFile
		output = confPath
	}
}
