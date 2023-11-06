// Package clireward cmd/skywire-cli/commands/reward/root.go
package clireward

import (
	"fmt"
	"log"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	coincipher "github.com/skycoin/skycoin/src/cipher"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/bitfield/script"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

var (
	rewardFile           string = visorconfig.PackageConfig().LocalPath + "/" + visorconfig.RewardFile
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
			fmt.Errorf("    reward settings misconfigured!") //nolint
		}
		_, err = coincipher.DecodeBase58Address(strings.TrimSpace(string(reward)))
		if err != nil {
			fmt.Errorf("    invalid address in reward config %v", err) //nolint
		}
		isRewarded = true
		defaultRewardAddress = fmt.Sprintf("%s\n", reward)
		return "\n    skycoin reward address set to:\n    " + fmt.Sprintf("%s\n", reward) //+longtext
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
			output = visorconfig.PackageConfig().LocalPath + "/" + visorconfig.RewardFile
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
		if clienterr != nil {
			internal.Catch(cmd.Flags(), os.WriteFile(output, []byte(cAddr.String()), 0644)) //nolint
			readRewardFile(cmd.Flags())
			return
		}

		if clienterr == nil {
			rwdAdd, err := client.SetRewardAddress(rewardAddress)
			if err != nil {
				internal.PrintError(cmd.Flags(), fmt.Errorf("Failed to connect: %v", err)) //nolint
				return
			}
			output := fmt.Sprintf("Reward address:\n  %s\n", rwdAdd)
			internal.PrintOutput(cmd.Flags(), output, output)
		}
		if clienterr != nil {
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




const yearlyTotalRewards int = 408000

var (
 yearlyTotal int
 surveyPath string
 wdate = time.Now().AddDate(0, 0, -1).Format("2006-01-02")
 utfile string
 disallowArchitectures string
)

type nodeinfo struct {
	SkyAddr string  `json:"skycoin_address"`
	PK      string  `json:"public_key"`
	Arch         string  `json:"go_arch"`
	IPAddr      string  `json:"ip_address"`
	Share         float64 `json:"reward_share"`
	Reward   float64 `json:"reward_amount"`
}

type ipCount struct {
	IP    string
	Count int
}

type rewardData struct {
  SkyAddr string
  Reward  float64
}


func init() {
	RootCmd.AddCommand(rewardCalcCmd)
	rewardCalcCmd.Flags().SortFlags = false
  rewardCalcCmd.Flags().StringVarP(&wdate, "date", "d", wdate, "date for which to calculate reward")
  rewardCalcCmd.Flags().StringVarP(&disallowArchitectures, "noarch", "n", "amd64", "disallowed architectures, comma separated")
  rewardCalcCmd.Flags().IntVarP(&yearlyTotal, "year", "y", yearlyTotalRewards, "yearly total rewards")
  rewardCalcCmd.Flags().StringVarP(&utfile, "utfile", "u", "ut.txt", "uptime tracker data file")
	rewardCalcCmd.Flags().StringVarP(&surveyPath, "path", "p", "./log_collecting", "path to the surveys ")
}

var rewardCalcCmd = &cobra.Command{
	Use:   "calc",
	Short: "calculate rewards from uptime data & collected surveys",
  Long: `
Collect surveys:  skywire-cli log
Fetch uptimes:    skywire-cli ut > ut.txt`,
	Run: func(cmd *cobra.Command, args []string) {
		_, err := os.Stat(surveyPath)
    if os.IsNotExist(err) {
      log.Fatal("the path to the surveys does not exist\n", err, "\nfetch the surveys with:\n$ skywire-cli log")
    }
		_, err = os.Stat(utfile)
    if os.IsNotExist(err) {
      log.Fatal("uptime tracker data file not found\n", err, "\nfetch the uptime tracker data with:\n$ skywire-cli ut > ut.txt")
    }

    archMap := make(map[string]struct{})
    for _, disallowedarch := range strings.Split(disallowArchitectures, ",") {
      if disallowedarch != "" {
        archMap[disallowedarch] = struct{}{}
      }
    }
    res, _ := script.File(utfile).Match(strings.TrimRight(wdate, "\n")).Column(1).Slice() //nolint
    var nodesInfos []nodeinfo
    for _, pk := range res {
      nodeInfo := fmt.Sprintf("%s/%s/node-info.json", surveyPath, pk)
      ip, _ := script.File(nodeInfo).JQ(`."ip.skycoin.com".ip_address`).Replace(" ", "").Replace(`"`, "").String() //nolint
      ip = strings.TrimRight(ip, "\n")
      sky, _ := script.File(nodeInfo).JQ(".skycoin_address").Replace(" ", "").Replace(`"`, "").String() //nolint
      sky = strings.TrimRight(sky, "\n")
      arch, _ := script.File(nodeInfo).JQ(".go_arch").Replace(" ", "").Replace(`"`, "").String() //nolint
      arch = strings.TrimRight(arch, "\n")
      if _, disallowed := archMap[arch]; !disallowed && ip != "" && strings.Count(ip, ".") == 3 && sky != "" {
        ni := nodeinfo{
          IPAddr: ip,
          SkyAddr: sky,
          PK:      pk,
          Arch:    arch,
        }
        nodesInfos = append(nodesInfos, ni)
      }
    }
    daysThisMonth := float64(time.Date(time.Now().Year(), time.Now().Month()+1, 0, 0, 0, 0, 0, time.UTC).Day())
    daysThisYear := float64(int(time.Date(time.Now().Year(), 12, 31, 23, 59, 59, 999999999, time.UTC).Sub(time.Date(time.Now().Year(), 1, 1, 0, 0, 0, 0, time.UTC)).Hours())/24)
    monthReward := (float64(yearlyTotal) / daysThisYear) * daysThisMonth
    dayReward := monthReward / daysThisMonth
    wdate = strings.ReplaceAll(wdate, " ", "0")
    fmt.Printf("date: %s\n", wdate)
    fmt.Printf("days this month: %.4f\n", daysThisMonth)
    fmt.Printf("days in the year: %.4f\n", daysThisYear)
    fmt.Printf("this month's rewards: %.4f\n", monthReward)
    fmt.Printf("reward total: %.4f\n", dayReward)

    uniqueIP, _ := script.Echo(func() string { //nolint
      var inputStr strings.Builder
      for _, ni := range nodesInfos {
        inputStr.WriteString(fmt.Sprintf("%s\n", ni.IPAddr))
      }
      return inputStr.String()
    }()).Freq().String() //nolint
    var ipCounts []ipCount
    lines := strings.Split(uniqueIP, "\n")
    for _, line := range lines {
      if line != "" {
        fields := strings.Fields(line)
        if len(fields) == 2 {
          count, _ := strconv.Atoi(fields[0]) //nolint
          ipCounts = append(ipCounts, ipCount{
            IP:    fields[1],
            Count: count,
          })
        }
      }
    }
    totalValidShares := 0
    for _, ipCount := range ipCounts {
      if ipCount.Count <= 8 {
        totalValidShares += ipCount.Count
        } else {
        totalValidShares += 8
      }
    }
    fmt.Printf("Total valid shares: %d\n", totalValidShares)

    for i, ni := range nodesInfos {
      for _, ipCount := range ipCounts {
        if ni.IPAddr == ipCount.IP {
          if ipCount.Count <= 8 {
            nodesInfos[i].Share = 1.0
            } else {
            nodesInfos[i].Share = 8.0 / float64(ipCount.Count)
          }
          break
        }
      }
      nodesInfos[i].Reward = nodesInfos[i].Share / float64(totalValidShares) * dayReward
    }

    fmt.Println("IP, Skycoin Address, Skywire Public Key, Architecture, Reward Shares, Reward $SKY amout")
    for _, ni := range nodesInfos {
      fmt.Printf("%s, %s, %s, %s, %4f, %4f\n", ni.IPAddr, ni.SkyAddr, ni.PK, ni.Arch, ni.Share, ni.Reward)
    }
    rewardSumBySkyAddr := make(map[string]float64)
    for _, ni := range nodesInfos {
      rewardSumBySkyAddr[ni.SkyAddr] += ni.Reward
    }
    var sortedSkyAddrs []rewardData
    for skyAddr, rewardSum := range rewardSumBySkyAddr {
      sortedSkyAddrs = append(sortedSkyAddrs, rewardData{SkyAddr: skyAddr, Reward: rewardSum})
    }
    sort.Slice(sortedSkyAddrs, func(i, j int) bool {
      return sortedSkyAddrs[i].Reward > sortedSkyAddrs[j].Reward
    })
    fmt.Println("Skycoin Address, Reward Amount")
    for _, skyAddrReward := range sortedSkyAddrs {
      fmt.Printf("%s, %.4f\n", skyAddrReward.SkyAddr, skyAddrReward.Reward)
    }
  },
}
