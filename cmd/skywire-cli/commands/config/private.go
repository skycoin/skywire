package cliconfig

import (
	"fmt"
	"os"
	"strings"

	"github.com/sirupsen/logrus"
	coincipher "github.com/skycoin/skycoin/src/cipher"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/visor/privacyconfig"
)

var (
	displayNodeIP bool
	rewardAddress string
	out           string
	pathStr       string
	fullPathStr   string
	getPathStr    string
)

func init() {

	privacyConfigCmd.Flags().SortFlags = false
	RootCmd.AddCommand(privacyConfigCmd)
	privacyConfigCmd.AddCommand(setPrivacyConfigCmd)
	privacyConfigCmd.AddCommand(getPrivacyConfigCmd)
	setPrivacyConfigCmd.Flags().BoolVarP(&displayNodeIP, "publicip", "i", false, "display node ip")
	// default is genesis address for skycoin blockchain ; for testing
	setPrivacyConfigCmd.Flags().StringVarP(&rewardAddress, "address", "a", "2jBbGxZRGoQG1mqhPBnXnLTxK6oxsTf8os6", "reward address")
	//use the correct path for the available permissions
	pathStr = skyenv.PackageConfig().LocalPath
	fullPathStr = strings.Join([]string{pathStr, skyenv.PrivFile}, "/")
	getPathStr = fullPathStr
	if _, err := os.Stat(getPathStr); os.IsNotExist(err) {
		getPathStr = ""
	}
	setPrivacyConfigCmd.Flags().StringVarP(&out, "out", "o", "", "output config: "+fullPathStr)
	getPrivacyConfigCmd.Flags().StringVarP(&out, "out", "o", "", "read config from: "+getPathStr)

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
		mLog := logging.NewMasterLogger()
		mLog.SetLevel(logrus.InfoLevel)
		if out == "" {
			out = fullPathStr
		}
		if len(args) > 0 {
			if args[0] != "" {
				rewardAddress = args[0]
			}
		}
		cAddr, err := coincipher.DecodeBase58Address(rewardAddress)
		if err != nil {
			logger.WithError(err).Fatal("invalid address specified")
		}

		confP := privacyconfig.Privacy{
			DisplayNodeIP: displayNodeIP,
			RewardAddress: cAddr,
		}

		j, err := privacyconfig.SetReward(confP, out, pathStr)
		if err != nil {
			logger.Fatal(err)
		}
		logger.Infof("Updated file '%s' to:\n%s\n", out, j)
	},
}
var getPrivacyConfigCmd = &cobra.Command{
	Use:   "get",
	Short: "read reward address & privacy setting from file",
	Long:  `read reward address & privacy setting from file`,
	Run: func(cmd *cobra.Command, args []string) {
		mLog := logging.NewMasterLogger()
		mLog.SetLevel(logrus.InfoLevel)
		if out == "" {
			out = getPathStr
		}
		if out == "" {
			logger.Fatal("config was not detected and no path was specified.")
		}

		j, err := privacyconfig.GetReward(out)
		if err != nil {
			logger.Fatal(err)
		}
		fmt.Printf("%s\n", j)
	},
}
