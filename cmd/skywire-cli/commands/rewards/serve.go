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
	runDefaults, runFlagDefaults := runOpts, runFlags
	rewardsserver.NewCycle = func(c rewardsserver.CycleConfig) func(context.Context) error {
		cc := runFlagDefaults
		cc.skipCollect, cc.fetch, cc.proxy, cc.minVersion = c.SkipCollect, c.Fetch, c.Proxy, c.MinVersion
		if c.PruneBelow != "" {
			cc.pruneBelow = c.PruneBelow
		}
		return func(context.Context) error {
			return containedCycle(runCmd.Flags(), cycleOpts(c.WorkDir, runDefaults), cc, c.SkycoinNode)
		}
	}
}

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "reward system UI server with the hourly reward cycle inside",
	Long: `Runs the reward system UI server ('rewards ui') and the hourly reward
cycle ('rewards run') in one process, in place of the systemd timer.

One cycle runs at start, to catch up, then one every hour. Cycles never
overlap. A cycle that fails is logged and tried again the next hour, and
the server keeps serving. /health reports the last cycle under reward_cycle.

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
			Cycle: func(context.Context) error {
				return containedCycle(runCmd.Flags(), cycleOpts(wd, runOpts), runFlags, node)
			},
			Log: logging.MustGetLogger("reward-cycle"),
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

// containedCycle runs a cycle that cannot end the process: a log.Fatal in
// it is returned as an error.
func containedCycle(flags *pflag.FlagSet, o rewardOpts, c cycleConfig, skycoinNode string) error {
	fatalHookOnce.Do(func() { logging.AddHook(fatalHook{}) })
	lastFatal.Store("")
	var cycleErr error
	err := cmdutil.Contain(func() {
		if skycoinNode != "" {
			skycoinNodeURL = skycoinNode
		}
		cycleErr = runCycle(flags, o, c)
	})
	if msg, _ := lastFatal.Load().(string); err != nil && msg != "" {
		err = fmt.Errorf("%s (%w)", msg, err)
	}
	return errors.Join(cycleErr, err)
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
