//go:build !tinygo

// Package clirewards cmd/skywire-cli/commands/rewards/serve.go c4-vis-cli
package clirewards

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/skycoin/skywire/pkg/cmdutil"
	rewardsbot "github.com/skycoin/skywire/pkg/deployment/rewards/bot"
	rewardsserver "github.com/skycoin/skywire/pkg/deployment/rewards/server"
	"github.com/skycoin/skywire/pkg/logging"
)

// serveSkip are the run flags serve leaves out: each cycle is for yesterday
// and fetches its uptimes, the ui's --sk and --skycoin-node serve both.
var serveSkip = map[string]bool{"date": true, "utfile": true, "sk": true, "skycoin-node": true}

func init() {
	RootCmd.AddCommand(serveCmd)
	f := serveCmd.Flags()
	f.SortFlags = false
	f.AddFlagSet(rewardsserver.ServerCmd.Flags())
	runCmd.Flags().VisitAll(func(fl *pflag.Flag) {
		if serveSkip[fl.Name] || f.Lookup(fl.Name) != nil {
			return
		}
		if fl.Shorthand != "" && f.ShorthandLookup(fl.Shorthand) != nil {
			long := *fl
			long.Shorthand = ""
			f.AddFlag(&long)
			return
		}
		f.AddFlag(fl)
	})
	f.DurationVar(&serveTimes.Interval, "cycle-interval", rewardsserver.DefaultCycleInterval, "fallback check: hash the inputs and recalculate when they changed")
	f.DurationVar(&serveTimes.CollectInterval, "collect-interval", rewardsserver.DefaultCollectInterval, "how often to clean the surveys and collect today's bandwidth")
	f.DurationVar(&serveTimes.Debounce, "debounce", rewardsserver.DefaultDebounce, "how long a changed survey waits before the recalculation, so a burst gives one")
	f.DurationVar(&serveTimes.WatchInterval, "watch-interval", rewardsserver.DefaultWatchInterval, "how often to look for a new block and re-check the unpaid day's single payout addresses")
	runDefaults, runFlagDefaults := runOpts, runFlags
	rewardsserver.NewCycle = func(c rewardsserver.CycleConfig) rewardsserver.Cycle {
		cc := runFlagDefaults
		cc.skipCollect, cc.fetch, cc.proxy, cc.minVersion = c.SkipCollect, c.Fetch, c.Proxy, c.MinVersion
		if c.PruneBelow != "" {
			cc.pruneBelow = c.PruneBelow
		}
		cc.dayCloseDelay = c.DayCloseDelay
		return newCycle(c.WorkDir, runDefaults, cc, c.SkycoinNode)
	}
}

// serveTimes are the scheduler intervals of serve.
var serveTimes rewardsserver.Scheduler

// newCycle is the reward cycle of the work dir wd, keeping its state
// between runs.
func newCycle(wd string, o rewardOpts, c cycleConfig, skycoinNode string) rewardsserver.Cycle {
	d := newDayCycle(runCmd.Flags(), c)
	d.watching = true
	cy := rewardsserver.Cycle{
		Calc: func(_ context.Context, _ string) (rewardsserver.CalcResult, error) {
			var res cycleResult
			err := contained(skycoinNode, func() (err error) {
				res, err = calcCycle(d, cycleOpts(wd, o))
				return err
			})
			return rewardsserver.CalcResult{Date: res.date, InputsHash: res.hash, Skipped: res.skipped, Note: res.note}, err
		},
		Watch: func(ctx context.Context) (bool, error) {
			if skycoinNode != "" {
				return d.watchSpent(ctx, skycoinNode)
			}
			return d.watchSpent(ctx, skycoinNodeURL)
		},
		SurveyChanged: surveyChanged,
	}
	if !c.skipCollect {
		cy.Collect = func(context.Context) error {
			return contained(skycoinNode, func() error { return collectCycle(d, cycleOpts(wd, o)) })
		}
	}
	return cy
}

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "reward system UI server with the reward cycle inside",
	Long: `Runs the reward system UI server ('rewards ui') and the hourly reward
cycle ('rewards run') in one process, in place of the systemd timer.

The unpaid day (yesterday UTC) is calculated at start, then again when an
input may have changed: a pushed survey that differs where the calculation
reads it (after --debounce, so a burst gives one run), a single payout
address that sent coins (looked for on each new block of --skycoin-node,
polled every --watch-interval), each collection (--collect-interval: survey
cleanup and today's bandwidth), and --day-close-delay after midnight UTC.
Every --cycle-interval a check runs too. Each run hashes the inputs first
and writes nothing when they did not change, and a paid day is never
recalculated. Runs never overlap. A run that fails is logged and tried
again at the next trigger or check, and the server keeps serving. /health
reports the cycle under reward_cycle.

The flags are those of 'rewards ui' and 'rewards run', without --date and
--utfile. Paths of the cycle (--lpath, --hist) are relative to --wd.

When TG_BOT_TOKEN and TG_CHAT_ID are set, the telegram bot ('rewards bot')
runs here too, watching <wd>/transactions0.txt.

  ExecStart=/usr/bin/skywire cli rewards serve`,
	Run: func(cmd *cobra.Command, args []string) {
		if logLvl != "" {
			if lvl, err := logging.LevelFromString(logLvl); err == nil {
				logging.SetLevel(lvl)
			}
		}
		flags := cmd.Flags()
		wd := flags.Lookup("wd").Value.String()
		node := flags.Lookup("skycoin-node").Value.String()
		ctx := context.Background()

		s := &rewardsserver.Scheduler{
			Cycle:           newCycle(wd, runOpts, runFlags, node),
			Interval:        serveTimes.Interval,
			CollectInterval: serveTimes.CollectInterval,
			Debounce:        serveTimes.Debounce,
			WatchInterval:   serveTimes.WatchInterval,
			DayCloseDelay:   runFlags.dayCloseDelay,
			Log:             logging.MustGetLogger("reward-cycle"),
		}
		rewardsserver.SetScheduler(s)
		go s.Start(ctx)
		startBot(ctx, filepath.Join(wd, "transactions0.txt"))

		rewardsserver.ServerCmd.Run(cmd, args)
	},
}

// cycleOpts is o for a cycle that starts now: yesterday in UTC, with
// relative paths under wd.
func cycleOpts(wd string, o rewardOpts) rewardOpts {
	o.date = time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	if !filepath.IsAbs(o.surveyPath) {
		o.surveyPath = filepath.Join(wd, o.surveyPath)
	}
	if !filepath.IsAbs(o.histDir) {
		o.histDir = filepath.Join(wd, o.histDir)
	}
	return o
}

// containedCycle runs a whole cycle, as `rewards run` does, that cannot end
// the process: a log.Fatal in it is returned as an error.
func containedCycle(d *dayCycle, o rewardOpts, skycoinNode string) (res cycleResult, err error) {
	err = contained(skycoinNode, func() (err error) {
		res, err = runCycle(d, o)
		return err
	})
	return res, err
}

// contained runs fn so that a log.Fatal in it is returned as an error.
func contained(skycoinNode string, fn func() error) error {
	fatalHookOnce.Do(func() { logging.AddHook(fatalHook{}) })
	lastFatal.Store("")
	var fnErr error
	err := cmdutil.Contain(func() {
		if skycoinNode != "" {
			skycoinNodeURL = skycoinNode
		}
		fnErr = fn()
	})
	if msg, _ := lastFatal.Load().(string); err != nil && msg != "" {
		err = fmt.Errorf("%s (%w)", msg, err)
	}
	return errors.Join(fnErr, err)
}

var (
	fatalHookOnce sync.Once
	lastFatal     atomic.Value
)

// fatalHook keeps the last Fatal line, so a failed cycle reports why.
type fatalHook struct{}

func (fatalHook) Levels() []logging.Level { return []logging.Level{logging.FatalLevel} }

func (fatalHook) Fire(e *logging.Entry) error {
	lastFatal.Store(e.Message)
	return nil
}

// startBot runs the telegram bot when TG_BOT_TOKEN and TG_CHAT_ID are set.
func startBot(ctx context.Context, txFile string) {
	token, chatID, err := rewardsbot.FromEnv()
	if err != nil {
		log.WithError(err).Info("Telegram bot off")
		return
	}
	rewardsbot.Start(ctx, token, chatID, txFile, log)
}
