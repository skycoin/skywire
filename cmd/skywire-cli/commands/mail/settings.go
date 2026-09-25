// Package climail cmd/skywire-cli/commands/mail/settings.go c4-vis-cli
package climail

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	"github.com/skycoin/skywire/pkg/skymail"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

func init() {
	RootCmd.AddCommand(settingsCmd)
}

var settingsCmd = &cobra.Command{
	Use:   "settings [key=value]...",
	Short: "Show or change the mailbox's settings, live",
	Long: `Show or change the mailbox's settings. Changes apply at once and are
saved to the visor config.

Keys:
  enable            true|false — run the mailbox
  max_message_size  largest message accepted, e.g. 1MiB
  max_total_size    room for Inbox and Sent together, e.g. 16MiB;
                    a full mailbox refuses mail until some is removed
  max_age           mail older than this is deleted, e.g. 7d or 36h

Sizes take B, KiB, MiB, GiB (or KB, MB, GB). "default" restores the
default (1MiB, 16MiB, 7d); "none" removes the bound.

Examples:
  skywire cli mail settings
  skywire cli mail settings max_total_size=64MiB max_age=30d
  skywire cli mail settings enable=false`,
	Run: func(cmd *cobra.Command, args []string) {
		c := mailClient(cmd)
		if len(args) > 0 {
			u, err := parseSettings(args)
			if err != nil {
				internal.PrintFatalError(cmd.Flags(), err)
			}
			if err := c.MailSetSettings(u); err != nil {
				internal.PrintFatalError(cmd.Flags(), err)
			}
		}
		st, err := c.MailStatus()
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		internal.PrintOutput(cmd.Flags(), st, renderSettings(st))
	},
}

func parseSettings(args []string) (visorapi.MailSettingsUpdate, error) {
	var u visorapi.MailSettingsUpdate
	for _, a := range args {
		k, v, ok := strings.Cut(a, "=")
		if !ok {
			return u, fmt.Errorf("%q: want key=value", a)
		}
		switch k {
		case "enable":
			b, err := strconv.ParseBool(v)
			if err != nil {
				return u, fmt.Errorf("enable: %w", err)
			}
			u.Enable = &b
		case "max_message_size", "max_total_size":
			n, err := skymail.ParseSize(v)
			if err != nil {
				return u, fmt.Errorf("%s: %w", k, err)
			}
			if k == "max_message_size" {
				u.MaxMessageSize = &n
			} else {
				u.MaxTotalSize = &n
			}
		case "max_age":
			d, err := skymail.ParseAge(v)
			if err != nil {
				return u, fmt.Errorf("max_age: %w", err)
			}
			u.MaxAge = &d
		default:
			return u, fmt.Errorf("unknown key %q (enable, max_message_size, max_total_size, max_age)", k)
		}
	}
	return u, nil
}

func renderSettings(st *visorapi.MailStatus) string {
	l := st.Limits
	running := "running"
	if !st.Running {
		running = "not running: " + st.Reason
	}
	return fmt.Sprintf("enable            %v (%s)\nmax_message_size  %s\nmax_total_size    %s (%s used)\nmax_age           %s\n",
		st.Enabled, running, skymail.FormatSize(l.MaxMessageSize), skymail.FormatSize(l.MaxTotalSize), skymail.FormatSize(st.Usage), skymail.FormatAge(l.MaxAge))
}
