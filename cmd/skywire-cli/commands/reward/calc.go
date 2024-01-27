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

	"github.com/bitfield/script"
	"github.com/spf13/cobra"
)

const yearlyTotalRewards int = 408000

var (
	yearlyTotal           int
	surveyPath            string
	wdate                 = time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	wDate                 time.Time
	utfile                string
	disallowArchitectures string
	h0                    bool
	h1                    bool
	h2                    bool
	grr                   bool
	pubkey                string
)

type nodeinfo struct {
	SkyAddr    string  `json:"skycoin_address"`
	PK         string  `json:"public_key"`
	Arch       string  `json:"go_arch"`
	Interfaces string  `json:"interfaces"`
	IPAddr     string  `json:"ip_address"`
	UUID       string  `json:"uuid"`
	Share      float64 `json:"reward_share"`
	Reward     float64 `json:"reward_amount"`
	MacAddr    string
}

type counting struct {
	Name  string
	Count int
}

type rewardData struct {
	SkyAddr string
	Reward  float64
	Shares  float64
}

func init() {
	RootCmd.AddCommand(rewardCalcCmd)
	rewardCalcCmd.Flags().SortFlags = false
	rewardCalcCmd.Flags().StringVarP(&wdate, "date", "d", wdate, "date for which to calculate reward")
	rewardCalcCmd.Flags().StringVarP(&pubkey, "pk", "k", pubkey, "check reward for pubkey")
	rewardCalcCmd.Flags().StringVarP(&disallowArchitectures, "noarch", "n", "amd64", "disallowed architectures, comma separated")
	rewardCalcCmd.Flags().IntVarP(&yearlyTotal, "year", "y", yearlyTotalRewards, "yearly total rewards")
	rewardCalcCmd.Flags().StringVarP(&utfile, "utfile", "u", "ut.txt", "uptime tracker data file")
	rewardCalcCmd.Flags().StringVarP(&surveyPath, "path", "p", "log_collecting", "path to the surveys")
	rewardCalcCmd.Flags().BoolVarP(&h0, "h0", "0", false, "hide statistical data")
	rewardCalcCmd.Flags().BoolVarP(&h1, "h1", "1", false, "hide survey csv data")
	rewardCalcCmd.Flags().BoolVarP(&h2, "h2", "2", false, "hide reward csv data")
	rewardCalcCmd.Flags().BoolVarP(&grr, "err", "e", false, "account for non rewarded keys")

}

var rewardCalcCmd = &cobra.Command{
	Use:   "calc",
	Short: "calculate rewards from uptime data & collected surveys",
	Long: `
Collect surveys:  skywire-cli log
Fetch uptimes:    skywire-cli ut > ut.txt`,
	Run: func(cmd *cobra.Command, args []string) {
		var err error
		wDate, err = time.Parse("2006-01-02", wdate)
		if err != nil {
			log.Fatal("Error parsing date:", err)
			return
		}
		_, err = os.Stat(surveyPath)
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
		var res []string
		if pubkey == "" {
			res, _ = script.File(utfile).Match(strings.TrimRight(wdate, "\n")).Column(1).Slice() //nolint
			if len(res) == 0 {
				log.Fatal("No keys achieved minimum uptime on " + wdate + " !")
			}
		} else {
			res, _ = script.File(utfile).Match(strings.TrimRight(wdate, "\n")).Column(1).Match(pubkey).Slice() //nolint
			script.Echo("len(res)" + string(len(res))).Stdout()
			if len(res) == 0 {
				log.Fatal("Specified key " + pubkey + "\n did not achieve minimum uptime on " + wdate + " !")
			}
		}
		var nodesInfos []nodeinfo
		var grrInfos []nodeinfo
		for _, pk := range res {
			nodeInfo := fmt.Sprintf("%s/%s/node-info.json", surveyPath, pk)
			ip, _ := script.File(nodeInfo).JQ(`."ip.skycoin.com".ip_address`).Replace(" ", "").Replace(`"`, "").String() //nolint
			ip = strings.TrimRight(ip, "\n")
			sky, _ := script.File(nodeInfo).JQ(".skycoin_address").Replace(" ", "").Replace(`"`, "").String() //nolint
			sky = strings.TrimRight(sky, "\n")
			arch, _ := script.File(nodeInfo).JQ(".go_arch").Replace(" ", "").Replace(`"`, "").String() //nolint
			arch = strings.TrimRight(arch, "\n")
			uu, _ := script.File(nodeInfo).JQ(".uuid").Replace(" ", "").Replace(`"`, "").String() //nolint
			uu = strings.TrimRight(uu, "\n")
			ifc, _ := script.File(nodeInfo).JQ(`[.ip_addr[]? | select(.ifname != "lo") | {address: .address, ifname: .ifname}]`).Replace(" ", "").Replace(`"`, "").String() //nolint
			ifc = strings.TrimRight(ifc, "\n")
			ifc1, _ := script.File(nodeInfo).JQ(`[.zcalusic_sysinfo.network[] | {address: .macaddress, ifname: .name}]`).Replace(" ", "").Replace(`"`, "").String() //nolint
			ifc1 = strings.TrimRight(ifc1, "\n")
			macs, _ := script.File(nodeInfo).JQ(`.ip_addr[]? | select(.ifname != "lo") | .address`).Replace(" ", "").Replace(`"`, "").Slice() //nolint
			macs1, _ := script.File(nodeInfo).JQ(`.zcalusic_sysinfo.network[] | .macaddress`).Replace(" ", "").Replace(`"`, "").Slice()       //nolint
			if ifc == "[]" && ifc1 != "[]" {
				ifc = ifc1
			}
			if len(macs) == 0 && len(macs1) > 0 {
				macs = macs1
			} else {
				macs = append(macs, "")
			}
			ni := nodeinfo{
				IPAddr:     ip,
				SkyAddr:    sky,
				PK:         pk,
				Arch:       arch,
				Interfaces: ifc,
				MacAddr:    macs[0],
				UUID:       uu,
			}
			if _, disallowed := archMap[arch]; !disallowed && ip != "" && strings.Count(ip, ".") == 3 && sky != "" && uu != "" && ifc != "" && len(macs) > 0 && macs[0] != "" {
				nodesInfos = append(nodesInfos, ni)
			} else {
				if grr {
					grrInfos = append(grrInfos, ni)
				}
			}
		}
		if grr {
			for _, ni := range grrInfos {
				fmt.Printf("%s, %s, %.6f, %.6f, %s, %s, %s, %s \n", ni.SkyAddr, ni.PK, ni.Share, ni.Reward, ni.IPAddr, ni.Arch, ni.UUID, ni.Interfaces)
			}
			return
		}
		daysThisMonth := time.Date(wDate.Year(), wDate.Month()+1, 0, 0, 0, 0, 0, time.UTC).Day()
		daysThisYear := int(time.Date(wDate.Year(), 12, 31, 23, 59, 59, 999999999, time.UTC).Sub(time.Date(wDate.Year(), 1, 1, 0, 0, 0, 0, time.UTC)).Hours()) / 24
		monthReward := (float64(yearlyTotal) / float64(daysThisYear)) * float64(daysThisMonth)
		dayReward := monthReward / float64(daysThisMonth)
		wdate = strings.ReplaceAll(wdate, " ", "0")
		if !h0 {
			fmt.Printf("date: %s\n", wdate)
			fmt.Printf("days this month: %d\n", daysThisMonth)
			fmt.Printf("days in the year: %d\n", daysThisYear)
			fmt.Printf("this month's rewards: %.6f\n", monthReward)
			fmt.Printf("reward total: %.6f\n", dayReward)
		}
		uniqueIP, _ := script.Echo(func() string { //nolint
			var inputStr strings.Builder
			for _, ni := range nodesInfos {
				inputStr.WriteString(fmt.Sprintf("%s\n", ni.IPAddr))
			}
			return inputStr.String()
		}()).Freq().Slice() //nolint
		var ipCounts []counting
		for _, line := range uniqueIP {
			if line != "" {
				fields := strings.Fields(line)
				if len(fields) == 2 {
					count, _ := strconv.Atoi(fields[0]) //nolint
					ipCounts = append(ipCounts, counting{
						Name:  fields[1],
						Count: count,
					})
				}
			}
		}
		uniqueUUID, _ := script.Echo(func() string { //nolint
			var inputStr strings.Builder
			for _, ni := range nodesInfos {
				inputStr.WriteString(fmt.Sprintf("%s\n", ni.UUID))
			}
			return inputStr.String()
		}()).Freq().Slice() //nolint

		// look at the first non loopback interface macaddress
		uniqueMac, _ := script.Echo(func() string { //nolint
			var inputStr strings.Builder
			for _, ni := range nodesInfos {
				inputStr.WriteString(fmt.Sprintf("%s\n", ni.MacAddr))
			}
			return inputStr.String()
		}()).Freq().Slice() //nolint

		var macCounts []counting
		for _, line := range uniqueMac {
			if line != "" {
				fields := strings.Fields(line)
				if len(fields) == 2 {
					count, _ := strconv.Atoi(fields[0]) //nolint
					macCounts = append(macCounts, counting{
						Name:  fields[1],
						Count: count,
					})

				}
			}
		}

		totalValidShares := 0.0
		for _, ni := range nodesInfos {
			share := 1.0
			for _, ipCount := range ipCounts {
				if ni.IPAddr == ipCount.Name {
					if ipCount.Count >= 8 {
						share = 8.0 / float64(ipCount.Count)
					}
				}
			}
			for _, macCount := range macCounts {
				if macCount.Name == ni.MacAddr {
					share = share / float64(macCount.Count)
				}
			}
			totalValidShares += share
		}

		if !h0 {
			fmt.Printf("Visors meeting uptime & architecture requirements: %d\n", len(nodesInfos))
			fmt.Printf("Unique mac addresses for first interface after lo: %d\n", len(uniqueMac))
			fmt.Printf("Unique Ip Addresses: %d\n", len(uniqueIP))
			fmt.Printf("Unique UUIDs: %d\n", len(uniqueUUID))
			fmt.Printf("Total valid shares: %.6f\n", totalValidShares)
			fmt.Printf("Skycoin Per Share: %.6f\n", dayReward/totalValidShares)
		}
		for i, ni := range nodesInfos {
			nodesInfos[i].Share = 1.0
			for _, ipCount := range ipCounts {
				if ni.IPAddr == ipCount.Name {
					if ipCount.Count >= 8 {
						nodesInfos[i].Share = 8.0 / float64(ipCount.Count)
					}
				}
			}
			for _, macCount := range macCounts {
				if macCount.Name == ni.MacAddr {
					nodesInfos[i].Share = nodesInfos[i].Share / float64(macCount.Count)
				}
			}
			nodesInfos[i].Reward = nodesInfos[i].Share * dayReward / float64(totalValidShares)
		}

		if !h1 {
			fmt.Println("Skycoin Address, Skywire Public Key, Reward Shares, Reward SKY Amount, IP, Architecture, UUID, Interfaces")
			for _, ni := range nodesInfos {
				fmt.Printf("%s, %s, %.6f, %.6f, %s, %s, %s, %s \n", ni.SkyAddr, ni.PK, ni.Share, ni.Reward, ni.IPAddr, ni.Arch, ni.UUID, ni.Interfaces)
			}
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
		if !h0 {
			fmt.Printf("Total Reward Amount: %.6f\n", func() (tr float64) {
				for _, skyAddrReward := range sortedSkyAddrs {
					tr += skyAddrReward.Reward
				}
				return tr
			}())
		}
		if !h2 {
			fmt.Println("Skycoin Address, Reward Amount")
			for _, skyAddrReward := range sortedSkyAddrs {
				fmt.Printf("%s, %.6f\n", skyAddrReward.SkyAddr, skyAddrReward.Reward)
			}

		}
	},
}
