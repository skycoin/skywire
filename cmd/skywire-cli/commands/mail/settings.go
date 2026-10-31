// Package climail cmd/skywire-cli/commands/mail/settings.go c4-vis-cli
package climail

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	"github.com/skycoin/skywire/pkg/cipher"
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
kept in settings.json beside the mail, so they survive a restart and,
in a browser tab, a reload. The config's skymail section is the
baseline they override.

Keys:
  enable            true|false — run the mailbox
  max_message_size  largest message accepted, e.g. 10MiB
  max_total_size    room for every folder together, e.g. 64MiB;
                    a full mailbox refuses mail until some is removed
  max_age           mail older than this is deleted, e.g. 7d or 36h;
                    Outbox mail this old is given up with a bounce
  helpers           visors that get a sealed copy of queued mail and
                    deliver it while this one is away, e.g. pk1,pk2;
                    default: the visors this one manages as hypervisor
  relay_for         visors whose sealed mail this one carries;
                    default: this visor's hypervisors

Sizes take B, KiB, MiB, GiB (or KB, MB, GB). "default" restores the
default (10MiB, 64MiB, 7d, or the hypervisor relation for the lists);
"none" removes the bound or empties the list. A helper carries mail
only for visors in its relay_for, and cannot read what it carries.

Examples:
  skywire cli mail settings
  skywire cli mail settings max_total_size=128MiB max_age=30d
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
		case "helpers", "relay_for":
			pks, def, err := parsePKList(v)
			if err != nil {
				return u, fmt.Errorf("%s: %w", k, err)
			}
			if k == "helpers" {
				u.Helpers, u.HelpersDefault = pks, def
			} else {
				u.RelayFor, u.RelayForDefault = pks, def
			}
		default:
			return u, fmt.Errorf("unknown key %q (enable, max_message_size, max_total_size, max_age, helpers, relay_for)", k)
		}
	}
	return u, nil
}

// parsePKList reads "default", "none" or a comma separated PK list.
func parsePKList(v string) (*[]cipher.PubKey, bool, error) {
	switch v {
	case "default":
		return nil, true, nil
	case "none", "":
		return &[]cipher.PubKey{}, false, nil
	}
	pks := []cipher.PubKey{}
	for _, f := range strings.Split(v, ",") {
		var pk cipher.PubKey
		if err := pk.Set(strings.TrimSpace(f)); err != nil {
			return nil, false, fmt.Errorf("%q: %w", f, err)
		}
		pks = append(pks, pk)
	}
	return &pks, false, nil
}

func renderSettings(st *visorapi.MailStatus) string {
	l := st.Limits
	running := "running"
	if !st.Running {
		running = "not running: " + st.Reason
	}
	out := fmt.Sprintf("enable            %v (%s)\nmax_message_size  %s\nmax_total_size    %s (%s used)\nmax_age           %s\n",
		st.Enabled, running, skymail.FormatSize(l.MaxMessageSize), skymail.FormatSize(l.MaxTotalSize), skymail.FormatSize(st.Usage), skymail.FormatAge(l.MaxAge))
	if st.HasOutbox {
		out += "helpers           " + renderPKList(st.Helpers, st.HelpersDefault, "the visors this one manages") + "\n"
		out += "relay_for         " + renderPKList(st.RelayFor, st.RelayForDefault, "this visor's hypervisors") + "\n"
	}
	return out
}

func renderPKList(pks []cipher.PubKey, def bool, what string) string {
	var b strings.Builder
	switch {
	case len(pks) == 0 && def:
		b.WriteString("none")
	case len(pks) == 0:
		return "none"
	default:
		for i, pk := range pks {
			if i > 0 {
				b.WriteString(",")
			}
			b.WriteString(pk.Hex())
		}
	}
	if def {
		b.WriteString(" (default: " + what + ")")
	}
	return b.String()
}
