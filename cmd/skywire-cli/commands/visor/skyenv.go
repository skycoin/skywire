// Package clivisor cmd/skywire-cli/commands/visor/skyenv.go c4-vis-cli
package clivisor

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

var skyenvUnset []string

func init() {
	RootCmd.AddCommand(skyenvCmd)
	skyenvCmd.Flags().StringSliceVar(&skyenvUnset, "unset", nil, "return these settings to their default (autoconfig flag names)")
}

var skyenvCmd = &cobra.Command{
	Use:     "skyenv [flag=value ...]",
	Aliases: []string{"conf"},
	Short:   "Show or edit the visor's /etc/skywire.conf",
	Long: `Show or edit the visor's /etc/skywire.conf — the settings every config
regen is built from, so unlike an edit to skywire-config.json they survive
` + "`skywire autoconfig`" + `, a package update, and a browser-visor reload.

Settings are named by their autoconfig flag, and an edit writes exactly
what ` + "`skywire autoconfig --<flag>=<value>`" + ` would:

  skywire cli visor skyenv ishv=true hvpks=<pk1>,<pk2>
  skywire cli visor skyenv --unset hvpks

Edits take effect at the next regen: ` + "`skywire autoconfig`" + ` on a host, or a
reload of the browser visor. With --via, this edits a remote visor's file.`,
	Run: func(cmd *cobra.Command, args []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}
		var st visorapi.SkyenvState
		if len(args) == 0 && len(skyenvUnset) == 0 {
			st, err = rpcClient.Skyenv()
		} else {
			req := visorapi.SkyenvEdits{Set: map[string]string{}, Unset: skyenvUnset}
			for _, a := range args {
				k, v, ok := strings.Cut(a, "=")
				if !ok {
					internal.PrintFatalError(cmd.Flags(), fmt.Errorf("%q: want flag=value", a))
				}
				req.Set[strings.TrimPrefix(k, "--")] = v
			}
			st, err = rpcClient.SetSkyenv(req)
		}
		if err != nil {
			internal.PrintFatalRPCError(cmd.Flags(), err)
		}
		internal.PrintOutput(cmd.Flags(), st, formatSkyenv(st))
	},
}

// formatSkyenv lists one row per variable: what the file sets it to, or the
// default config gen uses while it is commented out.
func formatSkyenv(st visorapi.SkyenvState) string {
	var b strings.Builder
	if !st.Exists {
		fmt.Fprintf(&b, "%s does not exist yet; `skywire autoconfig` creates it\n", st.Path)
		return b.String()
	}
	fmt.Fprintf(&b, "%s\n", st.Path)
	if !st.Writable {
		b.WriteString("read-only for this visor: edit it on the host with `sudo skywire autoconfig --<flag>`\n")
	}
	b.WriteString("\n")
	tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "FLAG\tVARIABLE\tVALUE") //nolint:errcheck
	seen := map[string]bool{}
	var set []string
	for _, f := range st.Flags {
		if f.EnvKey == "" || f.EnvNegate || seen[f.EnvKey] {
			continue
		}
		seen[f.EnvKey] = true
		val, ok := st.Values[f.EnvKey]
		switch {
		case ok:
			set = append(set, f.EnvKey)
		case f.EnvDefault != "":
			val = "(default " + f.EnvDefault + ")"
		default:
			val = "(default)"
		}
		fmt.Fprintf(tw, "--%s\t%s\t%s\n", f.Name, f.EnvKey, val) //nolint:errcheck
	}
	_ = tw.Flush() //nolint:errcheck
	sort.Strings(set)
	fmt.Fprintf(&b, "\nset in the file: %s\n", strings.Join(set, " "))
	return b.String()
}
