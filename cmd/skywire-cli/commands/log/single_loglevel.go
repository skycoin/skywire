// Package clilog cmd/skywire-cli/commands/log/single_loglevel.go c4-vis-cli
//
// `cli log level <pk> [level]` — read or set a remote visor's log level
// for a while, over dmsghttp. Visors run at info; this turns on debug for
// the one visor being looked at, while `cli log file <pk> -f` streams it.
package clilog

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/visor/logserver"
)

var (
	levelTTL   time.Duration
	levelClear bool
)

func init() {
	singleLevelCmd.Flags().DurationVar(&levelTTL, "ttl", logserver.DefaultTempLogLevelTTL,
		"how long the level holds before the visor goes back to its own (at most 1h)")
	singleLevelCmd.Flags().BoolVar(&levelClear, "clear", false,
		"end a temporary level now")
	RootCmd.AddCommand(singleLevelCmd)
}

var singleLevelCmd = &cobra.Command{
	Use:   "level <pk> [level]",
	Short: "Read or temporarily set a remote visor's log level",
	Long: `Read or temporarily set the log level of one visor over dmsghttp.

With no level, prints the current one. With a level, the visor logs at it
until --ttl runs out (default 15m, at most 1h), then goes back to its own
level. The temporary level is never saved, so a restart ends it as well.

  skywire cli log level <pk> debug --ttl 10m
  skywire cli log file <pk> -f --min-level debug
  skywire cli log level <pk> --clear

Gated by the remote visor's survey_whitelist.`,
	Args: cobra.RangeArgs(1, 2),
	Run: func(cmd *cobra.Command, args []string) {
		log := logging.MustGetLogger("log-cli")
		pk, err := parseTargetPK(args[0])
		if err != nil {
			log.Fatal(err)
		}
		method, path := http.MethodGet, "/debug/loglevel"
		switch {
		case levelClear && len(args) == 2:
			log.Fatal("give a level or --clear, not both")
		case levelClear:
			method = http.MethodDelete
		case len(args) == 2:
			if _, err := logging.LevelFromString(args[1]); err != nil {
				log.Fatal(err)
			}
			method = http.MethodPost
			path += "?" + url.Values{"level": {args[1]}, "ttl": {levelTTL.String()}}.Encode()
		}

		ctx, cancel := cmdutil.SignalContext(context.Background(), log)
		defer cancel()

		hc, cleanup, err := dmsgHTTPClient(ctx, 30*time.Second)
		if err != nil {
			log.Fatal(err)
		}
		defer cleanup()

		var st logserver.LogLevelStatus
		if err := doSurveyJSON(ctx, hc, method, pk, path, &st); err != nil {
			log.Fatal(err)
		}
		fmt.Println(formatLogLevelStatus(st))
	},
}

// formatLogLevelStatus renders a status as one line, naming the level the
// visor goes back to and when.
func formatLogLevelStatus(st logserver.LogLevelStatus) string {
	if st.Until == nil {
		return st.Level
	}
	return fmt.Sprintf("%s until %s (%s left), then %s",
		st.Level, st.Until.Local().Format(time.RFC3339),
		time.Until(*st.Until).Round(time.Second), st.Base)
}
