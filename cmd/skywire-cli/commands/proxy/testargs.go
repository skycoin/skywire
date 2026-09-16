// Package skysocksc cmd/skywire-cli/commands/proxy/testargs.go c4-vis-cli
package skysocksc

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	"github.com/skycoin/skywire/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/visor"
)

// proxyArgsRestorer puts an app's argument list back the way the operator had
// it. `proxy test --batch 1` borrows the DEFAULT skysocks-client rather than a
// throwaway proxy-test-N app: it rewrites --srv for every candidate, and
// DoCustomSetting deletes the whole existing arg set before applying the new
// one. Those args are persisted, so a test run rewrote skywire-config.json —
// the last candidate tested became the configured exit, and flags the operator
// had set (--reconnect, --direct, --tunnels, a non-default --addr) were gone
// for good.
type proxyArgsRestorer struct {
	cmd      *cobra.Command
	rpc      visor.API
	appName  string
	args     []string
	once     sync.Once
	finished chan struct{}
}

// restoreProxyArgsOnExit snapshots appName's current args and arranges for them
// to be restored when the caller is done (`defer r.done()`), on a fatal error
// (r.fatal) or on ctrl+c.
func restoreProxyArgsOnExit(cmd *cobra.Command, rpcClient visor.API, appName string) *proxyArgsRestorer {
	r := &proxyArgsRestorer{
		cmd:      cmd,
		rpc:      rpcClient,
		appName:  appName,
		finished: make(chan struct{}),
	}
	if st, err := rpcClient.App(appName); err == nil && st != nil && len(st.Args) > 0 {
		r.args = append([]string(nil), st.Args...)
	}
	sigCtx, sigCancel := cmdutil.SignalContext(context.Background(), &logrus.Logger{})
	go func() {
		defer sigCancel()
		select {
		case <-r.finished:
		case <-sigCtx.Done():
			r.restore()
			internal.PrintOutput(r.cmd.Flags(), nil, fmt.Sprintf("\nInterrupted; %s configuration restored.\n", appName))
			os.Exit(1)
		}
	}()
	return r
}

// done restores the snapshot and releases the signal watcher.
func (r *proxyArgsRestorer) done() {
	close(r.finished)
	r.restore()
}

// fatal restores the snapshot, then exits the way the rest of the command
// reports an unrecoverable error (internal.PrintFatalError does not return).
func (r *proxyArgsRestorer) fatal(err error) {
	r.restore()
	internal.PrintFatalError(r.cmd.Flags(), err)
}

// restore writes the snapshot back through the same RPC the test used to
// change it. It runs at most once.
func (r *proxyArgsRestorer) restore() {
	r.once.Do(func() {
		if len(r.args) == 0 {
			return
		}
		if err := r.rpc.DoCustomSetting(r.appName, argsToCustomSetting(r.args)); err != nil {
			internal.PrintOutput(r.cmd.Flags(), nil, fmt.Sprintf("Warning: could not restore %s arguments %v: %v\n", r.appName, r.args, err))
		}
	})
}

// argsToCustomSetting turns a persisted arg list back into the map shape
// DoCustomSetting takes. Three token forms occur in a visor config: the leading
// "app <name>" subcommand pair, "--flag <value>" string pairs, and bare
// "--flag" booleans (the form updateBoolArg writes); the old single-dash
// "-flag=value" bool form is still accepted on read.
func argsToCustomSetting(args []string) map[string]any {
	setting := make(map[string]any, len(args))
	isFlag := func(s string) bool { return strings.HasPrefix(s, "-") }
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !isFlag(a) {
			// Only the "app <name>" pair reaches here; a value token is
			// always consumed with its flag below.
			if i+1 < len(args) && !isFlag(args[i+1]) {
				setting[a] = args[i+1]
				i++
			}
			continue
		}
		if name, val, ok := strings.Cut(a, "="); ok {
			setting[name] = val == "true"
			continue
		}
		if i+1 < len(args) && !isFlag(args[i+1]) {
			setting[a] = args[i+1]
			i++
			continue
		}
		setting[a] = true
	}
	return setting
}
