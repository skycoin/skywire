// Package clivisor cmd/skywire-cli/commands/visor/app.go c4-vis-cli
package clivisor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	"github.com/skycoin/skywire/cmd/skywire-cli/cliutil/livetui"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/pkg/app/appcommon"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/visor"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

var appName string
var localPath string
var procKey string
var useInternal bool
var useExternal bool
var appExistingTP bool
var appLocalRoute bool
var appMuxRoutes int
var appMuxMode string
var appMinHops uint16
var lsAppsLive bool
var startAppRoutingPolicy string

func init() {
	// cobra.EnableCommandSorting used to be set to false here, which
	// globally disabled alphabetical sorting for every command in the
	// process. That prevented the root `skywire cli` help from
	// rendering its command groups in a predictable order (commands
	// showed up in insertion order instead). The workflow-ordered
	// subcommands this flag protected (ls/start/stop/…) are still
	// readable when sorted alphabetically, so the global flag is left
	// at its default (true) and groups render alphabetically.
	RootCmd.AddCommand(appCmd)
	appCmd.AddCommand(
		lsAppsCmd,
		startAppCmd,
		stopAppCmd,
		restartAppCmd,
		connsAppCmd,
		addAppCmd,
		rmAppCmd,
		envAppCmd,
		argsAppCmd,
		registerAppCmd,
		deregisterAppCmd,
		appLogsSinceCmd,
		argCmd,
	)
	argCmd.AddCommand(
		setAppAutostartCmd,
		setAppKillswitchCmd,
		setAppSecureCmd,
		setAppWhitelistCmd,
		setAppNetworkInterfaceCmd,
		setAppPKCmd,
	)
	registerAppCmd.Flags().StringVarP(&appName, "appname", "a", "", "name of the app")
	registerAppCmd.Flags().StringVarP(&localPath, "localpath", "p", "./local", "path of the local folder")
	deregisterAppCmd.Flags().StringVarP(&procKey, "procKey", "k", "", "proc key of the app to deregister")
	startAppCmd.Flags().BoolVar(&useInternal, "internal", false, "force internal launcher")
	startAppCmd.Flags().BoolVar(&useExternal, "external", false, "force external launcher")
	startAppCmd.MarkFlagsMutuallyExclusive("internal", "external")
	// Generic routing-session flags — the same ones `cli proxy start` / `cli vpn
	// start` expose, lifted onto the generic surface so ANY app can use them.
	// Applied (only when passed) before the app starts, matching proxy/vpn.
	startAppCmd.Flags().BoolVar(&appExistingTP, "existing-tp", false, "only use existing transports, don't create new ones")
	startAppCmd.Flags().BoolVar(&appLocalRoute, "local-route", false, "calculate routes locally instead of using the route finder")
	startAppCmd.Flags().IntVar(&appMuxRoutes, "mux", 1, "parallel mux routes: 0=unlimited, 1=disabled (default), 2+=N routes")
	startAppCmd.Flags().StringVar(&appMuxMode, "mux-mode", "auto", "mux weight mode: auto (latency-based) or equal (round-robin)")
	startAppCmd.Flags().Uint16Var(&appMinHops, "min-hops", 0, "minimum route hops for this app's routes")
	startAppCmd.Flags().StringVar(&startAppRoutingPolicy, "routing-policy", "",
		"per-app routing policy: @/path/to/policy.star or @/path/to/policy.wasm. "+
			"Installed before the app starts; backend dispatched by file extension. "+
			"Pass an empty string or \"none\" to clear a previously-installed override.")
	lsAppsCmd.Flags().BoolVarP(&lsAppsLive, "live", "L", false,
		"live-refresh mode (bubbletea TUI, 1s tick); shows app status transitions in place")
}

var argCmd = &cobra.Command{
	Use:   "arg",
	Short: "App args",
}

var appCmd = &cobra.Command{
	Use:   "app",
	Short: "App settings",
	Long:  "App settings",
}

var lsAppsCmd = &cobra.Command{
	Use:   "ls",
	Short: "List apps",
	Long:  "\n  List apps",
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}

		if lsAppsLive {
			err := livetui.Run(func(ctx context.Context) (string, error) {
				return renderAppListLive(rpcClient)
			}, livetui.Options{
				Title:    "skywire cli visor app ls",
				Interval: time.Second,
			})
			if err != nil && !errors.Is(err, context.Canceled) {
				internal.PrintFatalError(cmd.Flags(), err)
			}
			return
		}

		states, err := rpcClient.Apps()
		internal.Catch(cmd.Flags(), err)
		appStates, text := formatAppList(states)
		internal.PrintOutput(cmd.Flags(), appStates, text)
	},
}

type appLsState struct {
	App            string `json:"app"`
	Port           int    `json:"port"`
	AutoStart      bool   `json:"auto_start"`
	Status         string `json:"status"`
	DetailedStatus string `json:"detailed_status"`
}

// formatAppList returns both the JSON-friendly per-app rows and the
// rendered tab-aligned text block. Shared between the one-shot ls
// path and the --live watcher so they stay in lockstep.
func formatAppList(states []*appserver.AppState) ([]appLsState, string) {
	var b bytes.Buffer
	w := tabwriter.NewWriter(&b, 0, 0, 5, ' ', tabwriter.TabIndent)
	fmt.Fprintln(w, "app\tport\tauto_start\tstatus\tdetailed_status") //nolint:errcheck

	out := make([]appLsState, 0, len(states))
	for _, state := range states {
		status := "stopped"
		switch state.Status {
		case appserver.AppStatusRunning:
			status = "running"
		case appserver.AppStatusErrored:
			status = "errored"
		case appserver.AppStatusStarting:
			status = "starting"
		}
		fmt.Fprintf(w, "%s\t%s\t%t\t%s\t%s\n", //nolint:errcheck
			state.Name, strconv.Itoa(int(state.Port)),
			state.AutoStart, status, state.DetailedStatus)
		out = append(out, appLsState{
			App:            state.Name,
			Port:           int(state.Port),
			AutoStart:      state.AutoStart,
			Status:         status,
			DetailedStatus: state.DetailedStatus,
		})
	}
	w.Flush() //nolint:errcheck,gosec
	return out, b.String()
}

func renderAppListLive(rpcClient visor.API) (string, error) {
	states, err := rpcClient.Apps()
	if err != nil {
		return "", err
	}
	_, text := formatAppList(states)
	text += fmt.Sprintf("\n%d app(s)\n", len(states))
	return text, nil
}

var startAppCmd = &cobra.Command{
	Use:   "start <name>",
	Short: "Launch app",
	Long:  "\n  Launch app",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}

		launcherMode := ""
		if useInternal {
			launcherMode = "internal"
		} else if useExternal {
			launcherMode = "external"
		}

		// Install the per-app routing policy *before* the app
		// starts. SetAppRoutingPolicy returns immediately once
		// the engine is registered, so by the time StartApp
		// returns, the very first dial the app makes already
		// runs through the new policy. Passing "" / "none" still
		// fires — that's how the operator clears a previously-
		// installed runtime override.
		if cmd.Flags().Changed("routing-policy") {
			internal.Catch(cmd.Flags(), rpcClient.SetAppRoutingPolicy(args[0], startAppRoutingPolicy))
		}

		// Generic routing-session options — apply each only when the operator
		// passed it, so a bare `visor app start <name>` doesn't silently flip
		// visor-wide routing preferences. The shared helper applies the SAME
		// sequence + quirks (mux sentinel, min-hops guard) as `cli proxy start`
		// / `cli vpn start`, so the three surfaces can't drift.
		o := clirpc.RoutingSessionOpts{}
		if cmd.Flags().Changed("existing-tp") {
			o.ExistingTP = &appExistingTP
		}
		if cmd.Flags().Changed("local-route") {
			o.LocalRoute = &appLocalRoute
		}
		if cmd.Flags().Changed("mux") {
			o.MuxRoutes = &appMuxRoutes
		}
		if cmd.Flags().Changed("mux-mode") {
			o.MuxMode = &appMuxMode
		}
		if cmd.Flags().Changed("min-hops") {
			o.MinHops = &appMinHops
		}
		internal.Catch(cmd.Flags(), clirpc.ApplyRoutingSession(rpcClient, o))

		internal.Catch(cmd.Flags(), rpcClient.StartAppWithMode(args[0], launcherMode))
		internal.PrintOutput(cmd.Flags(), "OK", "OK\n")
	},
}

var addAppCmd = &cobra.Command{
	Use:   "add <name> <binary>",
	Short: "Add a new app entry to the visor's launcher config",
	Long: `Add a new app entry to the visor's launcher config. <binary> is
the binary the launcher should spawn (e.g. "skysocks-client",
"skycoin-daemon", "skywire" for an in-tree app).

This is the runtime equivalent of editing the apps[] array in
skywire-config.json by hand. The new entry is created with auto_start
disabled and no args/env. Use 'app env' / 'app arg autostart' / etc.
to configure it before starting.`,
	Args: cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}
		internal.Catch(cmd.Flags(), rpcClient.AddApp(args[0], args[1]))
		internal.PrintOutput(cmd.Flags(), "OK", "OK\n")
	},
}

var rmAppCmd = &cobra.Command{
	Use:   "rm <name>",
	Short: "Remove an app entry from the visor's launcher config",
	Long: `Stop the named app (best-effort — already-stopped apps are
fine) and remove its entry from the launcher config. The on-disk
config file is updated immediately. Use this to clean up stale
entries left behind by older mechanisms (e.g. the legacy 'cli
skynet srv' that pre-dates 'cli serve'), or to remove
multi-instance daemons / proxy clients added at runtime via 'app
add' or 'skycoin daemon add'.`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}
		internal.Catch(cmd.Flags(), rpcClient.DeleteApp(args[0]))
		internal.PrintOutput(cmd.Flags(), "OK", "OK\n")
	},
}

var argsAppCmd = &cobra.Command{
	Use:   "args <name> <shell-string>",
	Short: "Replace the entire Args list on an app",
	Long: `Replace the entire Args slice on the named app's launcher
config. The shell-string is parsed via the same shell-like
tokenizer the on-disk config file uses (whitespace separates,
"double quotes" and 'single quotes' group).

Examples:
  skywire cli visor app args skycoin-web "skycoin web --host 127.0.0.1 --port 8002 --node-url http://127.0.0.1:6420"
  skywire cli visor app args skycoin-daemon "skycoin daemon --enable-all-api-sets=true --log-level=debug"`,
	Args: cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		appName := args[0]
		shellStr := args[1]
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}
		// Tokenize via the visorconfig parser so the CLI matches
		// the server-side semantics exactly.
		toks, perr := visorconfig.SplitArgs(shellStr)
		if perr != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("parse args: %w", perr))
		}
		internal.Catch(cmd.Flags(), rpcClient.SetAppArgs(appName, toks))
		internal.PrintOutput(cmd.Flags(), "OK", "OK\n")
	},
}

var envAppCmd = &cobra.Command{
	Use:   "env <name> KEY=VALUE",
	Short: "Set, replace, or delete an env-var entry on an app",
	Long: `Mutate the Env list on the named app's launcher config.

  KEY=value   sets or replaces the entry
  KEY=        deletes the entry (empty value)

The app is restarted on the next call to 'app start' so the new
env takes effect for fresh proc spawns. Used to configure
multi-instance skycoin-daemon entries' FIBER_TOML at runtime
without dropping to a config edit.`,
	Args: cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		key, value, ok := splitKV(args[1])
		if !ok {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("expected KEY=VALUE, got %q", args[1]))
		}
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}
		internal.Catch(cmd.Flags(), rpcClient.SetAppEnv(args[0], key, value))
		internal.PrintOutput(cmd.Flags(), "OK", "OK\n")
	},
}

// splitKV splits "KEY=VALUE" — returns key, value, true on success.
// Empty value is allowed (means "delete this key" at the SetAppEnv
// layer); empty key is rejected.
func splitKV(s string) (string, string, bool) {
	for i := 0; i < len(s); i++ {
		if s[i] == '=' {
			return s[:i], s[i+1:], i > 0
		}
	}
	return "", "", false
}

var stopAppCmd = &cobra.Command{
	Use:   "stop <name>",
	Short: "Halt app",
	Long:  "\n  Halt app",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}
		internal.Catch(cmd.Flags(), rpcClient.StopApp(args[0]))
		internal.PrintOutput(cmd.Flags(), "OK", "OK\n")
	},
}

var registerAppCmd = &cobra.Command{
	Use:   "register",
	Short: "Register app",
	Long:  "\n  Register app",
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}
		if appName == "" {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("required flag not specified"))
		}
		// Ensure the existence of directories.
		err = ensureDir(&localPath)
		internal.Catch(cmd.Flags(), err)

		procConfig := appcommon.ProcConfig{
			AppName:     appName,
			AppSrvAddr:  "",
			ProcKey:     appcommon.RandProcKey(),
			ProcArgs:    nil,
			ProcWorkDir: "",
			VisorPK:     cipher.PubKey{},
			RoutingPort: 0,
			BinaryLoc:   "",
			LogDBLoc:    filepath.Join(localPath, appName+"_log.db"),
		}
		procKey, err := rpcClient.RegisterApp(procConfig)
		internal.Catch(cmd.Flags(), err)
		internal.PrintOutput(cmd.Flags(), procKey, fmt.Sprintf("%v\n", procKey))
	},
}

var deregisterAppCmd = &cobra.Command{
	Use:   "deregister",
	Short: "Deregister app",
	Long:  "\n  Deregister app",
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}
		if procKey == "" {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("required flag not specified"))
		}

		var pKey appcommon.ProcKey
		err = pKey.UnmarshalText([]byte(procKey))
		if err != nil {
			internal.Catch(cmd.Flags(), fmt.Errorf("failed to read procKey: %v", err))
		}
		err = rpcClient.DeregisterApp(pKey)
		internal.Catch(cmd.Flags(), err)
		internal.PrintOutput(cmd.Flags(), "OK", "OK\n")
	},
}

var setAppAutostartCmd = &cobra.Command{
	Use:   "autostart <name> (true|false)",
	Short: "Set app autostart",
	Args:  cobra.MinimumNArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		var autostart bool
		switch args[1] {
		case "true":
			autostart = true
		case "false":
			autostart = false
		default:
			internal.Catch(cmd.Flags(), fmt.Errorf("invalid args[1] value: %s", args[1]))
		}
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}
		internal.Catch(cmd.Flags(), rpcClient.SetAutoStart(args[0], autostart))
		internal.PrintOutput(cmd.Flags(), "OK", "OK\n")
	},
}

// setAppPKCmd wires the SetAppPK RPC into the generic app surface — the one
// capability `cli proxy`/`cli vpn start <pk>` had that `cli visor app` lacked.
// Sets the remote server/exit PK for any app (skysocks-client's --srv, vpn-
// client's server key, etc.) without hand-editing args.
var setAppPKCmd = &cobra.Command{
	Use:   "pk <name> <public-key>",
	Short: "Set an app's remote server/exit public key",
	Long:  "\n  Set an app's remote server/exit public key (e.g. skysocks-client exit, vpn-client server)",
	Args:  cobra.MinimumNArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		var pk cipher.PubKey
		if err := pk.Set(args[1]); err != nil {
			internal.Catch(cmd.Flags(), fmt.Errorf("invalid public key: %w", err))
		}
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}
		internal.Catch(cmd.Flags(), rpcClient.SetAppPK(args[0], pk))
		internal.PrintOutput(cmd.Flags(), "OK", "OK\n")
	},
}

// restartAppCmd wires the RestartApp RPC (previously implemented but unreachable
// from the CLI) into the generic app surface.
var restartAppCmd = &cobra.Command{
	Use:   "restart <name>",
	Short: "Restart an app",
	Long:  "\n  Restart an app",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}
		internal.Catch(cmd.Flags(), rpcClient.RestartApp(args[0]))
		internal.PrintOutput(cmd.Flags(), "OK", "OK\n")
	},
}

// connsAppCmd wires the GetAppConnectionsSummary RPC (previously CLI-unreachable)
// into the generic app surface, giving proxy/vpn/any app a per-connection
// bytes/latency summary neither `cli proxy` nor `cli vpn` exposed.
var connsAppCmd = &cobra.Command{
	Use:   "conns <name>",
	Short: "Show an app's connection summary (bytes, latency)",
	Long:  "\n  Show an app's per-connection summary (bandwidth, latency)",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}
		summaries, err := rpcClient.GetAppConnectionsSummary(args[0])
		internal.Catch(cmd.Flags(), err)
		var b bytes.Buffer
		if len(summaries) == 0 {
			b.WriteString("(no active connections)\n") //nolint:errcheck,gosec
		} else {
			w := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "alive\tlatency\tup/s\tdown/s\tsent\trecv\terror") //nolint:errcheck
			for _, s := range summaries {
				fmt.Fprintf(w, "%v\t%s\t%d\t%d\t%d\t%d\t%s\n", //nolint:errcheck
					s.IsAlive, s.Latency, s.UploadSpeed, s.DownloadSpeed, s.BandwidthSent, s.BandwidthReceived, s.Error)
			}
			w.Flush() //nolint:errcheck,gosec
		}
		internal.PrintOutput(cmd.Flags(), summaries, b.String())
	},
}

var setAppKillswitchCmd = &cobra.Command{
	Use:   "killswitch <name> (true|false)",
	Short: "Set app killswitch",
	Long:  "\n  Set app killswitch",
	Args:  cobra.MinimumNArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		var killswitch bool
		switch args[1] {
		case "true":
			killswitch = true
		case "false":
			killswitch = false
		default:
			internal.Catch(cmd.Flags(), fmt.Errorf("invalid args[1] value: %s", args[1]))
		}
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}
		internal.Catch(cmd.Flags(), rpcClient.SetAppKillswitch(args[0], killswitch))
		internal.PrintOutput(cmd.Flags(), "OK", "OK\n")
	},
}

var setAppSecureCmd = &cobra.Command{
	Use:   "secure <name> (true|false)",
	Short: "Set app secure",
	Long:  "\n  Set app secure",
	Args:  cobra.MinimumNArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		var secure bool
		switch args[1] {
		case "true":
			secure = true
		case "false":
			secure = false
		default:
			internal.Catch(cmd.Flags(), fmt.Errorf("invalid args[1] value: %s", args[1]))
		}
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}
		internal.Catch(cmd.Flags(), rpcClient.SetAppSecure(args[0], secure))
		internal.PrintOutput(cmd.Flags(), "OK", "OK\n")
	},
}

var setAppWhitelistCmd = &cobra.Command{
	Use:   "whitelist <name> <pks>",
	Short: "Set app connection whitelist (skysocks / vpn-server)",
	Long: `Set the comma-separated public-key whitelist that gates incoming
connections to skysocks or vpn-server. Empty / "remove" / "none"
clears the whitelist (open to all authenticated peers).`,
	Args: cobra.MinimumNArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		whitelist := args[1]
		switch whitelist {
		case "remove", "none", "clear":
			whitelist = ""
		}
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}
		internal.Catch(cmd.Flags(), rpcClient.SetAppWhitelist(args[0], whitelist))
		internal.PrintOutput(cmd.Flags(), "OK", "OK\n")
	},
}

var setAppNetworkInterfaceCmd = &cobra.Command{
	Use:   "netifc <name> <interface>",
	Short: "Set app network interface",
	Long:  "Set app network interface.\n\r\n\r  \"remove\" is a special arg to remove the netifc",
	Args:  cobra.MinimumNArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		netifc := args[1]
		if args[1] == "remove" {
			netifc = ""
		}
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}
		internal.Catch(cmd.Flags(), rpcClient.SetAppNetworkInterface(args[0], netifc))
		internal.PrintOutput(cmd.Flags(), "OK", "OK\n")
	},
}

var appLogsSinceCmd = &cobra.Command{
	Use:   "log <name> <timestamp>",
	Short: "Logs from app",
	Long:  "\n  Logs from app since RFC3339Nano-formatted timestamp.\n\r\n\r  \"beginning\" is a special timestamp to fetch all the logs",
	Args:  cobra.MinimumNArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		var t time.Time
		if args[1] == "beginning" {
			t = time.Unix(0, 0)
		} else {
			var err error
			strTime := args[1]
			t, err = time.Parse(time.RFC3339Nano, strTime)
			internal.Catch(cmd.Flags(), err)
		}
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}
		logs, err := rpcClient.LogsSince(t, args[0])
		internal.Catch(cmd.Flags(), err)
		if len(logs) > 0 {
			internal.PrintOutput(cmd.Flags(), logs, fmt.Sprintf("%v\n", logs))
		} else {
			internal.PrintOutput(cmd.Flags(), "no logs", "no logs\n")
		}
	},
}

func ensureDir(path *string) error {
	var err error
	if *path, err = filepath.Abs(*path); err != nil {
		return fmt.Errorf("failed to expand path: %s", err)
	}
	if _, err := os.Stat(*path); !os.IsNotExist(err) {
		return nil
	}
	if err := os.MkdirAll(*path, 0750); err != nil {
		return fmt.Errorf("failed to create dir: %s", err)
	}
	return nil
}
