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
	"github.com/skycoin/skywire/pkg/visor"
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
	hvCmd.AddCommand(hvRmCmd)
	hvRmCmd.Flags().BoolVar(&hvRmAll, "all", false, "remove every runtime-added hypervisor connection")
	hvCmd.AddCommand(hvLsCmd)
	hvLsCmd.Flags().BoolVar(&hvLsFlat, "flat", false, "single flat table instead of one section per hypervisor")
	hvCmd.AddCommand(hvTreeCmd)
	hvCmd.AddCommand(hvPasswdCmd)
	hvPasswdCmd.Flags().StringVar(&hvPasswdOld, "old", "", "current password (prompts if unset)")
	hvPasswdCmd.Flags().StringVar(&hvPasswdNew, "new", "", "new password (prompts if unset)")
}

var (
	hvRmAll     bool
	hvPasswdOld string
	hvPasswdNew string
	hvLsFlat    bool
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

var hvRmCmd = &cobra.Command{
	Use:   "rm [public-key]",
	Short: "Disconnect from a remote hypervisor at runtime",
	Long: `Tear down a runtime-added hypervisor connection. Mirrors hv add
— only affects connections created via AddHypervisor (this RPC or
the corresponding CLI). Config-loaded hypervisors aren't affected;
edit SKYENV HYPERVISORPKS and restart the visor to remove those.

Pass --all to disconnect every runtime-added hypervisor in one
call. Without --all, exactly one <public-key> argument is
required.`,
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
	Short: "Change the hypervisor UI admin password",
	Long: `Change the password for the hypervisor UI's "admin" account.
Mirrors the /api/change-password endpoint the UI uses, but without
the HTTP session check (RPC is local-only and already privileged).

Both --old and --new are required. Use the value-only form (avoid
shell history capturing the password) by sourcing them from an
env-var or a process-substitution.`,
	Run: func(cmd *cobra.Command, _ []string) {
		if hvPasswdOld == "" || hvPasswdNew == "" {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("both --old and --new are required"))
		}
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		if err := rpcClient.SetHypervisorPassword(hvPasswdOld, hvPasswdNew); err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		fmt.Println("Hypervisor UI password updated; all existing sessions invalidated.")
	},
}

// formatHVVisorRow writes a single visor row to the tabwriter with optional
// row-indent prefix. Centralized so the flat-mode and tree-mode renderings
// stay in lockstep (same columns, same width, same null sentinels).
func formatHVVisorRow(tw *tabwriter.Writer, e visor.HVVisorEntry, indent string) {
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
	fmt.Fprintf(tw, "%s%s\t%s\t%s\t%s\t%d\t%d\t%s\t%s\t%s\n", //nolint:errcheck
		indent, pk, label, ver, uptime, e.Transports, e.Apps, ip, cc, status)
}

const hvLsTableHeader = "PK\tLABEL\tVERSION\tUPTIME\tTP\tAPPS\tIP\tCC\tSTATUS"

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
			fmt.Fprintln(tw, hvLsTableHeader) //nolint:errcheck
			for _, e := range entries {
				formatHVVisorRow(tw, e, "")
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
			fmt.Fprintln(tw, "  "+hvLsTableHeader) //nolint:errcheck
			for _, e := range section.Visors {
				formatHVVisorRow(tw, e, "  ")
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
