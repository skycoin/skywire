// Package clivisor cmd/skywire-cli/commands/visor/uptime.go: renders
// the visor's own session-history (started_at, last_seen, version per
// process incarnation) recorded by pkg/serviceuptime. Pulls data via
// the visor RPC's UptimeHistory method, so works whether or not the
// visor's logserver is reachable from the CLI host.
package clivisor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	"github.com/skycoin/skywire/cmd/skywire-cli/cliutil/livetui"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/pkg/serviceuptime"
	"github.com/skycoin/skywire/pkg/visor"
)

var (
	uptimeLimit          int
	uptimeSince          string
	uptimeWithTimeline   bool
	uptimeTimelineDate   string
	uptimeGapThreshold   time.Duration
	uptimeRestartLoopMax time.Duration
	uptimeLive           bool
)

func init() {
	uptimeCmd.Flags().IntVarP(&uptimeLimit, "limit", "n", 0, "show only the most-recent N session rows (0 = all)")
	uptimeCmd.Flags().StringVar(&uptimeSince, "since", "", "show sessions starting at or after this time (RFC3339 or unix seconds)")
	uptimeCmd.Flags().BoolVarP(&uptimeWithTimeline, "timeline", "t", false, "also render today's 5-min slot bitmap")
	uptimeCmd.Flags().StringVar(&uptimeTimelineDate, "date", "", "render the bitmap for a specific UTC date (YYYY-MM-DD); implies --timeline")
	uptimeCmd.Flags().DurationVar(&uptimeGapThreshold, "gap-threshold", 30*time.Second,
		"render a (down: ...) row between sessions whose gap exceeds this")
	uptimeCmd.Flags().DurationVar(&uptimeRestartLoopMax, "restart-loop-threshold", 30*time.Second,
		"sessions shorter than this get a (restart loop?) tag")
	uptimeCmd.Flags().BoolVarP(&uptimeLive, "live", "L", false,
		"live-refresh mode (bubbletea TUI, 1s tick); current session 'running:' line ticks up in place")
	RootCmd.AddCommand(uptimeCmd)
}

var uptimeCmd = &cobra.Command{
	Use:   "uptime",
	Short: "Visor session history (version-tagged, with restart-loop / down-window detection)",
	Long: `
Render the visor's own session history recorded locally by pkg/serviceuptime.

Each session is one process incarnation: started_at, last_seen, and the
running binary's Version. Gaps between adjacent sessions show as
"(down: <duration>)"; sessions shorter than --restart-loop-threshold get
a "(restart loop?)" tag.

	skywire cli visor uptime
	skywire cli visor uptime -n 20
	skywire cli visor uptime --timeline
	skywire cli visor uptime --date 2026-05-04 -t
	skywire cli visor uptime --json | jq '.sessions[-5:]'
`,
	Run: func(cmd *cobra.Command, _ []string) {
		args, err := buildUptimeArgs()
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}

		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}

		if uptimeLive {
			err := livetui.Run(func(ctx context.Context) (string, error) {
				hist, err := rpcClient.UptimeHistory(args)
				if err != nil {
					return "", err
				}
				return renderUptimeHuman(hist, args), nil
			}, livetui.Options{
				Title:    "skywire cli visor uptime",
				Interval: time.Second,
			})
			if err != nil && !errors.Is(err, context.Canceled) {
				internal.PrintFatalError(cmd.Flags(), err)
			}
			return
		}

		hist, err := rpcClient.UptimeHistory(args)
		if err != nil {
			internal.PrintFatalRPCError(cmd.Flags(), err)
		}

		if jsonOut, _ := cmd.Flags().GetBool("json"); jsonOut { //nolint:errcheck
			b, _ := json.MarshalIndent(hist, "", "  ") //nolint:errcheck
			internal.PrintOutput(cmd.Flags(), hist, string(b)+"\n")
			return
		}

		internal.PrintOutput(cmd.Flags(), hist, renderUptimeHuman(hist, args))
	},
}

// buildUptimeArgs centralizes the flag-to-args translation so the live
// path and the one-shot path stay aligned.
func buildUptimeArgs() (visor.UptimeHistoryArgs, error) {
	args := visor.UptimeHistoryArgs{Limit: uptimeLimit}
	if uptimeSince != "" {
		t, err := parseSince(uptimeSince)
		if err != nil {
			return args, fmt.Errorf("invalid --since: %w", err)
		}
		args.Since = t
	}
	if uptimeTimelineDate != "" {
		d, err := time.Parse("2006-01-02", uptimeTimelineDate)
		if err != nil {
			return args, fmt.Errorf("invalid --date: %w", err)
		}
		args.IncludeTimeline = true
		args.TimelineDate = d
	} else if uptimeWithTimeline {
		args.IncludeTimeline = true
	}
	return args, nil
}

func renderUptimeHuman(hist *visor.UptimeHistoryResponse, args visor.UptimeHistoryArgs) string {
	if hist.Current.StartedAt.IsZero() && len(hist.Sessions) == 0 {
		return "(no sessions recorded — recorder may be unavailable)\n"
	}
	human := fmt.Sprintf("running: %s (since %s, %s)  pid=%d  instance=%s\n\n",
		nonEmpty(hist.Current.Version, "?"),
		hist.Current.StartedAt.UTC().Format("2006-01-02 15:04:05Z"),
		humanSince(hist.Current.StartedAt, hist.Current.LastSeen),
		hist.Current.PID,
		hist.Current.InstanceID,
	)
	human += serviceuptime.FormatHistory(hist.Sessions, uptimeGapThreshold, uptimeRestartLoopMax)
	if args.IncludeTimeline && len(hist.Timeline) > 0 {
		human += fmt.Sprintf("\nTimeline %s (288 5-min slots, '#' = online):\n%s\n",
			hist.TimelineDate, serviceuptime.FormatBitmap(hist.Timeline))
	}
	return human
}

func parseSince(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	var unix int64
	if _, err := fmt.Sscanf(s, "%d", &unix); err == nil && unix > 0 {
		return time.Unix(unix, 0).UTC(), nil
	}
	return time.Time{}, fmt.Errorf("want RFC3339 or unix seconds, got %q", s)
}

func humanSince(start, last time.Time) string {
	if last.Before(start) {
		return "0s"
	}
	d := last.Sub(start)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%.0fs", d.Seconds())
	case d < time.Hour:
		return fmt.Sprintf("%.0fm", d.Minutes())
	case d < 24*time.Hour:
		return fmt.Sprintf("%.1fh", d.Hours())
	default:
		return fmt.Sprintf("%.1fd", d.Hours()/24)
	}
}

func nonEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
