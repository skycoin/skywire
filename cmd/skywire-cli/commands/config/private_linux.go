//go:build linux
// +build linux

package cliconfig

import (
	"encoding/json"
	"os"

	"github.com/sirupsen/logrus"
	coincipher "github.com/skycoin/skycoin/src/cipher"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/skyenv"
)

func init() {
	privacyConfigCmd.Flags().SortFlags = false
	RootCmd.AddCommand(privacyConfigCmd)
	privacyConfigCmd.Flags().BoolVarP(&displayNodeIP, "publicip", "i", false, "display node ip")
	// default is genesis address for skycoin blockchain ; for testing
	privacyConfigCmd.Flags().StringVarP(&rewardAddress, "address", "a", "2jBbGxZRGoQG1mqhPBnXnLTxK6oxsTf8os6", "reward address")
	//use the correct path for the available pemissions
	ptext = skyenv.Config().LocalPath
	privacyConfigCmd.Flags().StringVarP(&output, "out", "o", "", "output config: "+ptext+"/privacy.json")
}

var privacyConfigCmd = &cobra.Command{
	Use:   "priv <address>",
	Short: "rewards & privacy setting",
	Long: `rewards & privacy setting

Sets the skycoin rewards address and ip public for the visor.
The config is written to the root of the default local directory
Run this command with root permissions for visors running as root via systemd
this config is served via dmsghttp along with transport logs
and the system hardware survey for automating rewards distribution`,
	Run: func(cmd *cobra.Command, args []string) {
		mLog := logging.NewMasterLogger()
		mLog.SetLevel(logrus.InfoLevel)

		if output == "" {
			output = ptext + "/privacy.json"
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
		if _, err := os.Stat(ptext); os.IsNotExist(err) {
			var tryRunningAsRoot string
			if !isRoot {
				tryRunningAsRoot = "	or try the same command again with root permissions\n"
			}
			logger.WithError(err).Fatal("\n	local directory not found ; run skywire first to create this path\n " + tryRunningAsRoot)
		}
		err = os.WriteFile(output, j, 0644) //nolint
		if err != nil {
			logger.WithError(err).Fatal("Failed to write config to file.")
		}
		logger.Infof("Updated file '%s' to:\n%s\n", output, j)
	},
}
