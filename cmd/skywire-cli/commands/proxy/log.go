// Package skysocksc cmd/skywire-cli/commands/proxy/log.go c4-vis-cli
package skysocksc

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/pkg/cmdutil"
)

var (
	logFollow bool
	logLevel  string
)

func init() {
	RootCmd.AddCommand(logCmd)
	logCmd.Flags().StringVarP(&clientName, "name", "n", "skysocks-client", "name of the skysocks client to attach to")
	logCmd.Flags().BoolVarP(&logFollow, "follow", "f", false, "follow: keep streaming new events (ctrl+c detaches without stopping the proxy); off = print recent and exit")
	logCmd.Flags().StringVar(&logLevel, "level", "debug", "minimum log level: trace|debug|info|warn|error")
}

var logCmd = &cobra.Command{
	Use:   "log",
	Short: "Stream a running proxy client's route/transport events + log",
	Long: `Attach to an already-running skysocks client and show its app-scoped
route/transport events + app log — the same class of lines the proxy
status page (http://status.skysocks/) and the route interstitial render.

This is attach-only: it never starts or stops the app. Ctrl+C detaches
and leaves the running proxy untouched.

  proxy log                 print the recent events + log for skysocks-client
  proxy log -f              follow: print recent, then stream new events live
  proxy log -n myclient     attach to a differently-named client
  proxy log --level trace   include trace-level entries`,
	Run: func(cmd *cobra.Command, _ []string) {
		if clientName == "" {
			clientName = "skysocks-client"
		}

		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("unable to create RPC client: %w", err))
		}
		defer rpcClient.Close() //nolint:errcheck,gosec

		// Recent history first (the "same as the status page" view): the
		// per-app ring already holds the route setup / mux lines even if
		// nothing new is happening on an established session.
		recent, err := rpcClient.RecentAppLog(clientName, logLevel)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to fetch recent app log: %w", err))
		}
		for _, ln := range recent {
			fmt.Fprintln(os.Stdout, ln)
		}

		if !logFollow {
			if len(recent) == 0 {
				fmt.Fprintf(os.Stderr, "no recent events for %q (is the proxy running? try -f to wait for new ones)\n", clientName)
			}
			return
		}

		// Follow mode: attach the live gRPC stream (the SAME mechanism
		// `proxy start --verbose` uses) scoped to this app's session, to
		// stdout. We do NOT touch the app — ctrl+c just cancels ctx and
		// detaches.
		ctx, cancel := cmdutil.SignalContext(context.Background(), &logrus.Logger{})
		defer cancel()

		vs, err := clirpc.OpenVerboseTo(ctx, clirpc.Addr, clirpc.VerboseFilter{
			AppName: clientName,
			Level:   logLevel,
		}, os.Stdout)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		_ = vs.WaitSubscribed(ctx, 2*time.Second) //nolint:errcheck
		fmt.Fprintf(os.Stderr, "--- attached to %q; streaming events (ctrl+c to detach) ---\n", clientName)

		<-ctx.Done()
		fmt.Fprintln(os.Stderr, "detaching...")
		vs.Close()
	},
}
