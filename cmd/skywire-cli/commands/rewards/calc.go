package clirewards

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/bitfield/script"
	"github.com/fatih/color"
	"github.com/oschwald/geoip2-golang/v2"
	"github.com/spf13/cobra"
	"github.com/tidwall/pretty"

	geoipcmd "github.com/skycoin/skywire/cmd/geoip/commands"
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
	saturationExponent    float64
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
	Country    string  `json:"country"`           //country code from GeoIP lookup
}

type rewardData struct {
	SkyAddr string
	Reward  float64
	Shares  float64
}

// surveyData represents the relevant fields parsed from a node-info.json survey file.
type surveyData struct {
	IPAddr  string `json:"ip_address"`
	SkyAddr string `json:"skycoin_address"`
	Arch    string `json:"go_arch"`
	UUID    string `json:"uuid"`
	SysInfo struct {
		Node struct {
			Hypervisor string `json:"hypervisor"`
		} `json:"node"`
		Network []struct {
			Name       string `json:"name"`
			MacAddress string `json:"macaddress"`
		} `json:"network"`
	} `json:"zcalusic_sysinfo"`
	IPAddrs []struct {
		IfName  string `json:"ifname"`
		Address string `json:"address"`
	} `json:"ip_addr"`
	Services json.RawMessage `json:"services"`
}

// parseSurvey reads and parses a node-info.json file into structured data.
func parseSurvey(path string) (*surveyData, error) {
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return nil, err
	}
	var s surveyData
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// extractInterfaces returns the interface list string and MAC addresses from a survey.
func extractInterfaces(s *surveyData) (ifc string, macs []string) {
	// Try ip_addr first (non-loopback)
	type ifcEntry struct {
		Address string `json:"address"`
		IfName  string `json:"ifname"`
	}
	var entries []ifcEntry
	for _, a := range s.IPAddrs {
		if a.IfName != "lo" {
			entries = append(entries, ifcEntry{Address: a.Address, IfName: a.IfName})
			macs = append(macs, a.Address)
		}
	}

	// Fall back to zcalusic sysinfo network
	if len(entries) == 0 {
		for _, n := range s.SysInfo.Network {
			entries = append(entries, ifcEntry{Address: n.MacAddress, IfName: n.Name})
			macs = append(macs, n.MacAddress)
		}
	}

	if len(entries) > 0 {
		b, _ := json.Marshal(entries) //nolint:errcheck
		ifc = string(b)
	}
	if len(macs) == 0 {
		macs = append(macs, "")
	}
	return ifc, macs
}

// checkServiceConfig verifies the survey's service configuration against the expected deployment config.
// Accepts three valid config types:
//   - HTTP-only: dmsg_discovery is http://, no _dmsg fields
//   - DMSG-only: dmsg_discovery is dmsg://, all URLs are dmsg://
//   - Dual (HTTP+DMSG): dmsg_discovery is http://, _dmsg fields also present
//
// Each present field is checked against the expected deployment config.
// Missing optional fields (_dmsg URLs) are not penalized.
func checkServiceConfig(surveyPath string, sConf, dConf []byte) bool {
	// Strip fields not relevant to comparison
	delFields := `del(.stun_servers, .dmsg_servers, .conf_dmsg, .reward_system, .reward_system_dmsg)`
	svcBytes, err := script.File(surveyPath).JQ(`.services | ` + delFields).Bytes()
	if err != nil {
		return false
	}

	confType, _ := script.File(surveyPath).JQ(`.services.dmsg_discovery`).Replace("\"", "").String() //nolint:errcheck
	confType = strings.TrimRight(confType, "\n")

	if strings.HasPrefix(confType, "dmsg://") {
		// DMSG-only config: compare against dConf
		stripped, err := script.Echo(string(dConf)).JQ(delFields).Bytes()
		if err != nil {
			stripped = dConf
		}
		return compareAndPrintDiffs(svcBytes, stripped, true)
	}

	if strings.HasPrefix(confType, "http://") {
		// HTTP or dual config: strip _dmsg and dmsg_servers fields from BOTH sides
		// so that HTTP-only, dual, and expected configs all compare cleanly.
		// The _dmsg fields are validated separately if present.
		httpOnly := `del(.stun_servers, .dmsg_servers, .conf_dmsg, .dmsg_discovery_dmsg, .transport_discovery_dmsg, .address_resolver_dmsg, .route_finder_dmsg, .uptime_tracker_dmsg, .service_discovery_dmsg)`
		surveyHTTP, err := script.File(surveyPath).JQ(`.services | ` + httpOnly).Bytes()
		if err != nil {
			return false
		}
		expectedHTTP, err := script.Echo(string(sConf)).JQ(httpOnly).Bytes()
		if err != nil {
			return false
		}
		httpOK := compareAndPrintDiffs(surveyHTTP, expectedHTTP, true)

		// If survey has _dmsg fields, validate them too (optional — not required for rewards)
		hasDmsgFields, _ := script.File(surveyPath).JQ(`.services.dmsg_discovery_dmsg`).Replace("\"", "").String() //nolint:errcheck
		if strings.TrimSpace(hasDmsgFields) != "" && strings.TrimSpace(hasDmsgFields) != "null" {
			dmsgOnly := `{dmsg_discovery_dmsg, transport_discovery_dmsg, address_resolver_dmsg, route_finder_dmsg, uptime_tracker_dmsg, service_discovery_dmsg}`
			surveyDmsg, _ := script.File(surveyPath).JQ(`.services | ` + dmsgOnly).Bytes() //nolint:errcheck
			expectedDmsg, _ := script.Echo(string(sConf)).JQ(dmsgOnly).Bytes()             //nolint:errcheck
			if len(surveyDmsg) > 0 && len(expectedDmsg) > 0 {
				compareAndPrintDiffs(surveyDmsg, expectedDmsg, false) // log but don't fail on _dmsg mismatch
			}
		}

		return httpOK
	}

	return false
}

// countFrequency counts occurrences of each string in a slice, returning a map.
func countFrequency(values []string) map[string]int {
	counts := make(map[string]int)
	for _, v := range values {
		if v != "" {
			counts[v]++
		}
	}
	return counts
}

// calcPresenceShare computes a visor's share after IP cap and MAC dedup.
func calcPresenceShare(ni nodeinfo, ipCounts, macCounts map[string]int) float64 {
	share := 1.0
	if count := ipCounts[ni.IPAddr]; count >= 8 {
		share = 8.0 / float64(count)
	}
	if count := macCounts[ni.MacAddr]; count > 1 {
		share /= float64(count)
	}
	return share
}

// applyRegionalSaturation applies diminishing returns scaling based on the
// number of unique IP addresses per country. Each country's weight is
// unique_ips^exponent (default sqrt). This scales each visor's share so that
// the total pool allocation is redistributed to favor geographic diversity.
func applyRegionalSaturation(nodes []nodeinfo, exponent float64) {
	if exponent >= 1.0 || len(nodes) == 0 {
		return
	}

	countryIPs := make(map[string]map[string]struct{})
	countryShares := make(map[string]float64)
	for _, ni := range nodes {
		c := ni.Country
		if c == "" {
			c = "XX"
		}
		if countryIPs[c] == nil {
			countryIPs[c] = make(map[string]struct{})
		}
		countryIPs[c][ni.IPAddr] = struct{}{}
		countryShares[c] += ni.Share
	}

	totalWeight := 0.0
	countryWeight := make(map[string]float64)
	for c, ips := range countryIPs {
		w := math.Pow(float64(len(ips)), exponent)
		countryWeight[c] = w
		totalWeight += w
	}

	totalRawShares := 0.0
	for _, s := range countryShares {
		totalRawShares += s
	}
	if totalRawShares == 0 || totalWeight == 0 {
		return
	}

	countryScale := make(map[string]float64)
	for c, raw := range countryShares {
		if raw > 0 {
			countryScale[c] = (countryWeight[c] / totalWeight * totalRawShares) / raw
		}
	}

	for i := range nodes {
		c := nodes[i].Country
		if c == "" {
			c = "XX"
		}
		if scale, ok := countryScale[c]; ok {
			nodes[i].Share *= scale
		}
	}
}

// computePoolShares computes shares and rewards for a pool of nodes.
func computePoolShares(nodes []nodeinfo, ipCounts, macCounts map[string]int) {
	for i, ni := range nodes {
		nodes[i].Share = calcPresenceShare(ni, ipCounts, macCounts)
	}
	applyRegionalSaturation(nodes, saturationExponent)
}

// computePoolRewards assigns reward amounts based on shares and the daily pool reward.
func computePoolRewards(nodes []nodeinfo, dayReward float64) float64 {
	total := 0.0
	for _, ni := range nodes {
		total += ni.Share
	}
	if total > 0 {
		for i := range nodes {
			nodes[i].Reward = nodes[i].Share * dayReward / total
		}
	}
	return total
}

// sumRewardsByAddress aggregates rewards per skycoin address, sorted descending.
func sumRewardsByAddress(nodes []nodeinfo) []rewardData {
	sums := make(map[string]float64)
	for _, ni := range nodes {
		sums[ni.SkyAddr] += ni.Reward
	}
	var result []rewardData
	for addr, reward := range sums {
		result = append(result, rewardData{SkyAddr: addr, Reward: reward})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Reward > result[j].Reward })
	return result
}

// printSaturationStats prints per-country saturation statistics.
func printSaturationStats(nodes []nodeinfo, dayReward, totalShares float64) {
	if saturationExponent >= 1.0 {
		return
	}
	fmt.Printf("\n--- Regional Saturation Scaling ---\n")
	fmt.Printf("saturation exponent: %.2f\n", saturationExponent)

	type countryStat struct {
		code   string
		visors int
		ips    int
		shares float64
	}

	cIPs := make(map[string]map[string]struct{})
	cVisors := make(map[string]int)
	cShares := make(map[string]float64)
	for _, ni := range nodes {
		c := ni.Country
		if c == "" {
			c = "XX"
		}
		if cIPs[c] == nil {
			cIPs[c] = make(map[string]struct{})
		}
		cIPs[c][ni.IPAddr] = struct{}{}
		cVisors[c]++
		cShares[c] += ni.Share
	}

	var stats []countryStat
	for c := range cVisors {
		stats = append(stats, countryStat{c, cVisors[c], len(cIPs[c]), cShares[c]})
	}
	sort.Slice(stats, func(i, j int) bool { return stats[i].shares > stats[j].shares })

	fmt.Printf("%-4s %7s %5s %10s %10s\n", "CC", "Visors", "IPs", "Shares", "SKY/visor")
	for _, cs := range stats {
		sky := cs.shares * dayReward / totalShares
		fmt.Printf("%-4s %7d %5d %10.2f %10.2f\n", cs.code, cs.visors, cs.ips, cs.shares, sky/float64(cs.visors))
	}
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
	RootCmd.Flags().Float64VarP(&saturationExponent, "sat-exp", "S", 0.5, "regional saturation exponent (1.0=no derating, 0.5=sqrt, 0=all countries equal)")
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
		dConf, err := script.Echo(string(deployment.ServicesJSON)).JQ(`.prod`).Bytes()
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

		// Initialize GeoIP database for regional saturation scaling
		var geoDB *geoip2.Reader
		if saturationExponent < 1.0 {
			geoDB, err = geoip2.OpenBytes(geoipcmd.EmbeddedGeoIP())
			if err != nil {
				log.Warn("Failed to load embedded GeoIP database, disabling regional saturation: ", err)
				saturationExponent = 1.0
			} else {
				defer func() { _ = geoDB.Close() }() //nolint:errcheck
			}
		}

		_, err = os.Stat(utfile)
		if os.IsNotExist(err) {
			log.Fatal("uptime tracker data file not found\n", err, "\nfetch the uptime tracker data with:\n$ skywire-cli ut > ut.txt")
		}

		// Build architecture lookup maps
		disallowedMap := make(map[string]struct{})
		for _, arch := range disallowArchitectures {
			disallowedMap[arch] = struct{}{}
		}

		allowArchMap1 := make(map[string]struct{})
		allowArchMap2 := make(map[string]struct{})
		supportedArchitecturesMap := make(map[string]struct{})
		for _, arch := range rewards.Architectures {
			supportedArchitecturesMap[arch] = struct{}{}
		}
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
			for arch := range allowArchMap1 {
				if _, exists := allowArchMap2[arch]; exists {
					log.Fatal("Error: Architecture cannot be specified in both pools: " + arch)
				}
			}
		}
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

		allowArchMapAll := make(map[string]struct{})
		if requireBandwidth {
			for arch := range allowArchMap1 {
				allowArchMapAll[arch] = struct{}{}
			}
			for arch := range allowArchMap2 {
				allowArchMapAll[arch] = struct{}{}
			}
		}

		// Load transport requirement data
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

		// Load bandwidth data
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

		// Get public keys that met uptime requirement
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

		// Collect eligible nodes by parsing surveys
		var nodesInfos1 []nodeinfo // pool 1 (presence in bandwidth mode, or arch pool 1 in legacy)
		var nodesInfos2 []nodeinfo // pool 2 (unused in bandwidth mode, or arch pool 2 in legacy)
		var grrInfos []nodeinfo    // ineligible nodes (for error reporting)

		for _, pk := range res {
			surveyPath := fmt.Sprintf("%s/%s/node-info.json", hwSurveyPath, pk)
			survey, parseErr := parseSurvey(surveyPath)
			if parseErr != nil {
				log.Debug(parseErr.Error())
				continue
			}

			svcconf := checkServiceConfig(surveyPath, sConf, dConf)
			ifc, macs := extractInterfaces(survey)

			ip := survey.IPAddr
			sky := survey.SkyAddr
			arch := survey.Arch
			hv := survey.SysInfo.Node.Hypervisor
			if hv == "" {
				hv = "null"
			}
			uu := survey.UUID

			_, allowed1 := allowArchMap1[arch]
			_, allowed2 := allowArchMap2[arch]
			_, _, addrErr := rewardconfig.ValidateRewardAddress(sky)

			_, hasTransports := transportMap[pk]
			meetsTransportReq := !requireTransports || hasTransports
			visorBW := bandwidthMap[pk]

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
			}

			baseEligible := archAllowed && strings.Count(ip, ".") == 3 && uu != "" && ifc != "" && len(macs) > 0 && macs[0] != "" && hv == "null" && addrErr == nil && meetsTransportReq

			if !baseEligible {
				ni.Reason = func() string {
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
					case addrErr != nil:
						return "Invalid Skycoin address"
					case !meetsTransportReq:
						return "No transports"
					default:
						return "Unknown reason"
					}
				}()
				if grr {
					grrInfos = append(grrInfos, ni)
				}
				continue
			}

			// GeoIP country lookup
			if geoDB != nil {
				if geoRes, geoErr := geoipcmd.LookupIP(geoDB, ip); geoErr == nil && geoRes.CountryCode != "" {
					ni.Country = geoRes.CountryCode
				} else {
					ni.Country = "XX"
				}
			}

			if requireBandwidth {
				nodesInfos1 = append(nodesInfos1, ni)
			} else {
				if allowed1 {
					nodesInfos1 = append(nodesInfos1, ni)
				}
				if allowed2 {
					nodesInfos2 = append(nodesInfos2, ni)
				}
			}
		}

		if grr {
			for _, ni := range grrInfos {
				fmt.Printf("%s, %s, %s, %.6f, %.6f, %s, %s, %s, %s \n", ni.SkyAddr, ni.PK, ni.Reason, ni.Share, ni.Reward, ni.IPAddr, ni.Arch, ni.UUID, ni.Interfaces)
			}
			return
		}

		// Calculate daily reward amount
		daysThisMonth := time.Date(wDate.Year(), wDate.Month()+1, 0, 0, 0, 0, 0, time.UTC).Day()
		daysThisYear := int(time.Date(wDate.Year(), 12, 31, 23, 59, 59, 999999999, time.UTC).Sub(time.Date(wDate.Year(), 1, 1, 0, 0, 0, 0, time.UTC)).Hours()) / 24
		monthReward := (float64(yearlyTotal) / float64(daysThisYear)) * float64(daysThisMonth)
		dayReward := monthReward / float64(daysThisMonth)
		wdate = strings.ReplaceAll(wdate, " ", "0")

		// Build dedup counts from all eligible visors
		allEligible := append(nodesInfos1, nodesInfos2...)
		allIPs := make([]string, 0, len(allEligible))
		allMACs := make([]string, 0, len(allEligible))
		allUUIDs := make([]string, 0, len(allEligible))
		for _, ni := range allEligible {
			allIPs = append(allIPs, ni.IPAddr)
			allMACs = append(allMACs, ni.MacAddr)
			allUUIDs = append(allUUIDs, ni.UUID)
		}
		ipCounts := countFrequency(allIPs)
		macCounts := countFrequency(allMACs)
		uuidCounts := countFrequency(allUUIDs)

		if requireBandwidth {
			// ==================== TWO-POOL MODEL ====================
			// Pool 1: Presence — equal shares with IP/MAC dedup + regional saturation
			// Pool 2: Bandwidth — proportional to bandwidth bytes

			computePoolShares(nodesInfos1, ipCounts, macCounts)
			totalPresenceShares := computePoolRewards(nodesInfos1, dayReward)

			// Bandwidth pool: add proportional bandwidth reward on top
			var totalBWShares float64
			var bwPoolCount int
			for _, ni := range nodesInfos1 {
				if ni.Bandwidth >= minBWThreshold {
					totalBWShares += float64(ni.Bandwidth)
					bwPoolCount++
				}
			}
			if totalBWShares > 0 {
				for i := range nodesInfos1 {
					if nodesInfos1[i].Bandwidth >= minBWThreshold {
						nodesInfos1[i].Reward += float64(nodesInfos1[i].Bandwidth) * dayReward / totalBWShares
					}
				}
			}

			// Output stats
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
				fmt.Printf("qualifying visors: %d\n", bwPoolCount)
				var totalBW uint64
				for _, ni := range nodesInfos1 {
					if ni.Bandwidth >= minBWThreshold {
						totalBW += ni.Bandwidth
					}
				}
				fmt.Printf("total network bandwidth: %s\n", formatBytes(totalBW))
				if totalBWShares > 0 {
					fmt.Printf("Skycoin Per GB (Pool 2): %.6f\n", dayReward/totalBWShares*1024*1024*1024)
				}
				fmt.Printf("\nUnique mac addresses: %d\n", len(macCounts))
				fmt.Printf("Unique IP Addresses: %d\n", len(ipCounts))
				fmt.Printf("Unique UUIDs: %d\n", len(uuidCounts))
				printSaturationStats(nodesInfos1, dayReward, totalPresenceShares)
			}

			if !h1 {
				fmt.Println("Skycoin Address, Skywire Public Key, Presence Share, Bandwidth (bytes), Total Reward SKY, IP, Architecture, UUID, Interfaces, Country, XPub")
				for _, ni := range nodesInfos1 {
					resolved := resolveRewardAddress(ni.SkyAddr)
					xpub := ""
					if strings.HasPrefix(ni.SkyAddr, "xpub") {
						xpub = ni.SkyAddr
					}
					fmt.Printf("%s, %s, %.6f, %d, %.6f, %s, %s, %s, %s, %s, %s \n", resolved, ni.PK, ni.Share, ni.Bandwidth, ni.Reward, ni.IPAddr, ni.Arch, ni.UUID, ni.Interfaces, ni.Country, xpub)
				}
			}

			sortedAddrs := sumRewardsByAddress(nodesInfos1)
			if !h0 {
				total := 0.0
				for _, a := range sortedAddrs {
					total += a.Reward
				}
				fmt.Printf("\nTotal Reward Amount (both pools): %.6f\n", total)
			}
			if !h2 {
				fmt.Println("Skycoin Address, Reward Amount")
				for _, a := range sortedAddrs {
					fmt.Printf("%s, %.6f\n", resolveRewardAddress(a.SkyAddr), a.Reward)
				}
			}
		} else {
			// ==================== LEGACY TWO-ARCH-POOL MODEL ====================
			computePoolShares(nodesInfos1, ipCounts, macCounts)
			computePoolShares(nodesInfos2, ipCounts, macCounts)
			totalShares1 := computePoolRewards(nodesInfos1, dayReward)
			totalShares2 := computePoolRewards(nodesInfos2, dayReward)

			if !h0 {
				fmt.Printf("date: %s\n", wdate)
				fmt.Printf("days this month: %d\n", daysThisMonth)
				fmt.Printf("days in the year: %d\n", daysThisYear)
				fmt.Printf("this month's rewards: %.6f\n", monthReward)
				fmt.Printf("reward total per pool: %.6f\n", dayReward)
				fmt.Printf("Visors meeting uptime & other requirements (Pool 1): %d\n", len(nodesInfos1))
				fmt.Printf("Visors meeting uptime & other requirements (Pool 2): %d\n", len(nodesInfos2))
				fmt.Printf("Unique mac addresses for first interface after lo: %d\n", len(macCounts))
				fmt.Printf("Unique IP Addresses: %d\n", len(ipCounts))
				fmt.Printf("Unique UUIDs: %d\n", len(uuidCounts))
				if saturationExponent < 1.0 {
					fmt.Printf("Regional saturation exponent: %.2f\n", saturationExponent)
				}
				fmt.Printf("Total valid shares (Pool 1): %.6f\n", totalShares1)
				fmt.Printf("Total valid shares (Pool 2): %.6f\n", totalShares2)
				if totalShares1 != 0 {
					fmt.Printf("Skycoin Per Share (Pool 1): %.6f\n", dayReward/totalShares1)
				} else {
					fmt.Printf("Skycoin Per Share (Pool 1): 0\n")
				}
				if totalShares2 != 0 {
					fmt.Printf("Skycoin Per Share (Pool 2): %.6f\n", dayReward/totalShares2)
				} else {
					fmt.Printf("Skycoin Per Share (Pool 2): 0\n")
				}
			}

			combinedNodes := append(nodesInfos1, nodesInfos2...)
			if !h1 {
				fmt.Println("Skycoin Address, Skywire Public Key, Reward Shares, Reward SKY Amount, IP, Architecture, UUID, Interfaces, Country, XPub")
				for _, ni := range combinedNodes {
					resolved := resolveRewardAddress(ni.SkyAddr)
					xpub := ""
					if strings.HasPrefix(ni.SkyAddr, "xpub") {
						xpub = ni.SkyAddr
					}
					fmt.Printf("%s, %s, %.6f, %.6f, %s, %s, %s, %s, %s, %s \n", resolved, ni.PK, ni.Share, ni.Reward, ni.IPAddr, ni.Arch, ni.UUID, ni.Interfaces, ni.Country, xpub)
				}
			}

			sortedAddrs := sumRewardsByAddress(combinedNodes)
			if !h0 {
				total := 0.0
				for _, a := range sortedAddrs {
					total += a.Reward
				}
				fmt.Printf("Total Reward Amount: %.6f\n", total)
			}
			if !h2 {
				fmt.Println("Skycoin Address, Reward Amount")
				for _, a := range sortedAddrs {
					fmt.Printf("%s, %.6f\n", resolveRewardAddress(a.SkyAddr), a.Reward)
				}
			}
		}

	},
}

// countUndistributedDays counts consecutive days before wdate (inclusive) that
// lack a transaction marker file (hist/{date}.txt). This tells us how many
// reward calculations are pending distribution, so we can offset the address
// index to avoid reuse.
func countUndistributedDays(forDate string) int {
	d, err := time.Parse("2006-01-02", forDate)
	if err != nil {
		return 0
	}
	count := 0
	for i := 0; i < 365; i++ { // look back up to a year
		checkDate := d.AddDate(0, 0, -i)
		markerFile := fmt.Sprintf("hist/%s.txt", checkDate.Format("2006-01-02"))
		if _, err := os.Stat(markerFile); err == nil {
			break // found a distributed day — stop counting
		}
		count++
	}
	return count
}

// resolveRewardAddress resolves an xpub key to the next unused external chain
// address using BIP44 derivation. The address index is offset by the number of
// undistributed reward days to prevent address reuse when distribution is delayed.
// Regular addresses are returned as-is.
// The xpub key should NEVER appear in public output — it is treated as private.
func resolveRewardAddress(addr string) string {
	if !strings.HasPrefix(addr, "xpub") {
		return addr
	}

	// Index 0 = first empty address for this xpub.
	// Offset by the number of consecutive undistributed days before the
	// current calculation date, so each pending day gets a distinct address.
	offset := countUndistributedDays(wdate)
	if offset > 0 {
		offset-- // current day is index 0; prior undistributed days offset from there
	}
	index := uint32(offset) //nolint:gosec

	resolved, err := rewardconfig.DeriveExternalAddressFromXpub(addr, index)
	if err != nil {
		log.Warnf("Failed to derive address from xpub %s... at index %d: %v", addr[:20], index, err)
		// NEVER fall back to the raw xpub — it must stay private
		return "INVALID_XPUB_DERIVATION"
	}

	log.Infof("Resolved xpub %s... → %s (index %d, undistributed offset %d)", addr[:20], resolved, index, offset)
	return resolved
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
		dConf, err := script.Echo(string(deployment.ServicesJSON)).JQ(`.prod`).Bytes()
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
