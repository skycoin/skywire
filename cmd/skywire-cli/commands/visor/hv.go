// Package clivisor cmd/skywire-cli/commands/visor/hv.go
package clivisor

import (
	"encoding/json"
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
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
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

// hvVisorInfo holds summary data for a remote visor connected to this hypervisor.
type hvVisorInfo struct {
	PK         string  `json:"pk"`
	Version    string  `json:"version,omitempty"`
	Uptime     float64 `json:"uptime_seconds,omitempty"`
	Transports int     `json:"transports,omitempty"`
	Apps       int     `json:"apps,omitempty"`
	Error      string  `json:"error,omitempty"`
}

var hvLsCmd = &cobra.Command{
	Use:   "ls",
	Short: "List visors connected to this hypervisor",
	Long: `List remote visors that have this visor configured as their hypervisor.

Shows the public key of each connected visor. With --detail, queries
each visor over the transport for version, uptime, and transport count
(requires the remote visor to support transport RPC).`,
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}

		visors, err := rpcClient.RemoteVisors()
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to get remote visors: %w", err))
		}

		if len(visors) == 0 {
			internal.PrintOutput(cmd.Flags(), []hvVisorInfo{}, "No remote visors connected.\n")
			return
		}

		if !hvLsDetail {
			// Simple mode: just list PKs
			results := make([]hvVisorInfo, 0, len(visors))
			var buf strings.Builder
			for i, pk := range visors {
				results = append(results, hvVisorInfo{PK: pk})
				fmt.Fprintf(&buf, "%d. %s\n", i+1, pk)
			}
			internal.PrintOutput(cmd.Flags(), results, buf.String())
			return
		}

		// Detail mode: query each visor via transport RPC
		results := make([]hvVisorInfo, 0, len(visors))
		for _, pkStr := range visors {
			info := hvVisorInfo{PK: pkStr}

			var remotePK cipher.PubKey
			if err := remotePK.Set(pkStr); err != nil {
				info.Error = "invalid PK"
				results = append(results, info)
				continue
			}

			result, err := rpcClient.TransportRPCCall(remotePK, "app-visor.Summary", nil)
			if err != nil {
				info.Error = err.Error()
				results = append(results, info)
				continue
			}

			// Parse the summary JSON
			var summary struct {
				Overview struct {
					BuildInfo struct {
						Version string `json:"version"`
					} `json:"build_info"`
					Transports []json.RawMessage `json:"transports"`
				} `json:"overview"`
				Uptime float64           `json:"uptime"`
				Apps   []json.RawMessage `json:"apps"`
			}
			if err := json.Unmarshal(result, &summary); err != nil {
				info.Error = "failed to parse summary"
				results = append(results, info)
				continue
			}
			info.Version = summary.Overview.BuildInfo.Version
			info.Uptime = summary.Uptime
			info.Transports = len(summary.Overview.Transports)
			info.Apps = len(summary.Apps)
			results = append(results, info)
		}

		// Build table output
		var buf strings.Builder
		tw := tabwriter.NewWriter(&buf, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "PK\tVERSION\tUPTIME\tTRANSPORTS\tAPPS\tSTATUS") //nolint:errcheck
		for _, info := range results {
			pk := info.PK
			if len(pk) > 10 {
				pk = pk[:8] + ".."
			}
			status := "ok"
			ver := info.Version
			uptime := "-"
			tps := "-"
			apps := "-"
			if info.Error != "" {
				status = info.Error
				if len(status) > 30 {
					status = status[:30] + "..."
				}
			} else {
				if info.Uptime > 0 {
					uptime = (time.Duration(info.Uptime) * time.Second).Truncate(time.Second).String()
				}
				tps = fmt.Sprintf("%d", info.Transports)
				apps = fmt.Sprintf("%d", info.Apps)
			}
			if ver == "" {
				ver = "-"
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", pk, ver, uptime, tps, apps, status) //nolint:errcheck
		}
		tw.Flush() //nolint:errcheck,gosec
		internal.PrintOutput(cmd.Flags(), results, buf.String())
	},
}

var hvLsDetail bool

func init() {
	hvLsCmd.Flags().BoolVarP(&hvLsDetail, "detail", "d", false, "query each visor for version, uptime, and transport count via transport RPC")
}
