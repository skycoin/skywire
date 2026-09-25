// Package climail cmd/skywire-cli/commands/mail/settings.go c4-vis-cli
package climail

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
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
			n, err := parseSize(v)
			if err != nil {
				return u, fmt.Errorf("%s: %w", k, err)
			}
			if k == "max_message_size" {
				u.MaxMessageSize = &n
			} else {
				u.MaxTotalSize = &n
			}
		case "max_age":
			d, err := parseAge(v)
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

var sizeUnits = []struct {
	suffix string
	mult   int64
}{
	{"gib", 1 << 30}, {"mib", 1 << 20}, {"kib", 1 << 10},
	{"gb", 1e9}, {"mb", 1e6}, {"kb", 1e3},
	{"g", 1 << 30}, {"m", 1 << 20}, {"k", 1 << 10}, {"b", 1},
}

// parseSize reads "16MiB", "500KB", "1048576", "default" (0) or
// "none" (-1).
func parseSize(s string) (int64, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "default":
		return 0, nil
	case "none", "unlimited":
		return -1, nil
	}
	mult := int64(1)
	for _, u := range sizeUnits {
		if strings.HasSuffix(s, u.suffix) {
			s, mult = strings.TrimSpace(strings.TrimSuffix(s, u.suffix)), u.mult
			break
		}
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f <= 0 {
		return 0, fmt.Errorf("%q is not a size", s)
	}
	return int64(f * float64(mult)), nil
}

// parseAge reads a Go duration, also accepting days ("7d"), "default"
// (0) or "none" (-1).
func parseAge(s string) (time.Duration, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "default":
		return 0, nil
	case "none", "forever":
		return -1, nil
	}
	if d, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.ParseFloat(d, 64)
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("%q is not a duration", s)
		}
		return time.Duration(n * float64(24*time.Hour)), nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%q is not a duration", s)
	}
	return d, nil
}

func formatSize(n int64) string {
	switch {
	case n < 0:
		return "no limit"
	case n >= 1<<30 && n%(1<<30) == 0:
		return fmt.Sprintf("%dGiB", n>>30)
	case n >= 1<<20:
		return strconv.FormatFloat(float64(n)/(1<<20), 'f', -1, 64) + "MiB"
	case n >= 1<<10:
		return strconv.FormatFloat(float64(n)/(1<<10), 'f', 1, 64) + "KiB"
	}
	return fmt.Sprintf("%dB", n)
}

func formatAge(d time.Duration) string {
	switch {
	case d < 0:
		return "never"
	case d%(24*time.Hour) == 0:
		return fmt.Sprintf("%dd", d/(24*time.Hour))
	}
	return d.String()
}

func renderSettings(st *visorapi.MailStatus) string {
	l := st.Limits
	running := "running"
	if !st.Running {
		running = "not running: " + st.Reason
	}
	return fmt.Sprintf("enable            %v (%s)\nmax_message_size  %s\nmax_total_size    %s (%s used)\nmax_age           %s\n",
		st.Enabled, running, formatSize(l.MaxMessageSize), formatSize(l.MaxTotalSize), formatSize(st.Usage), formatAge(l.MaxAge))
}
