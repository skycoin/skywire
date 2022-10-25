// Package clivisor app.go
package clivisor

import (
	"bytes"
	"fmt"
	"strconv"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
	"github.com/skycoin/skywire/pkg/app/appserver"
)

func init() {
	cobra.EnableCommandSorting = false
	RootCmd.AddCommand(appCmd)
	appCmd.AddCommand(
		lsAppsCmd,
		startAppCmd,
		stopAppCmd,
		appLogsSinceCmd,
		argCmd,
	)
	argCmd.AddCommand(
		setAppAutostartCmd,
		setAppKillswitchCmd,
		setAppSecureCmd,
		setAppPasscodeCmd,
		setAppNetworkInterfaceCmd,
	)
}

var argCmd = &cobra.Command{
	Use:   "arg",
	Short: "App args",
}

var appCmd = &cobra.Command{
	Use:   "app",
	Short: "App settings",
	Long:  "\n  App settings",
}

var lsAppsCmd = &cobra.Command{
	Use:   "ls",
	Short: "List apps",
	Long:  "\n  List apps",
	Run: func(cmd *cobra.Command, _ []string) {
		states, err := clirpc.Client(cmd.Flags()).Apps()
		internal.Catch(cmd.Flags(), err)
		var b bytes.Buffer
		w := tabwriter.NewWriter(&b, 0, 0, 5, ' ', tabwriter.TabIndent)
		_, err = fmt.Fprintln(w, "app\tport\tauto_start\tstatus\tdetailed_status")
		internal.Catch(cmd.Flags(), err)

		type appState struct {
			App            string `json:"app"`
			Port           int    `json:"port"`
			AutoStart      bool   `json:"auto_start"`
			Status         string `json:"status"`
			DetailedStatus string `json:"detailed_status"`
		}

		var appStates []appState
		for _, state := range states {
			status := "stopped"
			if state.Status == appserver.AppStatusRunning {
				status = "running"
			}
			if state.Status == appserver.AppStatusErrored {
				status = "errored"
			}
			_, err = fmt.Fprintf(w, "%s\t%s\t%t\t%s\t%s\n", state.Name, strconv.Itoa(int(state.Port)),
				state.AutoStart, status, state.DetailedStatus)
			internal.Catch(cmd.Flags(), err)
			s := appState{
				App:            state.Name,
				Port:           int(state.Port),
				AutoStart:      state.AutoStart,
				Status:         status,
				DetailedStatus: state.DetailedStatus,
			}
			appStates = append(appStates, s)
		}
		internal.Catch(cmd.Flags(), w.Flush())
		internal.PrintOutput(cmd.Flags(), appStates, b.String())
	},
}

var startAppCmd = &cobra.Command{
	Use:   "start <name>",
	Short: "Launch app",
	Long:  "\n  Launch app",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		internal.Catch(cmd.Flags(), clirpc.Client(cmd.Flags()).StartApp(args[0]))
		internal.PrintOutput(cmd.Flags(), "OK", "OK\n")
	},
}

var stopAppCmd = &cobra.Command{
	Use:   "stop <name>",
	Short: "Halt app",
	Long:  "\n  Halt app",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		internal.Catch(cmd.Flags(), clirpc.Client(cmd.Flags()).StopApp(args[0]))
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
		internal.Catch(cmd.Flags(), clirpc.Client(cmd.Flags()).SetAutoStart(args[0], autostart))
		internal.PrintOutput(cmd.Flags(), "OK", "OK\n")
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
		internal.Catch(cmd.Flags(), clirpc.Client(cmd.Flags()).SetAppKillswitch(args[0], killswitch))
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
		internal.Catch(cmd.Flags(), clirpc.Client(cmd.Flags()).SetAppSecure(args[0], secure))
		internal.PrintOutput(cmd.Flags(), "OK", "OK\n")
	},
}

var setAppPasscodeCmd = &cobra.Command{
	Use:   "passcode <name> <passcode>",
	Short: "Set app passcode",
	Long:  "\n  Set app passcode.\n\r\n\r  \"remove\" is a special arg to remove the passcode",
	Args:  cobra.MinimumNArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		passcode := args[1]
		if args[1] == "remove" {
			passcode = ""
		}
		internal.Catch(cmd.Flags(), clirpc.Client(cmd.Flags()).SetAppPassword(args[0], passcode))
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
		internal.Catch(cmd.Flags(), clirpc.Client(cmd.Flags()).SetAppNetworkInterface(args[0], netifc))
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
		logs, err := clirpc.Client(cmd.Flags()).LogsSince(t, args[0])
		internal.Catch(cmd.Flags(), err)
		if len(logs) > 0 {
			internal.PrintOutput(cmd.Flags(), logs, fmt.Sprintf("%v\n", logs))
		} else {
			internal.PrintOutput(cmd.Flags(), "no logs", "no logs\n")
		}
	},
}
