//go:build linux
// +build linux

package cliconfig

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/sirupsen/logrus"
	coincipher "github.com/skycoin/skycoin/src/cipher"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/skyenv"
)

var (
	displayNodeIP bool
	rewardAddress string
	out           string
	pathstr       string
	fullpathstr   string
	getpathstr    string
	dummy         string
)

func init() {

	privacyConfigCmd.Flags().SortFlags = false
	RootCmd.AddCommand(privacyConfigCmd)
	privacyConfigCmd.AddCommand(setPrivacyConfigCmd)
	privacyConfigCmd.AddCommand(getPrivacyConfigCmd)
	setPrivacyConfigCmd.Flags().BoolVarP(&displayNodeIP, "publicip", "i", false, "display node ip")
	// default is genesis address for skycoin blockchain ; for testing
	setPrivacyConfigCmd.Flags().StringVarP(&rewardAddress, "address", "a", "2jBbGxZRGoQG1mqhPBnXnLTxK6oxsTf8os6", "reward address")
	//use the correct path for the available pemissions
	pathstr = skyenv.PackageConfig().LocalPath
	fullpathstr = pathstr + "/privacy.json"
	getpathstr = fullpathstr
	if _, err := os.Stat(getpathstr); os.IsNotExist(err) {
		getpathstr = ""
	}
	setPrivacyConfigCmd.Flags().StringVarP(&out, "out", "o", "", "output config: "+fullpathstr)
	getPrivacyConfigCmd.Flags().StringVarP(&out, "out", "o", "", "read config from: "+getpathstr)
	RootCmd.PersistentFlags().StringVar(&dummy, "rpc", "localhost:3435", "RPC server address")
	RootCmd.PersistentFlags().MarkHidden("rpc") // nolint

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
			out = fullpathstr
		}
		if len(args) > 0 {
			if args[0] != "" {
				rewardAddress = args[0]
			}
		}
		_, err := coincipher.DecodeBase58Address(rewardAddress)
		if err != nil {
			logger.WithError(err).Fatal("invalid address specified")
		}
		//create the conf
		type privacy struct {
			DisplayNodeIP bool   `json:"display_node_ip"`
			RewardAddress string `json:"reward_address,omitempty"`
		}

		confp := &privacy{}
		confp.DisplayNodeIP = displayNodeIP
		confp.RewardAddress = rewardAddress

		// Print results.
		j, err := json.MarshalIndent(confp, "", "\t")
		if err != nil {
			logger.WithError(err).Fatal("Could not unmarshal json.")
		}
		if _, err := os.Stat(pathstr); os.IsNotExist(err) {
			logger.WithError(err).Fatal("\n	local directory not found ; run skywire first to create this path\n ")
		}
		err = os.WriteFile(out, j, 0644) //nolint
		if err != nil {
			logger.WithError(err).Fatal("Failed to write config to file.")
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
			out = getpathstr
		}
		if out == "" {
			logger.Fatal("config was not detected and no path was specified.")
		}
		p, err := os.ReadFile(filepath.Clean(out))
		if err != nil {
			logger.WithError(err).Fatal("Failed to read config file.")
		}
		fmt.Printf("%s\n", p)
	},
}
