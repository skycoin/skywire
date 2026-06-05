// Package clilog cmd/skywire-cli/commands/log/single_visor_log.go
//
// `cli log file <pk>` — stream a single visor's /visor.log over
// dmsghttp. Requires the remote visor to have been started with
// -s/--save-log; without that flag, the endpoint returns 404 and
// this command surfaces a clear hint.
//
// Filtering flags translate to URL query params that the remote
// visor applies server-side before streaming — avoids pulling the
// whole file just to grep it locally over a slow dmsg/skynet hop.
package clilog

import (
	"context"
	"net/url"
	"os"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/logging"
)

var (
	fileMinLevel  string
	fileModule    string
	fileGrep      string
	fileSinceLine int
	fileLimit     int
	fileFollow    bool
)

func init() {
	singleFileCmd.Flags().SortFlags = false
	singleFileCmd.Flags().StringVar(&fileMinLevel, "min-level", "",
		"keep only lines >= this severity (trace|debug|info|warn|error|fatal|panic); empty = no filter")
	singleFileCmd.Flags().StringVar(&fileModule, "module", "",
		"regex filter on the [module] tag (e.g. 'sudph' or '^tp:')")
	singleFileCmd.Flags().StringVar(&fileGrep, "grep", "",
		"regex filter on the full line text")
	singleFileCmd.Flags().IntVar(&fileSinceLine, "since-line", 0,
		"skip the first N lines (1-based) — useful for resume after disconnect")
	singleFileCmd.Flags().IntVar(&fileLimit, "limit", 0,
		"stop after N matching lines (0 = no limit)")
	singleFileCmd.Flags().BoolVarP(&fileFollow, "follow", "f", false,
		"keep streaming new appends after EOF (tail -f); cancel with ctrl+c")
	RootCmd.AddCommand(singleFileCmd)
}

var singleFileCmd = &cobra.Command{
	Use:   "file <pk>",
	Short: "Stream a single visor's /visor.log to stdout",
	Long: `Stream /visor.log from one visor over dmsghttp. Visor must have been
started with -s/--save-log; without that flag the file doesn't exist
and the endpoint returns 404.

Filtering flags translate to URL query params applied server-side, so
the visor only ships lines that match — useful over slow dmsg/skynet
hops where pulling the whole file is wasteful.

  --min-level    keep lines >= severity
  --module       regex on [module] tag
  --grep         regex on full line
  --since-line   skip first N lines
  --limit        stop after N matches
  --follow / -f  tail -f mode

Gated by the remote visor's survey_whitelist.`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		log := logging.MustGetLogger("log-cli")
		pk, err := parseTargetPK(args[0])
		if err != nil {
			log.Fatal(err)
		}
		ctx, cancel := cmdutil.SignalContext(context.Background(), log)
		defer cancel()

		// No per-request timeout — log files can be large and slow.
		// ctx cancellation (^C) still terminates the stream.
		hc, cleanup, err := dmsgHTTPClient(ctx, 0)
		if err != nil {
			log.Fatal(err)
		}
		defer cleanup()

		if err := fetchSurveyEndpoint(ctx, hc, pk, "/visor.log"+buildVisorLogQuery(), os.Stdout); err != nil {
			log.Fatal(err)
		}
	},
}

// buildVisorLogQuery assembles the ?... query string from the filter
// flags. Returns "" when no filter is set so the remote falls through
// to its raw-file fast path.
func buildVisorLogQuery() string {
	q := url.Values{}
	if fileMinLevel != "" {
		q.Set("min-level", fileMinLevel)
	}
	if fileModule != "" {
		q.Set("module", fileModule)
	}
	if fileGrep != "" {
		q.Set("grep", fileGrep)
	}
	if fileSinceLine > 0 {
		q.Set("since-line", strconv.Itoa(fileSinceLine))
	}
	if fileLimit > 0 {
		q.Set("limit", strconv.Itoa(fileLimit))
	}
	if fileFollow {
		q.Set("follow", "1")
	}
	if len(q) == 0 {
		return ""
	}
	return "?" + q.Encode()
}
