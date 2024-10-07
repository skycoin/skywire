// Package clirewards cmd/skywire-cli/commands/rewards/calc.go
package clirewards

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bitfield/script"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
	"github.com/tidwall/pretty"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	tgbot "github.com/skycoin/skywire/cmd/skywire-cli/commands/rewards/tgbot"
)

const yearlyTotalRewards int = 408000

var (
	yearlyTotal           int
	hwSurveyPath          string
	wdate                 = time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	wDate                 time.Time
	utfile                string
	disallowArchitectures string
	h0                    bool
	h1                    bool
	h2                    bool
	grr                   bool
	pubkey                string
	logLvl                string
	log                   *logging.Logger
	sConfig               string
	dConfig               string
	nodeInfoSvc           []byte
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
	SvcConf    bool
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
	RootCmd.AddCommand(
		tgbot.RootCmd,
	)
	RootCmd.Flags().SortFlags = false
	RootCmd.Flags().StringVarP(&logLvl, "loglvl", "s", "info", "[ debug | warn | error | fatal | panic | trace ] \u001b[0m*")
	RootCmd.Flags().StringVarP(&wdate, "date", "d", wdate, "date for which to calculate reward")
	RootCmd.Flags().StringVarP(&pubkey, "pk", "k", pubkey, "check reward for pubkey")
	RootCmd.Flags().StringVarP(&disallowArchitectures, "noarch", "n", "amd64,null", "disallowed architectures, comma separated")
	RootCmd.Flags().IntVarP(&yearlyTotal, "year", "y", yearlyTotalRewards, "yearly total rewards")
	RootCmd.Flags().StringVarP(&utfile, "utfile", "u", "ut.txt", "uptime tracker data file")
	RootCmd.Flags().StringVarP(&hwSurveyPath, "lpath", "p", "log_collecting", "path to the surveys")
	RootCmd.Flags().StringVarP(&sConfig, "svcconf", "f", "/opt/skywire/services-config.json", "path to the services-config.json")
	RootCmd.Flags().StringVarP(&dConfig, "dmsghttpconf", "g", "/opt/skywire/dmsghttp-config.json", "path to the dmsghttp-config.json")
	RootCmd.Flags().BoolVarP(&h0, "h0", "0", false, "hide statistical data")
	RootCmd.Flags().BoolVarP(&h1, "h1", "1", false, "hide survey csv data")
	RootCmd.Flags().BoolVarP(&h2, "h2", "2", false, "hide reward csv data")
	RootCmd.Flags().BoolVarP(&grr, "err", "e", false, "account for non rewarded keys")
}

// RootCmd is the root command for skywire-cli rewards
var RootCmd = &cobra.Command{
	Use:   "rewards",
	Short: "calculate rewards from uptime data & collected surveys",
	Long: `
Collect surveys:  skywire-cli log
Fetch uptimes:    skywire-cli ut > ut.txt`,
	Run: func(_ *cobra.Command, _ []string) {
		var err error
		if log == nil {
			log = logging.MustGetLogger("rewards")
		}
		if logLvl != "" {
			if lvl, err := logging.LevelFromString(logLvl); err == nil {
				logging.SetLevel(lvl)
			}
		}
		mustExist(sConfig)
		mustExist(dConfig)

		sConf, err := script.File(sConfig).JQ(`.prod  | del(.stun_servers)`).Bytes()
		if err != nil {
			log.Fatal("error parsing json with jq:\n", err)
		}
		dConf, err := script.File(dConfig).JQ(`.prod`).Bytes()
		if err != nil {
			log.Fatal("error parsing json with jq:\n", err)
		}

		wDate, err = time.Parse("2006-01-02", wdate)
		if err != nil {
			log.Fatal("Error parsing date:", err)
			return
		}
		_, err = os.Stat(hwSurveyPath)
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
			if len(res) == 0 {
				log.Fatal("Specified key " + pubkey + "\n did not achieve minimum uptime on " + wdate + " !")
			}
		}
		var nodesInfos []nodeinfo
		var grrInfos []nodeinfo
		for _, pk := range res {
			nodeInfoDotJSON := fmt.Sprintf("%s/%s/node-info.json", hwSurveyPath, pk)
			_, err = os.Stat(nodeInfoDotJSON)
			if os.IsNotExist(err) {
				log.Debug(err.Error())
				continue
			}
			var (
				ip      string
				sky     string
				arch    string
				uu      string
				ifc     string
				ifc1    string
				macs    []string
				macs1   []string
				svcconf bool
			)

			//stun_servers does not currently match between conf.skywire.skycoin.com & https://github.com/skycoin/skywire/blob/develop/services-config.json ; omit checking them until next version
			nodeInfoSvc, err = script.File(nodeInfoDotJSON).JQ(`.services | del(.stun_servers)`).Bytes()
			if err != nil {
				log.Debug(err.Error())
				continue
			}

			confType, _ := script.File(nodeInfoDotJSON).JQ(`.services.dmsg_discovery`).Replace("\"", "").String() //nolint
			if err != nil {
				log.Debug(err.Error())
				continue
			}
			if strings.HasPrefix(confType, "http://") {
				svcconf = compareAndPrintDiffs(nodeInfoSvc, sConf, true)
			}
			if strings.HasPrefix(confType, "dmsg://") {
				svcconf = compareAndPrintDiffs(nodeInfoSvc, dConf, true)
			}

			ip, _ = script.File(nodeInfoDotJSON).JQ(`.ip_address`).Replace(" ", "").Replace(`"`, "").String() //nolint
			ip = strings.TrimRight(ip, "\n")
			if strings.Count(ip, ".") != 3 {
				ip, _ = script.File(nodeInfoDotJSON).JQ(`."ip.skycoin.com".ip_address`).Replace(" ", "").Replace(`"`, "").String() //nolint
				ip = strings.TrimRight(ip, "\n")
			}
			sky, _ = script.File(nodeInfoDotJSON).JQ(".skycoin_address").Replace(" ", "").Replace(`"`, "").String() //nolint
			sky = strings.TrimRight(sky, "\n")
			arch, _ = script.File(nodeInfoDotJSON).JQ(`if .zcalusic_sysinfo.os.architecture == null or .go_arch == .zcalusic_sysinfo.os.architecture then .go_arch else null end`).Replace(" ", "").Replace(`"`, "").String() //nolint
			arch = strings.TrimRight(arch, "\n")
			uu, _ = script.File(nodeInfoDotJSON).JQ(".uuid").Replace(" ", "").Replace(`"`, "").String() //nolint
			uu = strings.TrimRight(uu, "\n")
			ifc, _ = script.File(nodeInfoDotJSON).JQ(`[.ip_addr[]? | select(.ifname != "lo") | {address: .address, ifname: .ifname}]`).Replace(" ", "").Replace(`"`, "").String() //nolint
			ifc = strings.TrimRight(ifc, "\n")
			ifc1, _ = script.File(nodeInfoDotJSON).JQ(`[.zcalusic_sysinfo.network[] | {address: .macaddress, ifname: .name}]`).Replace(" ", "").Replace(`"`, "").String() //nolint
			ifc1 = strings.TrimRight(ifc1, "\n")
			macs, _ = script.File(nodeInfoDotJSON).JQ(`.ip_addr[]? | select(.ifname != "lo") | .address`).Replace(" ", "").Replace(`"`, "").Slice() //nolint
			macs1, _ = script.File(nodeInfoDotJSON).JQ(`.zcalusic_sysinfo.network[] | .macaddress`).Replace(" ", "").Replace(`"`, "").Slice()       //nolint
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
				SvcConf:    svcconf,
			}
			//enforce all requirements for rewards
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
			fmt.Printf("Visors meeting uptime & other requirements: %d\n", len(nodesInfos))
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

func mustExist(path string) {
	_, err := os.Stat(path)
	if os.IsNotExist(err) {
		log.Fatal("the path to the file does not exist: ", path, "\n", err)
	}
	if err != nil {
		log.Fatal("error on os.Stat(", path, "):\n", err)
	}
}

func init() {
	RootCmd.AddCommand(
		testCmd,
	)
	testCmd.Flags().SortFlags = false
	testCmd.Flags().StringVarP(&logLvl, "loglvl", "s", "info", "[ debug | warn | error | fatal | panic | trace ] \u001b[0m*")
	testCmd.Flags().StringVarP(&pubkey, "pk", "k", pubkey, "verify services in survey for pubkey")
	testCmd.Flags().StringVarP(&hwSurveyPath, "lpath", "p", "log_collecting", "path to the surveys")
	testCmd.Flags().StringVarP(&sConfig, "svcconf", "f", "/opt/skywire/services-config.json", "path to the services-config.json")
	testCmd.Flags().StringVarP(&dConfig, "dmsghttpconf", "g", "/opt/skywire/dmsghttp-config.json", "path to the dmsghttp-config.json")
}

var testCmd = &cobra.Command{
	Use:   "svc",
	Short: "verify services in survey",
	Run: func(_ *cobra.Command, _ []string) {
		var err error
		if log == nil {
			log = logging.MustGetLogger("rewards")
		}
		if logLvl != "" {
			if lvl, err := logging.LevelFromString(logLvl); err == nil {
				logging.SetLevel(lvl)
			}
		}

		var pk1 cipher.PubKey
		err = pk1.Set(pubkey)
		if err != nil {
			log.Fatal("invalid public key\n", err)
		}

		mustExist(hwSurveyPath)
		mustExist(fmt.Sprintf("%s/%s/node-info.json", hwSurveyPath, pubkey))
		mustExist(sConfig)
		mustExist(dConfig)

		//stun_servers does not currently match between conf.skywire.skycoin.com & https://github.com/skycoin/skywire/blob/develop/services-config.json ; omit checking them until next version
		nodeInfoSvc, err = script.File(fmt.Sprintf("%s/%s/node-info.json", hwSurveyPath, pubkey)).JQ(`.services | del(.stun_servers)`).Bytes()
		if err != nil {
			log.Fatal("error parsing json with jq:\n", err)
		}

		sConf, err := script.File(sConfig).JQ(`.prod  | del(.stun_servers)`).Bytes()
		if err != nil {
			log.Fatal("error parsing json with jq:\n", err)
		}

		dConf, err := script.File(dConfig).JQ(`.prod`).Bytes()
		if err != nil {
			log.Fatal("error parsing json with jq:\n", err)
		}

		confType, err := script.File(fmt.Sprintf("%s/%s/node-info.json", hwSurveyPath, pubkey)).JQ(`.services.dmsg_discovery`).Replace("\"", "").String()
		if err != nil {
			log.Fatal("could not determine config type ; error parsing json with jq:\n", err)
		}

		if strings.HasPrefix(confType, "http://") {
			if !compareAndPrintDiffs(nodeInfoSvc, sConf, false) {
				log.Fatal("services are not configured correctly for http")
			}
			log.Info("services are configured correctly for http")
			fmt.Printf("%s\n", pretty.Color(pretty.Pretty(nodeInfoSvc), nil))
			return
		}

		if strings.HasPrefix(confType, "dmsg://") {
			if !compareAndPrintDiffs(nodeInfoSvc, dConf, false) {
				log.Fatal("services are not configured correctly for dmsghttp")
			}
			log.Info("services are configured correctly for dmsghttp")
			fmt.Printf("%s\n", pretty.Color(pretty.Pretty(nodeInfoSvc), nil))
			return
		}

		if !strings.HasPrefix(confType, "http://") && !strings.HasPrefix(confType, "dmsg://") {
			fmt.Printf("%s\n", pretty.Color(pretty.Pretty(nodeInfoSvc), nil))
			log.Fatal("could not determine config type from dmsg_discovery value ; invalid service configuration")
		}
	},
}

func compareAndPrintDiffs(nodeInfoData, configData []byte, noLogging bool) bool {
	var nodeInfoServices map[string]interface{}
	var configServices map[string]interface{}

	if err := json.Unmarshal(nodeInfoData, &nodeInfoServices); err != nil {
		if !noLogging {
			log.Fatal("error unmarshalling nodeInfoData: ", err)
		}
		return false
	}
	if err := json.Unmarshal(configData, &configServices); err != nil {
		if !noLogging {
			log.Fatal("error unmarshalling configData: ", err)
		}
		return false
	}

	return compareMaps(nodeInfoServices, configServices, noLogging)
}

func compareMaps(nodeInfoServices, configServices map[string]interface{}, noLogging bool) bool {
	for key, value1 := range nodeInfoServices {
		if value2, ok := configServices[key]; ok {
			if reflect.TypeOf(value1).Kind() == reflect.Slice && reflect.TypeOf(value2).Kind() == reflect.Slice {
				slice1 := value1.([]interface{})
				slice2 := value2.([]interface{})
				if !sliceContains(slice1, slice2) {
					if !noLogging {
						printDifference(key, value1, value2)
					}
					return false
				}
			} else if !reflect.DeepEqual(value1, value2) {
				if !noLogging {
					printDifference(key, value1, value2)
				}
				return false
			}
		}
	}
	return true
}

func sliceContains(slice1, slice2 []interface{}) bool {
	for _, v2 := range slice2 {
		found := false
		for _, v1 := range slice1 {
			if reflect.DeepEqual(v1, v2) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func toJSON(value interface{}) string {
	jsonData, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Sprintf("%v", value)
	}
	return string(jsonData)
}

func printDifference(key string, value1, value2 interface{}) {
	red := color.New(color.FgRed).SprintFunc()
	fmt.Printf("%s: %s != %s\n", key, red(toJSON(value1)), red(toJSON(value2)))
}
