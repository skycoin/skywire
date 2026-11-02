// Package cliasync cmd/skywire-cli/commands/async/settings.go c4-vis-cli
package cliasync

import (
	"fmt"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

func init() {
	RootCmd.AddCommand(settingsCmd)
}

var settingsCmd = &cobra.Command{
	Use:   "settings [key=value]...",
	Short: "Show or change the dmsg-async settings, live",
	Long: `Show or change the dmsg-async settings. Changes apply at once and are
kept in settings.json in the dmsg-async directory, so they survive a
restart and, in a browser tab, a reload. The config's dmsg_async section
is the baseline they override. A change restarts only the host or the
inbox, whichever it touches.

Keys:
  host            true|false — hold messages for other keys;
                  default: on for a hypervisor
  hosted          further keys the host holds for, e.g. pk1,pk2, beyond
                  its own and the visors it manages
  max_message     largest message the host takes, up to 1MiB, e.g. 64KiB
  max_total_size  room for every held message together, e.g. 256MiB
  ttl             how long the host keeps a message, e.g. 7d
  inbox           true|false — keep this visor's own messages on its hosts
  inbox_hosts     this visor's inbox hosts, at most 4, e.g. pk1,pk2;
                  default and none: its hypervisors
  publish         true|false — post the inbox record to dmsg discovery
  allow           senders that may leave messages, e.g. pk1,pk2;
                  default and none: every key

"default" returns a key to the config's value.`,
	Example: `  skywire cli async settings
  skywire cli async settings ttl=3d max_total_size=512MiB
  skywire cli async settings inbox_hosts=02a4...,03b5... publish=false
  skywire cli async settings host=default`,
	Run: func(cmd *cobra.Command, args []string) {
		c := api(cmd)
		if len(args) > 0 {
			kv, err := parseKV(args)
			if err != nil {
				internal.PrintFatalError(cmd.Flags(), err)
			}
			if err := c.DmsgAsyncSetSettings(kv); err != nil {
				internal.PrintFatalError(cmd.Flags(), err)
			}
		}
		s, err := c.DmsgAsyncSettings()
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		internal.PrintOutput(cmd.Flags(), s, renderSettings(s))
	},
}

func parseKV(args []string) (map[string]string, error) {
	kv := map[string]string{}
	for _, a := range args {
		k, v, ok := strings.Cut(a, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("%q: want key=value", a)
		}
		kv[k] = v
	}
	return kv, nil
}

func renderSettings(s *visorapi.DmsgAsyncSettings) string {
	var b strings.Builder
	tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	row := func(k, v string) {
		mark := ""
		if slices.Contains(s.Set, k) {
			mark = "  (set)"
		}
		fmt.Fprintf(tw, "%s\t%s%s\n", k, v, mark) //nolint:errcheck
	}
	keys := func(pks []cipher.PubKey, none string) string {
		if len(pks) == 0 {
			return none
		}
		out := make([]string, len(pks))
		for i, pk := range pks {
			out[i] = pk.Hex()
		}
		return strings.Join(out, ",")
	}
	row("host", fmt.Sprint(s.Host))
	row("hosted", keys(s.Hosted, "none"))
	row("max_message", fmt.Sprintf("%d bytes", s.MaxMessage))
	row("max_total_size", fmt.Sprintf("%d bytes", s.MaxTotalSize))
	row("ttl", s.TTL.String())
	row("inbox", fmt.Sprint(s.Inbox))
	row("inbox_hosts", keys(s.InboxHosts, "none"))
	row("publish", fmt.Sprint(s.Publish))
	row("allow", keys(s.Allow, "every key"))
	_ = tw.Flush() //nolint:errcheck
	return b.String()
}
