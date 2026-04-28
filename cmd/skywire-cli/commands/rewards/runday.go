// Package clirewards cmd/skywire-cli/commands/rewards/runday.go
//
// runday.go provides the in-process orchestration of the full hourly reward
// cycle, replacing the bash pipeline that was previously emitted by
// `skywire cli rewards script reward | bash`. It performs:
//
//  1. Survey collection via the existing cli log Run function (in-process)
//  2. Transport count collection via tp-collect (in-process)
//  3. Bandwidth collection via bw-collect (in-process)
//  4. UT fetch, filtered to a single day's slice
//  5. Reward calculation (calcDay) — pure, returns a dayResult
//  6. Writes the four output files (ineligible, shares, rewardtxn0, stats)
//     directly from the dayResult — no second calculation pass.
package clirewards

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/bitfield/script"
	"github.com/oschwald/geoip2-golang/v2"
	"github.com/spf13/cobra"

	clilog "github.com/skycoin/skywire/cmd/skywire-cli/commands/log"
	geoipcmd "github.com/skycoin/skywire/cmd/svc/geoip/commands"
	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/visor/rewardconfig"
	"github.com/skycoin/skywire/rewards"
)

// runDate, runHistDir, runHwSurveyPath are bound to the `cli rewards run` flags.
var (
	runDate         string
	runHistDir      string
	runHwSurveyPath string
	runMinVersion   string
	runSkipCollect  bool
)

func init() {
	RootCmd.AddCommand(runCmd)
	runCmd.Flags().SortFlags = false
	runCmd.Flags().StringVarP(&runDate, "date", "d", "", "date to calculate (YYYY-MM-DD); default = yesterday UTC")
	runCmd.Flags().StringVarP(&runHistDir, "hist", "H", "hist", "history output directory")
	runCmd.Flags().StringVarP(&runHwSurveyPath, "lpath", "p", "log_backups", "path to collected surveys")
	runCmd.Flags().StringVar(&runMinVersion, "minv", "", "minimum skywire version to fetch from / keep surveys for")
	runCmd.Flags().BoolVar(&runSkipCollect, "skip-collect", false, "skip survey/tp/bw collection (use existing data on disk)")
}

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "run the full hourly reward cycle in-process (no bash)",
	Long: `Performs the entire hourly reward cycle:

  1. Collect surveys (skywire cli log --survey --cleanup)
  2. Collect transport counts (skywire cli rewards tp-collect)
  3. Collect bandwidth data (skywire cli rewards bw-collect)
  4. Fetch uptime tracker data, prune to single day
  5. Calculate rewards
  6. Write hist/{date}_{ut.txt,ut.json,ineligible.csv,shares.csv,rewardtxn0.csv,stats.txt}

Designed to be invoked directly by systemd:

  ExecStart=/usr/bin/skywire cli rewards run

Replaces the legacy 'script reward | bash' pipeline.`,
	Run: func(_ *cobra.Command, _ []string) {
		if log == nil {
			log = logging.MustGetLogger("rewards")
		}

		date := runDate
		if date == "" {
			date = time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
		}
		if _, err := time.Parse("2006-01-02", date); err != nil {
			log.Fatal("Invalid --date: ", err)
		}

		// Bail if rewards already broadcast for this date.
		if _, err := os.Stat(filepath.Join(runHistDir, date+".txt")); err == nil {
			log.Infof("Transaction already broadcasted for %s; nothing to do", date)
			return
		}

		if err := os.MkdirAll(runHistDir, 0750); err != nil {
			log.Fatal("Failed to create hist dir: ", err)
		}

		if !runSkipCollect {
			collectAll(date, runMinVersion)
		}

		// Fetch & prune UT for this date.
		if err := fetchAndSaveUT(date, runHistDir); err != nil {
			log.Fatal("UT fetch failed: ", err)
		}

		opts := calcOpts{
			Date:           date,
			HistDir:        runHistDir,
			HwSurveyPath:   runHwSurveyPath,
			YearlyTotal:    yearlyTotalRewardsPerPool,
			SaturationExp:  0.5,
			MinBWThreshold: defaultMinBandwidth,
		}
		result, err := calcDay(opts)
		if err != nil {
			log.Fatal("Reward calculation failed: ", err)
		}

		if err := writeAllOutputs(result, runHistDir); err != nil {
			log.Fatal("Failed to write outputs: ", err)
		}

		log.Infof("Reward cycle complete for %s", date)
	},
}

// collectAll runs the three in-process collection steps. Failures are logged
// but do not abort the cycle — the calculation can still proceed with whatever
// data is already on disk (matching the previous bash pipeline's behavior).
func collectAll(date, minVer string) {
	log.Info("Collecting surveys (cli log --survey --cleanup)")
	if err := runLogCollection(minVer); err != nil {
		log.WithError(err).Warn("survey collection step reported errors; continuing")
	}

	log.Info("Collecting transport counts (tp-collect)")
	tpCollectCmd.Run(tpCollectCmd, nil)

	log.Info("Collecting bandwidth (bw-collect)")
	bwCollectCmd.Run(bwCollectCmd, nil)

	_ = date // reserved for future date-scoped collection
}

// runLogCollection invokes the cli log subcommand in-process, configured for
// surveys-only collection and post-cleanup with min-version pruning.
func runLogCollection(minVer string) error {
	// Reach into clilog's exported root cmd — set its flag values rather
	// than re-implementing the collection loop.
	cmd := clilog.RootCmd
	flags := cmd.Flags()

	if err := flags.Set("survey", "true"); err != nil {
		return fmt.Errorf("set --survey: %w", err)
	}
	if err := flags.Set("cleanup", "true"); err != nil {
		return fmt.Errorf("set --cleanup: %w", err)
	}
	if minVer != "" {
		if err := flags.Set("minv", minVer); err != nil {
			return fmt.Errorf("set --minv: %w", err)
		}
		if err := flags.Set("prune-below-version", minVer); err != nil {
			return fmt.Errorf("set --prune-below-version: %w", err)
		}
	}
	cmd.Run(cmd, nil)
	return nil
}

// fetchAndSaveUT downloads the uptime tracker response, prunes each visor's
// .daily map to only the requested date, and writes the pruned views to
// hist/{date}_ut.json and hist/{date}_ut.txt. This replaces the previous
// `skywire cli ut | tee ...` (which kept all 7 days) plus the jq prune step.
func fetchAndSaveUT(date, histDir string) error {
	dep := getDeploymentForUT()
	url := strings.TrimRight(dep.UptimeTracker, "/") + "/uptimes?v=v2"

	client := &http.Client{Timeout: 60 * time.Second}
	req, err := http.NewRequest(http.MethodGet, url, nil) //nolint:noctx
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("uptime tracker returned %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	type utEntry struct {
		PK      string            `json:"pk"`
		On      bool              `json:"on"`
		Version string            `json:"version,omitempty"`
		Daily   map[string]string `json:"daily,omitempty"`
	}
	var entries []utEntry
	if err := json.Unmarshal(body, &entries); err != nil {
		return fmt.Errorf("parse UT JSON: %w", err)
	}

	// Prune each visor's daily map to the single requested date and write
	// the JSON cache. The text file is built from the same pruned data.
	pruned := make([]utEntry, 0, len(entries))
	var txtBuf strings.Builder
	for _, e := range entries {
		v, ok := e.Daily[date]
		single := map[string]string{date: ""}
		if ok {
			single[date] = v
			// Match the legacy "<pk> <date> <pct>" textual format.
			fmt.Fprintf(&txtBuf, "%s %s %s\n", e.PK, date, v)
		}
		e.Daily = single
		pruned = append(pruned, e)
	}

	jsonOut, err := json.Marshal(pruned)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(histDir, date+"_ut.json"), jsonOut, 0600); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(histDir, date+"_ut.txt"), []byte(txtBuf.String()), 0600); err != nil {
		return err
	}
	return nil
}

// getDeploymentForUT picks prod by default; honors SKYWIRETEST=1 the same way
// cli ut does, so test environments flow through correctly.
func getDeploymentForUT() deployment.Services {
	if os.Getenv("SKYWIRETEST") == "1" {
		return deployment.Test
	}
	return deployment.Prod
}

// calcOpts is the input to calcDay. All paths are relative to the caller's
// working directory.
type calcOpts struct {
	Date           string
	HistDir        string
	HwSurveyPath   string
	YearlyTotal    int
	SaturationExp  float64
	MinBWThreshold uint64
}

// dayResult captures everything the four output files need to render. All
// expensive work (parsing surveys, counting freq, applying saturation) is
// done once during calcDay and never repeated.
type dayResult struct {
	Date          string
	WDate         time.Time
	DaysThisMonth int
	DaysThisYear  int
	MonthReward   float64
	DayReward     float64

	// Pool 1 (presence) and Pool 2 (legacy second arch pool / unused in BW mode)
	NodesInfos1 []nodeinfo
	NodesInfos2 []nodeinfo
	Ineligible  []nodeinfo

	IPCounts   map[string]int
	MacCounts  map[string]int
	UUIDCounts map[string]int

	TotalShares1   float64
	TotalShares2   float64
	SaturationExp  float64
	MinBWThreshold uint64
}

// calcDay performs the per-day reward calculation as a pure-ish function:
// it reads from disk (UT data, surveys, transport/bandwidth files) but does
// not write any output. The returned dayResult is enough to render all four
// output files via writeAllOutputs.
func calcDay(opts calcOpts) (*dayResult, error) {
	wDate, err := time.Parse("2006-01-02", opts.Date)
	if err != nil {
		return nil, fmt.Errorf("parse date: %w", err)
	}

	if _, err := os.Stat(opts.HwSurveyPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("survey path does not exist: %s", opts.HwSurveyPath)
	}

	utfilePath := filepath.Join(opts.HistDir, opts.Date+"_ut.txt")
	if _, err := os.Stat(utfilePath); os.IsNotExist(err) {
		return nil, fmt.Errorf("ut file not found: %s", utfilePath)
	}

	// Service-config JSON used by checkServiceConfig.
	sConf, err := script.Echo(string(deployment.ServicesJSON)).JQ(`.prod | del(.stun_servers)`).Bytes()
	if err != nil {
		return nil, fmt.Errorf("parse services JSON: %w", err)
	}
	dConf, err := script.Echo(string(deployment.ServicesJSON)).JQ(`.prod`).Bytes()
	if err != nil {
		return nil, fmt.Errorf("parse services JSON (dmsg): %w", err)
	}

	// Architecture allow-lists. Mirror the defaults from RootCmd's flags so
	// the new entry point matches behavior of the legacy four-pass script.
	allowArchMap1 := buildArchMap(rewards.Architectures, []string{"wasm", "amd64", "386"})
	allowArchMap2 := buildArchMap(rewards.Architectures, []string{"wasm", "arm64", "arm", "ppc64", "riscv64", "loong64", "mips", "mips64", "mips64le", "mipsle", "ppc64le", "s390x"})

	// GeoIP for regional saturation.
	var geoDB *geoip2.Reader
	satExp := opts.SaturationExp
	if satExp < 1.0 {
		geoDB, err = geoip2.OpenBytes(geoipcmd.EmbeddedGeoIP())
		if err != nil {
			log.Warnf("Failed to load embedded GeoIP database, disabling regional saturation: %v", err)
			satExp = 1.0
		} else {
			defer func() { _ = geoDB.Close() }() //nolint:errcheck
		}
	}

	// Transport requirement.
	transportMap := make(map[string]struct{})
	tpFile := filepath.Join(opts.HistDir, opts.Date+"_transports.txt")
	if data, err := os.ReadFile(tpFile); err == nil { //nolint:gosec
		for _, line := range strings.Split(string(data), "\n") {
			if line = strings.TrimSpace(line); line != "" {
				transportMap[line] = struct{}{}
			}
		}
		log.Infof("Loaded %d visors with sufficient transports from %s", len(transportMap), tpFile)
	} else {
		log.Warnf("Transport file not found: %s (transport requirement will be skipped)", tpFile)
	}
	requireTPs := len(transportMap) > 0

	// Get PKs that met uptime on this date.
	res, _ := script.File(utfilePath).Match(strings.TrimRight(opts.Date, "\n")).Column(1).Slice() //nolint:errcheck
	if len(res) == 0 {
		return nil, fmt.Errorf("no keys achieved minimum uptime on %s", opts.Date)
	}

	var nodesInfos1, nodesInfos2, grrInfos []nodeinfo
	for _, pk := range res {
		surveyPath := fmt.Sprintf("%s/%s/node-info.json", opts.HwSurveyPath, pk)
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
		meetsTransportReq := !requireTPs || hasTransports

		archAllowed := allowed1 || allowed2

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
		}

		baseEligible := archAllowed && strings.Count(ip, ".") == 3 && uu != "" && ifc != "" && len(macs) > 0 && macs[0] != "" && hv == "null" && addrErr == nil && meetsTransportReq

		if !baseEligible {
			ni.Reason = ineligibilityReason(archAllowed, arch, ip, uu, ifc, macs, hv, addrErr, meetsTransportReq)
			grrInfos = append(grrInfos, ni)
			continue
		}

		if geoDB != nil {
			if geoRes, geoErr := geoipcmd.LookupIP(geoDB, ip); geoErr == nil && geoRes.CountryCode != "" {
				ni.Country = geoRes.CountryCode
			} else {
				ni.Country = "XX"
			}
		}

		if allowed1 {
			nodesInfos1 = append(nodesInfos1, ni)
		}
		if allowed2 {
			nodesInfos2 = append(nodesInfos2, ni)
		}
	}

	daysThisMonth := time.Date(wDate.Year(), wDate.Month()+1, 0, 0, 0, 0, 0, time.UTC).Day()
	daysThisYear := int(time.Date(wDate.Year(), 12, 31, 23, 59, 59, 999999999, time.UTC).Sub(time.Date(wDate.Year(), 1, 1, 0, 0, 0, 0, time.UTC)).Hours()) / 24
	monthReward := (float64(opts.YearlyTotal) / float64(daysThisYear)) * float64(daysThisMonth)
	dayReward := monthReward / float64(daysThisMonth)

	allEligible := append(append([]nodeinfo{}, nodesInfos1...), nodesInfos2...)
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

	// Compute shares + rewards for each pool. Set the package-level
	// saturationExponent so computePoolShares (which reads it via
	// applyRegionalSaturation) uses the configured value for this run.
	prevSat := saturationExponent
	saturationExponent = satExp
	defer func() { saturationExponent = prevSat }()

	computePoolShares(nodesInfos1, ipCounts, macCounts)
	computePoolShares(nodesInfos2, ipCounts, macCounts)
	totalShares1 := computePoolRewards(nodesInfos1, dayReward)
	totalShares2 := computePoolRewards(nodesInfos2, dayReward)

	return &dayResult{
		Date:           opts.Date,
		WDate:          wDate,
		DaysThisMonth:  daysThisMonth,
		DaysThisYear:   daysThisYear,
		MonthReward:    monthReward,
		DayReward:      dayReward,
		NodesInfos1:    nodesInfos1,
		NodesInfos2:    nodesInfos2,
		Ineligible:     grrInfos,
		IPCounts:       ipCounts,
		MacCounts:      macCounts,
		UUIDCounts:     uuidCounts,
		TotalShares1:   totalShares1,
		TotalShares2:   totalShares2,
		SaturationExp:  satExp,
		MinBWThreshold: opts.MinBWThreshold,
	}, nil
}

// ineligibilityReason returns a human-readable reason; matches the legacy
// switch logic but with descriptive strings instead of returning empty
// values when IP/UUID are missing.
func ineligibilityReason(archAllowed bool, arch, ip, uu, ifc string, macs []string, hv string, addrErr error, meetsTP bool) string {
	switch {
	case !archAllowed:
		return arch
	case strings.Count(ip, ".") != 3:
		return "No IP"
	case uu == "":
		return "No UUID"
	case ifc == "":
		return "No interfaces"
	case len(macs) == 0 || macs[0] == "":
		return "No MAC"
	case hv != "null":
		return hv
	case addrErr != nil:
		return "Invalid Skycoin address"
	case !meetsTP:
		return "No transports"
	default:
		return "Unknown reason"
	}
}

// buildArchMap returns the set of supported architectures minus the
// excluded ones; used for the per-pool allow lists.
func buildArchMap(all, exclude []string) map[string]struct{} {
	excl := make(map[string]struct{}, len(exclude))
	for _, e := range exclude {
		excl[e] = struct{}{}
	}
	out := make(map[string]struct{})
	for _, a := range all {
		if _, bad := excl[a]; !bad {
			out[a] = struct{}{}
		}
	}
	return out
}

// writeAllOutputs writes the four reward-cycle output files atomically
// (each via a temp+rename) so a partial write can't leave a half-built file.
func writeAllOutputs(r *dayResult, histDir string) error {
	type job struct {
		path string
		fn   func(io.Writer) error
	}
	jobs := []job{
		{filepath.Join(histDir, r.Date+"_ineligible.csv"), func(w io.Writer) error { return writeIneligibleCSV(w, r) }},
		{filepath.Join(histDir, r.Date+"_shares.csv"), func(w io.Writer) error { return writeSharesCSV(w, r) }},
		{filepath.Join(histDir, r.Date+"_rewardtxn0.csv"), func(w io.Writer) error { return writeRewardTxnCSV(w, r) }},
		{filepath.Join(histDir, r.Date+"_stats.txt"), func(w io.Writer) error { return writeStatsTxt(w, r) }},
	}
	for _, j := range jobs {
		if err := writeAtomic(j.path, j.fn); err != nil {
			return fmt.Errorf("%s: %w", j.path, err)
		}
		log.Infof("wrote %s", j.path)
	}
	return nil
}

func writeAtomic(path string, write func(io.Writer) error) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp.*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	// Best-effort cleanup; rename success leaves nothing to remove.
	defer func() { _ = os.Remove(tmpName) }() //nolint:errcheck
	if err := write(tmp); err != nil {
		_ = tmp.Close() //nolint:errcheck
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0600); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// writeIneligibleCSV mirrors the legacy `-e` (grr) output format used by the
// reward UI: one CSV row per ineligible visor with the inferred reason.
func writeIneligibleCSV(w io.Writer, r *dayResult) error {
	for _, ni := range r.Ineligible {
		if _, err := fmt.Fprintf(w, "%s, %s, %s, %.6f, %.6f, %s, %s, %s, %s \n",
			ni.SkyAddr, ni.PK, ni.Reason, ni.Share, ni.Reward,
			ni.IPAddr, ni.Arch, ni.UUID, ni.Interfaces); err != nil {
			return err
		}
	}
	return nil
}

// writeSharesCSV mirrors the legacy `-2 -0` output (suppress stats &
// rewardtxn) — header line plus one row per eligible visor.
func writeSharesCSV(w io.Writer, r *dayResult) error {
	if _, err := fmt.Fprintln(w, "Skycoin Address, Skywire Public Key, Reward Shares, Reward SKY Amount, IP, Architecture, UUID, Interfaces, Country, XPub"); err != nil {
		return err
	}
	combined := append(append([]nodeinfo{}, r.NodesInfos1...), r.NodesInfos2...)
	for _, ni := range combined {
		resolved := resolveRewardAddress(ni.SkyAddr)
		xpub := ""
		if strings.HasPrefix(ni.SkyAddr, "xpub") {
			xpub = ni.SkyAddr
		}
		if _, err := fmt.Fprintf(w, "%s, %s, %.6f, %.6f, %s, %s, %s, %s, %s, %s \n",
			resolved, ni.PK, ni.Share, ni.Reward, ni.IPAddr, ni.Arch,
			ni.UUID, ni.Interfaces, ni.Country, xpub); err != nil {
			return err
		}
	}
	return nil
}

// writeRewardTxnCSV mirrors the legacy `-1 -0` output (suppress stats &
// shares) — `Skycoin Address, Reward Amount` rows aggregated per address.
func writeRewardTxnCSV(w io.Writer, r *dayResult) error {
	combined := append(append([]nodeinfo{}, r.NodesInfos1...), r.NodesInfos2...)
	sums := make(map[string]float64)
	for _, ni := range combined {
		sums[ni.SkyAddr] += ni.Reward
	}
	type pair struct {
		addr string
		amt  float64
	}
	rows := make([]pair, 0, len(sums))
	for a, v := range sums {
		rows = append(rows, pair{a, v})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].amt > rows[j].amt })
	if _, err := fmt.Fprintln(w, "Skycoin Address, Reward Amount"); err != nil {
		return err
	}
	for _, p := range rows {
		if _, err := fmt.Fprintf(w, "%s, %.6f\n", resolveRewardAddress(p.addr), p.amt); err != nil {
			return err
		}
	}
	return nil
}

// writeStatsTxt mirrors the legacy `-1 -2` output (suppress shares &
// rewardtxn) — the human-readable per-day stats block.
func writeStatsTxt(w io.Writer, r *dayResult) error {
	fmt.Fprintf(w, "date: %s\n", r.Date)                                                            //nolint:errcheck
	fmt.Fprintf(w, "days this month: %d\n", r.DaysThisMonth)                                        //nolint:errcheck
	fmt.Fprintf(w, "days in the year: %d\n", r.DaysThisYear)                                        //nolint:errcheck
	fmt.Fprintf(w, "this month's rewards: %.6f\n", r.MonthReward)                                   //nolint:errcheck
	fmt.Fprintf(w, "reward total per pool: %.6f\n", r.DayReward)                                    //nolint:errcheck
	fmt.Fprintf(w, "Visors meeting uptime & other requirements (Pool 1): %d\n", len(r.NodesInfos1)) //nolint:errcheck
	fmt.Fprintf(w, "Visors meeting uptime & other requirements (Pool 2): %d\n", len(r.NodesInfos2)) //nolint:errcheck
	fmt.Fprintf(w, "Unique mac addresses for first interface after lo: %d\n", len(r.MacCounts))     //nolint:errcheck
	fmt.Fprintf(w, "Unique IP Addresses: %d\n", len(r.IPCounts))                                    //nolint:errcheck
	fmt.Fprintf(w, "Unique UUIDs: %d\n", len(r.UUIDCounts))                                         //nolint:errcheck
	if r.SaturationExp < 1.0 {
		fmt.Fprintf(w, "Regional saturation exponent: %.2f\n", r.SaturationExp) //nolint:errcheck
	}
	fmt.Fprintf(w, "Total valid shares (Pool 1): %.6f\n", r.TotalShares1) //nolint:errcheck
	fmt.Fprintf(w, "Total valid shares (Pool 2): %.6f\n", r.TotalShares2) //nolint:errcheck
	if r.TotalShares1 != 0 {
		fmt.Fprintf(w, "Skycoin Per Share (Pool 1): %.6f\n", r.DayReward/r.TotalShares1) //nolint:errcheck
	} else {
		fmt.Fprintln(w, "Skycoin Per Share (Pool 1): 0") //nolint:errcheck
	}
	if r.TotalShares2 != 0 {
		fmt.Fprintf(w, "Skycoin Per Share (Pool 2): %.6f\n", r.DayReward/r.TotalShares2) //nolint:errcheck
	} else {
		fmt.Fprintln(w, "Skycoin Per Share (Pool 2): 0") //nolint:errcheck
	}
	combined := append(append([]nodeinfo{}, r.NodesInfos1...), r.NodesInfos2...)
	total := 0.0
	for _, ni := range combined {
		total += ni.Reward
	}
	fmt.Fprintf(w, "Total Reward Amount: %.6f\n", math.Round(total*1000000)/1000000) //nolint:errcheck
	return nil
}
