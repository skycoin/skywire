package cliconfig

import (
	"fmt"
	"os"

	coincipher "github.com/skycoin/skycoin/src/cipher"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
	"github.com/skycoin/skywire/pkg/skyenv"
)

var (
	rewardAddress string
)

func init() {

	privacyConfigCmd.Flags().SortFlags = false
	RootCmd.AddCommand(privacyConfigCmd)
	privacyConfigCmd.AddCommand(setPrivacyConfigCmd)
	privacyConfigCmd.AddCommand(getPrivacyConfigCmd)
	// default is genesis address for skycoin blockchain ; for testing
	setPrivacyConfigCmd.Flags().StringVarP(&rewardAddress, "address", "a", "2jBbGxZRGoQG1mqhPBnXnLTxK6oxsTf8os6", "reward address")

	path := skyenv.PackageConfig().LocalPath + "/" + skyenv.PrivFile
	setPrivacyConfigCmd.Flags().StringVarP(&output, "out", "o", "", "write reward address to: "+path)
	getPrivacyConfigCmd.Flags().StringVarP(&output, "out", "o", "", "read reward address from: "+path)

}

var privacyConfigCmd = &cobra.Command{
	SilenceErrors: true,
	SilenceUsage:  true,
	Hidden:        true,
	Use:           "priv",
	Short:         "reward setting",
	Long: `reward address setting

Sets the skycoin reward address for the visor.
The config is written to the root of the default local directory
>Run this command with root permissions for visors running as root via systemd<
this config is served via dmsghttp along with transport logs
and the system hardware survey for automating reward distribution`,
}

var setPrivacyConfigCmd = &cobra.Command{
	Use:   "set <address>",
	Short: "set reward address",
	Long:  "set reward address",
	Run: func(cmd *cobra.Command, args []string) {

		if output == "" {
			output = skyenv.PackageConfig().LocalPath + "/" + skyenv.PrivFile
		}

		if len(args) > 0 {
			if args[0] != "" {
				rewardAddress = args[0]
			}
		}

		cAddr, err := coincipher.DecodeBase58Address(rewardAddress)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("invalid address specified: %v", err))
		}

		internal.Catch(cmd.Flags(), os.WriteFile(output, []byte(cAddr.String()), 0644))
		readRewardFile(cmd.Flags())
	},
}

var getPrivacyConfigCmd = &cobra.Command{
	Use:   "get",
	Short: "read reward address",
	Long:  `read reward address from file`,
	Run: func(cmd *cobra.Command, args []string) {

		if output == "" {
			output = skyenv.PackageConfig().LocalPath + "/" + skyenv.PrivFile
		}
		readRewardFile(cmd.Flags())
	},
}

func readRewardFile(cmdFlags *pflag.FlagSet) {
	//read the file which was written
	dat, err := os.ReadFile(output)
	if err != nil {
		internal.PrintFatalError(cmdFlags, fmt.Errorf("Error reading file. err=%v", err))
	}
	output := fmt.Sprintf("Rewards settings file '%s' contents:\n%s\n", output, dat)
	internal.PrintOutput(cmdFlags, output, output)
}
