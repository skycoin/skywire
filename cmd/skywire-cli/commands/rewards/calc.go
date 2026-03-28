package clirewards

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bitfield/script"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
	"github.com/tidwall/pretty"

	tgbot "github.com/skycoin/skywire/cmd/skywire-cli/commands/rewards/tgbot"
	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/visor/rewardconfig"
	"github.com/skycoin/skywire/rewards"
)

const yearlyTotalRewardsPerPool int = 408000

var (
	yearlyTotal           int
	hwSurveyPath          string
	wdate                 = time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	wDate                 time.Time
	utfile                string
	disallowArchitectures []string
	allowArchitectures1   []string
	allowArchitectures2   []string
	h0                    bool
	h1                    bool
	h2                    bool
	grr                   bool
	pubkey                string
	logLvl                string
	processRewards        bool
	log                   = logging.MustGetLogger("rewards")
	nodeInfoSvc           []byte
	requireTransports     bool
	transportHistPath     string
	requireBandwidth      bool
	minBWThreshold        uint64
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
	MacAddr    string  `json:"mac_address"`
	SvcConf    bool    `json:"service_conf"`
	HV         string  `json:"hypervisor"`        //NOT the skywire hypervisor ; will be null unless the visor is running on virtual machine
	Reason     string  `json:"ineligible_reason"` //Reason why the visor will not be rewarded
	Bandwidth  uint64  `json:"bandwidth"`         //daily bandwidth in bytes from TPD
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

// runRewardProcessing executes the complete reward processing workflow
// This is equivalent to the reward.sh script functionality:
// 1. Checks if rewards already processed for the date
// 2. Fetches uptime tracker data
// 3. Generates ineligible list, shares CSV, reward transaction CSV, and stats
// All outputs are saved to hist/ directory with date prefix
func runRewardProcessing() {
	// Check if already processed
	txnFile := fmt.Sprintf("hist/%s.txt", wdate)
	if _, err := os.Stat(txnFile); err == nil {
		log.Fatal("Transaction already broadcasted for ", wdate)
	}

	// Create hist directory if it doesn't exist
	if err := os.MkdirAll("hist", 0750); err != nil {
		log.Fatal("Failed to create hist directory: ", err)
	}

	log.Info("Processing rewards for date: ", wdate)

	// Fetch uptime tracker data
	utOutputFile := fmt.Sprintf("hist/%s_ut.txt", wdate)
	log.Info("Fetching uptime tracker data...")

	//nolint:gosec
	cmd := exec.Command("skywire-cli", "ut", "--cdu", "hist/")
	utData, err := cmd.Output()
	if err != nil {
		log.Fatal("Failed to fetch uptime tracker data: ", err)
	}

	if err := os.WriteFile(utOutputFile, utData, 0600); err != nil {
		log.Fatal("Failed to write uptime tracker data: ", err)
	}

	// Update utfile to use the newly created file
	utfile = utOutputFile

	// Generate ineligible list
	ineligibleFile := fmt.Sprintf("hist/%s_ineligible.csv", wdate)
	log.Info("Generating ineligible list...")
	//nolint:gosec
	cmd = exec.Command("skywire-cli", "rewards", "--utfile", utfile, "-e", "-d", wdate, "-p", hwSurveyPath)
	var ineligibleOut bytes.Buffer
	cmd.Stdout = &ineligibleOut
	if err := cmd.Run(); err != nil {
		log.Fatal("Failed to generate ineligible list: ", err)
	}
	if err := os.WriteFile(ineligibleFile, ineligibleOut.Bytes(), 0600); err != nil {
		log.Fatal("Failed to write ineligible list: ", err)
	}

	// Generate shares CSV
	sharesFile := fmt.Sprintf("hist/%s_shares.csv", wdate)
	log.Info("Generating shares CSV...")
	//nolint:gosec
	cmd = exec.Command("skywire-cli", "rewards", "--utfile", utfile, "-2", "-0", "-d", wdate, "-p", hwSurveyPath)
	var sharesOut bytes.Buffer
	cmd.Stdout = &sharesOut
	if err := cmd.Run(); err != nil {
		log.Fatal("Failed to generate shares CSV: ", err)
	}
	if err := os.WriteFile(sharesFile, sharesOut.Bytes(), 0600); err != nil {
		log.Fatal("Failed to write shares CSV: ", err)
	}

	// Generate reward transaction CSV
	rewardTxnFile := fmt.Sprintf("hist/%s_rewardtxn0.csv", wdate)
	log.Info("Generating reward transaction CSV...")
	//nolint:gosec
	cmd = exec.Command("skywire-cli", "rewards", "--utfile", utfile, "-1", "-0", "-d", wdate, "-p", hwSurveyPath)
	var rewardTxnOut bytes.Buffer
	cmd.Stdout = &rewardTxnOut
	if err := cmd.Run(); err != nil {
		log.Fatal("Failed to generate reward transaction CSV: ", err)
	}
	if err := os.WriteFile(rewardTxnFile, rewardTxnOut.Bytes(), 0600); err != nil {
		log.Fatal("Failed to write reward transaction CSV: ", err)
	}

	// Generate stats
	statsFile := fmt.Sprintf("hist/%s_stats.txt", wdate)
	log.Info("Generating stats...")
	//nolint:gosec
	cmd = exec.Command("skywire-cli", "rewards", "--utfile", utfile, "-1", "-2", "-d", wdate, "-p", hwSurveyPath)
	var statsOut bytes.Buffer
	cmd.Stdout = &statsOut
	if err := cmd.Run(); err != nil {
		log.Fatal("Failed to generate stats: ", err)
	}
	if err := os.WriteFile(statsFile, statsOut.Bytes(), 0600); err != nil {
		log.Fatal("Failed to write stats: ", err)
	}

	log.Info("Reward processing complete!")
	log.Info("Files generated:")
	log.Info("  - ", utOutputFile)
	log.Info("  - ", ineligibleFile)
	log.Info("  - ", sharesFile)
	log.Info("  - ", rewardTxnFile)
	log.Info("  - ", statsFile)
}

func init() {
	RootCmd.AddCommand(
		tgbot.RootCmd,
	)
	RootCmd.Flags().SortFlags = false
	RootCmd.Flags().StringVarP(&logLvl, "loglvl", "s", "info", "[ debug | warn | error | fatal | panic | trace ]")
	RootCmd.Flags().StringVarP(&wdate, "date", "d", wdate, "date for which to calculate reward")
	RootCmd.Flags().StringVarP(&pubkey, "pk", "k", pubkey, "check reward for pubkey")
	RootCmd.Flags().StringSliceVarP(&disallowArchitectures, "noarch", "n", []string{"null", "wasm"}, "disallowed architectures, comma separated")
	RootCmd.Flags().StringSliceVarP(&allowArchitectures1, "a1", "w", func(all []string, dis []string) (res []string) {
		for _, v := range all {
			allow := true
			for _, d := range dis {
				if v == d {
					allow = false
					break
				}
			}
			if allow {
				res = append(res, v)
			}
		}
		return res
	}(rewards.Architectures, []string{"wasm", "amd64", "386"}), "pool 1 allowed arch, comma separated")

	RootCmd.Flags().StringSliceVarP(&allowArchitectures2, "a2", "x", func(all []string, dis []string) (res []string) {
		for _, v := range all {
			allow := true
			for _, d := range dis {
				if v == d {
					allow = false
					break
				}
			}
			if allow {
				res = append(res, v)
			}
		}
		return res
	}(rewards.Architectures, []string{"wasm", "arm64", "arm", "ppc64", "riscv64", "loong64", "mips", "mips64", "mips64le", "mipsle", "ppc64le", "s390x"}), "pool 2 allowed arch, comma separated")
	RootCmd.Flags().IntVarP(&yearlyTotal, "year", "y", yearlyTotalRewardsPerPool, "yearly total rewards per pool")
	RootCmd.Flags().StringVarP(&utfile, "utfile", "u", "ut.txt", "uptime tracker data file")
	RootCmd.Flags().StringVarP(&hwSurveyPath, "lpath", "p", "log_collecting", "path to the surveys")
	RootCmd.Flags().BoolVarP(&h0, "h0", "0", false, "hide statistical data")
	RootCmd.Flags().BoolVarP(&h1, "h1", "1", false, "hide survey csv data")
	RootCmd.Flags().BoolVarP(&h2, "h2", "2", false, "hide reward csv data")
	RootCmd.Flags().BoolVarP(&grr, "err", "e", false, "account for non rewarded keys")
	RootCmd.Flags().BoolVarP(&processRewards, "process", "r", false, "run complete reward processing workflow")
	RootCmd.Flags().BoolVarP(&requireTransports, "require-tp", "t", true, "require minimum transports (from hist/YYYY-MM-DD_transports.txt)")
	RootCmd.Flags().StringVarP(&transportHistPath, "tp-hist", "T", "hist", "path to transport history directory")
	RootCmd.Flags().BoolVarP(&requireBandwidth, "require-bw", "b", false, "require minimum bandwidth (proportional reward based on bandwidth)")
	RootCmd.Flags().Uint64VarP(&minBWThreshold, "min-bw", "B", defaultMinBandwidth, "minimum bandwidth in bytes to qualify (used with --require-bw)")
}

// RootCmd is the root command for skywire-cli rewards
var RootCmd = &cobra.Command{
	Use:   "rewards",
	Short: "calculate rewards from uptime data & collected surveys",
	Long: `
Collect surveys:  skywire-cli log
Fetch uptimes:    skywire-cli ut > ut.txt

Process rewards:  skywire-cli rewards --process

Architectures:
` + fmt.Sprintf("%v", append(rewards.Architectures, "null", "all")) + `

`,
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

		if processRewards {
			runRewardProcessing()
			return
		}

		sConf, err := script.Echo(string(deployment.ServicesJSON)).JQ(`.prod  | del(.stun_servers)`).Bytes()
		if err != nil {
			log.Fatal("error parsing json with jq:\n", err)
		}
		dConf, err := script.Echo(string(deployment.DmsghttpJSON)).JQ(`.prod`).Bytes()
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

		// Create a map for disallowed architectures
		disallowedMap := make(map[string]struct{})
		for _, disallowedArch := range disallowArchitectures {
			disallowedMap[disallowedArch] = struct{}{}
		}

		// Create maps for allowed architectures for pool 1 and pool 2
		allowArchMap1 := make(map[string]struct{})
		allowArchMap2 := make(map[string]struct{})

		// Create a map for quick lookup of skywire architectures
		supportedArchitecturesMap := make(map[string]struct{})
		for _, arch := range rewards.Architectures {
			supportedArchitecturesMap[arch] = struct{}{}
		}

		// Populate allowed architecture maps for pool 1 and pool 2, excluding disallowed ones
		for _, arch := range allowArchitectures1 {
			if _, isDisallowed := disallowedMap[arch]; !isDisallowed {
				allowArchMap1[arch] = struct{}{}
			}
		}
		for _, arch := range allowArchitectures2 {
			if _, isDisallowed := disallowedMap[arch]; !isDisallowed {
				allowArchMap2[arch] = struct{}{}
			}
		}

		if !requireBandwidth {
			// Legacy mode: check for common architectures between the two allowed slices
			for arch := range allowArchMap1 {
				if _, exists := allowArchMap2[arch]; exists {
					log.Fatal("Error: Architecture cannot be specified in both pools: " + arch)
				}
			}
		}

		// Validate each allowed architecture against the supported architectures
		for arch := range allowArchMap1 {
			if _, isValid := supportedArchitecturesMap[arch]; !isValid {
				log.Fatal("Error: Architecture is not valid: ", arch)
			}
		}

		for arch := range allowArchMap2 {
			if _, isValid := supportedArchitecturesMap[arch]; !isValid {
				log.Fatal("Error: Architecture is not valid: ", arch)
			}
		}

		// In bandwidth mode, merge both arch pools into a single allowed set for the presence pool
		// Pool 1 = presence (all archs, equal shares), Pool 2 = bandwidth (proportional)
		allowArchMapAll := make(map[string]struct{})
		if requireBandwidth {
			for arch := range allowArchMap1 {
				allowArchMapAll[arch] = struct{}{}
			}
			for arch := range allowArchMap2 {
				allowArchMapAll[arch] = struct{}{}
			}
		}

		// Load transport requirement data (visors that had >= 2 transports)
		transportMap := make(map[string]struct{})
		if requireTransports {
			transportFile := fmt.Sprintf("%s/%s_transports.txt", transportHistPath, wdate)
			if data, err := os.ReadFile(transportFile); err == nil { //nolint:gosec
				for _, line := range strings.Split(string(data), "\n") {
					line = strings.TrimSpace(line)
					if line != "" {
						transportMap[line] = struct{}{}
					}
				}
				log.Infof("Loaded %d visors with sufficient transports from %s", len(transportMap), transportFile)
			} else {
				log.Warnf("Transport file not found: %s (transport requirement will be skipped)", transportFile)
				requireTransports = false
			}
		}

		// Load bandwidth data for bandwidth pool
		bandwidthMap := make(map[string]uint64)
		if requireBandwidth {
			bwFile := fmt.Sprintf("%s/%s_bandwidth.json", transportHistPath, wdate)
			if data, err := os.ReadFile(bwFile); err == nil { //nolint:gosec
				if err := json.Unmarshal(data, &bandwidthMap); err != nil {
					log.Warnf("Failed to parse bandwidth file %s: %v", bwFile, err)
					requireBandwidth = false
				} else {
					log.Infof("Loaded bandwidth data for %d visors from %s", len(bandwidthMap), bwFile)
				}
			} else {
				log.Warnf("Bandwidth file not found: %s (bandwidth pool will be skipped)", bwFile)
				requireBandwidth = false
			}
		}

		var res []string
		if pubkey == "" {
			//nolint:errcheck
			res, _ = script.File(utfile).Match(strings.TrimRight(wdate, "\n")).Column(1).Slice()
			if len(res) == 0 {
				log.Fatal("No keys achieved minimum uptime on " + wdate + " !")
			}
		} else {
			//nolint:errcheck
			res, _ = script.File(utfile).Match(strings.TrimRight(wdate, "\n")).Column(1).Match(pubkey).Slice()
			if len(res) == 0 {
				log.Fatal("Specified key " + pubkey + "\n did not achieve minimum uptime on " + wdate + " !")
			}
		}

		// Collect eligible nodes
		// In legacy mode: nodesInfos1 = arch pool 1, nodesInfos2 = arch pool 2
		// In bandwidth mode: nodesInfos1 = presence pool (all archs), nodesInfos2 unused for presence
		var nodesInfos1 []nodeinfo
		var nodesInfos2 []nodeinfo
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
				hv      string
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

			//nolint:errcheck
			confType, _ := script.File(nodeInfoDotJSON).JQ(`.services.dmsg_discovery`).Replace("\"", "").String()
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

			//nolint:errcheck
			ip, _ = script.File(nodeInfoDotJSON).JQ(`.ip_address`).Replace(" ", "").Replace(`"`, "").String()
			ip = strings.TrimRight(ip, "\n")
			//nolint:errcheck
			sky, _ = script.File(nodeInfoDotJSON).JQ(".skycoin_address").Replace(" ", "").Replace(`"`, "").String()
			sky = strings.TrimRight(sky, "\n")
			arch, _ = script.File(nodeInfoDotJSON).JQ(`.go_arch`).Replace(" ", "").Replace(`"`, "").String() //nolint:errcheck
			arch = strings.TrimRight(arch, "\n")
			hv, _ = script.File(nodeInfoDotJSON).JQ(`.zcalusic_sysinfo.node.hypervisor`).Replace(" ", "").Replace(`"`, "").String() //nolint:errcheck
			hv = strings.TrimRight(hv, "\n")
			uu, _ = script.File(nodeInfoDotJSON).JQ(".uuid").Replace(" ", "").Replace(`"`, "").String() //nolint:errcheck
			uu = strings.TrimRight(uu, "\n")
			ifc, _ = script.File(nodeInfoDotJSON).JQ(`[.ip_addr[]? | select(.ifname != "lo") | {address: .address, ifname: .ifname}]`).Replace(" ", "").Replace(`"`, "").String() //nolint:errcheck
			ifc = strings.TrimRight(ifc, "\n")
			ifc1, _ = script.File(nodeInfoDotJSON).JQ(`[.zcalusic_sysinfo.network[] | {address: .macaddress, ifname: .name}]`).Replace(" ", "").Replace(`"`, "").String() //nolint:errcheck
			ifc1 = strings.TrimRight(ifc1, "\n")
			macs, _ = script.File(nodeInfoDotJSON).JQ(`.ip_addr[]? | select(.ifname != "lo") | .address`).Replace(" ", "").Replace(`"`, "").Slice() //nolint:errcheck
			macs1, _ = script.File(nodeInfoDotJSON).JQ(`.zcalusic_sysinfo.network[] | .macaddress`).Replace(" ", "").Replace(`"`, "").Slice()       //nolint:errcheck
			if ifc == "[]" && ifc1 != "[]" {
				ifc = ifc1
			}
			if len(macs) == 0 && len(macs1) > 0 {
				macs = macs1
			} else {
				macs = append(macs, "")
			}
			_, allowed1 := allowArchMap1[arch]
			_, allowed2 := allowArchMap2[arch]
			_, _, err := rewardconfig.ValidateRewardAddress(sky)

			// Check transport requirement
			_, hasTransports := transportMap[pk]
			meetsTransportReq := !requireTransports || hasTransports

			visorBW := bandwidthMap[pk]

			// Determine architecture eligibility
			var archAllowed bool
			if requireBandwidth {
				_, archAllowed = allowArchMapAll[arch]
			} else {
				archAllowed = allowed1 || allowed2
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
				HV:         hv,
				Bandwidth:  visorBW,
				Reason: func() string {
					switch {
					case !archAllowed:
						return arch
					case strings.Count(ip, ".") != 3:
						return ip
					case uu == "":
						return ip
					case ifc == "":
						return ifc
					case len(macs) == 0 || macs[0] == "":
						return macs[0]
					case hv != "null":
						return hv
					case err != nil:
						return "Invalid Skycoin address"
					case !meetsTransportReq:
						return "No transports"
					default:
						return "Unknown reason"
					}
				}(),
			}

			baseEligible := archAllowed && strings.Count(ip, ".") == 3 && uu != "" && ifc != "" && len(macs) > 0 && macs[0] != "" && hv == "null" && err == nil && meetsTransportReq

			if baseEligible {
				if requireBandwidth {
					// Bandwidth mode: all eligible visors go into the single presence pool
					nodesInfos1 = append(nodesInfos1, ni)
				} else {
					// Legacy mode: split by architecture
					if allowed1 {
						nodesInfos1 = append(nodesInfos1, ni)
					}
					if allowed2 {
						nodesInfos2 = append(nodesInfos2, ni)
					}
				}
			} else {
				if grr {
					grrInfos = append(grrInfos, ni)
				}
			}
		}
		if grr {
			for _, ni := range grrInfos {
				fmt.Printf("%s, %s, %s, %.6f, %.6f, %s, %s, %s, %s \n", ni.SkyAddr, ni.PK, ni.Reason, ni.Share, ni.Reward, ni.IPAddr, ni.Arch, ni.UUID, ni.Interfaces)
			}
			return
		}
		daysThisMonth := time.Date(wDate.Year(), wDate.Month()+1, 0, 0, 0, 0, 0, time.UTC).Day()
		daysThisYear := int(time.Date(wDate.Year(), 12, 31, 23, 59, 59, 999999999, time.UTC).Sub(time.Date(wDate.Year(), 1, 1, 0, 0, 0, 0, time.UTC)).Hours()) / 24
		monthReward := (float64(yearlyTotal) / float64(daysThisYear)) * float64(daysThisMonth)
		dayReward := monthReward / float64(daysThisMonth)
		wdate = strings.ReplaceAll(wdate, " ", "0")

		// Compute IP/MAC dedup counts across all eligible visors
		allEligible := append(nodesInfos1, nodesInfos2...)
		uniqueIP, _ := script.Echo(func() string { //nolint:errcheck
			var inputStr strings.Builder
			for _, ni := range allEligible {
				inputStr.WriteString(fmt.Sprintf("%s\n", ni.IPAddr))
			}
			return inputStr.String()
		}()).Freq().Slice()
		var ipCounts []counting
		for _, line := range uniqueIP {
			if line != "" {
				fields := strings.Fields(line)
				if len(fields) == 2 {
					//nolint:errcheck
					count, _ := strconv.Atoi(fields[0])
					ipCounts = append(ipCounts, counting{
						Name:  fields[1],
						Count: count,
					})
				}
			}
		}
		uniqueUUID, _ := script.Echo(func() string { //nolint:errcheck
			var inputStr strings.Builder
			for _, ni := range allEligible {
				inputStr.WriteString(fmt.Sprintf("%s\n", ni.UUID))
			}
			return inputStr.String()
		}()).Freq().Slice()

		// look at the first non loopback interface macaddress
		uniqueMac, _ := script.Echo(func() string { //nolint:errcheck
			var inputStr strings.Builder
			for _, ni := range allEligible {
				inputStr.WriteString(fmt.Sprintf("%s\n", ni.MacAddr))
			}
			return inputStr.String()
		}()).Freq().Slice()

		var macCounts []counting
		for _, line := range uniqueMac {
			if line != "" {
				fields := strings.Fields(line)
				if len(fields) == 2 {
					//nolint:errcheck
					count, _ := strconv.Atoi(fields[0])
					macCounts = append(macCounts, counting{
						Name:  fields[1],
						Count: count,
					})

				}
			}
		}

		// calcPresenceShare computes equal share with IP/MAC dedup (base = 1.0)
		calcPresenceShare := func(ni nodeinfo) float64 {
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
			return share
		}

		if requireBandwidth {
			// ==================== NEW TWO-POOL MODEL ====================
			// Pool 1: Presence — all archs combined, equal shares with IP/MAC dedup
			// Pool 2: Bandwidth — pure proportional to bandwidth bytes
			// Both pools are the same yearly total (408K/yr each)

			// --- Pool 1: Presence ---
			totalPresenceShares := 0.0
			for _, ni := range nodesInfos1 {
				totalPresenceShares += calcPresenceShare(ni)
			}

			for i, ni := range nodesInfos1 {
				nodesInfos1[i].Share = calcPresenceShare(ni)
				if totalPresenceShares > 0 {
					nodesInfos1[i].Reward = nodesInfos1[i].Share * dayReward / totalPresenceShares
				}
			}

			// --- Pool 2: Bandwidth (pure proportional) ---
			// Only visors with bandwidth >= threshold qualify for the bandwidth pool
			var bwPoolNodes []int // indices into nodesInfos1
			var totalBWShares float64
			for i, ni := range nodesInfos1 {
				if ni.Bandwidth >= minBWThreshold {
					bwPoolNodes = append(bwPoolNodes, i)
					totalBWShares += float64(ni.Bandwidth)
				}
			}

			for _, idx := range bwPoolNodes {
				bwShare := float64(nodesInfos1[idx].Bandwidth)
				if totalBWShares > 0 {
					nodesInfos1[idx].Reward += bwShare * dayReward / totalBWShares
				}
			}

			// --- Output ---
			if !h0 {
				fmt.Printf("date: %s\n", wdate)
				fmt.Printf("days this month: %d\n", daysThisMonth)
				fmt.Printf("days in the year: %d\n", daysThisYear)
				fmt.Printf("this month's rewards: %.6f\n", monthReward)
				fmt.Printf("reward per pool: %.6f\n", dayReward)
				fmt.Printf("reward mode: presence + bandwidth\n")
				fmt.Printf("\n--- Presence Pool (equal shares, IP/MAC dedup) ---\n")
				fmt.Printf("qualifying visors: %d\n", len(nodesInfos1))
				fmt.Printf("total presence shares: %.6f\n", totalPresenceShares)
				if totalPresenceShares > 0 {
					fmt.Printf("Skycoin Per Share (Pool 1): %.6f\n", dayReward/totalPresenceShares)
				}
				fmt.Printf("\n--- Bandwidth Pool (proportional to bytes) ---\n")
				fmt.Printf("minimum bandwidth threshold: %d bytes\n", minBWThreshold)
				fmt.Printf("qualifying visors: %d\n", len(bwPoolNodes))
				var totalBW uint64
				for _, idx := range bwPoolNodes {
					totalBW += nodesInfos1[idx].Bandwidth
				}
				fmt.Printf("total network bandwidth: %s\n", formatBytes(totalBW))
				if totalBWShares > 0 {
					fmt.Printf("Skycoin Per GB (Pool 2): %.6f\n", dayReward/totalBWShares*1024*1024*1024)
				}
				fmt.Printf("\nUnique mac addresses: %d\n", len(uniqueMac))
				fmt.Printf("Unique IP Addresses: %d\n", len(uniqueIP))
				fmt.Printf("Unique UUIDs: %d\n", len(uniqueUUID))
			}

			if !h1 {
				fmt.Println("Skycoin Address, Skywire Public Key, Presence Share, Bandwidth (bytes), Total Reward SKY, IP, Architecture, UUID, Interfaces")
				for _, ni := range nodesInfos1 {
					fmt.Printf("%s, %s, %.6f, %d, %.6f, %s, %s, %s, %s \n", ni.SkyAddr, ni.PK, ni.Share, ni.Bandwidth, ni.Reward, ni.IPAddr, ni.Arch, ni.UUID, ni.Interfaces)
				}
			}

			// Calculate reward sum by Skycoin Address
			rewardSumBySkyAddr := make(map[string]float64)
			for _, ni := range nodesInfos1 {
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
				fmt.Printf("\nTotal Reward Amount (both pools): %.6f\n", func() (tr float64) {
					for _, skyAddrReward := range sortedSkyAddrs {
						tr += skyAddrReward.Reward
					}
					return tr
				}())
			}
			if !h2 {
				fmt.Println("Skycoin Address, Reward Amount")
				for _, skyAddrReward := range sortedSkyAddrs {
					resolved := resolveRewardAddress(skyAddrReward.SkyAddr)
					fmt.Printf("%s, %.6f\n", resolved, skyAddrReward.Reward)
				}
			}
		} else {
			// ==================== LEGACY TWO-ARCH-POOL MODEL ====================
			totalValidShares1 := 0.0
			totalValidShares2 := 0.0

			for _, ni := range nodesInfos1 {
				totalValidShares1 += calcPresenceShare(ni)
			}
			for _, ni := range nodesInfos2 {
				totalValidShares2 += calcPresenceShare(ni)
			}

			if !h0 {
				fmt.Printf("date: %s\n", wdate)
				fmt.Printf("days this month: %d\n", daysThisMonth)
				fmt.Printf("days in the year: %d\n", daysThisYear)
				fmt.Printf("this month's rewards: %.6f\n", monthReward)
				fmt.Printf("reward total per pool: %.6f\n", dayReward)
				fmt.Printf("Visors meeting uptime & other requirements (Pool 1): %d\n", len(nodesInfos1))
				fmt.Printf("Visors meeting uptime & other requirements (Pool 2): %d\n", len(nodesInfos2))
				fmt.Printf("Unique mac addresses for first interface after lo: %d\n", len(uniqueMac))
				fmt.Printf("Unique IP Addresses: %d\n", len(uniqueIP))
				fmt.Printf("Unique UUIDs: %d\n", len(uniqueUUID))
				fmt.Printf("Total valid shares (Pool 1): %.6f\n", totalValidShares1)
				fmt.Printf("Total valid shares (Pool 2): %.6f\n", totalValidShares2)
				if totalValidShares1 != 0 {
					fmt.Printf("Skycoin Per Share (Pool 1): %.6f\n", dayReward/totalValidShares1)
				} else {
					fmt.Printf("Skycoin Per Share (Pool 1): 0\n")
				}
				if totalValidShares2 != 0 {
					fmt.Printf("Skycoin Per Share (Pool 2): %.6f\n", dayReward/totalValidShares2)
				} else {
					fmt.Printf("Skycoin Per Share (Pool 2): 0\n")
				}
			}

			for i, ni := range nodesInfos1 {
				nodesInfos1[i].Share = calcPresenceShare(ni)
				nodesInfos1[i].Reward = nodesInfos1[i].Share * dayReward / float64(totalValidShares1)
			}
			for i, ni := range nodesInfos2 {
				nodesInfos2[i].Share = calcPresenceShare(ni)
				nodesInfos2[i].Reward = nodesInfos2[i].Share * dayReward / float64(totalValidShares2)
			}

			combinedNodesInfos := append(nodesInfos1, nodesInfos2...)

			if !h1 {
				fmt.Println("Skycoin Address, Skywire Public Key, Reward Shares, Reward SKY Amount, IP, Architecture, UUID, Interfaces")
				for _, ni := range combinedNodesInfos {
					fmt.Printf("%s, %s, %.6f, %.6f, %s, %s, %s, %s \n", ni.SkyAddr, ni.PK, ni.Share, ni.Reward, ni.IPAddr, ni.Arch, ni.UUID, ni.Interfaces)
				}
			}

			rewardSumBySkyAddr := make(map[string]float64)
			for _, ni := range combinedNodesInfos {
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
					resolved := resolveRewardAddress(skyAddrReward.SkyAddr)
					fmt.Printf("%s, %.6f\n", resolved, skyAddrReward.Reward)
				}
			}
		}
	},
}

// resolveRewardAddress resolves an xpub key to the next unused skycoin address
// using skycoin-cli. Regular addresses are returned as-is.
func resolveRewardAddress(addr string) string {
	if !strings.HasPrefix(addr, "xpub") {
		return addr
	}
	out, err := exec.Command("skycoin-cli", "nextAddress", addr).Output() //nolint:gosec
	if err != nil {
		log.Warnf("Failed to derive address from xpub %s...: %v", addr[:20], err)
		return addr // fall back to xpub string — won't be sendable but won't lose data
	}
	// nextAddress output format: "address: <addr>\nchild_index: <n>\npublic_key: <pk>\n"
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "address: ") {
			resolved := strings.TrimPrefix(line, "address: ")
			log.Infof("Resolved xpub %s... → %s", addr[:20], resolved)
			return resolved
		}
	}
	log.Warnf("Could not parse nextAddress output for xpub %s...", addr[:20])
	return addr
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
	testCmd.Flags().StringVarP(&logLvl, "loglvl", "s", "info", "[ debug | warn | error | fatal | panic | trace ]")
	testCmd.Flags().StringVarP(&pubkey, "pk", "k", pubkey, "verify services in survey for pubkey")
	testCmd.Flags().StringVarP(&hwSurveyPath, "lpath", "p", "log_collecting", "path to the surveys")
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

		//stun_servers does not currently match between conf.skywire.skycoin.com & https://github.com/skycoin/skywire/blob/develop/services-config.json ; omit checking them until next version
		nodeInfoSvc, err = script.File(fmt.Sprintf("%s/%s/node-info.json", hwSurveyPath, pubkey)).JQ(`.services | del(.stun_servers)`).Bytes()
		if err != nil {
			log.Fatal("error parsing json with jq:\n", err)
		}

		sConf, err := script.Echo(string(deployment.ServicesJSON)).JQ(`.prod  | del(.stun_servers)`).Bytes()
		if err != nil {
			log.Fatal("error parsing json with jq:\n", err)
		}
		dConf, err := script.Echo(string(deployment.DmsghttpJSON)).JQ(`.prod`).Bytes()
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
