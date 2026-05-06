package cliskycoin

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/skyenv"
)

// daemon-add flag bindings.
var (
	daemonAddFiberToml string
	daemonAddPort      int
	daemonAddDataDir   string
	daemonAddAPISets   string
	daemonAddAutoStart bool
	daemonAddStart     bool
)

const skycoinDaemonBinary = "skycoin-daemon"

var daemonListCmd = &cobra.Command{
	Use:   "list",
	Short: "List skycoin-daemon instances on the visor",
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}
		states, err := rpcClient.Apps()
		internal.Catch(cmd.Flags(), err)

		var b bytes.Buffer
		w := tabwriter.NewWriter(&b, 0, 0, 5, ' ', tabwriter.TabIndent)
		_, _ = fmt.Fprintln(w, "name\tauto_start\tstatus\tfiber_toml")
		for _, s := range states {
			if !isDaemon(s.Name) {
				continue
			}
			status := "stopped"
			switch s.Status {
			case appserver.AppStatusRunning:
				status = "running"
			case appserver.AppStatusErrored:
				status = "errored"
			case appserver.AppStatusStarting:
				status = "starting"
			}
			fiber := envValue(s.Env, "FIBER_TOML")
			if fiber == "" {
				fiber = "(skycoin defaults)"
			}
			_, _ = fmt.Fprintf(w, "%s\t%t\t%s\t%s\n", s.Name, s.AutoStart, status, fiber)
		}
		_ = w.Flush()
		internal.PrintOutput(cmd.Flags(), nil, b.String())
	},
}

var daemonAddCmd = &cobra.Command{
	Use:   "add <instance-name>",
	Short: "Add a new skycoin-daemon instance",
	Long: `Add a new skycoin-daemon instance to the running visor. The
instance is named "skycoin-daemon-<instance-name>" — pass any
identifier; e.g. 'add aix' creates 'skycoin-daemon-aix'.

Chains the underlying primitives (AddApp + SetAppEnv +
UpdateAppArgBatch + SetAutoStart + StartApp) so the operator gets
a working multi-instance daemon entry in one command.`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		instance := strings.TrimSpace(args[0])
		if instance == "" {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("instance name must not be empty"))
		}
		appName := skyenv.SkycoinDaemonName + "-" + instance

		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}

		// Add the new app entry.
		if err := rpcClient.AddApp(appName, skycoinDaemonBinary); err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("AddApp(%s): %w", appName, err))
		}

		// Auto-allocate --port if not specified. Counts existing
		// daemon instances on the visor and steps from the base.
		port := daemonAddPort
		if port == 0 {
			states, err := rpcClient.Apps()
			internal.Catch(cmd.Flags(), err)
			n := 0
			for _, s := range states {
				if isDaemon(s.Name) && s.Name != appName {
					n++
				}
			}
			port = 6420 + 2*n
		}

		args2 := map[string]any{
			"--port": fmt.Sprintf("%d", port),
		}
		if daemonAddDataDir != "" {
			args2["--data-dir"] = daemonAddDataDir
		}
		if daemonAddAPISets != "" {
			args2["--enable-gui-api-sets"] = daemonAddAPISets
		}
		if err := rpcClient.DoCustomSetting(appName, args2); err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("DoCustomSetting: %w", err))
		}

		if daemonAddFiberToml != "" {
			if err := rpcClient.SetAppEnv(appName, "FIBER_TOML", daemonAddFiberToml); err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("SetAppEnv FIBER_TOML: %w", err))
			}
		}

		if daemonAddAutoStart {
			if err := rpcClient.SetAutoStart(appName, true); err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("SetAutoStart: %w", err))
			}
		}

		if daemonAddStart {
			if err := rpcClient.StartApp(appName); err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("StartApp: %w", err))
			}
		}

		fmt.Fprintf(os.Stderr, "Added %s on --port %d", appName, port)
		if daemonAddFiberToml != "" {
			fmt.Fprintf(os.Stderr, " (FIBER_TOML=%s)", daemonAddFiberToml)
		}
		fmt.Fprintln(os.Stderr)
	},
}

var daemonStartCmd = &cobra.Command{
	Use:   "start <instance-name>",
	Short: "Start a skycoin-daemon instance",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		appName := skyenv.SkycoinDaemonName + "-" + args[0]
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}
		internal.Catch(cmd.Flags(), rpcClient.StartApp(appName))
		internal.PrintOutput(cmd.Flags(), nil, "OK\n")
	},
}

var daemonStopCmd = &cobra.Command{
	Use:   "stop <instance-name>",
	Short: "Stop a skycoin-daemon instance",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		appName := skyenv.SkycoinDaemonName + "-" + args[0]
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}
		internal.Catch(cmd.Flags(), rpcClient.StopApp(appName))
		internal.PrintOutput(cmd.Flags(), nil, "OK\n")
	},
}

// isDaemon reports whether an app name belongs to the skycoin-daemon
// family — either the canonical single-instance name or a
// multi-instance "skycoin-daemon-<...>" suffix.
func isDaemon(name string) bool {
	return name == skyenv.SkycoinDaemonName ||
		strings.HasPrefix(name, skyenv.SkycoinDaemonName+"-")
}

// envValue returns the value for KEY in a KEY=VALUE list, or "" if
// the key isn't present.
func envValue(env []string, key string) string {
	prefix := key + "="
	for _, e := range env {
		if strings.HasPrefix(e, prefix) {
			return e[len(prefix):]
		}
	}
	return ""
}
