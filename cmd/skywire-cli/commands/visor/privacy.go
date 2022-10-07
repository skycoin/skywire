package clivisor

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	coincipher "github.com/skycoin/skycoin/src/cipher"

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
	privacyCmd.Flags().SortFlags = false
	privacyCmd.AddCommand(setPrivacyCmd)
	privacyCmd.AddCommand(getPrivacyCmd)
	setPrivacyCmd.Flags().BoolVarP(&displayNodeIP, "publicip", "i", false, "display node ip")
	// default is genesis address for skycoin blockchain ; for testing
	setPrivacyCmd.Flags().StringVarP(&rewardAddress, "address", "a", "2jBbGxZRGoQG1mqhPBnXnLTxK6oxsTf8os6", "reward address")
}

var privacyCmd = &cobra.Command{
	Use:   "priv",
	Short: "privacy settings",
	Long:  "configure privacy settings\n\ntest of the api endpoints GetPrivacy & SetPrivacy",
}

var setPrivacyCmd = &cobra.Command{
	Use:   "set",
	Short: "set privacy.json via rpc",
	Long:  "configure privacy settings\n\ntest of the api endpoint SetPrivacy",
	Run: func(cmd *cobra.Command, args []string) {
		client := clirpc.Client(cmd.Flags())

		cAddr, err := coincipher.DecodeBase58Address(rewardAddress)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("invalid address specified: %v", err))
		}

		resp, err := client.SetPrivacy(privacyconfig.Privacy{DisplayNodeIP: displayNodeIP, RewardAddress: cAddr})
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("Failed to connect: %v", err))
		}

		var prettyJSON bytes.Buffer
		err = json.Indent(&prettyJSON, []byte(resp), "", "   ")
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("JSON parse error: %v", err))
		}

		output := fmt.Sprintf("Privacy settings updated to:\n %v\n", prettyJSON.String())

		var jsonOutput map[string]interface{}
		err = json.Unmarshal([]byte(resp), &jsonOutput)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("Failed to unmarshal json: %v", err))
		}
		internal.PrintOutput(cmd.Flags(), jsonOutput, output)
	},
}

var getPrivacyCmd = &cobra.Command{
	Use:   "get",
	Short: "read privacy setting from file",
	Long:  "configure privacy settings\n\ntest of the api endpoints GetPrivacy",
	Run: func(cmd *cobra.Command, args []string) {
		j, err := clirpc.Client(cmd.Flags()).GetPrivacy()
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("Failed to connect: %v", err))
		}
		internal.PrintOutput(cmd.Flags(), string(j), string(j+"\n"))
	},
}
