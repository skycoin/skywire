// Package clivisor cmd/skywire-cli/commands/visor/hv_apps.go c4-vis-cli
package clivisor

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"text/tabwriter"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

// hvAppsLimit caps how many visors the hypervisor is asked about at once.
const hvAppsLimit = 4

var (
	hvAppsFilter string
	hvAppsPKs    string
	hvAppsAll    bool
	hvAppsStart  bool
)

func init() {
	hvCmd.AddCommand(hvAppsCmd)
	hvAppsCmd.Flags().StringVar(&hvAppsFilter, "app", "", "only visors running this app")
	for _, c := range []*cobra.Command{hvAppsStartCmd, hvAppsStopCmd, hvAppsAddCmd} {
		c.Flags().StringVar(&hvAppsPKs, "pks", "", "comma separated visor public keys")
		c.Flags().BoolVar(&hvAppsAll, "all", false, "every visor this hypervisor manages")
		hvAppsCmd.AddCommand(c)
	}
	hvAppsAddCmd.Flags().BoolVar(&hvAppsStart, "start", false, "start the app after adding it")
}

type hvVisorApps struct {
	PK      string   `json:"pk"`
	Running []string `json:"running"`
	Stopped []string `json:"stopped"`
	Err     string   `json:"error,omitempty"`
}

type hvAppResult struct {
	PK  string `json:"pk"`
	OK  bool   `json:"ok"`
	Err string `json:"error,omitempty"`
}

var hvAppsCmd = &cobra.Command{
	Use:   "apps",
	Short: "Which visors run which apps, and start, stop or add an app across visors",
	Long: `List the apps of every visor this hypervisor manages, including visors
reached through a sub-hypervisor. The start, stop and add subcommands act on
the visors named by --pks, or on all of them with --all. One failing visor
does not stop the others, and each visor's result is printed.`,
	Run: func(cmd *cobra.Command, _ []string) {
		client, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		entries, err := client.HVListVisors()
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to list visors: %w", err))
		}
		pks := make([]cipher.PubKey, 0, len(entries))
		for _, e := range entries {
			pks = append(pks, e.PK)
		}
		rows := make([]hvVisorApps, len(pks))
		hvForEach(pks, func(i int, pk cipher.PubKey) {
			rows[i] = hvVisorAppsOf(client, pk)
		})
		if hvAppsFilter != "" {
			kept := rows[:0]
			for _, r := range rows {
				if slices.Contains(r.Running, hvAppsFilter) {
					kept = append(kept, r)
				}
			}
			rows = kept
		}
		var buf strings.Builder
		tw := tabwriter.NewWriter(&buf, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "PK\tRUNNING\tSTOPPED") //nolint:errcheck
		for _, r := range rows {
			if r.Err != "" {
				fmt.Fprintf(tw, "%s\terror: %s\t\n", r.PK, r.Err) //nolint:errcheck
				continue
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\n", r.PK, hvJoin(r.Running), hvJoin(r.Stopped)) //nolint:errcheck
		}
		tw.Flush() //nolint:errcheck,gosec
		internal.PrintOutput(cmd.Flags(), rows, buf.String())
	},
}

var hvAppsStartCmd = &cobra.Command{
	Use:   "start <app>",
	Short: "Start an app on the chosen visors",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		hvAppsBulk(cmd, "start "+args[0], func(c visorapi.API, pk cipher.PubKey) error {
			return c.HVStartApp(pk, args[0])
		})
	},
}

var hvAppsStopCmd = &cobra.Command{
	Use:   "stop <app>",
	Short: "Stop an app on the chosen visors",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		hvAppsBulk(cmd, "stop "+args[0], func(c visorapi.API, pk cipher.PubKey) error {
			return c.HVStopApp(pk, args[0])
		})
	},
}

var hvAppsAddCmd = &cobra.Command{
	Use:   "add <name> [binary]",
	Short: "Add an app from the catalog to the chosen visors",
	Long: `Add an app entry to each chosen visor. The binary defaults to the name,
so "add skysocks-client" adds the catalog app of that name.`,
	Args: cobra.RangeArgs(1, 2),
	Run: func(cmd *cobra.Command, args []string) {
		name, binary := args[0], args[0]
		if len(args) == 2 {
			binary = args[1]
		}
		hvAppsBulk(cmd, "add "+name, func(c visorapi.API, pk cipher.PubKey) error {
			if err := c.HVAddApp(pk, name, binary); err != nil {
				return err
			}
			if hvAppsStart {
				return c.HVStartApp(pk, name)
			}
			return nil
		})
	},
}

func hvAppsBulk(cmd *cobra.Command, what string, op func(visorapi.API, cipher.PubKey) error) {
	client, err := clirpc.Client(cmd.Flags())
	if err != nil {
		internal.PrintFatalError(cmd.Flags(), err)
	}
	pkStrs, err := collectHVTargets(client, hvAppsPKs, hvAppsAll)
	if err != nil {
		internal.PrintFatalError(cmd.Flags(), err)
	}
	if len(pkStrs) == 0 {
		internal.PrintFatalError(cmd.Flags(), fmt.Errorf("no target visors, pass --pks or --all"))
	}
	results := make([]hvAppResult, len(pkStrs))
	var pks []cipher.PubKey
	var idx []int
	for i, s := range pkStrs {
		results[i].PK = s
		var pk cipher.PubKey
		if err := pk.Set(s); err != nil {
			results[i].Err = "invalid public key: " + err.Error()
			continue
		}
		pks = append(pks, pk)
		idx = append(idx, i)
	}
	hvForEach(pks, func(i int, pk cipher.PubKey) {
		r := &results[idx[i]]
		if err := op(client, pk); err != nil {
			r.Err = err.Error()
			return
		}
		r.OK = true
	})
	failed := 0
	var buf strings.Builder
	for _, r := range results {
		if r.OK {
			fmt.Fprintf(&buf, "  %s  OK\n", r.PK)
		} else {
			failed++
			fmt.Fprintf(&buf, "  %s  FAIL  %s\n", r.PK, r.Err)
		}
	}
	head := fmt.Sprintf("%s: %d visors, %d ok, %d failed\n", what, len(results), len(results)-failed, failed)
	internal.PrintOutput(cmd.Flags(), results, head+buf.String())
}

// collectHVTargets resolves --pks, or every managed visor with --all.
func collectHVTargets(client visorapi.API, csv string, all bool) ([]string, error) {
	seen := make(map[string]bool)
	var out []string
	add := func(s string) {
		if s = strings.TrimSpace(s); s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, s := range strings.Split(csv, ",") {
		add(s)
	}
	if all {
		entries, err := client.HVListVisors()
		if err != nil {
			return nil, fmt.Errorf("failed to list visors: %w", err)
		}
		for _, e := range entries {
			add(e.PK.String())
		}
	}
	return out, nil
}

// hvForEach runs fn for every key, hvAppsLimit at a time.
func hvForEach(pks []cipher.PubKey, fn func(int, cipher.PubKey)) {
	sem := make(chan struct{}, hvAppsLimit)
	var wg sync.WaitGroup
	for i, pk := range pks {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, pk cipher.PubKey) {
			defer func() { <-sem; wg.Done() }()
			fn(i, pk)
		}(i, pk)
	}
	wg.Wait()
}

func hvVisorAppsOf(client visorapi.API, pk cipher.PubKey) hvVisorApps {
	row := hvVisorApps{PK: pk.String(), Running: []string{}, Stopped: []string{}}
	s, err := client.HVVisorSummary(pk)
	if err != nil {
		row.Err = err.Error()
		return row
	}
	if s == nil || s.Overview == nil {
		row.Err = "no summary"
		return row
	}
	for _, a := range s.Overview.Apps {
		if a.Status == appserver.AppStatusRunning || a.Status == appserver.AppStatusStarting {
			row.Running = append(row.Running, a.Name)
		} else {
			row.Stopped = append(row.Stopped, a.Name)
		}
	}
	sort.Strings(row.Running)
	sort.Strings(row.Stopped)
	return row
}

func hvJoin(list []string) string {
	if len(list) == 0 {
		return "-"
	}
	return strings.Join(list, ",")
}
