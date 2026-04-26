// Package clivisor cmd/skywire-cli/commands/visor/hv.go
package clivisor

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/toqueteos/webbrowser"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

var (
	hvPersist bool
)

func init() {
	RootCmd.AddCommand(hvCmd)
	hvCmd.AddCommand(hvuiCmd)
	hvCmd.AddCommand(hvpkCmd)
	hvpkCmd.Flags().StringVarP(&path, "input", "i", "", "path of input config file.")
	hvpkCmd.Flags().BoolVarP(&pkg, "pkg", "p", false, "read from /opt/skywire/skywire.json")
	hvCmd.AddCommand(chvpkCmd)
	hvCmd.AddCommand(hvEnableCmd)
	hvEnableCmd.Flags().BoolVarP(&hvPersist, "persist", "w", false, "write change to config file")
	hvCmd.AddCommand(hvDisableCmd)
	hvDisableCmd.Flags().BoolVarP(&hvPersist, "persist", "w", false, "write change to config file")
	hvCmd.AddCommand(hvStatusCmd)
	hvCmd.AddCommand(hvAddCmd)
	hvCmd.AddCommand(hvLsCmd)
}

var hvCmd = &cobra.Command{
	Use:   "hv",
	Short: "Hypervisor",
	Long: `Hypervisor management commands.

Access the hypervisor UI, view remote hypervisors, and list
visors connected to this hypervisor.`,
}

var hvuiCmd = &cobra.Command{
	Use:   "ui",
	Short: "Open Hypervisor UI in default browser",
	Run: func(cmd *cobra.Command, _ []string) {
		if err := webbrowser.Open(fmt.Sprintf("http://127.0.0.1%s/", HypervisorPort(cmd.Flags()))); err != nil {
			logger.Fatal("Failed to open hypervisor UI in browser:", err)
		}
	},
}

var hvpkCmd = &cobra.Command{
	Use:   "cpk",
	Short: "Public key of remote hypervisor(s) set in config",
	Long:  "Public key of remote hypervisor(s) set in config",
	Run: func(cmd *cobra.Command, _ []string) {
		var hypervisors []cipher.PubKey
		if pkg {
			path = visorconfig.SkywireConfig()
		}
		if path != "" {
			conf, err := visorconfig.ReadFile(path)
			if err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("Failed to read config: %v", err))
			}
			hypervisors = conf.Hypervisors
		} else {
			rpcClient, err := clirpc.Client(cmd.Flags())
			if err != nil {
				os.Exit(1)
			}
			overview, err := rpcClient.Overview()
			if err != nil {
				internal.PrintFatalRPCError(cmd.Flags(), err)
			}
			hypervisors = overview.Hypervisors
		}
		internal.PrintOutput(cmd.Flags(), hypervisors, fmt.Sprintf("%v\n", hypervisors))
	},
}

var chvpkCmd = &cobra.Command{
	Use:   "pk",
	Short: "Public key of remote hypervisor(s)",
	Long:  "Public key of remote hypervisor(s) which are currently connected to",
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalRPCError(cmd.Flags(), err)
		}
		overview, err := rpcClient.Overview()
		if err != nil {
			internal.PrintFatalRPCError(cmd.Flags(), err)
		}
		internal.PrintOutput(cmd.Flags(), overview.ConnectedHypervisor, fmt.Sprintf("%v\n", overview.ConnectedHypervisor))
	},
}

// HypervisorPort returns the port of the hypervisor; either from the running visor or the default value
func HypervisorPort(cmdFlags *pflag.FlagSet) string {
	rpcClient, err := clirpc.Client(cmdFlags)
	if err != nil {
		return visorconfig.HTTPAddr()
	}
	ports, err := rpcClient.Ports()
	if err != nil {
		return visorconfig.HTTPAddr()
	}
	return fmt.Sprintf(":%s", ports["hypervisor"])
}

var hvEnableCmd = &cobra.Command{
	Use:   "enable",
	Short: "Enable hypervisor UI at runtime",
	Long:  "Enable hypervisor UI at runtime.\nUse -w to also persist the change to the config file.",
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}
		if err := rpcClient.EnableHypervisorPersist(hvPersist); err != nil {
			internal.PrintFatalRPCError(cmd.Flags(), err)
		}
		if hvPersist {
			internal.PrintOutput(cmd.Flags(), "Hypervisor enabled (persisted to config)\n", "Hypervisor enabled (persisted to config)\n")
		} else {
			internal.PrintOutput(cmd.Flags(), "Hypervisor enabled\n", "Hypervisor enabled\n")
		}
	},
}

var hvDisableCmd = &cobra.Command{
	Use:   "disable",
	Short: "Disable hypervisor UI at runtime",
	Long:  "Disable hypervisor UI at runtime.\nUse -w to also persist the change to the config file.",
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}
		if err := rpcClient.DisableHypervisorPersist(hvPersist); err != nil {
			internal.PrintFatalRPCError(cmd.Flags(), err)
		}
		if hvPersist {
			internal.PrintOutput(cmd.Flags(), "Hypervisor disabled (persisted to config)\n", "Hypervisor disabled (persisted to config)\n")
		} else {
			internal.PrintOutput(cmd.Flags(), "Hypervisor disabled\n", "Hypervisor disabled\n")
		}
	},
}

var hvStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Check if hypervisor is enabled",
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}
		if rpcClient.IsHypervisorEnabled() {
			internal.PrintOutput(cmd.Flags(), "enabled\n", "enabled\n")
		} else {
			internal.PrintOutput(cmd.Flags(), "disabled\n", "disabled\n")
		}
	},
}

var hvAddCmd = &cobra.Command{
	Use:   "add <public-key>",
	Short: "Connect to a remote hypervisor at runtime",
	Long: `Add a remote hypervisor connection at runtime without editing
the config file. The visor connects to the hypervisor immediately
via DMSG. Not persisted — use SKYENV HYPERVISORPKS for persistence.`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		var pk cipher.PubKey
		if err := pk.Set(args[0]); err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("invalid public key: %w", err))
		}
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		if err := rpcClient.AddHypervisor(pk); err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		fmt.Printf("Connected to hypervisor %s\n", pk)
	},
}

var hvLsCmd = &cobra.Command{
	Use:   "ls",
	Short: "List visors connected to this hypervisor",
	Long: `List all visors connected to this hypervisor with summary info.

Queries each remote visor over its DMSG connection for version, uptime,
transport count, and other details. The local visor is shown first.`,
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}

		entries, err := rpcClient.HVListVisors()
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to list visors: %w", err))
		}

		if len(entries) == 0 {
			internal.PrintOutput(cmd.Flags(), entries, "No visors connected.\n")
			return
		}

		var buf strings.Builder
		tw := tabwriter.NewWriter(&buf, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "PK\tVERSION\tUPTIME\tTP\tAPPS\tIP\tCC\tSTATUS") //nolint:errcheck
		for _, e := range entries {
			pk := e.PK.String()
			if len(pk) > 10 {
				pk = pk[:8] + ".."
			}
			status := "ok"
			if e.IsLocal {
				status = "local"
			}
			if e.Error != "" {
				status = e.Error
				if len(status) > 25 {
					status = status[:25] + "..."
				}
			}
			ver := e.Version
			if ver == "" {
				ver = "-"
			}
			uptime := "-"
			if e.Uptime > 0 {
				uptime = (time.Duration(e.Uptime) * time.Second).Truncate(time.Second).String()
			}
			ip := e.PublicIP
			if ip == "" {
				ip = "-"
			}
			cc := e.CountryCode
			if cc == "" {
				cc = "-"
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%d\t%s\t%s\t%s\n", //nolint:errcheck
				pk, ver, uptime, e.Transports, e.Apps, ip, cc, status)
		}
		tw.Flush() //nolint:errcheck,gosec
		internal.PrintOutput(cmd.Flags(), entries, buf.String())
	},
}
