package clivisor

import (
	"fmt"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	coincipher "github.com/skycoin/skycoin/src/cipher"

	"github.com/skycoin/skywire-utilities/pkg/logging"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"

	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
	"github.com/skycoin/skywire/pkg/visor/privacyconfig"
)

var (
	displayNodeIP bool
	rewardAddress string
)

func init() {

	RootCmd.AddCommand(privacyCmd)
	privacyCmd.AddCommand(setPrivacyCmd)
	privacyCmd.AddCommand(getPrivacyCmd)
	privacyCmd.Flags().SortFlags = false
	setPrivacyCmd.Flags().BoolVarP(&displayNodeIP, "publicip", "i", false, "display node ip")
	// default is genesis address for skycoin blockchain ; for testing
	setPrivacyCmd.Flags().StringVarP(&rewardAddress, "address", "a", "2jBbGxZRGoQG1mqhPBnXnLTxK6oxsTf8os6", "reward address")
	//use the correct path for the available permissions
}

var privacyCmd = &cobra.Command{
	Use:    "priv",
	Short:  "privacy settings",
	Long:   "configure privacy settings\n\ntest of the api endpoints GetPrivacy & SetPrivacy",
	Hidden: true,
}

var setPrivacyCmd = &cobra.Command{
	Use:   "set",
	Short: "set privacy.json via rpc",
	Long:  "configure privacy settings\n\ntest of the api endpoint SetPrivacy",
	Run: func(cmd *cobra.Command, args []string) {
		mLog := logging.NewMasterLogger()
		mLog.SetLevel(logrus.InfoLevel)
		log := logging.MustGetLogger("skywire-cli visor priv set")
		client := clirpc.Client(cmd.Flags())

		cAddr, err := coincipher.DecodeBase58Address(rewardAddress)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("invalid address specified: %v", err))
		}

		resp, err := client.SetPrivacy(privacyconfig.Privacy{DisplayNodeIP: displayNodeIP, RewardAddress: cAddr})
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("Failed to connect: %v", err))
		}
		log.Info("Privacy settings updated to:\n", resp)
	},
}

var getPrivacyCmd = &cobra.Command{
	Use:   "get",
	Short: "read privacy setting from file",
	Long:  "configure privacy settings\n\ntest of the api endpoints GetPrivacy",
	Run: func(cmd *cobra.Command, args []string) {
		p, err := clirpc.Client(cmd.Flags()).GetPrivacy()
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("Failed to connect: %v", err))
		}
		fmt.Printf("%s", p)
	},
}
