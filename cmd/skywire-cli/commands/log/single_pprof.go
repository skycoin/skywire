// Package clilog cmd/skywire-cli/commands/log/single_pprof.go
//
// `cli log pprof <pk> <profile>` — fetch one of the remote visor's
// pprof profiles over dmsghttp and emit the raw bytes to stdout.
// Direct equivalent of `go tool pprof http://<host>/debug/pprof/<profile>`
// but routed through the survey_whitelist gate.
package clilog

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/logging"
)

// pprofProfiles maps friendly subcommand-arg names to the pprof
// path segments served by the visor's logserver
// (pkg/visor/logserver/api.go:193-199). Friendly "cpu" maps to
// "profile" for the same reason `go tool pprof` accepts both.
var pprofProfiles = map[string]string{
	"cpu":          "profile",
	"profile":      "profile",
	"heap":         "heap",
	"goroutine":    "goroutine",
	"threadcreate": "threadcreate",
	"block":        "block",
	"mutex":        "mutex",
	"allocs":       "allocs",
	"trace":        "trace",
	"cmdline":      "cmdline",
	"symbol":       "symbol",
}

// pprofProfileNames is the sorted list used in the usage hint. Kept
// in init() to avoid drift from pprofProfiles.
var pprofProfileNames []string

var pprofSeconds int

func init() {
	for k := range pprofProfiles {
		pprofProfileNames = append(pprofProfileNames, k)
	}
	singlePprofCmd.Flags().IntVarP(&pprofSeconds, "seconds", "n", 0, "for cpu/profile/trace: duration in seconds (visor default is 30)")
	RootCmd.AddCommand(singlePprofCmd)
}

var singlePprofCmd = &cobra.Command{
	Use:   "pprof <pk> <profile>",
	Short: "Fetch a runtime pprof profile from a remote visor",
	Long: `Fetch /debug/pprof/<profile> from one visor over dmsghttp. Binary
profile bytes stream to stdout — redirect to a file and feed to
` + "`go tool pprof`" + `:

  skywire cli log pprof <pk> heap > heap.pprof
  go tool pprof heap.pprof

Available profiles: cpu (alias for profile), profile, heap, goroutine,
threadcreate, block, mutex, allocs, trace, cmdline, symbol.

For sampling profiles (cpu / profile / trace), --seconds controls
the sample duration; the visor caps this at its pprof default (30s).
Whitelisted via the remote visor's survey_whitelist.`,
	Args: cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		log := logging.MustGetLogger("log-cli")
		pk, err := parseTargetPK(args[0])
		if err != nil {
			log.Fatal(err)
		}
		profile := args[1]
		realProfile, ok := pprofProfiles[profile]
		if !ok {
			log.Fatalf("unknown profile %q (try: %v)", profile, pprofProfileNames)
		}

		path := "/debug/pprof/" + realProfile
		// CPU/trace endpoints accept ?seconds=N; non-sampling profiles
		// ignore it but it's safe to include unconditionally.
		if pprofSeconds > 0 {
			path = fmt.Sprintf("%s?seconds=%d", path, pprofSeconds)
		}

		ctx, cancel := cmdutil.SignalContext(context.Background(), log)
		defer cancel()

		// Sampling profiles run for `seconds` server-side; an
		// http.Client per-request Timeout must outlive that. Pad
		// generously — 30s default + 60s buffer covers typical use.
		timeout := time.Duration(pprofSeconds+60) * time.Second
		if pprofSeconds == 0 {
			timeout = 90 * time.Second
		}
		hc, cleanup, err := dmsgHTTPClient(ctx, timeout)
		if err != nil {
			log.Fatal(err)
		}
		defer cleanup()

		if err := fetchSurveyEndpoint(ctx, hc, pk, path, os.Stdout); err != nil {
			log.Fatal(err)
		}
	},
}
