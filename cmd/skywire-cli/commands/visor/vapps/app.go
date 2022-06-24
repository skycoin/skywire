package vapps

import (
	"fmt"
	"os"
	"strconv"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/app/launcher"
)

func init() {
	cobra.EnableCommandSorting = false
	RootCmd.AddCommand(
		lsAppsCmd,
		summaryAppCmd,
		startAppCmd,
		stopAppCmd,
		setAppAutostartCmd,
		appLogsSinceCmd,
	)
}

var lsAppsCmd = &cobra.Command{
	Use:   "ls",
	Short: "list apps",
	Run: func(_ *cobra.Command, _ []string) {
		states, err := rpcClient().Apps()
		internal.Catch(err)

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 5, ' ', tabwriter.TabIndent)
		_, err = fmt.Fprintln(w, "app\tports\tauto_start\tstatus")
		internal.Catch(err)

		for _, state := range states {
			status := "stopped"
			if state.Status == launcher.AppStatusRunning {
				status = "running"
			}
			if state.Status == launcher.AppStatusErrored {
				status = "errored"
			}
			_, err = fmt.Fprintf(w, "%s\t%s\t%t\t%s\n", state.Name, strconv.Itoa(int(state.Port)), state.AutoStart, status)
			internal.Catch(err)
		}
		internal.Catch(w.Flush())
	},
}

var summaryAppCmd = &cobra.Command{
	Use:   "info",
	Short: "app summary",
	//Args:  cobra.MinimumNArgs(1),
	Run: func(_ *cobra.Command, args []string) {
		if len(args) == 0 {
			stats, err := rpcClient().GetAppConnectionsSummary(skyenv.SkychatName)
			internal.CatchNonFatal(err)
			fmt.Println(skyenv.SkychatName)
			fmt.Println(stats)
			stats, err = rpcClient().GetAppConnectionsSummary(skyenv.SkysocksName)
			internal.CatchNonFatal(err)
			fmt.Println(skyenv.SkysocksName)
			fmt.Println(stats)
			stats, err = rpcClient().GetAppConnectionsSummary(skyenv.SkysocksClientName)
			internal.CatchNonFatal(err)
			fmt.Println(skyenv.SkysocksClientName)
			fmt.Println(stats)
			stats, err = rpcClient().GetAppConnectionsSummary(skyenv.VPNServerName)
			internal.CatchNonFatal(err)
			fmt.Println(skyenv.VPNServerName)
			fmt.Println(stats)
			stats, err = rpcClient().GetAppConnectionsSummary(skyenv.VPNClientName)
			internal.CatchNonFatal(err)
			fmt.Println(skyenv.VPNClientName)
			fmt.Println(stats)
		} else {
			stats, err := rpcClient().GetAppConnectionsSummary(args[0])
			internal.Catch(err)
			fmt.Println(stats)
		}
	},
}

var startAppCmd = &cobra.Command{
	Use:   "start <name>",
	Short: "launch app",
	Args:  cobra.MinimumNArgs(1),
	Run: func(_ *cobra.Command, args []string) {
		internal.Catch(rpcClient().StartApp(args[0]))
		fmt.Println("OK")
	},
}

var stopAppCmd = &cobra.Command{
	Use:   "stop <name>",
	Short: "halt app",
	Args:  cobra.MinimumNArgs(1),
	Run: func(_ *cobra.Command, args []string) {
		internal.Catch(rpcClient().StopApp(args[0]))
		fmt.Println("OK")
	},
}

var setAppAutostartCmd = &cobra.Command{
	Use:   "autostart <name> (on|off)",
	Short: "set autostart flag for app",
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
		internal.Catch(rpcClient().SetAutoStart(args[0], autostart))
		fmt.Println("OK")
	},
}

var appLogsSinceCmd = &cobra.Command{
	Use:   "log <name> <timestamp>",
	Short: "logs from app since RFC3339Nano-formated timestamp.\n                    \"beginning\" is a special timestamp to fetch all the logs",
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
		logs, err := rpcClient().LogsSince(t, args[0])
		internal.Catch(err)
		if len(logs) > 0 {
			fmt.Println(logs)
		} else {
			fmt.Println("no logs")
		}
	},
}
