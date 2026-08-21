// Package clivisor cmd/skywire-cli/commands/visor/hv.go c4-vis-cli
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
	"github.com/skycoin/skywire/pkg/visor"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

var (
	hvPersist bool
)

func init() {
	RootCmd.AddCommand(hvCmd)
	hvCmd.AddCommand(hvuiCmd)
	hvuiCmd.AddCommand(hvUIEnableCmd)
	hvUIEnableCmd.Flags().BoolVarP(&hvPersist, "persist", "w", false, "write change to config file")
	hvuiCmd.AddCommand(hvUIDisableCmd)
	hvUIDisableCmd.Flags().BoolVarP(&hvPersist, "persist", "w", false, "write change to config file")
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
	hvCmd.AddCommand(hvRmCmd)
	hvRmCmd.Flags().BoolVar(&hvRmAll, "all", false, "remove every runtime-added hypervisor connection")
	hvCmd.AddCommand(hvLsCmd)
	hvLsCmd.Flags().BoolVar(&hvLsFlat, "flat", false, "single flat table instead of one section per hypervisor")
	hvLsCmd.Flags().BoolVar(&hvLsLoad, "load", false, "add LOAD (1m/cores), MEM%, and DISK% columns per visor")
	hvCmd.AddCommand(hvTreeCmd)
	hvCmd.AddCommand(hvPasswdCmd)
	hvPasswdCmd.Flags().StringVar(&hvPasswdOld, "old", "", "current password (required unless --force)")
	hvPasswdCmd.Flags().StringVar(&hvPasswdNew, "new", "", "new password")
	hvPasswdCmd.Flags().BoolVar(&hvPasswdForce, "force", false, "set --new without the old password (reset a forgotten password, or first-time set)")
}

var (
	hvRmAll       bool
	hvPasswdOld   string
	hvPasswdNew   string
	hvPasswdForce bool
	hvLsFlat      bool
	hvLsLoad      bool
)

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
	// ports["hypervisor"] is a PortDetail struct; format only its Port
	// field. Using the whole struct with %s renders "{8000 TCP}", which
	// leaked into URLs as "http://127.0.0.1:{8000 TCP}/" for `hv ui`,
	// `vpn ui`, and `vpn url`.
	hp := ports["hypervisor"].Port
	if hp == "" {
		return visorconfig.HTTPAddr()
	}
	return fmt.Sprintf(":%s", hp)
}

var hvUIEnableCmd = &cobra.Command{
	Use:   "enable",
	Short: "Start the hypervisor web UI (leaves DMSG-RPC + hv ls running)",
	Long:  "Start ONLY the hypervisor web UI. The DMSG-RPC listener, managed-visor tracking, and `hv ls` are unaffected. Requires the hypervisor to be enabled.\nUse -w to also persist the change to the config file.",
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}
		if err := rpcClient.EnableHypervisorUIPersist(hvPersist); err != nil {
			internal.PrintFatalRPCError(cmd.Flags(), err)
		}
		if hvPersist {
			internal.PrintOutput(cmd.Flags(), "Hypervisor web UI enabled (persisted to config)\n", "Hypervisor web UI enabled (persisted to config)\n")
		} else {
			internal.PrintOutput(cmd.Flags(), "Hypervisor web UI enabled\n", "Hypervisor web UI enabled\n")
		}
	},
}

var hvUIDisableCmd = &cobra.Command{
	Use:   "disable",
	Short: "Stop the hypervisor web UI (keeps DMSG-RPC + hv ls running)",
	Long:  "Stop ONLY the hypervisor web UI (the HTTP server). The DMSG-RPC listener, managed-visor tracking, and `hv ls` over the visor RPC keep working — useful to shrink the public attack surface while retaining CLI access to connected visors.\nUse -w to also persist the change to the config file.",
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}
		if err := rpcClient.DisableHypervisorUIPersist(hvPersist); err != nil {
			internal.PrintFatalRPCError(cmd.Flags(), err)
		}
		if hvPersist {
			internal.PrintOutput(cmd.Flags(), "Hypervisor web UI disabled (persisted to config)\n", "Hypervisor web UI disabled (persisted to config)\n")
		} else {
			internal.PrintOutput(cmd.Flags(), "Hypervisor web UI disabled\n", "Hypervisor web UI disabled\n")
		}
	},
}

var hvEnableCmd = &cobra.Command{
	Use:   "enable",
	Short: "Enable the hypervisor (DMSG-RPC + tracking + web UI) at runtime",
	Long:  "Enable the hypervisor — DMSG-RPC listener, managed-visor tracking, and the web UI (unless ui_disable is set). Use `hv ui enable/disable` to toggle just the web UI.\nUse -w to also persist the change to the config file.",
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
	Short: "Disable the hypervisor entirely (DMSG-RPC + tracking + web UI) at runtime",
	Long:  "Disable the whole hypervisor — stops the DMSG-RPC listener, disconnects managed visors, and stops the web UI (so `hv ls` no longer works). To stop ONLY the web UI while keeping CLI access, use `hv ui disable`.\nUse -w to also persist the change to the config file.",
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
	Short: "Connect to a remote hypervisor at runtime (persisted)",
	Long: `Add a remote hypervisor connection at runtime without editing
the config file by hand. The visor connects to the hypervisor
immediately via DMSG AND persists the PK to the config's
hypervisors list, so the connection survives a restart. (On a
non-file-backed config — wasm tab / STDIN — the connection is made
but cannot be persisted.)

The outbound connection (hv ls / hv tui / web UI on the hypervisor)
works right away. The INBOUND access the PK grants — driving this
visor with 'skywire cli --via dmsg://<this-visor>' from the
hypervisor's machine — starts on this visor's next restart, when the
dmsg RPC listeners and the peer whitelist are rebuilt from config.
See docs/guides/remote-visor-cli.md.`,
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

var hvRmCmd = &cobra.Command{
	Use:   "rm [public-key]",
	Short: "Disconnect from a remote hypervisor at runtime (persisted)",
	Long: `Tear down a hypervisor connection at runtime AND remove its PK
from the config's hypervisors list, so it stays removed across a
restart. Works for both runtime-added (hv add) and config-loaded
hypervisors. (On a non-file-backed config — wasm tab / STDIN — the
disconnect happens but cannot be persisted.)

Pass --all to disconnect every hypervisor and clear the configured
list in one call. Without --all, exactly one <public-key> argument
is required.`,
	Args: func(cmd *cobra.Command, args []string) error {
		if hvRmAll {
			if len(args) > 0 {
				return fmt.Errorf("--all takes no positional arguments")
			}
			return nil
		}
		if len(args) != 1 {
			return fmt.Errorf("exactly one <public-key> argument required (or pass --all)")
		}
		return nil
	},
	Run: func(cmd *cobra.Command, args []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		if hvRmAll {
			n, err := rpcClient.RemoveAllHypervisors()
			if err != nil {
				internal.PrintFatalError(cmd.Flags(), err)
			}
			fmt.Printf("Disconnected from %d runtime-added hypervisor(s)\n", n)
			return
		}
		var pk cipher.PubKey
		if err := pk.Set(args[0]); err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("invalid public key: %w", err))
		}
		if err := rpcClient.RemoveHypervisor(pk); err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		fmt.Printf("Disconnected from hypervisor %s\n", pk)
	},
}

var hvPasswdCmd = &cobra.Command{
	Use:   "passwd",
	Short: "Set the hypervisor UI admin password",
	Long: `Set the password for the hypervisor UI's "admin" account.
Mirrors the /api/change-password endpoint the UI uses, but without
the HTTP session check (RPC is local-only and already privileged).

Normal change: --old and --new are both required.

--force sets --new without the old password — to reset a forgotten
password, or to set the password for the first time from the CLI
(creating the "admin" account if none exists, so you don't need the
UI's create-account page). All existing sessions are invalidated.

Avoid shell history capturing the password by sourcing the values
from an env-var or a process-substitution.`,
	Run: func(cmd *cobra.Command, _ []string) {
		if hvPasswdNew == "" {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("--new is required"))
		}
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		if hvPasswdForce {
			if err := rpcClient.SetHypervisorPasswordForce(hvPasswdNew); err != nil {
				internal.PrintFatalError(cmd.Flags(), err)
			}
			fmt.Println("Hypervisor UI password set; all existing sessions invalidated.")
			return
		}
		if hvPasswdOld == "" {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("--old is required (or use --force to set without it)"))
		}
		if err := rpcClient.SetHypervisorPassword(hvPasswdOld, hvPasswdNew); err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		fmt.Println("Hypervisor UI password updated; all existing sessions invalidated.")
	},
}

// hvLsHeader returns the table header, appending the resource columns when
// showLoad is set. A function (not a const) so flat- and tree-mode share one
// definition that tracks the --load flag.
func hvLsHeader(showLoad bool) string {
	h := "PK\tLABEL\tVERSION\tUPTIME\tTP\tAPPS\tIP\tCC\tSTATUS"
	if showLoad {
		h += "\tLOAD\tMEM%\tDISK%"
	}
	return h
}

// fmtLoadCells renders the LOAD / MEM% / DISK% cells for a visor. LOAD is the
// 1-minute average over the core count ("9.43/4") so saturation is obvious at a
// glance; a value >= cores means the run queue is backed up. "-" when the visor
// reported no load snapshot (older binary, or the metric was unavailable).
func fmtLoadCells(e visor.HVVisorEntry) string {
	if e.Load == nil {
		return "-\t-\t-"
	}
	loadCell := fmt.Sprintf("%.2f", e.Load.Load1)
	if e.Load.CPUCores > 0 {
		loadCell = fmt.Sprintf("%.2f/%d", e.Load.Load1, e.Load.CPUCores)
	}
	return fmt.Sprintf("%s\t%.0f%%\t%.0f%%", loadCell, e.Load.MemUsedPercent, e.Load.DiskUsedPercent)
}

// formatHVVisorRow writes a single visor row to the tabwriter with optional
// row-indent prefix. Centralized so the flat-mode and tree-mode renderings
// stay in lockstep (same columns, same width, same null sentinels). When
// showLoad is set it appends the LOAD / MEM% / DISK% cells.
func formatHVVisorRow(tw *tabwriter.Writer, e visor.HVVisorEntry, indent string, showLoad bool) {
	pk := e.PK.String()
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
	label := e.Hostname
	if label == "" {
		label = "-"
	}
	row := fmt.Sprintf("%s%s\t%s\t%s\t%s\t%d\t%d\t%s\t%s\t%s",
		indent, pk, label, ver, uptime, e.Transports, e.Apps, ip, cc, status)
	if showLoad {
		row += "\t" + fmtLoadCells(e)
	}
	fmt.Fprintln(tw, row) //nolint:errcheck
}

var hvLsCmd = &cobra.Command{
	Use:   "ls",
	Short: "List visors connected to this hypervisor (default: one section per hypervisor)",
	Long: `List visors connected to this hypervisor.

By default renders the same multi-section structure the hvui shows: the
local hypervisor with its directly-connected visors first, then each
sub-hypervisor (connected to this hypervisor) as its own section with
a chain breadcrumb and that hypervisor's directly-connected visors. A
visor reachable via multiple hypervisors appears in every relevant
section by design. Sub-hypervisor sections dedup by PK.

The columns match the hvui's node-list table: PK, LABEL (the visor's
hostname), VERSION, UPTIME, TP (transport count), APPS, IP, CC
(country code), STATUS.

For the legacy single flat-table output (e.g. for scripts), pass --flat.`,
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}

		if hvLsFlat {
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
			fmt.Fprintln(tw, hvLsHeader(hvLsLoad)) //nolint:errcheck
			for _, e := range entries {
				formatHVVisorRow(tw, e, "", hvLsLoad)
			}
			tw.Flush() //nolint:errcheck,gosec
			internal.PrintOutput(cmd.Flags(), entries, buf.String())
			return
		}

		tree, err := rpcClient.HVListVisorsTree()
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to list visors tree: %w", err))
		}

		var buf strings.Builder
		for sectionIdx, section := range tree.Sections {
			if sectionIdx > 0 {
				// Print breadcrumb chain for sub-hypervisors —
				// ascii-tree style so the structure is obvious in
				// plain terminals without needing UTF-8 box chars.
				buf.WriteString("\n")
				for chainIdx, chainPK := range section.ViaChain {
					prefix := strings.Repeat("    ", chainIdx)
					buf.WriteString(prefix + chainPK.String() + "\n")
				}
				prefix := strings.Repeat("    ", len(section.ViaChain)-1) + "└── "
				buf.WriteString(prefix + section.HypervisorPK.String() + "\n")
			} else {
				buf.WriteString(section.HypervisorPK.String() + "\n")
			}
			if section.SubError != "" {
				buf.WriteString("  (sub-hypervisor query error: " + section.SubError + ")\n")
				continue
			}
			if len(section.Visors) == 0 {
				buf.WriteString("  (no visors)\n")
				continue
			}
			tw := tabwriter.NewWriter(&buf, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "  "+hvLsHeader(hvLsLoad)) //nolint:errcheck
			for _, e := range section.Visors {
				formatHVVisorRow(tw, e, "  ", hvLsLoad)
			}
			tw.Flush() //nolint:errcheck,gosec
		}
		internal.PrintOutput(cmd.Flags(), tree, buf.String())
	},
}

// hvTreeCmd is retained as a deprecated alias for backwards compatibility —
// `hv ls` now does the same thing by default. Tagged Hidden=true so it
// doesn't clutter `hv --help` but still works when scripts or muscle memory
// call it.
var hvTreeCmd = &cobra.Command{
	Use:    "tree",
	Short:  "Alias for `hv ls` (deprecated; use `hv ls` instead)",
	Hidden: true,
	Run:    hvLsCmd.Run,
}
