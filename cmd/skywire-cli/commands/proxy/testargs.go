// Package skysocksc cmd/skywire-cli/commands/proxy/testargs.go c4-vis-cli
package skysocksc

import (
	"context"
	"fmt"
	"sync"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

// proxyArgsRestorer puts an app's argument list back the way the operator had
// it. `proxy test --batch 1` borrows the DEFAULT skysocks-client rather than a
// throwaway proxy-test-N app and rewrites its whole arg list for every
// candidate. Those args are persisted, so a test run rewrote skywire-config.json —
// the last candidate tested became the configured exit, and flags the operator
// had set (--reconnect, --direct, --tunnels, a non-default --addr) were gone
// for good.
type proxyArgsRestorer struct {
	cmd      *cobra.Command
	rpc      visorapi.API
	appName  string
	args     []string
	running  bool // the app ran before the test, so restore restarts it
	once     sync.Once
	finished chan struct{}
}

// restoreProxyArgsOnExit snapshots appName's current args and arranges for them
// to be restored when the caller is done (`defer r.done()`), on a fatal error
// (r.fatal) or on ctrl+c.
func restoreProxyArgsOnExit(cmd *cobra.Command, rpcClient visorapi.API, appName string) *proxyArgsRestorer {
	r := &proxyArgsRestorer{
		cmd:      cmd,
		rpc:      rpcClient,
		appName:  appName,
		finished: make(chan struct{}),
	}
	if st, err := rpcClient.App(appName); err == nil && st != nil && len(st.Args) > 0 {
		r.args = append([]string(nil), st.Args...)
		r.running = st.Status == appserver.AppStatusRunning
	}
	sigCtx, sigCancel := cmdutil.SignalContext(context.Background(), &logrus.Logger{})
	go func() {
		defer sigCancel()
		select {
		case <-r.finished:
		case <-sigCtx.Done():
			r.restore()
			internal.PrintOutput(r.cmd.Flags(), nil, fmt.Sprintf("\nInterrupted; %s configuration restored.\n", appName))
			cmdutil.Exit(1)
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

// restore writes the snapshot back exactly as it was, in order. It runs at most
// once.
func (r *proxyArgsRestorer) restore() {
	r.once.Do(func() {
		if len(r.args) == 0 {
			return
		}
		if err := r.rpc.SetAppArgs(r.appName, r.args); err != nil {
			internal.PrintOutput(r.cmd.Flags(), nil, fmt.Sprintf("Warning: could not restore %s arguments %v: %v\n", r.appName, r.args, err))
			return
		}
		// The client the test left running is on the test's args; run the
		// operator's again.
		if r.running {
			_ = r.rpc.StopApp(r.appName) //nolint:errcheck
			if err := r.rpc.StartApp(r.appName); err != nil {
				internal.PrintOutput(r.cmd.Flags(), nil, fmt.Sprintf("Warning: could not restart %s: %v\n", r.appName, err))
			}
		}
	})
}
