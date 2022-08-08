package clivisor

import (
	"fmt"
	"os"
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
		setAppAutostartCmd,
		appLogsSinceCmd,
	)
}

var appCmd = &cobra.Command{
	Use:   "app",
	Short: "App settings",
}

var lsAppsCmd = &cobra.Command{
	Use:   "ls",
	Short: "List apps",
	Run: func(_ *cobra.Command, _ []string) {
		states, err := clirpc.Client().Apps()
		internal.Catch(err)
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 5, ' ', tabwriter.TabIndent)
		_, err = fmt.Fprintln(w, "app\tports\tauto_start\tstatus")
		internal.Catch(err)
		for _, state := range states {
			status := "stopped"
			if state.Status == appserver.AppStatusRunning {
				status = "running"
			}
			if state.Status == appserver.AppStatusErrored {
				status = "errored"
			}
			_, err = fmt.Fprintf(w, "%s\t%s\t%t\t%s\n", state.Name, strconv.Itoa(int(state.Port)), state.AutoStart, status)
			internal.Catch(err)
		}
		internal.Catch(w.Flush())
	},
}

var startAppCmd = &cobra.Command{
	Use:   "start <name>",
	Short: "Launch app",
	Args:  cobra.MinimumNArgs(1),
	Run: func(_ *cobra.Command, args []string) {
		internal.Catch(clirpc.Client().StartApp(args[0]))
		fmt.Println("OK")
	},
}

var stopAppCmd = &cobra.Command{
	Use:   "stop <name>",
	Short: "Halt app",
	Args:  cobra.MinimumNArgs(1),
	Run: func(_ *cobra.Command, args []string) {
		internal.Catch(clirpc.Client().StopApp(args[0]))
		fmt.Println("OK")
	},
}

var setAppAutostartCmd = &cobra.Command{
	Use:   "autostart <name> (on|off)",
	Short: "Autostart app",
	Args:  cobra.MinimumNArgs(2),
	Run: func(_ *cobra.Command, args []string) {
		var autostart bool
		switch args[1] {
		case "on":
			autostart = true
		case "off":
			autostart = false
		default:
			internal.Catch(fmt.Errorf("invalid args[1] value: %s", args[1]))
		}
		internal.Catch(clirpc.Client().SetAutoStart(args[0], autostart))
		fmt.Println("OK")
	},
}

var appLogsSinceCmd = &cobra.Command{
	Use:   "log <name> <timestamp>",
	Short: "Logs from app since RFC3339Nano-formated timestamp.\n                    \"beginning\" is a special timestamp to fetch all the logs",
	Args:  cobra.MinimumNArgs(2),
	Run: func(_ *cobra.Command, args []string) {
		var t time.Time
		if args[1] == "beginning" {
			t = time.Unix(0, 0)
		} else {
			var err error
			strTime := args[1]
			t, err = time.Parse(time.RFC3339Nano, strTime)
			internal.Catch(err)
		}
		logs, err := clirpc.Client().LogsSince(t, args[0])
		internal.Catch(err)
		if len(logs) > 0 {
			fmt.Println(logs)
		} else {
			fmt.Println("no logs")
		}
	},
}
