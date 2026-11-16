// Package clirewards cmd/skywire-cli/commands/rewards/runday.go c4-vis-cli
//
// runday.go runs the hourly reward cycle in-process, the steps of the
// skywire-reward.service pipeline (`cli log --no-fetch`, `bw-collect`,
// `script reward | bash`), with the calculation done once by calcRewards.
package clirewards

import (
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/skycoin/skywire/cmd/skywire-cli/cliuptime"
	clilog "github.com/skycoin/skywire/cmd/skywire-cli/commands/log"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/deployment"
	deprewards "github.com/skycoin/skywire/pkg/deployment/rewards"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/uptimestats"
)

var (
	// runOpts defaults to what the reward script passes: -b -B 0 -p log_backups.
	runOpts = rewardOpts{
		surveyPath: "log_backups",
		histDir:    "hist",
		requireBW:  true,
	}
	runFlags cycleConfig
)

func init() {
	RootCmd.AddCommand(runCmd)
	f := runCmd.Flags()
	f.SortFlags = false
	f.StringVarP(&logLvl, "loglvl", "s", "info", "[ debug | warn | error | fatal | panic | trace ]")
	addCalcFlags(f, &runOpts)
	f.StringVarP(&runOpts.histDir, "hist", "H", runOpts.histDir, "history directory, read for bandwidth and written with every output")
	f.StringVarP(&runFlags.utFile, "utfile", "u", "", "uptime file to calculate from instead of fetching hist/<date>_ut.txt")
	f.BoolVar(&runFlags.skipCollect, "skip-collect", false, "skip the survey cleanup and bandwidth collection (use data already on disk)")
	f.StringVar(&runFlags.pruneBelow, "prune-below-version", "auto", "remove surveys whose skywire_version is below this (\"auto\" = the 14-day reward floor, \"\" = keep)")
	f.BoolVar(&runFlags.fetch, "fetch", false, "fetch surveys from visors (cli log --survey) instead of only cleaning the surveys visors push")
	f.StringVar(&runFlags.minVersion, "minv", "", "with --fetch, minimum skywire version to fetch from")
	f.StringVar(&runFlags.proxy, "proxy", "", "with --fetch, collect surveys via a dmsgweb SOCKS5 resolving proxy (host:port, e.g. 127.0.0.1:4443)")
	f.DurationVar(&runFlags.dayCloseDelay, "day-close-delay", defaultDayCloseDelay, "how long after midnight UTC a day's uptimes and bandwidth are final and fetched once more")
	f.StringVar(&clirpc.Addr, "rpc", clirpc.DefaultRPCAddr, "RPC server address (env: SKYWIRE_RPC)")
	clirpc.RegisterFetchFlags(runCmd)
}

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "run the full hourly reward cycle in-process (no bash)",
	Long: `Performs the hourly reward cycle of skywire-reward.service in one process:

  1. Clean the surveys visors push (cli log --no-fetch --prune-below-version auto)
  2. Collect today's bandwidth (cli rewards bw-collect)
  3. Stop if hist/<date>.txt shows the date's rewards were already sent
  4. Fetch the TPD uptimes for the date into hist/<date>_ut.txt, hist/<date>_ut.json
     and hist/uptimes.json (as cli ut tpd --since <date> --until <date>), unless
     the file was written after the day closed (--day-close-delay after midnight UTC).
     Once the day closed, collect its bandwidth once more so the file holds all of it.
  5. Calculate with the same code as the bare 'rewards' command
  6. Write hist/<date>_{ineligible.csv,shares.csv,rewardtxn0.csv,stats.txt}
     and the per-pool files

The date defaults to yesterday in UTC, as the service runs with TZ=UTC.
The calculation flags default to what the reward script passes (-b -B 0 -p log_backups).

  ExecStart=/usr/bin/skywire cli rewards run`,
	Run: func(cmd *cobra.Command, _ []string) {
		if log == nil {
			log = logging.MustGetLogger("rewards")
		}
		if logLvl != "" {
			if lvl, err := logging.LevelFromString(logLvl); err == nil {
				logging.SetLevel(lvl)
			}
		}
		if _, err := runCycle(newDayCycle(cmd.Flags(), runFlags), runOpts); err != nil {
			log.Fatal(err)
		}
	},
}

// cycleConfig is how a cycle collects and where it reads the uptimes.
type cycleConfig struct {
	utFile      string
	skipCollect bool
	fetch       bool
	pruneBelow  string
	minVersion  string
	proxy       string
	// dayCloseDelay is how long after midnight UTC a day is final, defaultDayCloseDelay when zero.
	dayCloseDelay time.Duration
}

// cycleMu keeps two cycles in one process from sharing the package state.
var cycleMu sync.Mutex

// runCycle is one hourly cycle for o.date: the collection, then the
// calculation unless the date was paid or its inputs did not change since
// d last wrote its outputs. A step that fails returns an error. Deeper code
// may still log.Fatal, which a caller that must outlive the cycle contains
// with cmdutil.Contain.
func runCycle(d *dayCycle, o rewardOpts) (cycleResult, error) {
	cycleMu.Lock()
	defer cycleMu.Unlock()
	if _, err := time.Parse("2006-01-02", o.date); err != nil {
		return cycleResult{date: o.date}, fmt.Errorf("invalid --date: %w", err)
	}
	if err := os.MkdirAll(o.histDir, 0750); err != nil {
		return cycleResult{date: o.date}, fmt.Errorf("create hist dir: %w", err)
	}
	xpubIndexPath = filepath.Join(filepath.Dir(o.histDir), deprewards.XpubIndexFile)

	// Collection runs even when the date was paid, as in the service: bw-collect fills today's file.
	if !d.cfg.skipCollect {
		collectAll(o, d.cfg)
	}
	return d.calcDay(o)
}

// collectAll runs the collection steps of the service. A survey cleanup
// failure stops the cycle. A bandwidth failure does not.
func collectAll(o rewardOpts, c cycleConfig) {
	flags := clilog.RootCmd.Flags()
	set := func(name, value string) {
		if err := flags.Set(name, value); err != nil {
			log.Fatalf("set cli log --%s: %v", name, err)
		}
	}
	if c.fetch {
		log.Info("Collecting surveys (cli log --survey --cleanup)")
		set("survey", "true")
		set("cleanup", "true")
		if c.proxy != "" {
			set("proxy", c.proxy)
		}
		if c.minVersion != "" {
			set("minv", c.minVersion)
		}
	} else {
		log.Info("Cleaning pushed surveys (cli log --no-fetch)")
		set("no-fetch", "true")
	}
	set("backup-dir", o.surveyPath)
	set("prune-below-version", c.pruneBelow)
	clilog.RootCmd.Run(clilog.RootCmd, nil)

	log.Info("Collecting bandwidth (bw-collect)")
	histPath, bwSurveyPath, bwDate, bwDays, noCache = o.histDir, o.surveyPath, "", 1, true
	if err := collectBandwidth(bwCollectCmd.Flags(), logging.MustGetLogger("bw-collect")); err != nil {
		log.Errorf("Bandwidth collection failed, so a date with no bandwidth file pays pool 2 as pool 1: %v", err)
	}
}

// fetchAndSaveUT writes what the reward script gets from
// `cli ut tpd --since <date> --until <date> --cache-dir hist/`: the date's
// uptimes as "<pk> <date> <pct>" lines in hist/<date>_ut.txt, the whole
// answer in hist/uptimes.json, and the date's slice of it in hist/<date>_ut.json.
func fetchAndSaveUT(flags *pflag.FlagSet, date, histDir string) error {
	entries, err := cliuptime.FetchEntries(flags, deployment.Prod.TransportDiscovery, "v3", nil, histDir, 5)
	if err != nil {
		return err
	}

	sorted := slices.Clone(entries)
	slices.SortFunc(sorted, func(a, b uptimestats.VisorSummary) int { return cmp.Compare(a.PK.Hex(), b.PK.Hex()) })
	var txt strings.Builder
	for _, e := range sorted {
		if v, ok := e.Daily[date]; ok {
			fmt.Fprintf(&txt, "%s %s %s\n", e.PK.Hex(), date, v)
		}
	}
	if err := writeAtomic(filepath.Join(histDir, date+"_ut.txt"), 0644, func(w io.Writer) error {
		_, err := io.WriteString(w, txt.String())
		return err
	}); err != nil {
		return err
	}

	if err := writeAtomic(filepath.Join(histDir, "uptimes.json"), 0644, func(w io.Writer) error {
		return json.NewEncoder(w).Encode(entries)
	}); err != nil {
		return err
	}

	// The reward UI reads this as the script's jq writes it:
	// [.[] | {pk, on, version, daily: {($date): .daily[$date]}}]
	type utDay struct {
		PK      string             `json:"pk"`
		On      bool               `json:"on"`
		Version *string            `json:"version"`
		Daily   map[string]*string `json:"daily"`
	}
	days := make([]utDay, 0, len(entries))
	for _, e := range entries {
		d := utDay{PK: e.PK.Hex(), On: e.Online, Daily: map[string]*string{date: nil}}
		if e.Version != "" {
			d.Version = &e.Version
		}
		if v, ok := e.Daily[date]; ok {
			d.Daily[date] = &v
		}
		days = append(days, d)
	}
	return writeAtomic(filepath.Join(histDir, date+"_ut.json"), 0644, func(w io.Writer) error {
		enc := json.NewEncoder(w)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		return enc.Encode(days)
	})
}

// writeAllOutputs writes the four files the script tees from the four bare
// command calls (-e, -20, -10, -12), in the same order.
func writeAllOutputs(r *rewardResult, histDir string) error {
	jobs := []struct {
		name string
		fn   func(io.Writer) error
	}{
		{"ineligible.csv", func(w io.Writer) error { return writeIneligible(w, r) }},
		{"shares.csv", func(w io.Writer) error { return writeShares(w, r) }},
		{"rewardtxn0.csv", func(w io.Writer) error { return writeRewardTxn(w, r) }},
		{"stats.txt", func(w io.Writer) error {
			if err := writeStats(w, r); err != nil {
				return err
			}
			return writeTotal(w, r)
		}},
	}
	for _, j := range jobs {
		path := filepath.Join(histDir, r.date+"_"+j.name)
		if err := writeAtomic(path, 0644, j.fn); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		log.Infof("wrote %s", path)
	}
	return nil
}

// writeAtomic writes path through a temporary file and a rename, so a reader never sees half a file.
func writeAtomic(path string, mode os.FileMode, write func(io.Writer) error) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp.*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() //nolint:errcheck
	if err := write(tmp); err != nil {
		_ = tmp.Close() //nolint:errcheck
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
