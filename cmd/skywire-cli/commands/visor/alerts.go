// Package clivisor cmd/skywire-cli/commands/visor/alerts.go c4-vis-cli
package clivisor

import (
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/pkg/alerts"
	"github.com/skycoin/skywire/pkg/cipher"
)

func init() {
	RootCmd.AddCommand(alertsCmd)
	alertsCmd.AddCommand(alertsSetCmd, alertsTestCmd)
	f := alertsSetCmd.Flags()
	f.StringSliceVar(&alEmail, "email", nil, "email address(es) to alert, comma separated (empty clears)")
	f.DurationVar(&alDelay, "offline-after", 0, "how long a visor must stay offline before an alert, 1m to 1h (default 5m)")
	f.StringVar(&alWebhook, "webhook", "", "URL to POST a JSON alert to (empty clears)")
	f.StringSliceVar(&alWatch, "watch", nil, "extra visor public keys to watch by their dmsg discovery entry (empty clears)")
	f.StringVar(&alFrom, "from", "", "sender address (default skywire-alerts@<hostname>, or the relay login)")
	f.StringVar(&alSMTPHost, "smtp-host", "", "SMTP relay host; without one, mail goes to the recipient's MX on port 25")
	f.IntVar(&alSMTPPort, "smtp-port", 0, "SMTP relay port (default 587 with --smtp-starttls, else 25; 465 is implicit TLS)")
	f.StringVar(&alSMTPUser, "smtp-user", "", "SMTP relay username")
	f.StringVar(&alSMTPPass, "smtp-pass", "", "SMTP relay password (kept when not given)")
	f.BoolVar(&alSMTPTLS, "smtp-starttls", false, "require STARTTLS on the relay")
	f.BoolVar(&alNoSMTP, "no-smtp", false, "remove the relay and deliver directly")
}

var (
	alEmail    []string
	alDelay    time.Duration
	alWebhook  string
	alWatch    []string
	alFrom     string
	alSMTPHost string
	alSMTPPort int
	alSMTPUser string
	alSMTPPass string
	alSMTPTLS  bool
	alNoSMTP   bool
)

var alertsCmd = &cobra.Command{
	Use:   "alerts",
	Short: "Show the offline alerts this visor sends",
	Long: `Show the offline alerts this visor sends.

The visor mails, or posts to a webhook, when a watched visor has been
offline for the offline delay, and once more when it is back. A visor
that returns within the delay sends nothing. Watched visors are those its
hypervisor manages, remembered offline ones included, plus the --watch
list, which is checked by its dmsg discovery entry.

Without a relay, mail goes straight to each recipient domain's MX on
port 25. Many home connections block port 25 or are refused by large
mail providers; set a relay with 'alerts set --smtp-host' then.`,
	Run: func(cmd *cobra.Command, _ []string) {
		st := alertsStatus(cmd)
		internal.PrintOutput(cmd.Flags(), st, formatAlerts(st))
	},
}

func alertsStatus(cmd *cobra.Command) *alerts.Status {
	rpcClient, err := clirpc.Client(cmd.Flags())
	if err != nil {
		internal.PrintFatalError(cmd.Flags(), err)
	}
	st, err := rpcClient.AlertsStatus()
	if err != nil {
		internal.PrintFatalError(cmd.Flags(), err)
	}
	return st
}

func formatAlerts(st *alerts.Status) string {
	var b strings.Builder
	s := st.Settings
	or := func(v, d string) string {
		if v == "" {
			return d
		}
		return v
	}
	fmt.Fprintf(&b, "enabled:        %v\n", st.Enabled)
	fmt.Fprintf(&b, "email:          %s\n", or(strings.Join(s.EmailTo, ", "), "-"))
	fmt.Fprintf(&b, "offline after:  %s\n", st.OfflineAfter)
	fmt.Fprintf(&b, "webhook:        %s\n", or(s.Webhook, "-"))
	fmt.Fprintf(&b, "from:           %s\n", or(s.From, "(default)"))
	if s.SMTP != nil {
		pw := "not set"
		if s.SMTP.PasswordSet {
			pw = "set"
		}
		fmt.Fprintf(&b, "relay:          %s port %d user %s starttls %v password %s\n",
			s.SMTP.Host, s.SMTP.Port, or(s.SMTP.Username, "-"), s.SMTP.StartTLS, pw)
	} else {
		b.WriteString("relay:          none, direct to MX\n")
	}
	if !st.LastSent.IsZero() {
		fmt.Fprintf(&b, "last sent:      %s\n", st.LastSent.UTC().Format(time.RFC3339))
	}
	if st.Pending > 0 {
		fmt.Fprintf(&b, "pending:        %d\n", st.Pending)
	}
	for _, r := range st.LastResults {
		if r.Err != "" {
			fmt.Fprintf(&b, "last error:     %s %s: %s\n", r.Channel, r.Target, r.Err)
		}
	}
	if len(st.Watched) == 0 {
		b.WriteString("\nno visors watched yet\n")
		return b.String()
	}
	b.WriteString("\n")
	tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "PK\tHOSTNAME\tSOURCE\tSTATE\tLAST SEEN") //nolint:errcheck
	for _, w := range st.Watched {
		state := "online"
		switch {
		case w.Online:
		case w.Alerted:
			state = "offline, alerted"
		default:
			state = "offline"
		}
		seen := "-"
		if !w.LastSeen.IsZero() {
			seen = w.LastSeen.UTC().Format(time.RFC3339)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", w.PK, or(w.Hostname, "-"), w.Source, state, seen) //nolint:errcheck
	}
	tw.Flush() //nolint:errcheck,gosec
	return b.String()
}

var alertsSetCmd = &cobra.Command{
	Use:   "set",
	Short: "Change the offline alert settings, applied at once and saved",
	Long: `Change the offline alert settings. Only the flags given change; the
rest are kept. The change applies at once and is saved to alerts.json in
the visor's local directory, which wins over the config's alerts block
and survives a config regen.

  skywire cli visor alerts set --email ops@example.com --offline-after 15m
  skywire cli visor alerts set --smtp-host smtp.example.com --smtp-starttls --smtp-user me@example.com --smtp-pass '...'
  skywire cli visor alerts set --email ''      # stop mail alerts`,
	Run: func(cmd *cobra.Command, _ []string) {
		f := cmd.Flags()
		rpcClient, err := clirpc.Client(f)
		if err != nil {
			internal.PrintFatalError(f, err)
		}
		st, err := rpcClient.AlertsStatus()
		if err != nil {
			internal.PrintFatalError(f, err)
		}
		s := st.Settings
		if f.Changed("email") {
			s.EmailTo = trimmedList(alEmail)
		}
		if f.Changed("offline-after") {
			if alDelay < alerts.MinOfflineAfter || alDelay > alerts.MaxOfflineAfter {
				internal.PrintFatalError(f, fmt.Errorf("--offline-after must be between %s and %s", alerts.MinOfflineAfter, alerts.MaxOfflineAfter))
			}
			s.OfflineAfter = alerts.Duration(alDelay)
		}
		if f.Changed("webhook") {
			s.Webhook = alWebhook
		}
		if f.Changed("from") {
			s.From = alFrom
		}
		if f.Changed("watch") {
			s.Watch = nil
			for _, k := range trimmedList(alWatch) {
				var pk cipher.PubKey
				if err := pk.Set(k); err != nil {
					internal.PrintFatalError(f, fmt.Errorf("invalid public key %q: %w", k, err))
				}
				s.Watch = append(s.Watch, pk)
			}
		}
		relayFlags := []string{"smtp-host", "smtp-port", "smtp-user", "smtp-pass", "smtp-starttls"}
		if alNoSMTP {
			s.SMTP = nil
		} else if anyChanged(cmd, relayFlags...) {
			r := alerts.SMTP{}
			if s.SMTP != nil {
				r = *s.SMTP
			}
			if f.Changed("smtp-host") {
				r.Host = alSMTPHost
			}
			if f.Changed("smtp-port") {
				r.Port = alSMTPPort
			}
			if f.Changed("smtp-user") {
				r.Username = alSMTPUser
			}
			if f.Changed("smtp-starttls") {
				r.StartTLS = alSMTPTLS
			}
			r.Password = alSMTPPass
			s.SMTP = &r
		}
		if err := rpcClient.AlertsSet(s); err != nil {
			internal.PrintFatalError(f, err)
		}
		st, err = rpcClient.AlertsStatus()
		if err != nil {
			internal.PrintFatalError(f, err)
		}
		internal.PrintOutput(f, st, formatAlerts(st))
	},
}

var alertsTestCmd = &cobra.Command{
	Use:   "test",
	Short: "Send a test alert to every configured address and webhook",
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		res, err := rpcClient.AlertsTest()
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		var b strings.Builder
		for _, r := range res {
			if r.Err == "" {
				fmt.Fprintf(&b, "%s %s: sent\n", r.Channel, r.Target)
			} else {
				fmt.Fprintf(&b, "%s %s: FAILED: %s\n", r.Channel, r.Target, r.Err)
			}
		}
		internal.PrintOutput(cmd.Flags(), res, b.String())
	},
}

func trimmedList(in []string) []string {
	var out []string
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func anyChanged(cmd *cobra.Command, names ...string) bool {
	for _, n := range names {
		if cmd.Flags().Changed(n) {
			return true
		}
	}
	return false
}
