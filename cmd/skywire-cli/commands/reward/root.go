// Package clireward cmd/skywire-cli/commands/reward/root.go
package clireward

import (
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/visor"
	"github.com/skycoin/skywire/pkg/visor/rewardconfig"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

var (
	rewardFile           string = visorconfig.PackageConfig().LocalPath + "/" + skyenv.RewardFile
	rewardAddress        string
	defaultRewardAddress string
	output               string
	isRead               bool
	isRewarded           bool
	isDeleteFile         bool
	isAll                bool
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
	rewardCmd.Flags().BoolVarP(&isDeleteFile, "delete", "d", false, "delete reward addresss file - opt out of rewards")
	cHiddenFlags = append(cHiddenFlags, "delete")
	rewardCmd.Flags().BoolVar(&isAll, "all", false, "show all flags")
	for _, j := range cHiddenFlags {
		rewardCmd.Flags().MarkHidden(j) //nolint:errcheck,gosec
	}

}

// RootCmd is rewardCmd
var RootCmd = rewardCmd

const longtext = `
	reward address setting

	Sets the skycoin reward address or xpub key for the visor.
	Accepts either a skycoin address or a BIP44 account-level xpub key.
	The address is written to the root of the default 'local' directory
	specified in the visor's config.

	This file is parsed by the visor at runtime, and the reward address
	is included in the survey which is served via dmsghttp along with
	transport logs and the system hardware survey for automating reward
	distribution.

	By setting a reward address, you consent to collection of the survey
	from the visors where the reward address is set.
	The survey is ONLY for verification of reward eligibility. We respect
	your privacy.

	XPUB KEY SETUP (BIP44 wallets)

	When using an xpub key, you must provide the account-level xpub
	(BIP44 path: m/44'/coin'/account'). This is NOT the same as the
	external chain xpub shown in the Skycoin web wallet GUI.

	The Skycoin web wallet displays the external chain xpub (path 0/0,
	depth 4). The reward and login systems require the account-level
	xpub (path 0, depth 3) which is the parent of both the external
	and change chains.

	To export the correct xpub from a bip44 wallet using the CLI:

	  1. Start a skycoin node with seed API enabled:
	     skywire skycoin daemon --enable-api-sets="INSECURE_WALLET_SEED"

	  2. Export the account-level xpub (path 0):
	     skywire skycoin cli walletKeyExport WALLET_FILE -k xpub --path=0

	     The default --path=0/0 gives the external chain xpub (WRONG).
	     You must use --path=0 for the account-level xpub (CORRECT).

	  3. Set the reward address:
	     skywire reward ACCOUNT_XPUB

	BIP44 key hierarchy:

	  m/44'/coin'/0'            <-- account xpub (depth 3) USE THIS
	    ├── 0/                  <-- external chain (depth 4, receiving)
	    │   ├── 0  address 1
	    │   ├── 1  address 2
	    │   └── ...
	    └── 1/                  <-- change chain (depth 4, login)
	        ├── 0  login address
	        └── ...

	The login system derives verification addresses from the change
	chain (m/44'/coin'/0'/1/i) so they do not collide with receiving
	addresses on the external chain.`

func longText() string {
	//show configured reward address if valid configuration exists
	// try RPC first to get the visor's actual reward address
	const rpcDialTimeout = time.Second * 2
	conn, dialErr := net.DialTimeout("tcp", clirpc.Addr, rpcDialTimeout)
	if dialErr == nil {
		rpcLogger := logging.MustGetLogger("rpc-reward")
		client := visor.NewRPCClient(rpcLogger, conn, visor.RPCPrefix, 0)
		rwdAdd, err := client.GetRewardAddress()
		if err == nil && strings.TrimSpace(rwdAdd) != "" {
			_, _, err = rewardconfig.ValidateRewardAddress(strings.TrimSpace(rwdAdd))
			if err == nil {
				isRewarded = true
				defaultRewardAddress = fmt.Sprintf("%s\n", rwdAdd)
				return "\n    skycoin reward address set to:\n    " + strings.TrimSpace(rwdAdd) + "\n"
			}
		}
	}
	// fallback to local file
	if _, err := os.Stat(rewardFile); err == nil {
		//nolint:gosec
		reward, err := os.ReadFile(rewardFile)
		if err != nil {
			fmt.Printf("    reward settings misconfigured!")
		}
		_, _, err = rewardconfig.ValidateRewardAddress(strings.TrimSpace(string(reward)))
		if err != nil {
			fmt.Printf("    invalid address in reward config %v", err)
		}
		isRewarded = true
		defaultRewardAddress = fmt.Sprintf("%s\n", reward)
		return "\n    skycoin reward address set to:\n    " + fmt.Sprintf("%s\n", reward) //+longtext
	}
	return longtext
}

var rewardCmd = &cobra.Command{
	Use:                   "reward <address | xpub> || [flags]",
	DisableFlagsInUseLine: true,
	Short:                 "skycoin reward address or xpub key",
	Long:                  longText(),
	PreRun: func(cmd *cobra.Command, _ []string) {
		//--all unhides flags, prints help menu, and exits
		if isAll {
			for _, j := range cHiddenFlags {
				f := cmd.Flags().Lookup(j)
				f.Hidden = false
			}
			cmd.Flags().MarkHidden("all") //nolint:errcheck,gosec
			internal.Catch(cmd.Flags(), cmd.Help())
			os.Exit(0)
		}
	},
	Run: func(cmd *cobra.Command, args []string) {
		//set default output file
		if output == "" {
			output = visorconfig.PackageConfig().LocalPath + "/" + skyenv.RewardFile
		}
		if isDeleteFile {
			_, err := os.Stat(output)
			if err != nil {
				out1 := "reward file does not exist - reward address not set\n"
				internal.PrintOutput(cmd.Flags(), out1, out1)
				os.Exit(0)
			}
		}
		//using the rpc of the running visor avoids needing sudo permissions
		client, clienterr := clirpc.Client(cmd.Flags())
		if clienterr != nil {
			internal.PrintError(cmd.Flags(), clienterr)
		}

		if isDeleteFile {
			if clienterr == nil {
				err := client.DeleteRewardAddress()
				if err != nil {
					internal.PrintError(cmd.Flags(), err)
				}
			}
			if clienterr != nil {
				err := os.Remove(rewardFile)
				if err != nil {
					internal.PrintError(cmd.Flags(), err)
				}
			}
			os.Exit(1)
			return
		}
		//print reward address and exit
		if isRead {
			// prefer RPC to get the visor's actual reward address
			if clienterr == nil {
				rwdAdd, err := client.GetRewardAddress()
				if err == nil && rwdAdd != "" {
					out := fmt.Sprintf("%s\n", rwdAdd)
					internal.PrintOutput(cmd.Flags(), out, out)
					os.Exit(0)
				}
			}
			// fallback to local file
			//nolint:gosec
			dat, err := os.ReadFile(output)
			if err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("Error reading file. err=%v", err))
			}
			out := fmt.Sprintf("%s\n", dat)
			internal.PrintOutput(cmd.Flags(), out, out)
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
		//validate the skycoin address or xpub key
		canonical, isXpub, err := rewardconfig.ValidateRewardAddress(rewardAddress)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("invalid address or xpub key specified: %v", err))
		}
		if isXpub {
			if warning := rewardconfig.CheckXpubDepth(canonical); warning != "" {
				fmt.Fprintf(os.Stderr, "\n%s\n\n", warning)
			}
		}

		//using the rpc of the running visor avoids needing sudo permissions
		if clienterr != nil {
			internal.Catch(cmd.Flags(), os.WriteFile(output, []byte(canonical), 0600))
			readRewardFile(cmd.Flags())
			return
		}

		if clienterr == nil {
			rwdAdd, err := client.SetRewardAddress(canonical)
			if err != nil {
				internal.PrintError(cmd.Flags(), fmt.Errorf("Failed to connect: %v", err))
				return
			}
			// also write to the local file so -r works without visor running
			os.WriteFile(output, []byte(rwdAdd), 0600) //nolint:errcheck,gosec
			out := fmt.Sprintf("Reward address:\n  %s\n", rwdAdd)
			internal.PrintOutput(cmd.Flags(), out, out)
		} else {
			internal.Catch(cmd.Flags(), os.WriteFile(output, []byte(canonical), 0600))
			readRewardFile(cmd.Flags())
		}
	},
}

func readRewardFile(cmdFlags *pflag.FlagSet) {
	//read the file which was written
	//nolint:gosec
	dat, err := os.ReadFile(output)
	if err != nil {
		internal.PrintFatalError(cmdFlags, fmt.Errorf("Error reading file. err=%v", err))
	}
	output := fmt.Sprintf("Reward address file:\n  %s\nreward address:\n  %s\n", output, dat)
	internal.PrintOutput(cmdFlags, output, output)
}
