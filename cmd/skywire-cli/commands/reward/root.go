// Package clireward cmd/skywire-cli/commands/reward/root.go
package clireward

import (
	"fmt"
	"os"
	"strings"

	"github.com/bitfield/script"
	coincipher "github.com/skycoin/skycoin/src/cipher"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
	"github.com/skycoin/skywire/pkg/skyenv"
)

var (
	rewardFile           string = skyenv.PackageConfig().LocalPath + "/" + skyenv.RewardFile
	rewardAddress        string
	defaultRewardAddress string
	output               string
	isUseRPC             bool
	useRPC               bool
	isRead               bool
	isRewarded           bool
	isDeleteFile         bool
	isAll                bool
	rpcFlagTxt           string
	readFlagTxt          string
	cHiddenFlags         []string
)

func init() {

	rewardCmd.Flags().SortFlags = false
	if defaultRewardAddress == "" {
		//default is genesis address for skycoin blockchain ; for testing
		defaultRewardAddress = "2jBbGxZRGoQG1mqhPBnXnLTxK6oxsTf8os6"
	}
	defaultRewardAddress = strings.TrimSuffix(defaultRewardAddress, "\n")
	rewardCmd.Flags().StringVarP(&rewardAddress, "address", "a", "", "reward address\ndefault: "+defaultRewardAddress)
	cHiddenFlags = append(cHiddenFlags, "address")
	rewardCmd.Flags().StringVarP(&output, "out", "o", "", "write reward address to: "+rewardFile)
	cHiddenFlags = append(cHiddenFlags, "out")
	if isRewarded {
		readFlagTxt = "\n" + defaultRewardAddress
	}
	rewardCmd.Flags().BoolVarP(&isRead, "read", "r", false, "print the skycoin reward address & exit"+readFlagTxt)
	cHiddenFlags = append(cHiddenFlags, "read")

	//check if the visor is running

	//	_, err = net.DialTimeout("tcp", "localhost:3435", 5)
	//the above was insufficient in practice

	//TODO: re-implement this simple check for the visor running
	_, err := script.Exec(`skywire-cli visor pk`).String()
	if err == nil {
		useRPC = true
	} else {
		useRPC = false
		rpcFlagTxt = "default: false - visor is not running"
	}
	if skyenv.IsRoot() {
		useRPC = false
		rpcFlagTxt = "default: false - root permissions available"
	}
	rewardCmd.Flags().BoolVarP(&isUseRPC, "userpc", "u", useRPC, "use the rpc of the running visor\n"+rpcFlagTxt)
	cHiddenFlags = append(cHiddenFlags, "userpc")
	rewardCmd.Flags().BoolVarP(&isDeleteFile, "delete", "d", false, "delete reward addresss file - opt out of rewards")
	cHiddenFlags = append(cHiddenFlags, "delete")

	rewardCmd.Flags().BoolVar(&isAll, "all", false, "show all flags")
	for _, j := range cHiddenFlags {
		rewardCmd.Flags().MarkHidden(j) //nolint
	}

}

// RootCmd is rewardCmd
var RootCmd = rewardCmd

const longtext = `
	reward address setting

	Sets the skycoin reward address for the visor.
	The config is written to the root of the default local directory

	this config is served via dmsghttp along with transport logs
	and the system hardware survey for automating reward distribution`

func longText() string {
	//show configured reward address if valid configuration exists
	//only the default is supported
	if _, err := os.Stat(rewardFile); err == nil {
		reward, err := os.ReadFile(rewardFile) //nolint
		if err != nil {
			fmt.Errorf("	reward settings misconfigured!") //nolint
		}
		_, err = coincipher.DecodeBase58Address(string(reward))
		if err != nil {
			fmt.Errorf("	invalid address in reward config %v", err) //nolint
		}
		isRewarded = true
		defaultRewardAddress = fmt.Sprintf("%s\n", reward)
		return "\n	skycoin reward address set to:\n	" + fmt.Sprintf("%s\n", reward) //+longtext
	}
	return longtext
}

var rewardCmd = &cobra.Command{
	Use:                   "reward <address> || [flags]",
	DisableFlagsInUseLine: true,
	Short:                 "skycoin reward address",
	Long:                  longText(),
	PreRun: func(cmd *cobra.Command, _ []string) {
		//--all unhides flags, prints help menu, and exits
		if isAll {
			for _, j := range cHiddenFlags {
				f := cmd.Flags().Lookup(j) //nolint
				f.Hidden = false
			}
			cmd.Flags().MarkHidden("all") //nolint
			cmd.Help()                    //nolint
			os.Exit(0)
		}
	},
	Run: func(cmd *cobra.Command, args []string) {
		//set default output file
		if output == "" {
			output = skyenv.PackageConfig().LocalPath + "/" + skyenv.RewardFile
		}
		if isDeleteFile {
			err := os.Remove(output)
			if err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("Error deleting file. err=%v", err))
			}
		}
		//print reward address and exit
		if isRead {
			dat, err := os.ReadFile(output) //nolint
			if err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("Error reading file. err=%v", err))
			}
			output := fmt.Sprintf("%s\n", dat)
			internal.PrintOutput(cmd.Flags(), output, output)
			os.Exit(0)
		}
		//set reward address from first argument
		if len(args) > 0 {
			if args[0] != "" {
				rewardAddress = args[0]
			}
		}
		if rewardAddress == "" {
			rewardAddress = defaultRewardAddress
		}
		//remove any newline from rewardAddress string
		rewardAddress = strings.TrimSuffix(rewardAddress, "\n")
		//validate the skycoin address
		cAddr, err := coincipher.DecodeBase58Address(rewardAddress)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("invalid address specified: %v", err))
		}
		//using the rpc of the running visor avoids needing sudo permissions
		//true if visor is running
		//false if sudo permissions exist
		if isUseRPC {
			client := clirpc.Client(cmd.Flags())
			rewardaddress, err := client.SetRewardAddress(rewardAddress)
			if err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("Failed to connect: %v", err))
			}
			internal.PrintOutput(cmd.Flags(), rewardaddress, rewardaddress)

		} else {
			internal.Catch(cmd.Flags(), os.WriteFile(output, []byte(cAddr.String()), 0644)) //nolint
			readRewardFile(cmd.Flags())
		}
	},
}

func readRewardFile(cmdFlags *pflag.FlagSet) {
	//read the file which was written
	dat, err := os.ReadFile(output) //nolint
	if err != nil {
		internal.PrintFatalError(cmdFlags, fmt.Errorf("Error reading file. err=%v", err))
	}
	output := fmt.Sprintf("Reward address file:\n  %s\nreward address:\n  %s\n", output, dat)
	internal.PrintOutput(cmdFlags, output, output)
}
