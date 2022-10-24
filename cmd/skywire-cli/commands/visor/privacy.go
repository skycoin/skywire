package clivisor

import (
	"encoding/json"
	"fmt"

	coincipher "github.com/skycoin/skycoin/src/cipher"
	"github.com/spf13/cobra"

	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
	"github.com/skycoin/skywire/pkg/visor/privacyconfig"
)

var (
	rewardAddress string
)

func init() {

	RootCmd.AddCommand(privacyCmd)
	privacyCmd.Flags().SortFlags = false
	privacyCmd.AddCommand(setPrivacyCmd)
	privacyCmd.AddCommand(getPrivacyCmd)
	// default is genesis address for skycoin blockchain ; for testing
	setPrivacyCmd.Flags().StringVarP(&rewardAddress, "address", "a", "2jBbGxZRGoQG1mqhPBnXnLTxK6oxsTf8os6", "reward address")
}

var privacyCmd = &cobra.Command{
	Use:   "priv",
	Short: "privacy settings",
	Long:  "configure privacy settings",
}

var setPrivacyCmd = &cobra.Command{
	Use:   "set",
	Short: "set privacy.json via rpc",
	Long:  "configure privacy settings",
	Run: func(cmd *cobra.Command, args []string) {
		client := clirpc.Client(cmd.Flags())

		cAddr, err := coincipher.DecodeBase58Address(rewardAddress)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("invalid address specified: %v", err))
		}

		pConfig, err := client.SetPrivacy(&privacyconfig.Privacy{RewardAddress: cAddr.String()})
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("Failed to connect: %v", err))
		}

		j, err := json.MarshalIndent(pConfig, "", "\t")
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("Could not marshal json. err=%v", err))
		}
		output := fmt.Sprintf("Privacy settings updated to:\n %v\n", string(j))

		internal.PrintOutput(cmd.Flags(), pConfig, output)
	},
}

var getPrivacyCmd = &cobra.Command{
	Use:   "get",
	Short: "read privacy setting from file",
	Long:  "configure privacy settings",
	Run: func(cmd *cobra.Command, args []string) {
		pConfig, err := clirpc.Client(cmd.Flags()).GetPrivacy()
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("Failed to connect: %v", err))
		}
		j, err := json.MarshalIndent(pConfig, "", "\t")
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("Could not marshal json. err=%v", err))
		}
		internal.PrintOutput(cmd.Flags(), pConfig, string(j)+"\n")
	},
}
