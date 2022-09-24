package clivisor

import (
	"encoding/json"
	"fmt"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire-utilities/pkg/logging"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
	"github.com/skycoin/skywire/pkg/skyenv"
)

var (
	displayNodeIP bool
	rewardAddress string
	out           string
	pathstr       string
)

func init() {

	RootCmd.AddCommand(privacyCmd)
	privacyCmd.AddCommand(setPrivacyCmd)
	privacyCmd.AddCommand(getPrivacyCmd)
	privacyCmd.Flags().SortFlags = false
	setPrivacyCmd.Flags().BoolVarP(&displayNodeIP, "publicip", "i", false, "display node ip")
	// default is genesis address for skycoin blockchain ; for testing
	setPrivacyCmd.Flags().StringVarP(&rewardAddress, "address", "a", "2jBbGxZRGoQG1mqhPBnXnLTxK6oxsTf8os6", "reward address")
	//use the correct path for the available pemissions
	pathstr = skyenv.Config().LocalPath + "/privacy.json"
	setPrivacyCmd.Flags().StringVarP(&out, "out", "o", "", "output config: "+pathstr)
	//getPrivacyCmd.Flags().StringVarP(&out, "out", "o", "", "read from: "+pathstr)

}

var privacyCmd = &cobra.Command{
	Use:   "priv",
	Short: "privacy settings",
	Long: `configure privacy settings

test of the api endpoints GetPrivacy & SetPrivacy`,
	Hidden: true,
}
var setPrivacyCmd = &cobra.Command{
	Use:   "set",
	Short: "set privacy.json via rpc",
	Long: `configure privacy settings

test of the api endpoint SetPrivacy`,
	Hidden: true,
	Run: func(cmd *cobra.Command, args []string) {
		mLog := logging.NewMasterLogger()
		mLog.SetLevel(logrus.InfoLevel)
		log := logging.MustGetLogger("skywire-cli visor priv set")
		client := clirpc.Client(cmd.Flags())
		err := client.SetPrivacy(skyenv.Privacy{DisplayNodeIP: displayNodeIP, RewardAddress: rewardAddress})
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("Failed to connect: %v", err))
		}
		log.Info("OK")
	},
}
var getPrivacyCmd = &cobra.Command{
	Use:   "get",
	Short: "read privacy setting from file",
	Long: `configure privacy settings

test of the api endpoints GetPrivacy & SetPrivacy`,
	Run: func(cmd *cobra.Command, args []string) {
		//mLog := logging.NewMasterLogger()
		//mLog.SetLevel(logrus.InfoLevel)
		log := logging.MustGetLogger("skywire-cli visor priv get")
		client := clirpc.Client(cmd.Flags())
		p, err := client.GetPrivacy()
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("Failed to connect: %v", err))
		}
		j, err := json.MarshalIndent(p, "", "\t")
		if err != nil {
			log.WithError(err).Fatal("Could not unmarshal json.")
		}
		fmt.Printf("%s", j)
	},
}
